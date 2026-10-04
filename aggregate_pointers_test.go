package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestAggregatePointerChainsRetainOutsideOrigins(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PointerChainCaller"}, Depth: 4, MaxNodes: 300,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "aggregate", DatabaseOrigins: []string{"example.com/sample::knownAggregateDatabase"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		owner, query, table, snippet string
		outside                      bool
	}{
		{"PublicPointerChain", "pointerChainInputQuery", "pointer_chain_input_records", "(*record).Run((*record).Database)", true},
		{"PublicPointerFields", "pointerChainInputQuery", "pointer_chain_input_records", "(*record.Record).Run((*record.Record).Database)", true},
		{"PublicPointerFields", "pointerFieldQuery", "pointer_field_records", "(**record.Callback)(**record.Database)", true},
		{"OpenPointerChainResult", "pointerChainResultQuery", "pointer_chain_result_records", "(*record).Run((*record).Database)", true},
		{"closedPointerChain", "closedPointerChainQuery", "closed_pointer_chain_records", "(*record).Run((*record).Database)", false},
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
			outside = outside || b.Kind == "partial_function_call" && strings.HasSuffix(b.Node, "::"+c.owner) && b.Evidence.Snippet == c.snippet
		}
		count := 1
		if c.outside {
			count = 2
		}
		if !known || outside != c.outside || len(tables) != count || !tables["table:aggregate:"+c.table] || tables["table:"+c.table] != c.outside {
			t.Errorf("%s %s: known=%t outside=%t tables=%v", c.owner, c.snippet, known, outside, tables)
		}
	}
}
