package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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

func sqlSymlinkFixture(t *testing.T) (root, outsideSQL, symlinkPath, sentinel string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "analysis")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/symlinkfixture\n\ngo 1.27.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package app\nfunc Seed() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sentinel = "outside-root-sql-sentinel-4fd829"
	externalSQL := filepath.Join(base, "external.sql")
	if err := os.WriteFile(externalSQL, []byte("-- "+sentinel+"\nCREATE TABLE external_inventory(id TEXT);\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "schema.sql"), []byte("-- "+sentinel+"\nCREATE TABLE external_inventory(id TEXT);\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, externalSQL, filepath.Join(root, "schema.sql"), sentinel
}

func linkOutsideSQL(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("platform does not permit creating a symlink: %v", err)
	}
}

func assertSQLSymlinkRejected(t *testing.T, err error, operation, sentinel string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s accepted SQL symlink to a file outside the analysis root", operation)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Errorf("%s did not report a clear SQL symlink rejection: %v", operation, err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Errorf("%s disclosed outside SQL content in its error", operation)
	}
}

func TestTraceRejectsSQLSymlinkToOutsideRoot(t *testing.T) {
	root, externalSQL, link, sentinel := sqlSymlinkFixture(t)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	linkOutsideSQL(t, externalSQL, link)
	report, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"schema.sql"}, Depth: 1, MaxNodes: 20})
	if err == nil {
		leaked := false
		for _, node := range report.Nodes {
			if node.Kind == "sql_file" && strings.Contains(node.Evidence.Snippet, sentinel) {
				leaked = true
			}
		}
		t.Errorf("Trace accepted outside SQL symlink (sentinel evidence disclosed=%t)", leaked)
	}
	assertSQLSymlinkRejected(t, err, "Trace", sentinel)
}

func TestTraceRejectsSQLSymlinkEvenWhenExcludedByConfig(t *testing.T) {
	root, externalSQL, link, sentinel := sqlSymlinkFixture(t)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	linkOutsideSQL(t, externalSQL, link)
	config := DefaultConfig()
	config.SQLFiles = []string{"migrations/**/*.sql"}
	_, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20, Config: config})
	assertSQLSymlinkRejected(t, err, "Trace", sentinel)
}

func TestExploreRejectsSQLSymlinkAddedAfterSnapshot(t *testing.T) {
	root, externalSQL, link, sentinel := sqlSymlinkFixture(t)
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"schema.sql"}, Depth: 1, MaxNodes: 20})
	if err != nil {
		t.Fatalf("TraceWithAnalysis on the clean root: %v", err)
	}
	var snapshot bytes.Buffer
	if err := WriteAnalysis(&snapshot, analysis, true); err != nil {
		t.Fatalf("save clean analysis: %v", err)
	}
	analysis, err = ReadAnalysis(&snapshot)
	if err != nil {
		t.Fatalf("reload clean analysis: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	linkOutsideSQL(t, externalSQL, link)
	_, err = Explore(context.Background(), analysis, ExploreOptions{Seeds: []string{"schema.sql"}, Depth: 1, MaxNodes: 20})
	assertSQLSymlinkRejected(t, err, "Explore", sentinel)
}

func TestTraceRejectsDanglingSQLSymlink(t *testing.T) {
	root, _, link, sentinel := sqlSymlinkFixture(t)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	danglingTarget := filepath.Join(filepath.Dir(root), "missing-external.sql")
	linkOutsideSQL(t, danglingTarget, link)
	_, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"schema.sql"}, Depth: 1, MaxNodes: 20})
	assertSQLSymlinkRejected(t, err, "Trace", sentinel)
}
