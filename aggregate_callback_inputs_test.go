package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestEscapedCallbacksRetainAggregateOutsideInputs(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"EscapedAggregateInputCaller"}, Depth: 4, MaxNodes: 300,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "aggregate", DatabaseOrigins: []string{"example.com/sample::knownAggregateDatabase"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for owner, query := range map[string]string{
		"returnedAggregateCallback":   "returnedAggregateQuery",
		"globalAggregateCallback":     "globalAggregateQuery",
		"dependencyAggregateCallback": "dependencyAggregateQuery",
		"closedAggregateCallback":     "privateAggregateQuery",
	} {
		known, outside := false, false
		for _, edge := range r.Relationships {
			known = known || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::"+owner) && strings.HasSuffix(edge.To, "::"+query)
		}
		for _, boundary := range r.Boundaries {
			outside = outside || boundary.Kind == "partial_function_call" && strings.HasSuffix(boundary.Node, "::"+owner)
		}
		if !known || outside != (owner != "closedAggregateCallback") {
			t.Errorf("%s known=%t outside=%t", owner, known, outside)
		}
	}
	for query, table := range map[string]string{
		"returnedAggregateQuery": "returned_aggregate_records", "globalAggregateQuery": "global_aggregate_records",
		"dependencyAggregateQuery": "dependency_aggregate_records", "privateAggregateQuery": "private_aggregate_records",
	} {
		tables := map[string]bool{}
		for _, edge := range r.Relationships {
			if edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::"+query) {
				tables[edge.To] = true
			}
		}
		outside := query != "privateAggregateQuery"
		count := 1
		if outside {
			count = 2
		}
		if len(tables) != count || !tables["table:aggregate:"+table] || tables["table:"+table] != outside {
			t.Errorf("%s origins: %v", query, tables)
		}
	}
}
