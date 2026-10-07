package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWaitGroupConnectsWorkerAndJoinWithoutMergingGroups(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"GroupStart"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	group, unrelated := "", ""
	for _, e := range r.Relationships {
		if e.Kind != "waitgroup_add" || !strings.HasSuffix(e.From, "::GroupStart") {
			continue
		}
		if strings.Contains(e.Evidence.Snippet, "group.Add(1)") {
			group = e.To
		}
		if strings.Contains(e.Evidence.Snippet, "unrelated.Add(2)") {
			unrelated = e.To
		}
	}
	if group == "" || unrelated == "" || group == unrelated {
		t.Fatalf("distinct coordination identities missing: group=%s unrelated=%s", group, unrelated)
	}
	done, wait, deferred := false, false, false
	for _, e := range r.Relationships {
		if e.To == group && e.Kind == "waitgroup_done" && strings.HasSuffix(e.From, "::GroupWorker") {
			done = true
		}
		if e.To == group && e.Kind == "waitgroup_wait" && strings.HasSuffix(e.From, "::GroupWait") {
			wait = true
		}
		if e.To == group && e.Kind == "cleanup_waitgroup_done" {
			deferred = true
		}
		if e.To == unrelated && (strings.HasSuffix(e.From, "::GroupWorker") || strings.HasSuffix(e.From, "::GroupWait")) {
			t.Fatal("unrelated WaitGroup acquired worker/join relationships")
		}
	}
	if !done || !wait || !deferred {
		t.Fatalf("coordination path missing: done=%t wait=%t deferred=%t", done, wait, deferred)
	}
}

func TestWaitGroupGoExposesManagedTask(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"GroupGo"}, Depth: 3, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	group := ""
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::GroupGo") && e.Kind == "waitgroup_go" {
			group = e.To
		}
	}
	task, wait := false, false
	for _, e := range r.Relationships {
		if e.From == group && strings.HasSuffix(e.To, "::ManagedTask") && e.Kind == "waitgroup_task" {
			task = true
		}
		if e.To == group && e.Kind == "waitgroup_wait" {
			wait = true
		}
	}
	if group == "" || !task || !wait {
		t.Fatalf("managed coordination path missing: group=%s task=%t wait=%t", group, task, wait)
	}
}

func TestWaitGroupFieldsGlobalsAndUnknownReceiverSurviveExploration(t *testing.T) {
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"GroupFields"}, Depth: 3, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(strings.NewReader(string(bytes)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"GroupFields", "UnknownGroup"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	workers, other, global := "", "", ""
	negative := false
	for _, e := range r.Relationships {
		if !strings.HasSuffix(e.From, "::GroupFields") || e.Kind != "waitgroup_add" {
			continue
		}
		switch {
		case strings.Contains(e.Evidence.Snippet, "workers.Add"):
			workers = e.To
		case strings.Contains(e.Evidence.Snippet, "other.Add"):
			other = e.To
			negative = len(e.Values) == 1 && e.Values[0] == "integer:-1"
		case strings.Contains(e.Evidence.Snippet, "GlobalGroup.Add"):
			global = e.To
		}
	}
	if workers == "" || other == "" || global == "" || workers == other || workers == global || other == global || !negative {
		t.Fatalf("field/global identity or delta lost: workers=%s other=%s global=%s negative=%t", workers, other, global, negative)
	}
	done, wait, unknown := false, false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::GroupWorker") && e.Kind == "waitgroup_done" {
			done = done || e.To == workers
			if e.To == other || e.To == global {
				t.Fatal("separate field/global acquired worker completion")
			}
		}
		if strings.HasSuffix(e.From, "::GroupWait") && e.Kind == "waitgroup_wait" && e.To == global {
			wait = true
		}
	}
	for _, b := range r.Boundaries {
		unknown = unknown || b.Kind == "unresolved_waitgroup" && strings.HasSuffix(b.Node, "::UnknownGroup")
	}
	if !done || !wait || !unknown {
		t.Fatalf("saved coordination scope missing: done=%t wait=%t unknown=%t", done, wait, unknown)
	}
}

