package contracttrace

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestGroupedCandidatesRemainInspectableAfterSerialization(t *testing.T) {
	want := []string{"external.a", "external.b", "external.c", "external.d", "external.e", "external.f", "external.g"}
	r := Report{}
	// Duplicates and out-of-order discovery must not lose any distinct target.
	for _, name := range []string{"external.g", "external.b", "external.a", "external.e", "external.f", "external.c", "external.d", "external.b"} {
		r.Boundaries = append(r.Boundaries, Boundary{Node: "caller", Kind: "external_call", Reason: "external bodies excluded", Examples: []string{name}})
	}
	groupBoundaries(&r)
	bytes, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var restored Report
	if err := json.Unmarshal(bytes, &restored); err != nil {
		t.Fatal(err)
	}
	groupBoundaries(&restored)
	if len(restored.Boundaries) != 1 {
		t.Fatalf("want one grouped source site, got %d", len(restored.Boundaries))
	}
	b := restored.Boundaries[0]
	if b.CandidateCount != 7 || !reflect.DeepEqual(b.Candidates, want) || !reflect.DeepEqual(b.Examples, want[:5]) {
		t.Fatalf("discovered targets lost across grouping/serialization: %+v", b)
	}
}

func TestCandidateInventoryAvailabilityIsExplicitInMarkdown(t *testing.T) {
	legacy := Markdown(Report{Boundaries: []Boundary{{CandidateCount: 9, Examples: []string{"a"}}}})
	if !strings.Contains(legacy, "1 entries lack a full retained candidate inventory") {
		t.Fatal("older snapshot lost targets without a visible warning")
	}
	fresh := Markdown(Report{Boundaries: []Boundary{{CandidateCount: 1, Examples: []string{"a"}, Candidates: []string{"a"}}}})
	if strings.Contains(fresh, "entries lack") {
		t.Fatal("fully retained inventory was reported missing")
	}
}

func TestFrontierRetainsEachSourceSite(t *testing.T) {
	ix := &index{funcs: map[string]*function{
		"seed":   {node: Node{ID: "seed", Name: "Seed"}},
		"target": {node: Node{ID: "target", Name: "Target"}},
	}, edges: []Relationship{
		{From: "seed", To: "target", Kind: "call", Evidence: Evidence{File: "main.go", Line: 10}},
		{From: "seed", To: "target", Kind: "call", Evidence: Evidence{File: "main.go", Line: 20}},
	}}
	r, err := ix.expand(Options{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 1}, Coverage{})
	if err != nil {
		t.Fatal(err)
	}
	groupBoundaries(&r)
	if len(r.Boundaries) != 2 || r.Boundaries[0].Evidence.Line != 10 || r.Boundaries[1].Evidence.Line != 20 {
		t.Fatalf("distinct excluded call sites were collapsed: %+v", r.Boundaries)
	}
	for _, b := range r.Boundaries {
		if b.CandidateCount != 1 || !reflect.DeepEqual(b.Candidates, []string{"target"}) {
			t.Fatalf("call-site candidate missing: %+v", b)
		}
	}
}
