package contracttrace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedAnalysisExpandsBeyondFirstScope(t *testing.T) {
	options := Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 2}
	first, analysis, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Coverage.Truncated {
		t.Fatal("fixture must start with a truncated scope")
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"WrappedWrite"}, Depth: 4, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(expanded, "AuditRead") || expanded.Coverage.SourceSHA256 != first.Coverage.SourceSHA256 || expanded.ContractComplete {
		t.Fatal("saved graph did not preserve source identity and reveal previously excluded storage paths")
	}
	for _, boundary := range append(append([]Boundary(nil), saved.Boundaries...), expanded.Boundaries...) {
		if boundary.CandidateCount > 0 && len(boundary.Candidates) != boundary.CandidateCount {
			t.Fatalf("fresh saved/explored candidate inventory incomplete: %+v", boundary)
		}
	}
	located, err := Explore(context.Background(), saved, ExploreOptions{Locations: []string{"sample.go:8"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(located, "Validate") {
		t.Fatal("saved graph lost function-containing source locations")
	}
	// Caller-owned seed slices must not be mutated by exploration.
	if len(first.Nodes) != 2 {
		t.Fatal("exploration changed original scope")
	}
}

func TestSavedAnalysisRejectsChangedSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/snapshot\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package snapshot\nfunc Seed() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package snapshot\nfunc Seed() { panic(1) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Explore(context.Background(), analysis, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 10}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale snapshot accepted: %v", err)
	}
}

func TestSavedAnalysisPreservesGroupedBoundaryCounts(t *testing.T) {
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 10})
	if err != nil {
		t.Fatal(err)
	}
	analysis.Boundaries = append(analysis.Boundaries, Boundary{Node: "example.com/sample::Validate", Kind: "external_call", Reason: "summary from original analysis", CandidateCount: 17, Examples: []string{"external.a", "external.b", "external.c", "external.d", "external.e"}})
	report, err := Explore(context.Background(), analysis, ExploreOptions{Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range report.Boundaries {
		if b.Reason == "summary from original analysis" {
			found = true
			if b.CandidateCount != 17 || len(b.Candidates) != 0 {
				t.Fatalf("grouped count shrank from 17 to %d", b.CandidateCount)
			}
		}
	}
	if !found {
		t.Fatal("original analysis boundary disappeared")
	}
}

func TestSavedAnalysisRejectsMalformedGraph(t *testing.T) {
	_, original, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 10})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(original)
	cases := map[string]func(*Analysis){
		"duplicate node":           func(a *Analysis) { a.Nodes = append(a.Nodes, a.Nodes[0]) },
		"query-specific relevance": func(a *Analysis) { a.Nodes[0].Relevance = &Relevance{Anchor: "fake", Distance: 0} },
		"missing edge target":      func(a *Analysis) { a.Relationships[0].To = "missing" },
		"missing boundary owner": func(a *Analysis) {
			a.Boundaries = append(a.Boundaries, Boundary{Node: "missing", Kind: "external_call"})
		},
		"negative candidate count": func(a *Analysis) { a.Boundaries = append(a.Boundaries, Boundary{CandidateCount: -1}) },
		"outside embedded asset":   func(a *Analysis) { a.Coverage.EmbeddedFiles = []string{"../outside.txt"} },
		"partial candidate inventory": func(a *Analysis) {
			a.Boundaries = append(a.Boundaries, Boundary{CandidateCount: 2, Candidates: []string{"a"}})
		},
		"duplicate candidates": func(a *Analysis) {
			a.Boundaries = append(a.Boundaries, Boundary{CandidateCount: 2, Candidates: []string{"a", "a"}})
		},
		"unsorted candidates": func(a *Analysis) {
			a.Boundaries = append(a.Boundaries, Boundary{CandidateCount: 2, Candidates: []string{"b", "a"}})
		},
		"missing example target": func(a *Analysis) {
			a.Boundaries = append(a.Boundaries, Boundary{CandidateCount: 1, Candidates: []string{"a"}, Examples: []string{"b"}})
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var a Analysis
			if err := json.Unmarshal(encoded, &a); err != nil {
				t.Fatal(err)
			}
			change(&a)
			data, _ := json.Marshal(a)
			if _, err := ReadAnalysis(strings.NewReader(string(data))); err == nil {
				t.Fatal("malformed graph accepted")
			}
		})
	}
}