func TestBoundWaitGroupMethodsRetainReceiverAndCleanup(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"BoundGroup", "BoundGroupGo"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	group, managed, unrelated := "", "", ""
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::BoundGroup") && e.Kind == "waitgroup_add" && strings.Contains(e.Evidence.Snippet, "group.Add(1)") {
			group = e.To
		}
		if strings.HasSuffix(e.From, "::BoundGroupGo") && e.Kind == "waitgroup_wait" {
			managed = e.To
		}
		if strings.HasSuffix(e.From, "::BoundGroup") && e.Kind == "waitgroup_add" && strings.Contains(e.Evidence.Snippet, "unrelated.Add(2)") {
			unrelated = e.To
		}
	}
	done, wait, cleanup, task, delta := false, false, false, false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::BoundCompletion") && e.Kind == "waitgroup_done" {
			if e.To == unrelated {
				t.Fatal("bound receiver was merged with unrelated group")
			}
			done = done || e.To == group
		}
		if strings.HasSuffix(e.From, "::BoundJoin") && e.Kind == "waitgroup_wait" && e.To == group {
			wait = true
		}
		cleanup = cleanup || e.Kind == "cleanup_waitgroup_done" && e.To == group
		task = task || e.Kind == "waitgroup_task" && e.From == managed && strings.HasSuffix(e.To, "::ManagedTask")
		delta = delta || e.Kind == "waitgroup_add" && e.To == managed && len(e.Values) == 1 && e.Values[0] == "integer:0"
	}
	if group == "" || managed == "" || unrelated == "" || !done || !wait || !cleanup || !task || !delta {
		t.Fatalf("bound lifecycle path missing: done=%t wait=%t cleanup=%t task=%t delta=%t", done, wait, cleanup, task, delta)
	}
}

func TestBoundWaitGroupChoicesKeepOperationAndReceiverTogether(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"BoundGroupChoices"}, Depth: 3, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	first, second := "", ""
	for _, e := range r.Relationships {
		if e.Kind != "waitgroup_add" || !strings.HasSuffix(e.From, "::BoundGroupChoices") {
			continue
		}
		if strings.Contains(e.Evidence.Snippet, "first.Add") {
			first = e.To
		} else if strings.Contains(e.Evidence.Snippet, "second.Add") {
			second = e.To
		}
	}
	done, wait := false, false
	for _, e := range r.Relationships {
		if e.Kind == "cleanup_waitgroup_done" {
			if e.To != first {
				t.Fatal("Done candidate acquired Wait's receiver")
			}
			done = true
		}
		if e.Kind == "cleanup_waitgroup_wait" {
			if e.To != second {
				t.Fatal("Wait candidate acquired Done's receiver")
			}
			wait = true
		}
	}
	if first == "" || second == "" || first == second || !done || !wait {
		t.Fatalf("mixed closure candidates lost correlation: first=%s second=%s done=%t wait=%t", first, second, done, wait)
	}
}

func TestWaitGroupInterfacesRetainTypedReceiverIdentity(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"InterfaceGroups"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	group := ""
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::InterfaceGroups") && e.Kind == "waitgroup_add" {
			group = e.To
		}
	}
	done, wait, cleanup, boundDone, boundWait := false, false, false, false, false
	for _, e := range r.Relationships {
		if e.Kind == "waitgroup_done" && strings.HasSuffix(e.From, "::InterfaceCompletion") {
			if e.To != group {
				t.Fatal("non-WaitGroup implementation became a coordination resource")
			}
			done = true
		}
		wait = wait || e.Kind == "waitgroup_wait" && strings.HasSuffix(e.From, "::InterfaceJoin") && e.To == group
		cleanup = cleanup || e.Kind == "cleanup_waitgroup_done" && e.To == group
		boundDone = boundDone || e.Kind == "waitgroup_done" && e.To == group && strings.HasSuffix(e.From, "::BoundCompletion")
		boundWait = boundWait || e.Kind == "waitgroup_wait" && e.To == group && strings.HasSuffix(e.From, "::BoundJoin")
	}
	if group == "" || !done || !wait || !cleanup || !boundDone || !boundWait {
		t.Fatalf("interface path missing: done=%t wait=%t cleanup=%t boundDone=%t boundWait=%t", done, wait, cleanup, boundDone, boundWait)
	}
}

