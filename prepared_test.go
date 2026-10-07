package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestPreparedSQLConnectsExecutionAndExcludesParameterText(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PreparedStart", "UnknownStatement"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	execution, close, unknown := false, false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::ExecuteRecords") && e.To == "table:prepared_records" && e.Kind == "sql_write" {
			execution = true
		}
		close = close || e.Kind == "statement_close" && strings.HasSuffix(e.From, "::PreparedStart")
		if strings.HasSuffix(e.From, "::ExecuteRecords") && e.To == "table:decoy_payload" {
			t.Fatal("bound parameter was interpreted as a prepared query")
		}
		if strings.HasSuffix(e.From, "::UnknownStatement") && e.To == "table:unknown_payload" {
			t.Fatal("unknown statement parameter invented a query")
		}
	}
	for _, b := range r.Boundaries {
		unknown = unknown || b.Kind == "unresolved_prepared_sql" && strings.HasSuffix(b.Node, "::UnknownStatement")
	}
	if !execution || !close || !unknown {
		t.Fatalf("prepared contract path missing: execution=%t close=%t unknown=%t", execution, close, unknown)
	}
}

func TestTransactionAndPreparedLifecycleSites(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"TransactionStatement"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	transaction := ""
	commit, rollback, query, origin := false, false, false, false
	parent, cleanupStatement, cleanupTransaction := false, false, false
	for _, e := range r.Relationships {
		if e.Kind == "transaction_commit" && strings.HasSuffix(e.From, "::TransactionStatement") {
			transaction = e.To
			commit = true
		}
		query = query || e.Kind == "sql_read" && strings.HasSuffix(e.From, "::TransactionStatement") && e.To == "table:transaction_records"
		origin = origin || e.Kind == "prepared_sql_read" && e.To == "table:transaction_records" && strings.Contains(e.Evidence.Snippet, "transaction.Prepare")
	}
	for _, e := range r.Relationships {
		rollback = rollback || e.Kind == "transaction_rollback" && e.To == transaction
		parent = parent || e.Kind == "statement_transaction" && e.To == transaction
		cleanupStatement = cleanupStatement || e.Kind == "cleanup_statement_close"
		cleanupTransaction = cleanupTransaction || e.Kind == "cleanup_transaction_rollback" && e.To == transaction
	}
	if transaction == "" || !commit || !rollback || !query || !origin || !parent || !cleanupStatement || !cleanupTransaction {
		t.Fatalf("transaction lifecycle scope missing: commit=%t rollback=%t query=%t origin=%t parent=%t cleanupStatement=%t cleanupTransaction=%t", commit, rollback, query, origin, parent, cleanupStatement, cleanupTransaction)
	}
}

func TestPreparedSQLRetainsPreparationNamespaceAcrossFiles(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{
		{Namespace: "origin", GoFiles: []string{"prepared.go"}},
		{Namespace: "execution", GoFiles: []string{"prepared_execution.go"}},
	}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PreparedStart"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range r.Relationships {
		if !strings.HasSuffix(edge.From, "::ExecuteRecords") || edge.Kind != "sql_write" {
			continue
		}
		if edge.To != "table:origin:prepared_records" {
			t.Fatalf("execution changed the preparation database scope: %+v", edge)
		}
		found = true
	}
	if !found {
		t.Fatal("cross-file prepared execution lost its preparation namespace")
	}
}

func TestTransactionRebindingRetainsQueryAndOwnership(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "prepared", GoFiles: []string{"prepared.go"}}, {Namespace: "execution", GoFiles: []string{"prepared_execution.go"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"ReboundStatement", "ReboundUnknown"}, Depth: 5, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	rebindings := map[string]bool{}
	parents := map[string]string{}
	cleanup := ""
	query, unknown := false, false
	for _, edge := range r.Relationships {
		if edge.Kind == "statement_rebind" && strings.Contains(edge.Evidence.Snippet, "transaction.Stmt") {
			rebindings[edge.From] = true
		}
		if edge.Kind == "statement_transaction" {
			parents[edge.From] = edge.To
		}
		if edge.Kind == "cleanup_statement_close" && strings.Contains(edge.Evidence.Snippet, "second.Close") {
			cleanup = edge.To
		}
		query = query || edge.Kind == "sql_write" && strings.HasSuffix(edge.From, "::ExecuteRebound") && edge.To == "table:prepared:prepared_records"
		if strings.Contains(edge.To, "decoy") && strings.HasPrefix(edge.To, "table:") {
			t.Fatalf("rebound statement parameters became SQL: %+v", edge)
		}
	}
	for _, boundary := range r.Boundaries {
		unknown = unknown || boundary.Kind == "unresolved_prepared_sql" && strings.HasSuffix(boundary.Node, "::ReboundUnknown")
	}
	if len(rebindings) != 2 || cleanup == "" || !rebindings[cleanup] || !query || !unknown {
		t.Fatalf("rebound path incomplete: rebindings=%d cleanup=%q query=%t unknown=%t", len(rebindings), cleanup, query, unknown)
	}
	transaction := parents[cleanup]
	for rebound := range rebindings {
		if transaction == "" || parents[rebound] != transaction {
			t.Fatalf("rebound statement lost transaction identity: %+v", parents)
		}
	}
}
