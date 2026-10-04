package contracttrace

import (
	"context"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestLifecycleEscapeSeesEffectOnlyMutationAfterCachedWalk(t *testing.T) {
	signature := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	record := types.NewStruct([]*types.Var{types.NewVar(token.NoPos, nil, "Cancel", signature)}, nil)
	value := ssa.NewConst(nil, types.NewPointer(record))
	a := &flowAnalysis{values: map[ssa.Value]flowValue{}, memory: map[string]flowValue{}, contextKeys: map[string]contextSite{"context:mutable": {}}}
	root := emptyFlow()
	root.addresses["record"] = true
	a.put(value, root)
	a.accessibleCallbacks(value)
	ix := &index{}
	a.callableSummaryEscapes(value, "owner", "_escape", Evidence{}, ix)
	if len(ix.edges) != 0 {
		t.Fatal("empty storage invented lifecycle escape")
	}
	updated := emptyFlow()
	updated.effects["cancel:context:mutable"] = true
	a.store("record.field:0", updated)
	a.callableSummaryEscapes(value, "owner", "_escape", Evidence{}, ix)
	if len(ix.edges) != 1 || ix.edges[0].Kind != "context_cancel_escape" || ix.edges[0].To != "context:mutable" {
		t.Fatalf("cached absence hid effect-only mutation: %+v", ix.edges)
	}
}

func TestCallableLifecycleSummariesRetainDependencyReturnAndGlobalEscapes(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"SummaryEscapeCaller", "PublicLifecycleSummaries", "PublicCancellationSummary", "EscapingSchedulingCaller"}, Depth: 5, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{}
	for _, kind := range []string{"context_cancel_escape", "deferred_context_cancel_escape", "goroutine_context_cancel_escape", "cancellation_stop_escape", "context_cancel_return_escape", "cancellation_stop_return_escape", "context_cancel_global_escape", "cancellation_delivery_escape"} {
		expected[kind] = false
	}
	for _, edge := range r.Relationships {
		if _, wanted := expected[edge.Kind]; wanted {
			expected[edge.Kind] = true
		}
	}
	for kind, found := range expected {
		if !found {
			t.Errorf("missing callable summary relationship %s", kind)
		}
	}
	delivery, outsideStop := false, false
	for _, boundary := range r.Boundaries {
		delivery = delivery || boundary.Kind == "callable_summary_escape" && strings.Contains(boundary.Node, "escapingSchedulingContext.AfterFunc")
		outsideStop = outsideStop || boundary.Kind == "unresolved_cancellation_scheduler_stop"
	}
	if !delivery || !outsideStop {
		t.Errorf("external behavior limits: delivery=%t stop=%t", delivery, outsideStop)
	}
}