func TestPromotedWaitGroupMethodExpressionsPreserveEmbeddedIdentity(t *testing.T) {
	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PromotedMethodExpressionSites", "SimplePromotedMethodExpressions"}, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	var saved strings.Builder
	if err := WriteAnalysis(&saved, analysis, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(saved.String()))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: []string{"PromotedMethodExpressionSites", "SimplePromotedMethodExpressions"}, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, r} {
		known, unrelated := "", ""
		for _, edge := range report.Relationships {
			if edge.Kind != "waitgroup_add" || !strings.HasSuffix(edge.From, "::PromotedMethodExpressionSites") {
				continue
			}
			if strings.Contains(edge.Evidence.Snippet, "known.WaitGroup.Add(7)") {
				known = edge.To
			}
			if strings.Contains(edge.Evidence.Snippet, "unrelated.WaitGroup.Add(3)") {
				unrelated = edge.To
			}
		}
		if known == "" || unrelated == "" || known == unrelated {
			t.Fatalf("distinct direct-control resources missing in fresh/saved report: known=%q unrelated=%q", known, unrelated)
		}
		want := map[string]bool{"waitgroup_add": false, "waitgroup_done": false, "waitgroup_wait": false}
		selectedReceivers := map[string]bool{}
		for _, edge := range report.Relationships {
			if !strings.HasSuffix(edge.From, "::PromotedMethodExpressionSites") && !strings.HasSuffix(edge.From, "::callGroupWait") {
				continue
			}
			if edge.To == unrelated && (edge.Kind == "waitgroup_add" || edge.Kind == "waitgroup_done" || edge.Kind == "waitgroup_wait") && !strings.Contains(edge.Evidence.Snippet, "unrelated.WaitGroup.Add(3)") {
				t.Fatalf("unrelated direct control acquired an expression edge: %+v", edge)
			}
			if edge.To != known {
				if edge.Kind == "waitgroup_done" && strings.Contains(edge.Evidence.Snippet, "Done(selected)") {
					selectedReceivers[edge.To] = true
				}
				continue
			}
			if edge.Kind == "waitgroup_done" && strings.Contains(edge.Evidence.Snippet, "Done(selected)") {
				selectedReceivers[edge.To] = true
			}
			switch edge.Kind {
			case "waitgroup_add":
				if strings.Contains(edge.Evidence.Snippet, "add(known, 1)") && len(edge.Values) == 1 && edge.Values[0] == "integer:1" {
					want[edge.Kind] = true
				}
			case "waitgroup_done":
				if strings.Contains(edge.Evidence.Snippet, "done(known)") {
					want[edge.Kind] = true
				}
			case "waitgroup_wait":
				if strings.Contains(edge.Evidence.Snippet, "wait(known)") || strings.Contains(edge.Evidence.Snippet, "wait(group)") || strings.Contains(edge.Evidence.Snippet, "Wait, known") {
					want[edge.Kind] = true
				}
			}
		}
		if !want["waitgroup_add"] || !want["waitgroup_done"] || !want["waitgroup_wait"] {
			t.Fatalf("promoted method-expression edges lost embedded identity or argument offset: resource=%s operations=%v; edges=%+v", known, want, report.Relationships)
		}
		unresolved := false
		unresolvedCandidates := []Boundary{}
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "unresolved_waitgroup" && strings.HasSuffix(boundary.Node, "::PromotedMethodExpressionSites") {
				unresolvedCandidates = append(unresolvedCandidates, boundary)
			}
			unresolved = unresolved || boundary.Kind == "unresolved_waitgroup" && strings.HasSuffix(boundary.Node, "::PromotedMethodExpressionSites") && strings.Contains(boundary.Evidence.Snippet, "Done(selected)")
		}
		if !unresolved {
			t.Fatalf("known and outside receiver alternatives did not retain a source-backed unresolved boundary: %+v", unresolvedCandidates)
		}
		outsideCandidate := false
		for receiver := range selectedReceivers {
			outsideCandidate = outsideCandidate || strings.Contains(receiver, "input")
		}
		if !selectedReceivers[known] || !outsideCandidate {
			t.Fatalf("known direct resource and outside input were not both retained at Done(selected): known=%s candidates=%v", known, selectedReceivers)
		}
		simpleDirect, simpleExpression := "", ""
		for _, edge := range report.Relationships {
			if edge.Kind != "waitgroup_add" || !strings.HasSuffix(edge.From, "::SimplePromotedMethodExpressions") {
				continue
			}
			if strings.Contains(edge.Evidence.Snippet, "group.WaitGroup.Add(2)") {
				simpleDirect = edge.To
			}
			if strings.Contains(edge.Evidence.Snippet, "(*DirectNestedGroup).Add(group, 1)") && len(edge.Values) == 1 && edge.Values[0] == "integer:1" {
				simpleExpression = edge.To
			}
		}
		if simpleDirect == "" || simpleExpression != simpleDirect {
			t.Fatalf("nested pointer/value control lost direct-expression identity: direct=%q expression=%q", simpleDirect, simpleExpression)
		}
	}
}

