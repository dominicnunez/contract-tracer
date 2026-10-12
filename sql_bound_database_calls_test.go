package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundDatabaseCallsUseModeledSQLReceivers(t *testing.T) {
	root := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/boundsql\n\ngo 1.27.0\n")
	write("workflow.go", `package boundsql

import (
	"context"
	"database/sql"
)

type Wrapped struct { *sql.DB }
type unrelated struct{}
func (unrelated) Query() {}
type Queryer interface { Query(string, ...any) (*sql.Rows, error) }

func OpenPrimary() *sql.DB {
	db, _ := sql.Open("driver", "primary")
	return db
}
func OpenArchive() *sql.DB {
	db, _ := sql.Open("driver", "archive")
	return db
}

func DirectQuery() {
	db := OpenPrimary()
	db.Query("SELECT id FROM shared_records")
}
func BoundQuery() {
	db := OpenPrimary()
	query := db.Query
	query("SELECT id FROM shared_records")
}
func ConfiguredBoundQuery() {
	db := OpenPrimary()
	query := db.Query
	query("SELECT id FROM configured_records")
}
func runBoundQuery(db *sql.DB, text string) {
	query := db.Query
	query(text)
}
func QueryThroughHelper() {
	runBoundQuery(OpenPrimary(), "SELECT id FROM helper_records")
}
func QueryThroughEmbedding() {
	db := OpenPrimary()
	wrapped := &Wrapped{DB: db}
	query := wrapped.Query
	query("SELECT id FROM promoted_records")
}
func QueryMethodExpression() {
	db := OpenPrimary()
	(*sql.DB).Query(db, "SELECT id FROM expression_records")
}
func BoundExecContext() {
	db := OpenPrimary()
	exec := db.ExecContext
	exec(context.Background(), "UPDATE exec_records SET id = 1")
}
func BoundConnectionQuery() {
	db := OpenPrimary()
	conn, _ := db.Conn(context.Background())
	query := conn.QueryContext
	query(context.Background(), "SELECT id FROM connection_records")
}
func BoundTransactionQuery() {
	db := OpenPrimary()
	tx, _ := db.Begin()
	query := tx.Query
	query("SELECT id FROM transaction_records")
}
func ArchiveBoundQuery() {
	db := OpenArchive()
	query := db.Query
	query("SELECT id FROM shared_records")
}
func UnrelatedBoundQuery() {
	query := (unrelated{}).Query
	query()
}
func UnknownBoundQuery(db *sql.DB) {
	query := db.Query
	query("SELECT id FROM unknown_records")
}
func queryThroughInterface(q Queryer, text string) {
	q.Query(text)
}
func QueryKnownAndOutsideInterface(outside Queryer) {
	db := OpenPrimary()
	var query Queryer = db
	if outside != nil {
		query = outside
	}
	queryThroughInterface(query, "SELECT id FROM interface_records")
}
func PreparedQuery() {
	db := OpenPrimary()
	stmt, _ := db.Prepare("SELECT id FROM prepared_records")
	query := stmt.Query
	query("decoy_records")
}
`)

	config := DefaultConfig()
	config.StorageScopes = []StorageScope{
		{Namespace: "primary", DatabaseOrigins: []string{"example.com/boundsql::OpenPrimary"}},
		{Namespace: "archive", DatabaseOrigins: []string{"example.com/boundsql::OpenArchive"}},
	}
	seeds := []string{
		"DirectQuery", "BoundQuery", "ConfiguredBoundQuery", "QueryThroughHelper", "QueryThroughEmbedding",
		"QueryMethodExpression", "BoundExecContext", "BoundConnectionQuery", "BoundTransactionQuery",
		"ArchiveBoundQuery", "UnrelatedBoundQuery", "UnknownBoundQuery", "QueryKnownAndOutsideInterface", "PreparedQuery",
	}
	report, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 4, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot bytes.Buffer
	if err := WriteAnalysis(&snapshot, analysis, true); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(bytes.NewReader(snapshot.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 4, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range []Report{report, resumed} {
		for _, want := range []struct{ owner, table, namespace, kind string }{
			{"DirectQuery", "shared_records", "primary", "sql_read"},
			{"BoundQuery", "shared_records", "primary", "sql_read"},
			{"ConfiguredBoundQuery", "configured_records", "primary", "sql_read"},
			{"runBoundQuery", "helper_records", "primary", "sql_read"},
			{"QueryThroughEmbedding", "promoted_records", "primary", "sql_read"},
			{"QueryMethodExpression", "expression_records", "primary", "sql_read"},
			{"BoundExecContext", "exec_records", "primary", "sql_write"},
			{"BoundConnectionQuery", "connection_records", "primary", "sql_read"},
			{"BoundTransactionQuery", "transaction_records", "primary", "sql_read"},
			{"ArchiveBoundQuery", "shared_records", "archive", "sql_read"},
			{"PreparedQuery", "prepared_records", "primary", "sql_read"},
		} {
			owner := "example.com/boundsql::" + want.owner
			table := "table:" + want.namespace + ":" + want.table
			if !hasRelationship(current, owner, table, want.kind, "workflow.go", 0) {
				t.Errorf("%s did not inventory %s from its modeled SQL receiver", want.owner, table)
			}
		}
		for _, edge := range current.Relationships {
			if strings.HasSuffix(edge.From, "::UnrelatedBoundQuery") && edge.Kind == "sql_read" {
				t.Errorf("unrelated Query method was treated as database/sql: %+v", edge)
			}
			if strings.HasSuffix(edge.From, "::PreparedQuery") && strings.Contains(edge.To, "decoy_records") &&
				(edge.Kind == "sql_read" || edge.Kind == "sql_write") {
				t.Errorf("prepared statement execution argument was misread as default SQL: %+v", edge)
			}
		}
		if hasRelationship(current, "example.com/boundsql::BoundQuery", "table:archive:shared_records", "sql_read", "workflow.go", 0) ||
			hasRelationship(current, "example.com/boundsql::ArchiveBoundQuery", "table:primary:shared_records", "sql_read", "workflow.go", 0) {
			t.Error("a same-table query leaked across primary/archive receiver namespaces")
		}
		if !hasRelationship(current, "example.com/boundsql::UnknownBoundQuery", "table:unknown_records", "sql_read", "workflow.go", 0) ||
			!hasBoundaryKind(current, "example.com/boundsql::UnknownBoundQuery", "unresolved_database_namespace") {
			t.Error("unknown bound receiver lost its possible query candidate or namespace uncertainty")
		}
		if !hasRelationship(current, "example.com/boundsql::queryThroughInterface", "table:primary:interface_records", "sql_read", "workflow.go", 0) ||
			!hasBoundaryKind(current, "example.com/boundsql::queryThroughInterface", "unresolved_database_namespace") {
			t.Error("known interface receiver candidate or outside namespace uncertainty was lost")
		}
	}
	withRule := config
	withRule.CallRules = []CallRule{{Symbol: "database/sql::DB.Query", Kind: "sql_query", Argument: 0, Namespace: "rule"}}
	configuredReport, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"ConfiguredBoundQuery"}, Depth: 3, MaxNodes: 100, Config: withRule})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRelationship(configuredReport, "example.com/boundsql::ConfiguredBoundQuery", "table:rule:configured_records", "sql_read", "workflow.go", 0) ||
		hasRelationship(configuredReport, "example.com/boundsql::ConfiguredBoundQuery", "table:primary:configured_records", "sql_read", "workflow.go", 0) {
		t.Error("a configured sql_query rule did not take precedence for a bound database method")
	}
	disabled := config
	disabled.SQLMethods = []string{}
	disabledReport, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"BoundQuery"}, Depth: 3, MaxNodes: 100, Config: disabled})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range disabledReport.Relationships {
		if edge.From == "example.com/boundsql::BoundQuery" && (edge.Kind == "sql_read" || edge.Kind == "sql_write") {
			t.Error("canonical database/sql method metadata bypassed the configured SQLMethods filter")
			break
		}
	}
}
