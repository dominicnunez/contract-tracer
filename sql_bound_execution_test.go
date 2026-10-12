package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedSQLFindsBoundAndPromotedStatementExecutions(t *testing.T) {
	root := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/boundstatement\n\ngo 1.27.0\n")
	write("workflow.go", `package workflow

import "database/sql"

type promoted struct { *sql.Stmt }
type unrelatedQueryer struct{}
func (unrelatedQueryer) Query() {}

func inspectStatements() {
	db, _ := sql.Open("unused", "dsn")
	defer db.Close()
	rows, _ := db.Query("SELECT id FROM direct_records")
	if rows != nil { defer rows.Close() }
	stmt, _ := db.Prepare("SELECT id FROM bound_records")
	defer stmt.Close()

	run := stmt.Query
	run()
	wrapped := promoted{Stmt: stmt}
	wrapped.Query()
	query := (*sql.Stmt).Query
	query(stmt)
	stmt.Query()
}

func unrelatedQueryCall() {
	var queryer unrelatedQueryer
	queryer.Query()
}

func unknownStatement(stmt *sql.Stmt) {
	run := stmt.Query
	run("SELECT id FROM argument_decoy")
}

`)
	write("schema.sql", "CREATE TABLE bound_records (id INTEGER);\nCREATE TABLE direct_records (id INTEGER);\nCREATE TABLE argument_decoy (id INTEGER);\n")
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "bound", GoFiles: []string{"workflow.go"}, SQLFiles: []string{"schema.sql"}, DatabaseOrigins: []string{"example.com/boundstatement::inspectStatements"}}}
	report, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"inspectStatements", "unknownStatement", "unrelatedQueryCall"}, Depth: 5, MaxNodes: 300, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	owners := []struct {
		name, snippet string
	}{
		{"inspectStatements", "run()"},
		{"inspectStatements", "wrapped.Query()"},
		{"inspectStatements", "query(stmt)"},
		{"inspectStatements", "stmt.Query()"},
	}
	for _, want := range owners {
		found := false
		for _, edge := range report.Relationships {
			if edge.Kind == "sql_read" && strings.HasSuffix(edge.From, "::"+want.name) && edge.To == "table:bound:bound_records" && strings.Contains(edge.Evidence.Snippet, want.snippet) {
				found = true
			}
		}
		if !found {
			t.Errorf("prepared query was not connected to bound_records at %s: %s", want.name, want.snippet)
		}
	}
	unknown := false
	for _, edge := range report.Relationships {
		if strings.HasSuffix(edge.From, "::unknownStatement") && strings.HasPrefix(edge.To, "table:") {
			t.Errorf("bound Query argument was misread as SQL source: %+v", edge)
		}
		if strings.HasSuffix(edge.From, "::unrelatedQueryCall") && strings.HasPrefix(edge.To, "table:") {
			t.Errorf("same-name non-SQL method was treated as a prepared query: %+v", edge)
		}
	}
	direct := false
	for _, edge := range report.Relationships {
		direct = direct || edge.Kind == "sql_read" && edge.To == "table:bound:direct_records" && strings.Contains(edge.Evidence.Snippet, `db.Query("SELECT id FROM direct_records")`)
	}
	if !direct {
		t.Error("direct database Query control was lost")
	}
	for _, boundary := range report.Boundaries {
		if boundary.Kind == "unresolved_prepared_sql" && (strings.Contains(boundary.Evidence.Snippet, "db.Query(") || strings.Contains(boundary.Evidence.Snippet, "queryer.Query()")) {
			t.Errorf("non-statement Query call was classified as prepared SQL: %+v", boundary)
		}
	}
	for _, boundary := range report.Boundaries {
		unknown = unknown || boundary.Kind == "unresolved_prepared_sql" && strings.HasSuffix(boundary.Node, "::unknownStatement")
	}
	if !unknown {
		t.Error("opaque prepared statement execution did not retain unresolved query provenance")
	}
}
