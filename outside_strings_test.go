package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutsideStringsKeepFormatUncertaintyAndPrivateControls(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/stringinputs\n\ngo 1.26.0\n",
		"strings.go": `package stringinputs
import ("errors"; "fmt"; "database/sql")
func source() error { return errors.New("local") }
type Record struct { Format string; hidden string }
var Format = "%v"
var privateFormat = "%v"
var Shared = &Record{Format: "%v", hidden: "%v"}
var alias = Shared
func Global() error { return fmt.Errorf(Format, source()) }
func Private() error { return fmt.Errorf(privateFormat, source()) }
func Alias() error { return fmt.Errorf(alias.Format, source()) }
func Hidden() error { return fmt.Errorf(Shared.hidden, source()) }
func Field(r *Record) error { return fmt.Errorf(r.Format, source()) }
func Slice(formats []string) error { return fmt.Errorf(formats[0], source()) }
func Map(formats map[string]string) error { return fmt.Errorf(formats["key"], source()) }
func Channel(formats <-chan string) error { return fmt.Errorf(<-formats, source()) }
func Pointer(format *string) error { return fmt.Errorf(*format, source()) }
func closed(r *Record) error { return fmt.Errorf(r.Format, source()) }
func Locals() { r := &Record{Format: "%v"}; _ = Field(r); _ = closed(r); _ = Slice([]string{"%v"}); _ = Map(map[string]string{"key":"%v"}); ch := make(chan string,1); ch <- "%v"; _ = Channel(ch); s := "%v"; _ = Pointer(&s) }
var Query = "select * from known_records"
var privateQuery = "select * from known_records"
func Prepared(db *sql.DB) { stmt, _ := db.Prepare(Query); stmt.Exec() }
func ClosedPrepared(db *sql.DB) { stmt, _ := db.Prepare(privateQuery); stmt.Exec() }
func other() error { return errors.New("other") }
func ZLookup(key string) error { m := map[string]func() error{"first": source, "second": other}; return m[key]() }
func closedLookup(key string) error { m := map[string]func() error{"first": source, "second": other}; return m[key]() }
func ALookupCaller() error { _ = ZLookup("first"); return closedLookup("first") }
func AFormat() string { return "%v" }
func AResults() { _ = Result(AFormat); _ = closedResult(AFormat) }
func Result(factory func() string) error { return fmt.Errorf(factory(), source()) }
func closedResult(factory func() string) error { return fmt.Errorf(factory(), source()) }
func ATupleFormat() (int, string) { return 1, "%v" }
func ATuples() { _ = Tuple(ATupleFormat); _ = closedTuple(ATupleFormat) }
func Tuple(factory func() (int, string)) error { _, format := factory(); return fmt.Errorf(format, source()) }
func closedTuple(factory func() (int, string)) error { _, format := factory(); return fmt.Errorf(format, source()) }
func AAssertions() { _ = Assert("%v"); _ = closedAssert("%v") }
func Assert(value any) error { return fmt.Errorf(value.(string), source()) }
func closedAssert(value any) error { return fmt.Errorf(value.(string), source()) }
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"source", "Prepared", "ClosedPrepared", "other"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 8, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 8, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		origins := map[string]bool{}
		for _, node := range report.Nodes {
			if node.Kind == "error_result" && strings.Contains(node.Evidence.Snippet, `errors.New("local")`) {
				origins[node.ID] = true
			}
		}
		if len(origins) != 1 {
			t.Fatal("missing source error")
		}
		for _, owner := range []string{"Global", "Private", "Alias", "Hidden", "Field", "Slice", "Map", "Channel", "Pointer", "closed", "Result", "closedResult", "Tuple", "closedTuple", "Assert", "closedAssert"} {
			cause, boundary := false, false
			for _, edge := range report.Relationships {
				cause = cause || strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "error_return" && origins[edge.To]
			}
			for _, item := range report.Boundaries {
				boundary = boundary || item.Kind == "error_wrap_format" && strings.HasSuffix(item.Node, "::"+owner)
			}
			outside := owner != "Private" && owner != "Hidden" && owner != "closed" && owner != "closedResult" && owner != "closedTuple" && owner != "closedAssert"
			if cause != outside || boundary != outside {
				t.Errorf("%s cause=%v boundary=%v expected outside=%v", owner, cause, boundary, outside)
			}
		}
		for _, owner := range []string{"Prepared", "ClosedPrepared"} {
			known, outside := false, false
			for _, edge := range report.Relationships {
				known = known || edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::"+owner) && edge.To == "table:known_records"
			}
			for _, boundary := range report.Boundaries {
				outside = outside || boundary.Kind == "unresolved_prepared_sql" && strings.HasSuffix(boundary.Node, "::"+owner)
			}
			if !known || outside != (owner == "Prepared") {
				t.Errorf("%s known query=%v outside query=%v", owner, known, outside)
			}
		}
		other := map[string]bool{}
		for _, node := range report.Nodes {
			if node.Kind == "error_result" && strings.Contains(node.Evidence.Snippet, `errors.New("other")`) {
				other[node.ID] = true
			}
		}
		if len(other) != 1 {
			t.Fatal("missing second map target's error")
		}
		for _, owner := range []string{"ZLookup", "closedLookup"} {
			found := false
			for _, edge := range report.Relationships {
				found = found || edge.Kind == "error_return" && strings.HasSuffix(edge.From, "::"+owner) && other[edge.To]
			}
			if found != (owner == "ZLookup") {
				t.Errorf("%s second map callback=%v", owner, found)
			}
		}
	}
}
