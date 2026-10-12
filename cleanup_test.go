package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDeferredCleanupOwnershipAndReachableExits(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"CleanupBranches"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	registrations := map[string]bool{}
	exits := map[string]Node{}
	ordinary := ""
	for _, n := range r.Nodes {
		if n.Kind == "function_exit" {
			exits[n.ID] = n
		}
		if n.Kind == "channel" && strings.Contains(n.Evidence.Snippet, "ordinary :=") {
			ordinary = n.ID
		}
	}
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::CleanupBranches") && e.Kind == "defer_registration" {
			registrations[e.To] = true
		}
	}
	if len(registrations) != 2 {
		t.Fatalf("want cancel and close registrations, got %d", len(registrations))
	}
	cancel, close := false, false
	for registration := range registrations {
		returnExit, panicExit := false, false
		for _, e := range r.Relationships {
			if e.From != registration {
				continue
			}
			switch e.Kind {
			case "cleanup_cancel":
				cancel = strings.HasPrefix(e.To, "context:")
			case "cleanup_close":
				if e.To == ordinary {
					t.Fatal("ordinary close was assigned to a defer registration")
				}
				close = strings.HasPrefix(e.To, "channel:")
			case "cleanup_exit_candidate":
				n := exits[e.To]
				if n.Evidence.Line == 7 {
					t.Fatal("return before registration was treated as its cleanup exit")
				}
				returnExit = returnExit || n.Name == "return exit"
				panicExit = panicExit || n.Name == "panic exit"
			}
		}
		if !returnExit || !panicExit {
			t.Fatalf("registration %s missed return/panic paths", registration)
		}
	}
	if !cancel || !close {
		t.Fatalf("resource ownership missing: cancel=%t close=%t", cancel, close)
	}
}

func TestDeferredHelperAndUnresolvedLoopSurviveSavedExploration(t *testing.T) {
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"DeferredHelper"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"DeferredHelper", "DeferredLoop"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	helper, loop := false, false
	for _, e := range r.Relationships {
		if e.Kind == "cleanup_call" && strings.HasSuffix(e.To, "::CleanupHelper") {
			helper = true
		}
	}
	for _, b := range r.Boundaries {
		if b.Kind == "cleanup_exit_unresolved" && strings.Contains(b.Evidence.Snippet, "defer func()") {
			loop = true
		}
	}
	if !helper || !loop {
		t.Fatalf("saved lifecycle scope missed helper or loop boundary: helper=%t loop=%t", helper, loop)
	}
}
