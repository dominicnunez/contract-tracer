package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestOutsideInterfacesRetainAssertedCandidates(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"OutsideInterfaceCaller"}, Depth: 4, MaxNodes: 400,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "aggregate", DatabaseOrigins: []string{"example.com/sample::knownAggregateDatabase"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		owner, query, table string
		outside             bool
	}{
		{"PublicAnyInput", "inputAnyQuery", "input_any_records", true},
		{"PublicAnyField", "fieldAnyQuery", "field_any_records", true},
		{"PublicAnyFactory", "factoryAnyQuery", "factory_any_records", true},
		{"escapedAnyCallback", "callbackAnyQuery", "callback_any_records", true},
		{"useAnyGlobal", "globalAnyQuery", "global_any_records", true},
		{"PublicAnyProvider", "providerAnyQuery", "provider_any_records", true},
		{"closedAnyInput", "closedAnyQuery", "closed_any_records", false},
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
			outside = outside || b.Kind == "partial_function_call" && strings.HasSuffix(b.Node, "::"+c.owner) && strings.Contains(b.Evidence.Snippet, "record.Run(")
		}
		count := 1
		if c.outside {
			count = 2
		}
		if !known || outside != c.outside || len(tables) != count || !tables["table:aggregate:"+c.table] || tables["table:"+c.table] != c.outside {
			t.Errorf("%s known=%t outside=%t tables=%v", c.owner, known, outside, tables)
		}
	}
	function := false
	for _, b := range r.Boundaries {
		function = function || b.Kind == "partial_function_call" && strings.HasSuffix(b.Node, "::PublicAnyFunction") && strings.Contains(b.Evidence.Snippet, "callback()")
	}
	if !function {
		t.Error("asserted function lost outside candidate")
	}
}
