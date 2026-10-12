package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestUnresolvedAggregateResultRetainsSourceBoundary(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"unresolvedRecordResult"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	call, result, table := false, false, false
	for _, boundary := range r.Boundaries {
		call = call || boundary.Kind == "unresolved_function_call" && boundary.Evidence.Snippet == "record.Run(record.Database)"
		result = result || boundary.Kind == "aggregate_result_model" && boundary.Evidence.Snippet == "record := factory()"
	}
	for _, edge := range r.Relationships {
		table = table || edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::unresolvedRecordResult") && edge.To == "table:unresolved_result_records"
	}
	if !call || !result || !table {
		t.Fatalf("unresolved aggregate: call=%t result=%t table=%t", call, result, table)
	}
}

func TestOutsideAggregateResultsKeepKnownAndUnknownOrigins(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"AggregateResultCaller"}, Depth: 4, MaxNodes: 350,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "aggregate", DatabaseOrigins: []string{"example.com/sample::knownAggregateDatabase"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		owner, query, table string
		outside             bool
	}{
		{"OpenAggregateResult", "recordResultQuery", "record_result_records", true},
		{"OpenPointerAggregateResult", "pointerResultQuery", "pointer_result_records", true},
		{"OpenTupleAggregateResult", "tupleResultQuery", "tuple_result_records", true},
		{"OpenSliceAggregateResult", "sliceResultQuery", "slice_result_records", true},
		{"OpenMapAggregateResult", "mapResultQuery", "map_result_records", true},
		{"OpenArrayAggregateResult", "arrayResultQuery", "array_result_records", true},
		{"closedAggregateResult", "closedResultQuery", "closed_result_records", false},
	} {
		known, outside := false, false
		tables := map[string]bool{}
		for _, e := range r.Relationships {
			known = known || e.Kind == "resolved_callback_call" && strings.HasSuffix(e.From, "::"+c.owner) && strings.HasSuffix(e.To, "::"+c.query)
			if e.Kind == "sql_read" && strings.HasSuffix(e.From, "::"+c.query) {
				tables[e.To] = true
			}
		}
		for _, b := range r.Boundaries {
			outside = outside || b.Kind == "partial_function_call" && strings.HasSuffix(b.Node, "::"+c.owner) && strings.Contains(b.Evidence.Snippet, ".Run(")
		}
		count := 1
		if c.outside {
			count = 2
		}
		if !known || outside != c.outside || len(tables) != count || !tables["table:aggregate:"+c.table] || tables["table:"+c.table] != c.outside {
			t.Errorf("%s known=%t outside=%t tables=%v", c.owner, known, outside, tables)
		}
	}
}
