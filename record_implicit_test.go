package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestImplicitRecordResultsAndBindingsRetainFieldRoles(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/implicitrecords\n\ngo 1.27.0\n",
		"records.go": `package implicitrecords
type State struct { count int }
type Other struct { flag bool }
var factory func() (State, Other)
func Forward() (State, Other) { return factory() }
func Named() (s State) { return }
func Binding(s State) { x := s; _ = x }
func DeclBinding(s State) { var x = s; _ = x }
func Pointer() (s *State) { return }
func Unnamed() State { panic("no return") }
func Closure() (s State) { f := func() (o Other) { return }; _ = f; panic("no outer return") }
func TupleBinding() { x, y := factory(); _, _ = x, y }
func Blank() { var _ State }
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Forward", "Named", "Binding", "DeclBinding", "Pointer", "Unnamed", "Closure", "TupleBinding", "Blank"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		seen := map[string]bool{}
		for _, edge := range report.Relationships {
			seen[edge.From+"/"+edge.Kind+"/"+edge.To] = true
			if edge.From == "example.com/implicitrecords::Closure" && edge.Kind == "field_record_read" && edge.To == "field:example.com/implicitrecords::State.count" {
				t.Error("closure bare return borrowed outer result")
			}
			if edge.From == "example.com/implicitrecords::Blank" && edge.Kind == "field_zero_initialize" {
				t.Error("discarded blank variable invented record storage")
			}
			if (edge.From == "example.com/implicitrecords::Pointer" || edge.From == "example.com/implicitrecords::Unnamed") && edge.Kind == "field_zero_initialize" {
				t.Error("non-record pointer or unnamed result gets invented record initialization")
			}
		}
		for _, expected := range []struct{ owner, kind, field string }{
			{"Forward", "field_record_read", "State.count"},
			{"Forward", "field_record_read", "Other.flag"},
			{"Named", "field_zero_initialize", "State.count"},
			{"Named", "field_record_read", "State.count"},
			{"Binding", "field_record_write", "State.count"},
			{"DeclBinding", "field_record_write", "State.count"},
			{"TupleBinding", "field_record_write", "State.count"},
			{"TupleBinding", "field_record_write", "Other.flag"},
			{"Closure", "field_record_read", "Other.flag"},
			{"Closure", "field_zero_initialize", "Other.flag"},
		} {
			if !seen["example.com/implicitrecords::"+expected.owner+"/"+expected.kind+"/field:example.com/implicitrecords::"+expected.field] {
				t.Errorf("missing %s %s %s", expected.owner, expected.kind, expected.field)
			}
		}
	}
}
