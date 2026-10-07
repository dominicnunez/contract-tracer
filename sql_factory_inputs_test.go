package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenSQLFactoriesRetainKnownAndOutsideHandleResults(t *testing.T) {
	options := Options{Root: "testdata/sample", Seeds: []string{"SQLFactoryCaller"}, Depth: 4, MaxNodes: 250,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "factory", DatabaseOrigins: []string{"example.com/sample::knownSQLFactoryDatabase"}}}}}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: options.Seeds, Depth: options.Depth, MaxNodes: options.MaxNodes})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, item := range []struct {
			owner, table string
			outside      bool
		}{
			{"openDatabaseFactoryQuery", "factory_database_records", true},
			{"openConnectionFactoryQuery", "factory_connection_records", true},
			{"openTransactionFactoryQuery", "factory_transaction_records", true},
			{"closedDatabaseFactoryQuery", "closed_factory_database_records", false},
			{"closedConnectionFactoryQuery", "closed_factory_connection_records", false},
			{"closedTransactionFactoryQuery", "closed_factory_transaction_records", false},
		} {
			tables := map[string]bool{}
			for _, edge := range report.Relationships {
				if edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::"+item.owner) {
					tables[edge.To] = true
				}
			}
			want := 1
			if item.outside {
				want = 2
			}
			if len(tables) != want || !tables["table:factory:"+item.table] || tables["table:"+item.table] != item.outside {
				t.Errorf("%s result origins: %v", item.owner, tables)
			}
		}
		for _, owner := range []string{"openStatementFactoryQuery", "closedStatementFactoryQuery"} {
			known, unknown := false, false
			for _, edge := range report.Relationships {
				known = known || edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::"+owner) && edge.To == "table:factory:factory_statement_records"
			}
			for _, b := range report.Boundaries {
				unknown = unknown || b.Kind == "unresolved_prepared_sql" && strings.HasSuffix(b.Node, "::"+owner)
			}
			if !known || unknown != (owner == "openStatementFactoryQuery") {
				t.Errorf("%s statement origins: known=%t unknown=%t", owner, known, unknown)
			}
		}
	}
}
