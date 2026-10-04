package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinSQLInvocationFamilyPreservesParentsQueriesAndCleanup(t *testing.T) {
	root := t.TempDir()
	source := `package sqlfamily
import (
	"context"
	"database/sql"
)

func open() (*sql.DB, error) { return sql.Open("unused", "dsn") }
func queryBound(fn func(string) (*sql.Stmt, error), query string) (*sql.Stmt, error) { return fn(query) }
func beginBound(fn func() (*sql.Tx, error)) (*sql.Tx, error) { return fn() }
type statementPreparer interface { Prepare(string) (*sql.Stmt, error) }
func PrepareThroughInterface(preparer statementPreparer, query string) (*sql.Stmt, error) { return preparer.Prepare(query) }
type fakePreparer struct{}
func (fakePreparer) Prepare(string) (*sql.Stmt, error) { return nil, nil }
func UnrelatedPrepareThroughInterface(preparer statementPreparer, query string) (*sql.Stmt, error) { return preparer.Prepare(query) }

func SQLFamily() {
	db, _ := open()
	prepare := db.Prepare
	statement, _ := queryBound(prepare, "SELECT body FROM prepared_records")
	defer statement.Close()
	statement.Query()

	begin := db.Begin
	tx, _ := beginBound(begin)
	defer tx.Rollback()
	stmtMethod := (*sql.Tx).Stmt
	rebound := stmtMethod(tx, statement)
	defer rebound.Close()
	rebound.Query()
	stmtContextMethod := (*sql.Tx).StmtContext
	reboundWithContext := stmtContextMethod(tx, context.Background(), statement)
	defer reboundWithContext.Close()
	reboundWithContext.Query()
	txPrepare := tx.Prepare
	txStatement, _ := queryBound(txPrepare, "SELECT body FROM transaction_records")
	defer txStatement.Close()
	txStatement.Query()

	ctx := context.Background()
	conn, _ := db.Conn(ctx)
	connectionTransaction, _ := conn.BeginTx(ctx, nil)
	defer connectionTransaction.Rollback()
	prepareContext := conn.PrepareContext
	connStatement, _ := prepareContext(ctx, "SELECT body FROM connection_records")
	defer connStatement.Close()
	connStatement.Query()
	go conn.Close()
	db.Exec("INSERT INTO written_records (body) VALUES (?)", "body")
	interfaceStatement, _ := PrepareThroughInterface(db, "SELECT body FROM interface_records")
	interfaceStatement.Query()
	unrelated, _ := UnrelatedPrepareThroughInterface(fakePreparer{}, "SELECT body FROM unrelated_records")
	unrelated.Query()
}
`
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/sqlfamily\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "family.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"SQLFamily"}, Depth: 8, MaxNodes: 500})
	if err != nil {
		t.Fatal(err)
	}
	resources := map[string]string{}
	for _, node := range report.Nodes {
		if strings.HasPrefix(node.ID, "database:") || strings.HasPrefix(node.ID, "connection:") || strings.HasPrefix(node.ID, "statement:") || strings.HasPrefix(node.ID, "transaction:") {
			resources[node.ID] = node.Evidence.Snippet
		}
	}
	resourceFor := func(kind, snippet string) string {
		for id, evidence := range resources {
			if strings.HasPrefix(id, kind+":") && strings.Contains(evidence, snippet) {
				return id
			}
		}
		return ""
	}
	database := resourceFor("database", `sql.Open("unused", "dsn")`)
	connection := resourceFor("connection", `db.Conn(ctx)`)
	primaryStatement := resourceFor("statement", `fn(query)`)
	connectionStatement := resourceFor("statement", `prepareContext(ctx`)
	transaction := resourceFor("transaction", `fn()`)
	rebound := resourceFor("statement", `stmtMethod(tx, statement)`)
	txRebound := resourceFor("statement", `stmtContextMethod(tx, context.Background(), statement)`)
	transactionStatement := resourceFor("statement", `fn(query)`)
	connectionTransaction := resourceFor("transaction", `conn.BeginTx(ctx, nil)`)
	if database == "" || connection == "" || primaryStatement == "" || connectionStatement == "" || transaction == "" || rebound == "" || txRebound == "" || transactionStatement == "" || connectionTransaction == "" {
		t.Fatalf("SQL resource creation sites are missing: db=%q conn=%q stmt=%q connStmt=%q tx=%q rebound=%q txRebound=%q txStmt=%q connTx=%q resources=%v", database, connection, primaryStatement, connectionStatement, transaction, rebound, txRebound, transactionStatement, connectionTransaction, resources)
	}
	has := func(from, to, kind, snippet string) bool {
		for _, edge := range report.Relationships {
			if edge.From == from && edge.To == to && edge.Kind == kind && strings.Contains(edge.Evidence.Snippet, snippet) {
				return true
			}
		}
		return false
	}
	if !has(connection, database, "connection_database", "db.Conn(ctx)") {
		t.Errorf("Conn result lost its DB parent: conn=%s db=%s", connection, database)
	}
	if !has(transaction, database, "transaction_database", "fn()") {
		t.Errorf("Begin result lost its DB parent: tx=%s db=%s", transaction, database)
	}
	if !has(rebound, primaryStatement, "statement_rebind", "stmtMethod(tx, statement)") {
		t.Errorf("Tx.Stmt result lost its original statement candidate: rebound=%s stmt=%s", rebound, primaryStatement)
	}
	if !has(txRebound, primaryStatement, "statement_rebind", "stmtContextMethod(tx, context.Background(), statement)") {
		t.Errorf("Tx.StmtContext method expression lost its original statement candidate: rebound=%s stmt=%s", txRebound, primaryStatement)
	}
	if !has(transactionStatement, transaction, "statement_transaction", "fn(query)") {
		t.Errorf("Tx.Prepare alias helper lost the transaction parent candidate: stmt=%s tx=%s", transactionStatement, transaction)
	}
	if !has(connectionTransaction, connection, "transaction_connection", "conn.BeginTx(ctx, nil)") {
		t.Errorf("Conn.BeginTx result lost the connection parent: tx=%s conn=%s", connectionTransaction, connection)
	}
	queryEdge := func(owner, table, snippet string) bool {
		for _, edge := range report.Relationships {
			if strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "sql_read" && edge.To == "table:"+table && strings.Contains(edge.Evidence.Snippet, snippet) {
				return true
			}
		}
		return false
	}
	if !queryEdge("SQLFamily", "prepared_records", `statement.Query()`) || !queryEdge("SQLFamily", "connection_records", `connStatement.Query()`) || !queryEdge("SQLFamily", "transaction_records", `txStatement.Query()`) || !queryEdge("PrepareThroughInterface", "interface_records", `preparer.Prepare(query)`) {
		var evidence []string
		for _, edge := range report.Relationships {
			if strings.Contains(edge.From, "PrepareThroughInterface") || strings.Contains(edge.To, "PrepareThroughInterface") || strings.Contains(edge.Evidence.Snippet, "interface") || strings.Contains(edge.To, "interface_records") {
				evidence = append(evidence, edge.From+" -> "+edge.To+" ["+edge.Kind+"] "+edge.Evidence.Snippet)
			}
		}
		for _, boundary := range report.Boundaries {
			if strings.Contains(boundary.Evidence.Snippet, "interface") || strings.Contains(boundary.Evidence.Snippet, "preparer.Prepare") {
				evidence = append(evidence, boundary.Node+" ["+boundary.Kind+"] "+boundary.Evidence.Snippet)
			}
		}
		t.Errorf("prepared SQL query origins were lost: prepared=%t connection=%t transaction=%t interface=%t evidence=%v", queryEdge("SQLFamily", "prepared_records", `statement.Query()`), queryEdge("SQLFamily", "connection_records", `connStatement.Query()`), queryEdge("SQLFamily", "transaction_records", `txStatement.Query()`), queryEdge("PrepareThroughInterface", "interface_records", `preparer.Prepare(query)`), evidence)
	}
	write := false
	for _, edge := range report.Relationships {
		write = write || edge.Kind == "sql_write" && edge.To == "table:written_records" && strings.Contains(edge.Evidence.Snippet, `db.Exec("INSERT INTO written_records`)
	}
	if !write {
		t.Error("variadic DB.Exec invocation lost its write-table candidate")
	}
	interfaceOperation := ""
	interfaceBoundary := false
	for _, edge := range report.Relationships {
		if edge.Kind == "sql_operation" && strings.Contains(edge.Evidence.Snippet, "preparer.Prepare(query)") {
			interfaceOperation = edge.To
		}
	}
	operationID := interfaceOperation
	for _, edge := range report.Relationships {
		if edge.From == operationID && edge.To == database && edge.Kind == "sql_receiver" {
			interfaceOperation = "known"
		}
	}
	for _, boundary := range report.Boundaries {
		interfaceBoundary = interfaceBoundary || boundary.Kind == "unresolved_sql_handle" && boundary.Node == operationID && strings.Contains(boundary.Evidence.Snippet, "preparer.Prepare(query)")
	}
	if interfaceOperation != "known" || !interfaceBoundary {
		t.Errorf("SQL interface invocation must preserve the modeled DB alongside outside receiver uncertainty: known receiver=%t unresolved boundary=%t", interfaceOperation == "known", interfaceBoundary)
	}
	for _, edge := range report.Relationships {
		if edge.Kind == "sql_read" && edge.To == "table:alpha:unrelated_records" {
			t.Errorf("same-signature user-defined preparer was incorrectly assigned the unrelated modeled DB namespace: %+v", edge)
		}
	}
	cleanupHas := func(resource, kind, snippet string) bool {
		for _, edge := range report.Relationships {
			if edge.To == resource && edge.Kind == kind && strings.Contains(edge.Evidence.Snippet, snippet) {
				return true
			}
		}
		return false
	}
	for _, want := range []struct{ resource, kind, snippet string }{
		{primaryStatement, "cleanup_statement_close", `statement.Close()`},
		{rebound, "cleanup_statement_close", `rebound.Close()`},
		{txRebound, "cleanup_statement_close", `reboundWithContext.Close()`},
		{transactionStatement, "cleanup_statement_close", `txStatement.Close()`},
		{connectionStatement, "cleanup_statement_close", `connStatement.Close()`},
		{transaction, "cleanup_transaction_rollback", `tx.Rollback()`},
		{connectionTransaction, "cleanup_transaction_rollback", `connectionTransaction.Rollback()`},
	} {
		if !cleanupHas(want.resource, want.kind, want.snippet) {
			t.Errorf("missing source-site cleanup registration resource=%s kind=%s site=%s", want.resource, want.kind, want.snippet)
		}
	}
	if !has("example.com/sqlfamily::SQLFamily", connection, "connection_close", "go conn.Close()") {
		t.Errorf("goroutine Close site lost the exact connection resource: %s", connection)
	}
}
