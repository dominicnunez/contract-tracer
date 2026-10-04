package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScalarBranchSelectionReachesReturnsAndCallers(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/control\n\ngo 1.26.0\n",
		"control.go": `package control
func choose(n int) bool { if n > 0 { return true }; return false }
func Caller(n int) bool { return choose(n) }
func Phi(n int) int { result := 1; if n > 0 { result = 2 }; return result }
func consume(n int) {}
func Conditional(n int) { if n > 0 { consume(1) } }
func Independent(n int) bool { if n > 0 { consume(1) }; return false }
func Loop(n int) int { result := 0; for n > 0 { result++; n-- }; return result }
func Memory(n int) int { value := new(int); if n > 0 { *value = 1 }; return *value }
func Map(n int) int { values := map[int]int{}; if n > 0 { values[0] = 1 }; return values[0] }
func Channel(n int) int { ch := make(chan int,1); if n > 0 { ch <- 1 }; return <-ch }
func Diverge(n int) int { if n > 0 { for {} }; return 1 }
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"choose", "Caller", "Phi", "Conditional", "Independent", "Loop", "Memory", "Map", "Channel", "Diverge"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		divergence := false
		for _, boundary := range report.Boundaries {
			divergence = divergence || strings.HasSuffix(boundary.Node, "::Diverge") && boundary.Kind == "scalar_control_nonterminating"
		}
		if !divergence {
			t.Error("nonterminating control path was not disclosed")
		}
		for owner, kind := range map[string]string{"choose": "scalar_control_return", "Caller": "scalar_return", "Phi": "scalar_return", "Conditional": "scalar_control_call", "Loop": "scalar_return", "Memory": "scalar_return", "Map": "scalar_return", "Channel": "scalar_return"} {
			found := false
			for _, edge := range report.Relationships {
				found = found || edge.Kind == kind && strings.HasSuffix(edge.From, "::"+owner) && edge.To == "scalar-parameter:example.com/control."+owner+":0"
			}
			if !found {
				t.Errorf("%s missing %s input influence", owner, kind)
			}
		}
		for _, edge := range report.Relationships {
			if strings.HasSuffix(edge.From, "::Independent") && (edge.Kind == "scalar_return" || edge.Kind == "scalar_control_return") && edge.To == "scalar-parameter:example.com/control.Independent:0" {
				t.Error("unrelated branch contaminated unconditional return")
			}
		}
	}
}
