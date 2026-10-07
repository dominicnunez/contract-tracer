package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func multiStoreConfig() Config {
	c := DefaultConfig()
	c.StorageScopes = []StorageScope{
		{Namespace: "orders", GoFiles: []string{"orders/**/*.go"}, SQLFiles: []string{"migrations/orders/**/*.sql"}},
		{Namespace: "billing", GoFiles: []string{"billing/**/*.go"}, SQLFiles: []string{"migrations/billing/**/*.sql"}},
	}
	return c
}

func TestStorageNamespacesJoinSchemaWithoutCrossingDatabases(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/multistore", Seeds: []string{"example.com/multistore/orders::Write"}, Depth: 3, MaxNodes: 100, Config: multiStoreConfig()})
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, n := range r.Nodes {
		present[n.ID] = true
		if strings.Contains(n.ID, "/billing::") || n.ID == "table:billing:records" || strings.Contains(n.Evidence.File, "migrations/billing/") {
			t.Errorf("unrelated database joined scope: %s", n.ID)
		}
	}
	for _, id := range []string{"table:orders:records", "table:orders:audit_log", "example.com/multistore/orders::Read", "sql_file:orders:migrations%2Forders%2F001.sql"} {
		if !present[id] {
			t.Errorf("missing namespaced relationship path: %s", id)
		}
	}
	trigger, fk := false, false
	for _, e := range r.Relationships {
		if strings.Contains(e.Evidence.File, "migrations/orders/") {
			if e.To == "table:orders:audit_log" && e.Kind == "sql_write" {
				trigger = true
			}
			if e.To == "table:orders:records" && e.Kind == "sql_schema_ref" {
				fk = true
			}
		}
	}
	if !trigger || !fk {
		t.Errorf("scoped schema lost trigger or foreign-key paths: trigger=%t fk=%t", trigger, fk)
	}
	assigned := false
	for _, assignment := range r.Coverage.Storage.Assignments {
		if assignment.File == "migrations/orders/001.sql" && assignment.Namespace == "orders" {
			assigned = true
		}
	}
	if !assigned {
		t.Fatal("report omitted the schema namespace assignment")
	}
}

func TestSavedExplorationRetainsStorageIdentity(t *testing.T) {
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/multistore", Seeds: []string{"example.com/multistore/orders::Write"}, Depth: 1, MaxNodes: 2, Config: multiStoreConfig()})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Explore(context.Background(), analysis, ExploreOptions{Seeds: []string{"table:billing:records"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	read := false
	for _, n := range r.Nodes {
		if n.ID == "example.com/multistore/billing::Read" {
			read = true
		}
		if strings.Contains(n.ID, "/orders::") || n.ID == "table:orders:records" {
			t.Errorf("saved exploration crossed databases: %s", n.ID)
		}
	}
	if !read {
		t.Fatal("saved namespace resource could not seed its readers")
	}
}

func TestUnconfiguredSQLSitesRemainExplicitBoundaries(t *testing.T) {
	c := DefaultConfig()
	c.StorageScopes = []StorageScope{{Namespace: "billing", GoFiles: []string{"billing/**"}, SQLFiles: []string{"migrations/billing/**"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/multistore", Seeds: []string{"example.com/multistore/orders::Write"}, Depth: 3, MaxNodes: 100, Config: c})
	if err != nil {
		t.Fatal(err)
	}
	query, schema := false, false
	for _, b := range r.Boundaries {
		if b.Kind == "unresolved_storage_namespace" {
			if b.Node == "example.com/multistore/orders::Write" {
				query = true
			}
			if b.Evidence.File == "migrations/orders/001.sql" {
				schema = true
			}
		}
	}
	if !query || !schema {
		t.Errorf("unconfigured namespaces silently looked resolved: query=%t schema=%t", query, schema)
	}
}

func TestTypedStorageRuleOverridesFileScopeWithoutFallbackJoin(t *testing.T) {
	c := DefaultConfig()
	c.StorageScopes = []StorageScope{{Namespace: "file_default", GoFiles: []string{"sample.go"}}, {Namespace: "api_database", SQLFiles: []string{"migrations/*.sql"}}}
	c.CallRules = []CallRule{{Symbol: "database/sql::DB.Exec", Kind: "sql_query", Argument: 0, Namespace: "api_database"}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Write"}, Depth: 3, MaxNodes: 200, Config: c})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	operationSites := map[string]bool{}
	for _, e := range r.Relationships {
		if e.From == "example.com/sample::Write" && e.Kind == "sql_operation" {
			operationSites[e.To] = true
		}
	}
	operationTable := false
	for _, e := range r.Relationships {
		if (e.From == "example.com/sample::Write" || operationSites[e.From]) && strings.HasPrefix(e.Kind, "sql_") && strings.HasPrefix(e.To, "table:") {
			if e.To != "table:api_database:records" {
				t.Errorf("typed SQL site also joined fallback namespace: %s", e.To)
			} else {
				found = true
				operationTable = operationTable || operationSites[e.From]
			}
		}
	}
	if !found || !operationTable || !hasName(r, "migrations/001.sql") {
		t.Fatal("explicit API namespace did not join its schema")
	}
}

func TestAmbiguousStorageFileRulesFailExplicitly(t *testing.T) {
	for _, sqlFile := range []bool{false, true} {
		c := multiStoreConfig()
		conflicting := StorageScope{Namespace: "other"}
		if sqlFile {
			conflicting.SQLFiles = []string{"migrations/orders/**"}
		} else {
			conflicting.GoFiles = []string{"orders/**"}
		}
		c.StorageScopes = append(c.StorageScopes, conflicting)
		_, err := Trace(context.Background(), Options{Root: "testdata/multistore", Seeds: []string{"example.com/multistore/orders::Write"}, Depth: 3, MaxNodes: 100, Config: c})
		if err == nil || !strings.Contains(err.Error(), "ambiguous storage namespace") {
			t.Errorf("ambiguous SQL file=%t assignment was not rejected: %v", sqlFile, err)
		}
	}
}

func TestStorageScopeConfigurationRejectsUnusableRules(t *testing.T) {
	for _, input := range []string{
		`{"storage_scopes":[{"namespace":"","go_files":["orders/**"]}]}`,
		`{"storage_scopes":[{"namespace":"   ","sql_files":["schema.sql"]}]}`,
		`{"storage_scopes":[{"namespace":"orders"}]}`,
		`{"storage_scopes":[{"namespace":"orders","go_files":["["]}]}`,
		`{"storage_scopes":[{"namespace":"orders","sql_files":["../schema.sql"]}]}`,
	} {
		if _, err := ReadConfig(strings.NewReader(input)); err == nil {
			t.Errorf("invalid storage scope accepted: %s", input)
		}
	}
}
