package contracttrace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorResultInvestigationFlow(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/errorflow\n\ngo 1.26.0\n",
		"error.go": `package errorflow
import "errors"
func fail() error { return errors.New("failure") }
func Relay() error { return fail() }
func Check() bool { return Relay() != nil }
func Discard() { _ = Relay(); defer Relay(); go Relay() }
func Pair() (int, error) { return 1, Relay() }
func ReturnPair() error { _, err := Pair(); return err }
type record struct { E error }
func ThroughStorage() error { r := &record{E: Relay()}; return r.E }
func UseError(err error) bool { return err != nil }
func Pass() bool { return UseError(Relay()) }
func DiscardTuple() int { n, _ := Pair(); return n }
func UsedTuple() error { _, err := Pair(); return err }
func Channel() error { ch := make(chan error, 1); ch <- Relay(); return <-ch }
type provider interface { Failure() error }
type localProvider struct{}
func (localProvider) Failure() error { return Relay() }
func Interface(p provider) bool { return p.Failure() != nil }
func SeedInterface() bool { return Interface(localProvider{}) }
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	report, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"fail"}, Depth: 8, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []Report{report} {
		for _, expected := range []struct{ owner, kind string }{
			{"Check", "error_compare"}, {"ReturnPair", "error_return"},
			{"ThroughStorage", "error_return"}, {"Pass", "error_argument"},
			{"UseError", "error_compare"}, {"Discard", "error_result_unused"},
			{"Channel", "error_return"}, {"Interface", "error_compare"}, {"DiscardTuple", "error_result_unused"},
			{"Discard", "deferred_error_result_discard"}, {"Discard", "goroutine_error_result_discard"},
		} {
			found := false
			for _, edge := range document.Relationships {
				if strings.HasSuffix(edge.From, "::"+expected.owner) && edge.Kind == expected.kind && strings.HasPrefix(edge.To, "error-result:") {
					found = true
				}
			}
			if !found {
				t.Errorf("missing %s in %s", expected.kind, expected.owner)
			}
		}
	}
	for _, owner := range []string{"UseError", "ThroughStorage", "Channel", "Interface"} {
		found := false
		for _, edge := range report.Relationships {
			if !strings.HasSuffix(edge.From, "::"+owner) {
				continue
			}
			for _, node := range report.Nodes {
				if node.ID == edge.To && node.Kind == "error_result" && node.Evidence.Line == 3 {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s lost the original errors.New result", owner)
		}
	}
	for _, edge := range report.Relationships {
		if strings.HasSuffix(edge.From, "::UsedTuple") && edge.Kind == "error_result_unused" {
			t.Error("used tuple error classified as unused")
		}
		if strings.HasSuffix(edge.From, "::DiscardTuple") && edge.Kind == "error_result_unused" && edge.Slot != "result:1" {
			t.Error("integer tuple slot classified as an error")
		}
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Check"}, Depth: 8, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(resumed, "ThroughStorage") {
		t.Fatal("saved error investigation lost storage sibling")
	}
}

func TestRecursiveErrorForwardingDoesNotInventDistinctOrigins(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recursiveerror\n\ngo 1.26.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	source := "package recursiveerror\nimport \"errors\"\nfunc Recurse(n int) error {\nif n == 0 { return errors.New(\"failure\") }\n"
	for i := 1; i <= 80; i++ {
		source += fmt.Sprintf("if n == %d { return Recurse(n-1) }\n", i)
	}
	source += "return nil\n}\n"
	if err := os.WriteFile(filepath.Join(root, "error.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"Recurse"}, Depth: 3, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	if r.Coverage.ValueFlow.Widened {
		t.Fatal("forwarding one error through recursive call sites exhausted origin budget")
	}
	sites := 0
	for _, edge := range r.Relationships {
		if edge.Kind == "error_result" {
			sites++
		}
	}
	if sites != 81 {
		t.Fatalf("lost source call result sites: %d", sites)
	}
	original := ""
	for _, node := range r.Nodes {
		if node.Kind == "error_result" && node.Evidence.Line == 4 {
			original = node.ID
		}
	}
	found := false
	for _, edge := range r.Relationships {
		if edge.Kind == "error_return" && edge.To == original {
			found = true
		}
	}
	if !found || original == "" {
		t.Fatal("recursive forwarding lost original dependency error result")
	}
}
