package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestUnresolvedFunctionCallsExposeEachExecutionForm(t *testing.T) {
	seeds := []string{"UnknownFunctionCalls", "KnownFunctionCalls", "SliceFunctionIterationCaller", "Start", "DualContexts"}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: seeds, Depth: 2, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: seeds, Depth: 2, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for kind, snippet := range map[string]string{"unresolved_function_call": "callback()", "deferred_unresolved_function_call": "defer callback()", "goroutine_unresolved_function_call": "go callback()"} {
			found := false
			for _, boundary := range report.Boundaries {
				found = found || boundary.Kind == kind && strings.HasSuffix(boundary.Node, "::UnknownFunctionCalls") && boundary.Evidence.Snippet == snippet
			}
			if !found {
				t.Errorf("missing source-backed %s", kind)
			}
		}
		for _, boundary := range report.Boundaries {
			if strings.Contains(boundary.Kind, "unresolved_function_call") && (!strings.HasSuffix(boundary.Node, "::UnknownFunctionCalls") || strings.Contains(boundary.Evidence.Snippet, "copy(")) {
				t.Errorf("resolved/builtin/modeled iterator falsely unresolved: %+v", boundary)
			}
		}
	}
}
