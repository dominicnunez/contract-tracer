package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestFunctionIterationRetainsYieldedCallbacks(t *testing.T) {
	for _, example := range []struct{ caller, callback, table string }{
		{"FunctionIterationCaller", "functionIterationQuery", "function_iteration_records"},
		{"SliceFunctionIterationCaller", "sliceFunctionIterationQuery", "slice_function_iteration_records"},
		{"SliceAllIterationCaller", "sliceAllIterationQuery", "slice_all_iteration_records"},
		{"SliceBackwardIterationCaller", "sliceBackwardIterationQuery", "slice_backward_iteration_records"},
		{"MapValuesIterationCaller", "mapValuesIterationQuery", "map_values_iteration_records"},
		{"MapKeysIterationCaller", "mapKeysIterationQuery", "map_keys_iteration_records"},
		{"MapAllIterationCaller", "mapAllIterationQuery", "map_all_iteration_records"},
	} {
		t.Run(example.caller, func(t *testing.T) {
			r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{example.caller}, Depth: 4, MaxNodes: 200,
				Config: Config{StorageScopes: []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			called := false
			tables := map[string]bool{}
			for _, edge := range r.Relationships {
				called = called || strings.HasSuffix(edge.From, "::"+example.caller) && strings.HasSuffix(edge.To, "::"+example.callback) && edge.Kind == "resolved_callback_call"
				if strings.HasSuffix(edge.From, "::"+example.callback) && edge.Kind == "sql_read" {
					tables[edge.To] = true
				}
			}
			if !called || len(tables) != 1 || !tables["table:orders:"+example.table] {
				t.Fatalf("yielded callback lost: called=%t tables=%v", called, tables)
			}
		})
	}
}

func TestReturnedIteratorRetainsCallbackEscape(t *testing.T) {
	for _, example := range []struct{ caller, factory, callback, table string }{
		{"PublicIteratorCaller", "PublicSliceIterator", "publicIteratorQuery", "public_iterator_records"},
		{"RecordIteratorCaller", "PublicIteratorRecord", "recordIteratorQuery", "record_iterator_records"},
	} {
		r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{example.caller}, Depth: 4, MaxNodes: 200,
			Config: Config{StorageScopes: []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}}})
		if err != nil {
			t.Fatal(err)
		}
		tables := map[string]bool{}
		escaped := false
		for _, edge := range r.Relationships {
			if strings.HasSuffix(edge.From, "::"+example.callback) && edge.Kind == "sql_read" {
				tables[edge.To] = true
			}
			escaped = escaped || edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.From, "::"+example.factory) && strings.HasSuffix(edge.To, "::"+example.callback)
		}
		if !escaped || len(tables) != 2 || !tables["table:orders:"+example.table] || !tables["table:"+example.table] {
			t.Fatalf("returned iterator lost outside callback input: escaped=%t tables=%v", escaped, tables)
		}
	}
}
