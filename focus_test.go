package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestContractFocusProvidesPathsWithoutChangingScope(t *testing.T) {
	opts := Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 200}
	plain, err := Trace(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Focus = []string{"Validate"}
	focused, err := Trace(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(focused.Nodes) != len(plain.Nodes) || len(focused.Relationships) != len(plain.Relationships) {
		t.Fatal("focus silently changed discovered scope")
	}
	if focused.Coverage.SourceSHA256 != plain.Coverage.SourceSHA256 {
		t.Fatal("exploration focus changed analysis source identity")
	}
	foundAnchor, foundPath := false, false
	for _, n := range focused.Nodes {
		if n.Name == "Validate" {
			foundAnchor = n.Relevance != nil && n.Relevance.Distance == 0 && n.Relevance.Anchor == n.ID
		}
		if n.Relevance != nil && n.Relevance.Distance > 0 {
			foundPath = foundPath || n.Relevance.Via != nil && (n.Relevance.Via.From == n.ID || n.Relevance.Via.To == n.ID)
		}
	}
	if !foundAnchor || !foundPath {
		t.Fatalf("focus paths missing: anchor=%t path=%t", foundAnchor, foundPath)
	}
}

func TestContractFocusSavedExplorationAndOutsideScope(t *testing.T) {
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Focus: []string{"Validate"}, Depth: 1, MaxNodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range analysis.Nodes {
		if n.Relevance != nil {
			t.Fatal("query-specific relevance leaked into saved graph")
		}
	}
	data, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Validate"}, Focus: []string{"ManagedTask"}, Depth: 1, MaxNodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	outside := false
	for _, b := range r.Boundaries {
		outside = outside || b.Kind == "focus_outside_scope"
	}
	if !outside || len(r.Focus) != 1 {
		t.Fatal("discovered focus outside scope was silently lost")
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Validate"}, Focus: []string{"missing-contract-anchor"}, Depth: 1, MaxNodes: 2}); err == nil {
		t.Fatal("missing focus accepted")
	}
}

func TestContractFocusPathsRetainUncertaintyAndDisconnectedNodes(t *testing.T) {
	ix := &index{funcs: map[string]*function{}}
	r := Report{Nodes: []Node{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"}, {ID: "z", Name: "Z"}}, Relationships: []Relationship{
		{From: "a", To: "b", Kind: "call", Certainty: "fact", Evidence: Evidence{File: "main.go", Line: 10}},
		{From: "b", To: "c", Kind: "possible_interface_call", Certainty: "possible", Evidence: Evidence{File: "main.go", Line: 20}},
	}}
	for _, n := range r.Nodes {
		ix.funcs[n.ID] = &function{node: n}
	}
	if err := ix.applyFocus(&r, []string{"C", "c"}); err != nil {
		t.Fatal(err)
	}
	if len(r.Focus) != 1 {
		t.Fatal("duplicate focus anchors were not canonicalized")
	}
	if r.Nodes[0].Relevance == nil || r.Nodes[0].Relevance.Distance != 2 || r.Nodes[0].Relevance.Via.To != "b" {
		t.Fatal("two-hop focus path missing")
	}
	if r.Nodes[1].Relevance == nil || r.Nodes[1].Relevance.Distance != 1 || r.Nodes[1].Relevance.Via.Certainty != "possible" || r.Nodes[1].Relevance.Via.Evidence.Line != 20 {
		t.Fatal("possible edge lost uncertainty or source evidence")
	}
	if r.Nodes[3].Relevance != nil {
		t.Fatal("disconnected node received an invented path")
	}
	ix.funcs["other"] = &function{node: Node{ID: "other", Name: "C"}}
	if err := ix.applyFocus(&r, []string{"C"}); err == nil {
		t.Fatal("ambiguous focus name accepted")
	}
}
