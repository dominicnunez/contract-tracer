package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExternalSchemaContract(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Write"}, Depth: 3, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "migrations/001.sql") {
		t.Error("reader/writer contract omitted its migration file")
	}
	trigger, fk, index := false, false, false
	for _, e := range r.Relationships {
		if e.Evidence.File == "migrations/001.sql" {
			if e.To == "table:audit_log" && e.Kind == "sql_write" && e.Evidence.Line == 6 {
				trigger = true
			}
			if e.To == "table:records" && e.Kind == "sql_schema_ref" && e.Evidence.Line == 3 {
				fk = true
			}
			if e.To == "table:records" && e.Kind == "sql_schema" && e.Evidence.Line == 4 {
				index = true
			}
		}
	}
	if !trigger || !fk || !index {
		t.Errorf("precise schema relationships missing: trigger=%t fk=%t index=%t", trigger, fk, index)
	}
}

func TestEmbeddedSQLValueFlow(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"LoadEmbedded"}, Depth: 3, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range r.Relationships {
		if e.From == "example.com/sample::LoadEmbedded" && e.To == "table:embedded_records" && e.Kind == "sql_schema" {
			found = true
		}
	}
	if !found || !hasName(r, "EmbeddedRead") {
		t.Error("embedded schema value did not connect the loader to its persisted-state readers")
	}
}
func TestSQLSourceIdentityAndParserFailure(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "schema.SQL")
	if err := os.WriteFile(file, []byte("CREATE TABLE records(id);"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _, err := fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("CREATE TABLE records(id, version);"), 0600); err != nil {
		t.Fatal(err)
	}
	after, _, err := fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Error("SQL source changes did not revoke source identity")
	}
	if _, err := parseSQLTables("SELECT FROM records"); err == nil {
		t.Error("invalid SQL looked fully parsed")
	}
}