func TestWaitGroupExternalInputsAndGlobalControlsKeepBoundariesLocal(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PublicWaitGroupInput", "uncalledWaitGroupInput", "PublicGlobalWaitGroupSites", "EscapingWaitGroupCallback", "ClosedWaitGroupHelper"}, Depth: 4, MaxNodes: 350})
	if err != nil {
		t.Fatal(err)
	}
	inputSites := map[string]bool{"group.Add(1)": false, "wait()": false}
	inputCandidates := map[string]bool{}
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::PublicWaitGroupInput") && strings.HasPrefix(edge.Kind, "waitgroup_") {
			for source := range inputSites {
				if strings.Contains(edge.Evidence.Snippet, source) {
					inputSites[source] = true
					inputCandidates[edge.To] = strings.Contains(edge.To, "input")
				}
			}
		}
	}
	if !inputSites["group.Add(1)"] || !inputSites["wait()"] {
		t.Fatalf("public direct/bound input operations missing: %+v", inputSites)
	}
	for _, site := range []string{"group.Add(1)", "wait()"} {
		found := false
		for _, boundary := range r.Boundaries {
			found = found || boundary.Kind == "unresolved_waitgroup" && strings.HasSuffix(boundary.Node, "::PublicWaitGroupInput") && strings.Contains(boundary.Evidence.Snippet, site)
		}
		if !found {
			t.Fatalf("public input operation %q lacks a source-backed outside boundary", site)
		}
	}
	inputResource := false
	for _, candidate := range inputCandidates {
		inputResource = inputResource || candidate
	}
	if !inputResource {
		t.Fatal("public direct/bound methods did not retain their aggregate input candidate")
	}
	uncalledBoundary := false
	globalAdd, globalDone := "", ""
	for _, boundary := range r.Boundaries {
		uncalledBoundary = uncalledBoundary || boundary.Kind == "unresolved_waitgroup" && strings.HasSuffix(boundary.Node, "::uncalledWaitGroupInput")
	}
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::PublicGlobalWaitGroupSites") && strings.Contains(edge.Evidence.Snippet, "PublicGlobalWaitGroup.Add(1)") && edge.Kind == "waitgroup_add" {
			globalAdd = edge.To
		}
		if strings.HasSuffix(edge.From, "::PublicGlobalWaitGroupSites") && strings.Contains(edge.Evidence.Snippet, "(*sync.WaitGroup).Done") && edge.Kind == "waitgroup_done" {
			globalDone = edge.To
		}
	}
	if !uncalledBoundary || globalAdd == "" || globalDone != globalAdd {
		t.Fatalf("uncalled/global controls lost bounded behavior: uncalledBoundary=%t globalAdd=%q globalDone=%q", uncalledBoundary, globalAdd, globalDone)
	}
	globalOutside := false
	for _, boundary := range r.Boundaries {
		globalOutside = globalOutside || strings.HasSuffix(boundary.Node, "::PublicGlobalWaitGroupSites") && boundary.Kind == "unresolved_waitgroup" && strings.Contains(boundary.Evidence.Snippet, "PublicGlobalWaitGroup.Add(1)")
	}
	if !globalOutside {
		t.Fatal("exported global operation did not disclose its outside mutable-input boundary")
	}
	escapedEdge, escapedBoundary := false, false
	for _, edge := range r.Relationships {
		escapedEdge = escapedEdge || edge.Kind == "waitgroup_done" && strings.Contains(edge.Evidence.Snippet, "(*sync.WaitGroup).Done(group)") && strings.Contains(edge.To, "input")
	}
	for _, boundary := range r.Boundaries {
		escapedBoundary = escapedBoundary || boundary.Kind == "unresolved_waitgroup" && strings.Contains(boundary.Evidence.Snippet, "(*sync.WaitGroup).Done(group)")
	}
	if !escapedEdge || !escapedBoundary {
		t.Fatalf("escaped callback receiver input was not retained with a per-site boundary: edge=%t boundary=%t", escapedEdge, escapedBoundary)
	}
	for _, boundary := range r.Boundaries {
		if boundary.Kind == "unresolved_waitgroup" && strings.HasSuffix(boundary.Node, "::closedGroupWait") {
			t.Fatalf("closed helper with a known local caller acquired an outside-input boundary: %+v", boundary)
		}
	}
}

