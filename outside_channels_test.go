package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutsideChannelsKeepContentsLifecycleAndPrivateControl(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/channelinputs\n\ngo 1.27.0\n",
		"channels.go": `package channelinputs
import ("errors"; "database/sql")
func source() error { return errors.New("local") }
func Public(ch <-chan error) error { return <-ch }
func closed(ch <-chan error) error { return <-ch }
func Local() error { ch := make(chan error, 1); ch <- source(); _ = Public(ch); return closed(ch) }
type Record struct { E error; Events <-chan error }
func Nested(r Record) error { return <-r.Events }
func Payload(ch <-chan Record) error { return (<-ch).E }
func Selected(ch <-chan error) error { select { case e := <-ch: return e; default: return nil } }
func Result(factory func() <-chan error) error { return <-factory() }
func callback() {}
func Callback(ch <-chan func()) { f := <-ch; f() }
func LocalCallback() { ch := make(chan func(), 1); ch <- callback; Callback(ch) }
func SQL(ch <-chan *sql.DB) { db := <-ch; db.Exec("select * from channel_records") }
func Signal(ch chan struct{}) { close(ch) }
type Loop chan Loop
func Cycle(ch Loop) { _ = <-ch }
func Pointer(ch *<-chan error) error { return <-*ch }
func LocalSQL() { db, _ := sql.Open("sqlite", "known"); ch := make(chan *sql.DB, 1); ch <- db; SQL(ch) }
func escaped(err error) bool { return err != nil }
func Export() <-chan func(error) bool { ch := make(chan func(error) bool, 1); ch <- escaped; return ch }
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Public", "closed", "Nested", "Payload", "Selected", "Result", "Callback", "SQL", "Signal", "Cycle", "Pointer", "Export", "escaped"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 4, MaxNodes: 250,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "known", DatabaseOrigins: []string{"example.com/channelinputs::LocalSQL"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, owner := range []string{"Public", "closed", "Nested", "Payload", "Selected", "Result", "Pointer"} {
			outside, local := false, false
			for _, edge := range report.Relationships {
				if !strings.HasSuffix(edge.From, "::"+owner) || edge.Kind != "error_return" {
					continue
				}
				outside = outside || strings.HasPrefix(edge.To, "error-input:")
				for _, node := range report.Nodes {
					if node.ID == edge.To && node.Kind == "error_result" && strings.Contains(node.Evidence.Snippet, `errors.New("local")`) {
						local = true
					}
				}
			}
			if outside != (owner != "closed") {
				t.Errorf("%s outside error=%v", owner, outside)
			}
			if (owner == "Public" || owner == "closed") && !local {
				t.Errorf("%s lost local error", owner)
			}
		}
		callback, unknownCallback, sql, signal := false, false, false, false
		tables := map[string]bool{}
		for _, edge := range report.Relationships {
			if edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::SQL") {
				tables[edge.To] = true
			}
			callback = callback || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::Callback") && strings.HasSuffix(edge.To, "::callback")
			signal = signal || edge.Kind == "channel_close" && strings.HasSuffix(edge.From, "::Signal") && strings.HasPrefix(edge.To, "channel:outside:")
		}
		for _, boundary := range report.Boundaries {
			unknownCallback = unknownCallback || boundary.Kind == "partial_function_call" && strings.HasSuffix(boundary.Node, "::Callback")
			sql = sql || boundary.Kind == "unresolved_sql_handle" && strings.Contains(boundary.Evidence.Snippet, "db.Exec")
		}
		if !callback || !unknownCallback || !sql || !signal {
			t.Errorf("callback=%v outside callback=%v sql=%v signal=%v", callback, unknownCallback, sql, signal)
		}
		if !tables["table:known:channel_records"] || !tables["table:channel_records"] {
			t.Errorf("channel SQL receiver candidates=%v", tables)
		}
		escaped, exported := false, false
		for _, edge := range report.Relationships {
			escaped = escaped || edge.Kind == "error_compare" && strings.HasSuffix(edge.From, "::escaped") && strings.HasPrefix(edge.To, "error-input:")
			exported = exported || edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.From, "::Export") && strings.HasSuffix(edge.To, "::escaped")
		}
		if !escaped || !exported {
			t.Error("callback sent through exported channel lost outside inputs")
		}
		outsideChannel := false
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "outside_channel" && boundary.Evidence.File == "channels.go" && boundary.Evidence.Line > 0 {
				outsideChannel = true
			}
		}
		if !outsideChannel {
			t.Error("missing source-backed outside channel boundary")
		}
	}
}
