package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestKnownFunctionTargetsRetainOutsideInputUncertainty(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"MixedFunctionCaller", "readPrivateFunctionField"}, Depth: 3, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	for kind, snippet := range map[string]string{"partial_function_call": "callback();", "deferred_partial_function_call": "defer callback()", "goroutine_partial_function_call": "go callback()"} {
		found := false
		for _, b := range r.Boundaries {
			found = found || b.Kind == kind && strings.HasSuffix(b.Node, "::forwardOpenFunction") && strings.Contains(b.Evidence.Snippet, snippet)
		}
		if !found {
			t.Errorf("known callback hid %s", kind)
		}
	}
	for _, snippet := range []string{"PublicReplaceableFunction()", "PublicFunctionRecord.Call()", "PublicFunctionSlice[0]()", `PublicFunctionMap["local"]()`} {
		found := false
		for _, b := range r.Boundaries {
			found = found || b.Kind == "partial_function_call" && strings.HasSuffix(b.Node, "::readPublicFunctions") && b.Evidence.Snippet == snippet
		}
		if !found {
			t.Errorf("outside replacement hidden at %s", snippet)
		}
	}
	for _, owner := range []string{"forwardOpenFunction", "readPublicFunctions", "closedInvokeFunction", "readPrivateFunctionField"} {
		found := false
		for _, e := range r.Relationships {
			found = found || strings.HasSuffix(e.From, "::"+owner) && strings.HasSuffix(e.To, "::knownMixedFunction") && e.Kind == "resolved_callback_call"
		}
		if !found {
			t.Errorf("known path lost at %s", owner)
		}
	}
	for _, b := range r.Boundaries {
		if strings.Contains(b.Kind, "partial_function_call") && (strings.HasSuffix(b.Node, "::closedInvokeFunction") || strings.HasSuffix(b.Node, "::readPrivateFunctionField")) {
			t.Errorf("closed input incorrectly open: %+v", b)
		}
	}
}

func TestFunctionResultsPreserveKnownAndOutsideFactoryTargets(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"MixedDependencyFunction", "MixedFactoryCaller"}, Depth: 3, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	for owner, callback := range map[string]string{"MixedDependencyFunction": "localFunctionStop", "consumeOpenFactory": "knownMixedFunction"} {
		known, partial := false, false
		for _, edge := range r.Relationships {
			known = known || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::"+owner) && strings.HasSuffix(edge.To, "::"+callback)
		}
		for _, boundary := range r.Boundaries {
			for _, edge := range r.Relationships {
				partial = partial || boundary.Kind == "partial_function_call" && strings.HasSuffix(boundary.Node, "::"+owner) && edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.To, "::"+callback) && boundary.Evidence.File == edge.Evidence.File && boundary.Evidence.Line == edge.Evidence.Line && boundary.Evidence.Column == edge.Evidence.Column
			}
		}
		if !known || !partial {
			t.Errorf("%s callable result: known=%t partial=%t", owner, known, partial)
		}
	}
}

func TestOpenDispatcherRetainsCallbackEscapeAlongsideLocalConsumer(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"MixedDispatchCaller"}, Depth: 4, MaxNodes: 200,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]bool{}
	escaped := false
	for _, edge := range r.Relationships {
		if edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::mixedDispatchQuery") {
			tables[edge.To] = true
		}
		escaped = escaped || edge.Kind == "callback_escape" && strings.HasSuffix(edge.From, "::PublicDispatchFunction") && strings.HasSuffix(edge.To, "::mixedDispatchQuery")
	}
	if !escaped || len(tables) != 2 || !tables["table:orders:mixed_dispatch_records"] || !tables["table:mixed_dispatch_records"] {
		t.Fatalf("known dispatcher hid outside retention/invocation: escaped=%t tables=%v", escaped, tables)
	}
}
