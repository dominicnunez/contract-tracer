package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSharedGlobalContainerAccessSurvivesHelpersAndSavedAnalysis(t *testing.T) {
	seeds := []string{"global:example.com/sample::privateMutationMap", "global:example.com/sample::privateMutationSlice"}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: seeds, Depth: 1, MaxNodes: 100})
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
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: seeds, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, want := range []struct{ owner, global, kind, snippet string }{
			{"mutateSharedMap", "privateMutationMap", "global_write", `state["new"] =`},
			{"mutateSharedMap", "privateMutationMap", "global_write", "delete(state"},
			{"mutateSharedMap", "privateMutationMap", "global_write", "clear(state"},
			{"readSharedMap", "privateMutationMap", "global_read", `state["new"]`},
			{"readSharedMap", "privateMutationMap", "global_read", "range state"},
			{"mutateSharedSlice", "privateMutationSlice", "global_write", "state[0] ="},
			{"mutateSharedSlice", "privateMutationSlice", "global_write", "copy(state"},
			{"mutateSharedSlice", "privateMutationSlice", "global_write", "append(state"},
			{"mutateSharedSlice", "privateMutationSlice", "global_write", "clear(state"},
			{"readSharedSlice", "privateMutationSlice", "global_read", "copy(make"},
			{"readSharedSlice", "privateMutationSlice", "global_read", "return state[0]"},
			{"GlobalContainerCaller", "privateMutationSlice", "global_write", "defer copy"},
			{"GlobalContainerCaller", "privateMutationSlice", "global_write", "go copy"},
		} {
			found := false
			for _, edge := range report.Relationships {
				found = found || strings.HasSuffix(edge.From, "::"+want.owner) && edge.To == "global:example.com/sample::"+want.global && edge.Kind == want.kind && strings.Contains(edge.Evidence.Snippet, want.snippet)
			}
			if !found {
				t.Errorf("missing %s %s at %s: %s", want.global, want.kind, want.owner, want.snippet)
			}
		}
		if hasName(report, "mutateUnrelatedMap") {
			t.Fatal("unrelated map mutation joined shared state")
		}
	}
}
