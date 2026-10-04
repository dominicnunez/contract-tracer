package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLInventoryStopsAtNestedGoModuleBoundaries(t *testing.T) {
	root := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/rootmodule\n\ngo 1.27.0\n")
	write("app.go", `package app

import "example.com/rootmodule/api"

func RootSeed() { api.Query("SELECT id FROM shared_records") }
`)
	write("api/api.go", "package api\nfunc Query(string) {}\n")
	write("schema.sql", "CREATE TABLE shared_records (id INTEGER PRIMARY KEY);\n")
	write("nested/go.mod", "module example.com/nestedmodule\n\ngo 1.27.0\n")
	write("nested/nested.go", "package nested\nfunc NestedSeed() {}\n")
	write("nested/schema.sql", "CREATE TABLE shared_records (id INTEGER PRIMARY KEY);\nCREATE TABLE nested_only (id INTEGER);\n")

	rootConfig := Config{
		SQLFiles:      []string{"**/*.sql"},
		CallRules:     []CallRule{{Symbol: "example.com/rootmodule/api::Query", Kind: "sql_query", Argument: 0, Namespace: "root-store"}},
		StorageScopes: []StorageScope{{Namespace: "root-store", SQLFiles: []string{"**/*.sql"}}},
	}
	rootReport, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"RootSeed"}, Depth: 2, MaxNodes: 100, Config: rootConfig})
	if err != nil {
		t.Fatal(err)
	}
	if len(rootReport.Coverage.Storage.Files) != 1 || rootReport.Coverage.Storage.Files[0] != "schema.sql" {
		t.Errorf("root module SQL inventory crossed a nested go.mod boundary: %v", rootReport.Coverage.Storage.Files)
	}
	if !nestedModuleHasNode(rootReport, "table:root-store:shared_records", "table") {
		t.Error("root module schema table is missing")
	}
	if !nestedModuleHasRelationship(rootReport, "example.com/rootmodule::RootSeed", "table:root-store:shared_records", "sql_read", "app.go") {
		t.Error("parent module query did not join to its own shared_records schema")
	}
	if nestedModuleHasNode(rootReport, "table:root-store:nested_only", "table") {
		t.Error("nested-only table entered the parent module graph")
	}
	for _, node := range rootReport.Nodes {
		if node.Evidence.File == "nested/schema.sql" {
			t.Errorf("parent module report retained nested SQL node evidence: %+v", node)
		}
	}
	for _, edge := range rootReport.Relationships {
		if edge.To == "table:root-store:nested_only" || edge.Evidence.File == "nested/schema.sql" {
			t.Errorf("parent module report retained nested SQL relationship evidence: %+v", edge)
		}
	}
	for _, assignment := range rootReport.Coverage.Storage.Assignments {
		if assignment.File == "nested/schema.sql" {
			t.Error("nested module SQL received a root storage namespace despite the broad explicit glob")
		}
	}
	for _, file := range rootReport.Coverage.Files {
		if filepath.ToSlash(file) == "nested/nested.go" {
			t.Error("nested module Go source unexpectedly entered the parent module package inventory")
		}
	}

	nestedConfig := Config{
		SQLFiles:      []string{"**/*.sql"},
		StorageScopes: []StorageScope{{Namespace: "nested-store", SQLFiles: []string{"**/*.sql"}}},
	}
	nestedReport, err := Trace(context.Background(), Options{
		Root: filepath.Join(root, "nested"), Seeds: []string{"NestedSeed", "sql_file:nested-store:schema.sql"}, Depth: 2, MaxNodes: 100, Config: nestedConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(nestedReport.Coverage.Storage.Files) != 1 || nestedReport.Coverage.Storage.Files[0] != "schema.sql" {
		t.Errorf("nested module did not inventory its own SQL when selected as Root: %v", nestedReport.Coverage.Storage.Files)
	}
	if !nestedModuleHasNode(nestedReport, "table:nested-store:nested_only", "table") {
		t.Error("nested module table is missing when that module is selected as Root")
	}
}

func nestedModuleHasNode(report Report, id, kind string) bool {
	for _, node := range report.Nodes {
		if node.ID == id && node.Kind == kind {
			return true
		}
	}
	return false
}

func nestedModuleHasRelationship(report Report, from, to, kind, file string) bool {
	for _, edge := range report.Relationships {
		if edge.From == from && edge.To == to && edge.Kind == kind && edge.Evidence.File == file {
			return true
		}
	}
	return false
}
