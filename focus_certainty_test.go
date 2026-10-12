package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestFocusCertaintySurvivesSavedExplorationAndKeepsUnknownAnnotations(t *testing.T) {
	options := Options{Root: "testdata/sample", Seeds: []string{"Entry"}, Focus: []string{"Validate"}, Depth: 2, MaxNodes: 100}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: options.Seeds, Focus: options.Focus, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		found := false
		for _, node := range report.Nodes {
			if node.Name == "Entry" {
				found = node.Relevance != nil && node.Relevance.PathCertainty == "possible"
			}
		}
		if !found {
			t.Error("interface caller path lost cumulative uncertainty")
		}
	}
	legacy := Report{Nodes: []Node{{ID: "a", Name: "A", Relevance: &Relevance{Anchor: "a"}}}}
	if !strings.Contains(Markdown(legacy), "Whole focus path: unknown") {
		t.Error("legacy annotation invented factual certainty")
	}
}

func TestFocusCertaintyIncludesEveryPathEdge(t *testing.T) {
	ix := &index{funcs: map[string]*function{}}
	report := Report{Nodes: []Node{{ID: "anchor", Name: "Anchor"}, {ID: "possible", Name: "Possible"}, {ID: "leaf", Name: "Leaf"}, {ID: "fact", Name: "Fact"}, {ID: "factleaf", Name: "FactLeaf"}}, Relationships: []Relationship{
		{From: "anchor", To: "possible", Kind: "possible_interface_call", Certainty: "possible"},
		{From: "possible", To: "leaf", Kind: "call", Certainty: "fact"},
		{From: "anchor", To: "fact", Kind: "call", Certainty: "fact"},
		{From: "fact", To: "factleaf", Kind: "call", Certainty: "fact"},
	}}
	for _, node := range report.Nodes {
		ix.funcs[node.ID] = &function{node: node}
	}
	if err := ix.applyFocus(&report, []string{"anchor"}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Nodes []struct {
			ID        string `json:"id"`
			Relevance struct {
				Certainty string `json:"path_certainty"`
			} `json:"contract_relevance"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for _, node := range document.Nodes {
		want := "fact"
		if node.ID == "possible" || node.ID == "leaf" {
			want = "possible"
		}
		if node.Relevance.Certainty != want {
			t.Errorf("%s path certainty=%q want %s", node.ID, node.Relevance.Certainty, want)
		}
	}
	text := Markdown(report)
	if !strings.Contains(text, "Whole focus path: possible") || !strings.Contains(text, "Whole focus path: fact") {
		t.Error("markdown omits cumulative path uncertainty")
	}
}
