package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutsideAggregateErrorsRetainOriginsAndPrivateControl(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/aggregateerrors\n\ngo 1.27.0\n",
		"input.go": `package aggregateerrors
import "errors"
type Record struct { E error; Next *Record; hidden error }
func source() error { return errors.New("local") }
func Public(r *Record) bool { return r.E != nil }
func Nested(r *Record) error { return r.Next.E }
func Slice(es []error) error { return es[0] }
func Map(es map[string]error) error { return es["key"] }
func Key(es map[error]int) error { for e := range es { return e }; return nil }
func Pointer(e **error) error { return **e }
func callback(r *Record) bool { return r.E != nil }
func Factory() func(*Record) bool { return callback }
func closed(r *Record) bool { return r.E != nil }
func Local() bool { r := &Record{E: source()}; return Public(r) || callback(r) || closed(r) }
func Hidden(r *Record) error { return r.hidden }
func Result(factory func() *Record) error { return factory().E }
func closedResult(factory func() *Record) error { return factory().E }
func localRecord() *Record { return &Record{E: source()} }
func Results() error { _ = Result(localRecord); return closedResult(localRecord) }
type Wrapper struct { E error }
func (Wrapper) Error() string { return "wrapper" }
func Concrete(w Wrapper) error { return w.E }
func ConcretePointer(w *Wrapper) error { return w.E }
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Public", "Nested", "Slice", "Map", "Key", "Pointer", "callback", "closed", "Hidden", "Result", "closedResult", "Concrete", "ConcretePointer"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 4, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 4, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, owner := range seeds {
			outside, local := false, false
			for _, edge := range report.Relationships {
				if !strings.HasSuffix(edge.From, "::"+owner) || (edge.Kind != "error_compare" && edge.Kind != "error_return") {
					continue
				}
				outside = outside || strings.HasPrefix(edge.To, "error-input:")
				for _, node := range report.Nodes {
					if node.ID == edge.To && node.Kind == "error_result" && strings.Contains(node.Evidence.Snippet, `errors.New("local")`) {
						local = true
					}
				}
			}
			if outside != (owner != "closed" && owner != "Hidden" && owner != "closedResult") {
				t.Errorf("%s outside=%v", owner, outside)
			}
			if (owner == "Public" || owner == "callback" || owner == "closed" || owner == "Result" || owner == "closedResult") && !local {
				t.Errorf("%s lost local origin", owner)
			}
		}
		found := false
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "outside_error_input" && boundary.Evidence.File == "input.go" && boundary.Evidence.Line > 0 {
				found = true
			}
		}
		if !found {
			t.Error("missing source-backed aggregate error boundary")
		}
	}
}