func TestWaitGroupMethodExpressionDispatchAndTaskAlternatives(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"MixedMethodExpressionRoles", "SameNameNonWaitGroupMethod", "PublicMethodExpressionGo", "PublicMethodExpressionGoNil"}, Depth: 5, MaxNodes: 350})
	if err != nil {
		t.Fatal(err)
	}
	group := ""
	for _, edge := range r.Relationships {
		if edge.Kind == "waitgroup_add" && strings.HasSuffix(edge.From, "::MixedMethodExpressionRoles") && strings.Contains(edge.Evidence.Snippet, "group.WaitGroup.Add(1)") {
			group = edge.To
		}
	}
	roles := map[string]bool{"waitgroup_done": false, "waitgroup_wait": false}
	var mixedEdges []Relationship
	for _, edge := range r.Relationships {
		if strings.Contains(edge.From, "callMixedGroupMethod") || strings.Contains(edge.Evidence.Snippet, "method(group)") {
			mixedEdges = append(mixedEdges, edge)
			if _, expectedRole := roles[edge.Kind]; edge.To == group && expectedRole {
				roles[edge.Kind] = true
			}
		}
		if strings.HasSuffix(edge.From, "::SameNameNonWaitGroupMethod") && strings.HasPrefix(edge.Kind, "waitgroup_") {
			t.Fatalf("same-name user method was misclassified as sync.WaitGroup: %+v", edge)
		}
	}
	if group == "" || !roles["waitgroup_done"] || !roles["waitgroup_wait"] {
		t.Fatalf("shared helper did not retain both method-expression roles: group=%q roles=%v edges=%+v", group, roles, mixedEdges)
	}
	goSite, knownTask, nilKnownTask, nilTaskBoundary := false, false, false, false
	for _, edge := range r.Relationships {
		if edge.Kind == "waitgroup_go" && strings.HasSuffix(edge.From, "::PublicMethodExpressionGo") && strings.Contains(edge.Evidence.Snippet, "WaitGroup).Go") {
			goSite = edge.To != ""
		}
		knownTask = knownTask || edge.Kind == "waitgroup_task" && edge.To == "example.com/sample::knownWaitGroupTask"
		nilKnownTask = nilKnownTask || edge.Kind == "waitgroup_task" && edge.To == "example.com/sample::knownWaitGroupTask" && strings.HasSuffix(edge.Evidence.Snippet, "(*sync.WaitGroup).Go(group, task)")
	}
	unknownTask := false
	for _, boundary := range r.Boundaries {
		unknownTask = unknownTask || boundary.Kind == "unresolved_waitgroup_task" && strings.HasSuffix(boundary.Node, "::PublicMethodExpressionGo") && strings.Contains(boundary.Evidence.Snippet, "WaitGroup).Go")
		nilTaskBoundary = nilTaskBoundary || boundary.Kind == "unresolved_waitgroup_task" && strings.HasSuffix(boundary.Node, "::PublicMethodExpressionGoNil") && strings.Contains(boundary.Evidence.Snippet, "WaitGroup).Go") && strings.Contains(boundary.Reason, "nil callback")
	}
	if !goSite || !knownTask || !unknownTask || !nilKnownTask || !nilTaskBoundary {
		t.Fatalf("method-expression Go receiver/task alternatives incomplete: goSite=%t knownTask=%t unknownTask=%t nilKnownTask=%t nilTaskBoundary=%t", goSite, knownTask, unknownTask, nilKnownTask, nilTaskBoundary)
	}
}
