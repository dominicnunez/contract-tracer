package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestValueReceiverCopiesDistinguishPointersAndMethodExpressions(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/receivers\n\ngo 1.27.0\n",
		"receivers.go": `package receivers
type State struct { count int }
func (State) Value() {}
func (*State) Pointer() {}
type Wrapper struct { *State }
func Direct(s State) { s.Value() }
func Dereference(s *State) { s.Value() }
func Bound(s *State) func() { return s.Value }
func Promoted(w *Wrapper) { w.Value() }
func Expression(s *State) { (*State).Value(s) }
func Unbound() { _ = State.Value }
func PointerCall(s *State) { s.Pointer() }
func PointerBound(s *State) func() { return s.Pointer }
type Values interface { Value() }
func InterfaceCall(v Values) { v.Value() }
func Parenthesized(s *State) { ((*State).Value)(s) }
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Direct", "Dereference", "Bound", "Promoted", "Expression", "Unbound", "PointerCall", "PointerBound", "InterfaceCall", "Parenthesized", "State.Value", "State.Pointer"}
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
		formal := false
		unknown := false
		for _, edge := range report.Relationships {
			if edge.To == "field:example.com/receivers::State.count" && edge.Kind == "field_receiver_copy" {
				seen[edge.From] = true
			}
			formal = formal || edge.From == "example.com/receivers::State.Value" && edge.To == "field:example.com/receivers::State.count" && edge.Kind == "field_receiver_parameter"
			if edge.From == "example.com/receivers::State.Pointer" && edge.Kind == "field_receiver_parameter" {
				t.Error("pointer formal classified as copied record")
			}
		}
		for _, boundary := range report.Boundaries {
			unknown = unknown || boundary.Node == "example.com/receivers::InterfaceCall" && boundary.Kind == "unresolved_record_receiver"
		}
		if !formal || !unknown {
			t.Errorf("formal value receiver=%t dynamic interface boundary=%t", formal, unknown)
		}
		for _, owner := range []string{"Direct", "Dereference", "Bound", "Promoted", "Expression", "Parenthesized"} {
			if !seen["example.com/receivers::"+owner] {
				t.Errorf("missing value receiver copy in %s", owner)
			}
		}
		for _, owner := range []string{"Unbound", "PointerCall", "PointerBound", "InterfaceCall"} {
			if seen["example.com/receivers::"+owner] {
				t.Errorf("invented receiver copy in %s", owner)
			}
		}
	}
}
