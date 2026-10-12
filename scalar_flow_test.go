package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScalarOriginsCrossArithmeticStorageCallsAndDecisions(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/scalars\n\ngo 1.27.0\n",
		"flow.go": `package scalars
type Account struct { Count int }
func Adjust(n int) int { return n + 1 }
func Limit(n int) bool { return Adjust(n) <= 10 }
func Storage(n int) bool { r := &Account{Count: Adjust(n)}; return r.Count < 0 }
func Channel(n int) bool { ch := make(chan int, 1); ch <- Adjust(n); return <-ch > 0 }
func Negated(n int) bool { return !Limit(-n) }
func cleanup(n int) {}
func Conditional(n int) { if Limit(n) { cleanup(n) } }
func Defer(n int) { defer cleanup(n); go cleanup(n) }
func Independent(n int) bool { _ = Adjust(n); return false }
func PublicField(r *Account) bool { return r.Count > 0 }
var Shared int
func Global(n int) bool { Shared = Adjust(n); return Shared > 0 }
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Adjust", "Limit", "Storage", "Channel", "Negated", "Conditional", "Defer", "Independent", "PublicField", "Global"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, owner := range []string{"Adjust", "Limit", "Storage", "Channel", "Negated", "Global", "Independent"} {
			origin := "scalar-parameter:example.com/scalars." + owner + ":0"
			found := false
			for _, edge := range report.Relationships {
				found = found || edge.Kind == "scalar_return" && strings.HasSuffix(edge.From, "::"+owner) && edge.To == origin
			}
			if found != (owner != "Independent") {
				t.Errorf("%s retained own input count=%v", owner, found)
			}
		}
		for _, owner := range []string{"Conditional", "Defer"} {
			kind := "scalar_condition"
			if owner == "Defer" {
				kind = "scalar_argument"
			}
			found := false
			for _, edge := range report.Relationships {
				found = found || edge.Kind == kind && strings.HasSuffix(edge.From, "::"+owner) && edge.To == "scalar-parameter:example.com/scalars."+owner+":0"
			}
			if !found {
				t.Errorf("%s missing %s input provenance", owner, kind)
			}
		}
		outside := false
		for _, edge := range report.Relationships {
			outside = outside || edge.Kind == "scalar_return" && strings.HasSuffix(edge.From, "::PublicField") && strings.HasPrefix(edge.To, "scalar-input:")
		}
		if !outside {
			t.Error("public count field lost outside origin")
		}
		model := false
		for _, boundary := range report.Boundaries {
			model = model || boundary.Kind == "scalar_flow_model"
		}
		if !model {
			t.Error("missing scalar model limitations")
		}
	}
}
