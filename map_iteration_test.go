package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMapIterationRetainsSeparateKeysValuesAndRecordCallbacks(t *testing.T) {
	options := Options{Root: "testdata/sample", Seeds: []string{"MapIterationCaller"}, Depth: 4, MaxNodes: 250,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}}}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: options.Seeds, Depth: options.Depth, MaxNodes: options.MaxNodes})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, callback := range []string{"iterationValueQuery", "iterationKeyQuery", "iterationRecordQuery"} {
			found := false
			for _, edge := range report.Relationships {
				found = found || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::MapIterationCaller") && strings.HasSuffix(edge.To, "::"+callback)
			}
			if !found {
				t.Errorf("map iteration lost callback %s", callback)
			}
		}
		for owner, table := range map[string]string{
			"iterationValueQuery": "iteration_callback_records", "iterationKeyQuery": "iteration_key_callback_records", "iterationRecordQuery": "iteration_record_callback_records",
			"consumeIterationKey": "iteration_key_records", "consumeIterationValue": "iteration_value_records",
		} {
			tables := map[string]bool{}
			for _, edge := range report.Relationships {
				if edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::"+owner) {
					tables[edge.To] = true
				}
			}
			if len(tables) != 1 || !tables["table:orders:"+table] {
				t.Errorf("%s iteration input: %v", owner, tables)
			}
		}
	}
}

func TestExportedMapIterationRetainsOutsideSQLCandidates(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PublicMapIterationCaller"}, Depth: 3, MaxNodes: 150,
		Config: Config{StorageScopes: []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]bool{}
	for _, edge := range r.Relationships {
		if edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::publicIterationDatabaseQuery") {
			tables[edge.To] = true
		}
	}
	if len(tables) != 2 || !tables["table:orders:iteration_public_database_records"] || !tables["table:iteration_public_database_records"] {
		t.Fatalf("iteration lost known or outside map inputs: %v", tables)
	}
}

func TestUnknownMapIterationExposesMissingStorageOrigin(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"UnknownMapIteration"}, Depth: 1, MaxNodes: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range r.Boundaries {
		if boundary.Kind == "unresolved_map_iteration" && strings.HasSuffix(boundary.Node, "::UnknownMapIteration") && strings.Contains(boundary.Evidence.Snippet, "range callbacks") {
			return
		}
	}
	t.Fatal("unknown incoming map silently looks empty")
}
