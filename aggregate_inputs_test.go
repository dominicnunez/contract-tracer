package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestAggregateAPIInputsKeepKnownAndOutsideCandidates(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"AggregateInputCaller"}, Depth: 4, MaxNodes: 250,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "aggregate", DatabaseOrigins: []string{"example.com/sample::knownAggregateDatabase"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"consumeValueRecord", "PublicPointerRecordInput", "PublicSliceInput", "PublicMapInput"} {
		known, open := false, false
		for _, e := range r.Relationships {
			known = known || e.Kind == "resolved_callback_call" && strings.HasSuffix(e.From, "::"+owner) && strings.HasSuffix(e.To, "::aggregateInputQuery")
		}
		for _, b := range r.Boundaries {
			open = open || b.Kind == "partial_function_call" && strings.HasSuffix(b.Node, "::"+owner)
		}
		if !known || !open {
			t.Errorf("%s record callback: known=%t outside=%t", owner, known, open)
		}
	}
	for owner, table := range map[string]string{"aggregateInputQuery": "aggregate_input_records", "closedAggregateQuery": "closed_aggregate_input_records"} {
		tables := map[string]bool{}
		for _, e := range r.Relationships {
			if e.Kind == "sql_read" && strings.HasSuffix(e.From, "::"+owner) {
				tables[e.To] = true
			}
		}
		outside := owner == "aggregateInputQuery"
		count := 1
		if outside {
			count = 2
		}
		if len(tables) != count || !tables["table:aggregate:"+table] || tables["table:"+table] != outside {
			t.Errorf("%s database origins: %v", owner, tables)
		}
	}
}

func TestRepeatedAggregateFieldsAndFunctionPointersStayOpen(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"AggregatePairCaller"}, Depth: 3, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, snippet := range []string{"pair.First.Run(pair.First.Database)", "pair.Second.Run(pair.Second.Database)", "(*pair.FunctionPointer)(pair.Second.Database)"} {
		found := false
		for _, boundary := range r.Boundaries {
			found = found || boundary.Kind == "partial_function_call" && strings.HasSuffix(boundary.Node, "::PublicRecordPair") && boundary.Evidence.Snippet == snippet
		}
		if !found {
			t.Errorf("aggregate type reuse hid %s", snippet)
		}
	}
}

func TestRecursiveAggregateContainerUsesBoundedTypeRoots(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"RecursiveContainerCaller"}, Depth: 3, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range r.Boundaries {
		if boundary.Kind == "partial_function_call" && strings.HasSuffix(boundary.Node, "::PublicRecursiveContainer") {
			return
		}
	}
	t.Fatal("recursive container lost outside callable origin")
}
