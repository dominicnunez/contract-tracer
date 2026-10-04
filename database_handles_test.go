package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestDatabaseHandlesAcrossSharedQueryAndConnection(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"DatabasePaths", "UnknownDatabaseQuery", "ConnectorDatabase"}, Depth: 5, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	databases := map[string]bool{}
	firstDatabase := ""
	expectedShared := map[string]bool{}
	for _, node := range r.Nodes {
		if node.Kind == "database" {
			databases[node.ID] = true
			if strings.Contains(node.Evidence.Snippet, "sql.Open(") {
				expectedShared[node.ID] = true
			}
			if strings.Contains(node.Evidence.Snippet, "first, _ := sql.Open") {
				firstDatabase = node.ID
			}
		}
	}
	operation := ""
	for _, edge := range r.Relationships {
		if edge.Kind == "sql_operation" && strings.HasSuffix(edge.From, "::SharedDatabaseQuery") {
			operation = edge.To
		}
	}
	queryHandles := map[string]bool{}
	connectionID := ""
	transactionParents := map[string]bool{}
	cleanupDB := map[string]bool{}
	cleanupConn := map[string]bool{}
	queryTable := false
	for _, edge := range r.Relationships {
		if edge.Kind == "sql_receiver" && edge.From == operation && databases[edge.To] {
			queryHandles[edge.To] = true
		}
		if edge.Kind == "connection_database" && edge.To == firstDatabase && strings.Contains(edge.Evidence.Snippet, "first.Conn") {
			connectionID = edge.From
		}
		if edge.Kind == "transaction_connection" && strings.Contains(edge.Evidence.Snippet, "connection.BeginTx") {
			transactionParents[edge.To] = true
		}
		if edge.Kind == "cleanup_database_close" && (strings.Contains(edge.Evidence.Snippet, "first.Close") || strings.Contains(edge.Evidence.Snippet, "second.Close")) {
			cleanupDB[edge.To] = true
		}
		if edge.Kind == "cleanup_connection_close" {
			cleanupConn[edge.To] = true
		}
		queryTable = queryTable || edge.From == operation && edge.To == "table:shared_records" && edge.Kind == "sql_write"
	}
	unknown := false
	for _, boundary := range r.Boundaries {
		unknown = unknown || boundary.Kind == "unresolved_sql_handle" && strings.Contains(boundary.Evidence.Snippet, "external_records")
	}
	for id := range expectedShared {
		if !queryHandles[id] {
			t.Fatalf("shared helper lost independently identified Open handle: %s", id)
		}
	}
	if len(databases) != 5 || len(expectedShared) != 4 || len(queryHandles) != 4 || firstDatabase == "" || connectionID == "" || !transactionParents[connectionID] || len(cleanupDB) != 2 || !cleanupConn[connectionID] || !queryTable || !unknown {
		t.Fatalf("database contract links missing: databases=%d sharedReceivers=%d first=%q connection=%q transaction=%t cleanupDB=%d cleanupConn=%t table=%t unknown=%t", len(databases), len(queryHandles), firstDatabase, connectionID, transactionParents[connectionID], len(cleanupDB), cleanupConn[connectionID], queryTable, unknown)
	}
}

func TestDatabaseOriginNamespacesAcrossSharedHelpers(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{
		{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}},
		{Namespace: "audit", DatabaseOrigins: []string{"example.com/sample::OpenAuditDatabase"}},
	}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"NamespacedDatabasePaths"}, Depth: 5, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	writes := map[string]bool{}
	prepared := false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::SharedDatabaseQuery") && edge.Kind == "sql_write" {
			writes[edge.To] = true
		}
		prepared = prepared || strings.HasSuffix(edge.From, "::NamespacedDatabasePaths") && edge.Kind == "sql_read" && edge.To == "table:orders:scoped_records"
	}
	if !writes["table:orders:shared_records"] || !writes["table:audit:shared_records"] || !prepared {
		t.Fatalf("origin namespaces lost across wrappers or transaction preparation: writes=%v prepared=%t", writes, prepared)
	}
	// Other unconfigured callers also reach the shared helper. Do not hide them.
	unresolved := false
	for _, boundary := range r.Boundaries {
		unresolved = unresolved || boundary.Kind == "unresolved_database_namespace"
	}
	if !unresolved {
		t.Fatal("unconfigured receiver candidates were hidden")
	}
}

func TestDatabaseOriginConfigurationRejectsAmbiguity(t *testing.T) {
	for _, origins := range [][]StorageScope{
		{{Namespace: "orders", DatabaseOrigins: []string{"unqualified"}}},
		{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}, {Namespace: "audit", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}},
	} {
		config := DefaultConfig()
		config.StorageScopes = origins
		if _, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"NamespacedDatabasePaths"}, Config: config}); err == nil {
			t.Fatal("invalid/ambiguous origin declaration was accepted")
		}
	}
}

func TestTypedSQLNamespaceOverridesDatabaseOrigins(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	config.CallRules = []CallRule{{Symbol: "database/sql::DB.Exec", Kind: "sql_query", Argument: 0, Namespace: "explicit"}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"NamespacedDatabasePaths"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::SharedDatabaseQuery") && edge.Kind == "sql_write" {
			if edge.To != "table:explicit:shared_records" {
				t.Fatalf("origin overrode explicit API namespace: %+v", edge)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("explicit API namespace lost")
	}
}

func TestUnmatchedDatabaseOriginIsDisclosed(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::MissingConstructor"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"NamespacedDatabasePaths"}, Depth: 2, MaxNodes: 100, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range r.Boundaries {
		if boundary.Kind == "unmatched_database_origin" && strings.Contains(boundary.Reason, "MissingConstructor") {
			return
		}
	}
	t.Fatal("unmatched database declaration silently looked usable")
}

func TestKnownAndUnknownDatabaseInputsRetainUncertainty(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"MixedDatabaseInput"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]bool{}
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::mixedDatabaseQuery") && edge.Kind == "sql_read" {
			reads[edge.To] = true
		}
	}
	for _, table := range []string{"mixed_receiver_records", "mixed_statement_records"} {
		if !reads["table:orders:"+table] || !reads["table:"+table] {
			t.Fatalf("known namespace hid unknown receiver candidate for %s: %v", table, reads)
		}
	}
	boundary := false
	cleanup := false
	for _, b := range r.Boundaries {
		boundary = boundary || b.Kind == "unresolved_database_namespace" && strings.HasSuffix(b.Node, "::mixedDatabaseQuery")
		cleanup = cleanup || b.Kind == "unresolved_cleanup_resource" && strings.Contains(b.Evidence.Snippet, "db.Close")
	}
	if !boundary || !cleanup {
		t.Fatal("mixed known/unknown input lost its namespace boundary")
	}
}

func TestMixedStatementRebindingRetainsUnknownOrigin(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"MixedStatementInput"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	query, rebind, unresolved := false, false, false
	for _, edge := range r.Relationships {
		query = query || strings.HasSuffix(edge.From, "::mixedStatementRebind") && edge.Kind == "sql_read" && edge.To == "table:mixed_rebind_records"
	}
	for _, b := range r.Boundaries {
		rebind = rebind || b.Kind == "unresolved_statement_rebind" && strings.Contains(b.Evidence.Snippet, "transaction.Stmt")
		unresolved = unresolved || b.Kind == "unresolved_prepared_sql" && strings.HasSuffix(b.Node, "::mixedStatementRebind")
	}
	if !query || !rebind || !unresolved {
		t.Fatalf("mixed statement origin path missing: query=%t rebind=%t unresolved=%t", query, rebind, unresolved)
	}
}

func TestBoundPrepareAliasesRetainKnownOrigins(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"OpaqueStatementInput"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	known, opaque := false, false
	for _, edge := range r.Relationships {
		known = known || strings.HasSuffix(edge.From, "::opaqueStatementQuery") && edge.Kind == "sql_read" && edge.To == "table:unknown_candidate_records"
		opaque = opaque || strings.HasSuffix(edge.From, "::opaqueStatementQuery") && edge.Kind == "sql_read" && edge.To == "table:opaque_records"
	}
	for _, b := range r.Boundaries {
		if strings.HasSuffix(b.Node, "::opaqueStatementQuery") && b.Kind == "unresolved_prepared_sql" {
			t.Fatalf("modeled bound Prepare alias retained an obsolete unknown prepared-query boundary: %+v", b)
		}
	}
	if !known || !opaque {
		t.Fatalf("bound Prepare aliases lost known query candidates: known=%t opaque=%t", known, opaque)
	}
}

func TestOpaqueAndNilStatementCandidatesDoNotDisappear(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"UnknownStatementInput", "NilStatementInput"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"unknownStatementQuery", "NilStatementInput"} {
		known, unresolved := false, false
		for _, edge := range r.Relationships {
			known = known || strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "sql_read" && edge.To == "table:unknown_candidate_records"
		}
		for _, b := range r.Boundaries {
			unresolved = unresolved || strings.HasSuffix(b.Node, "::"+owner) && b.Kind == "unresolved_prepared_sql"
		}
		if !known || !unresolved {
			t.Fatalf("%s discarded a known or unknown statement candidate: known=%t unresolved=%t", owner, known, unresolved)
		}
	}
}

func TestExportedSQLHelperRetainsExternalInputDespiteLocalCaller(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PublicDatabaseCaller"}, Depth: 3, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	known, unknown, entry := false, false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::PublicDatabaseQuery") && edge.Kind == "sql_read" {
			known = known || edge.To == "table:orders:public_input_records"
			unknown = unknown || edge.To == "table:public_input_records"
		}
	}
	for _, b := range r.Boundaries {
		entry = entry || b.Kind == "external_sql_input" && strings.HasSuffix(b.Node, "::PublicDatabaseQuery") && strings.Contains(b.Evidence.Snippet, "PublicDatabaseQuery")
	}
	if !known || !unknown || !entry {
		t.Fatalf("local caller incorrectly closed exported SQL input: known=%t unknown=%t entry=%t", known, unknown, entry)
	}
}

func TestPrivateSQLHelperKeepsKnownLocalOrigin(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PrivateDatabaseCaller"}, Depth: 3, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::privateDatabaseQuery") && edge.Kind == "sql_read" {
			if edge.To != "table:orders:private_input_records" {
				t.Fatalf("private closed helper acquired an unrelated namespace: %+v", edge)
			}
			found = true
		}
	}
	for _, b := range r.Boundaries {
		if b.Kind == "external_sql_input" && strings.HasSuffix(b.Node, "::privateDatabaseQuery") {
			t.Fatal("private helper with known caller was classified as an exported entry")
		}
	}
	if !found {
		t.Fatal("private SQL helper lost its known namespace")
	}
}

func TestEscapingPrivateSQLCallbackRetainsUnknownInput(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"EscapingSQLCaller"}, Depth: 3, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	known, unknown, escape, boundary := false, false, false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::escapingSQLQuery") && edge.Kind == "sql_read" {
			known = known || edge.To == "table:orders:escaping_input_records"
			unknown = unknown || edge.To == "table:escaping_input_records"
		}
		escape = escape || edge.Kind == "callback_escape" && strings.HasSuffix(edge.To, "::escapingSQLQuery") && strings.Contains(edge.Evidence.Snippet, "reflect.ValueOf(callback)")
	}
	for _, b := range r.Boundaries {
		boundary = boundary || b.Kind == "escaped_sql_input" && strings.HasSuffix(b.Node, "::escapingSQLQuery")
	}
	if !known || !unknown || !escape || !boundary {
		t.Fatalf("escaped private callback origin was closed: known=%t unknown=%t escape=%t boundary=%t", known, unknown, escape, boundary)
	}
}

func TestFunctionEscapeIsReportedWithoutSQLParameters(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"GenericCallbackEscape"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	escape, boundary := false, false
	for _, edge := range r.Relationships {
		escape = escape || edge.Kind == "callback_escape" && edge.Certainty == "possible" && strings.HasSuffix(edge.To, "::privateTextConsumer")
	}
	for _, b := range r.Boundaries {
		boundary = boundary || b.Kind == "external_callback" && strings.HasSuffix(b.Node, "::privateTextConsumer")
	}
	if !escape || !boundary {
		t.Fatalf("generic callback escape missing: edge=%t boundary=%t", escape, boundary)
	}
}

func TestExportedCallbackReturnRetainsUnknownInputs(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"ReturnedCallbackCaller"}, Depth: 3, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	known, unknown, escape, boundary := false, false, false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::privateReturnedQuery") && edge.Kind == "sql_read" {
			known = known || edge.To == "table:orders:returned_callback_records"
			unknown = unknown || edge.To == "table:returned_callback_records"
		}
		escape = escape || edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.From, "::PublicCallbackFactory") && strings.HasSuffix(edge.To, "::privateReturnedQuery") && strings.Contains(edge.Evidence.Snippet, "return privateCallbackFactory")
	}
	for _, b := range r.Boundaries {
		boundary = boundary || b.Kind == "escaped_sql_input" && strings.HasSuffix(b.Node, "::privateReturnedQuery")
	}
	if !known || !unknown || !escape || !boundary {
		t.Fatalf("public callback return hid inputs: known=%t unknown=%t escape=%t boundary=%t", known, unknown, escape, boundary)
	}
}

func TestPrivateReturnedCallbackUsesResolvedLocalCaller(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"ClosedCallbackCaller"}, Depth: 3, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::closedCallbackQuery") && edge.Kind == "sql_read" {
			if edge.To != "table:orders:closed_callback_records" {
				t.Fatalf("resolved local callback retained a fabricated external namespace: %+v", edge)
			}
			found = true
		}
	}
	for _, b := range r.Boundaries {
		if b.Kind == "external_sql_input" && strings.HasSuffix(b.Node, "::closedCallbackQuery") {
			t.Fatal("resolved local callback still claimed to have no local caller")
		}
	}
	if !found {
		t.Fatal("resolved callback lost its local namespace")
	}
}

func TestResolvedLocalDatabaseFactoryDoesNotInventExternalResult(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"ClosedDatabaseFactoryCaller"}, Depth: 3, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::closedDatabaseConsumer") && edge.Kind == "sql_read" {
			if edge.To != "table:orders:closed_factory_records" {
				t.Fatalf("resolved local factory invented an external result: %+v", edge)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("local factory origin was lost")
	}
}

func TestExportedRecordReturnTracesAccessibleCallbacks(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"RecordCallbackCaller"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	known, unknown, escape := false, false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::recordCallbackQuery") && edge.Kind == "sql_read" {
			known = known || edge.To == "table:orders:record_callback_records"
			unknown = unknown || edge.To == "table:record_callback_records"
		}
		escape = escape || edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.From, "::PublicRecordFactory") && strings.HasSuffix(edge.To, "::recordCallbackQuery")
		if edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.To, "::hiddenRecordQuery") {
			t.Fatal("private field was treated as accessible to outside callers")
		}
		if strings.HasSuffix(edge.From, "::hiddenRecordQuery") && edge.Kind == "sql_read" && edge.To != "table:orders:hidden_record_records" {
			t.Fatalf("private field acquired an outside SQL origin: %+v", edge)
		}
	}
	if !known || !unknown || !escape {
		t.Fatalf("returned record hid public callback: known=%t unknown=%t escape=%t", known, unknown, escape)
	}
}

func TestDependencyRecordArgumentRetainsCallbackEscape(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"RecordArgumentCaller"}, Depth: 3, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	known, unknown, escape := false, false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::argumentRecordQuery") && edge.Kind == "sql_read" {
			known = known || edge.To == "table:orders:argument_record_records"
			unknown = unknown || edge.To == "table:argument_record_records"
		}
		escape = escape || edge.Kind == "callback_escape" && strings.HasSuffix(edge.To, "::argumentRecordQuery") && strings.Contains(edge.Evidence.Snippet, "reflect.ValueOf(record)")
	}
	if !known || !unknown || !escape {
		t.Fatalf("dependency record hid callback escape: known=%t unknown=%t escape=%t", known, unknown, escape)
	}
}

func TestPromotedPublicCallbackThroughPrivateEmbeddedRecord(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PromotedRecordCaller"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	known, unknown, escape := false, false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::promotedRecordQuery") && edge.Kind == "sql_read" {
			known = known || edge.To == "table:orders:promoted_record_records"
			unknown = unknown || edge.To == "table:promoted_record_records"
		}
		escape = escape || edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.From, "::PublicPromotedRecordFactory") && strings.HasSuffix(edge.To, "::promotedRecordQuery")
	}
	if !known || !unknown || !escape {
		t.Fatalf("promoted public callback hidden by embedding: known=%t unknown=%t escape=%t", known, unknown, escape)
	}
}

func TestExportedContainerReturnsRetainCallbackInputs(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"ContainerCallbackCaller"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []string{"slice", "array", "map"} {
		known, unknown, escape, invoked := false, false, false, false
		for _, edge := range r.Relationships {
			if strings.HasSuffix(edge.From, "::"+shape+"CallbackQuery") && edge.Kind == "sql_read" {
				known = known || edge.To == "table:orders:"+shape+"_callback_records"
				unknown = unknown || edge.To == "table:"+shape+"_callback_records"
			}
			escape = escape || edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.To, "::"+shape+"CallbackQuery")
			invoked = invoked || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::ContainerCallbackCaller") && strings.HasSuffix(edge.To, "::"+shape+"CallbackQuery")
		}
		if !known || !unknown || !escape || !invoked {
			t.Errorf("%s container lost flow: known=%t unknown=%t escape=%t invoked=%t", shape, known, unknown, escape, invoked)
		}
	}
}

func TestInterfaceContainerArgumentsTraceMadeSlicesAndMapKeys(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"ContainerArgumentCaller"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	for owner, table := range map[string]string{"madeSliceQuery": "made_slice_records", "mapKeyQuery": "map_key_records"} {
		known, unknown, escape := false, false, false
		for _, edge := range r.Relationships {
			if strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "sql_read" {
				known = known || edge.To == "table:orders:"+table
				unknown = unknown || edge.To == "table:"+table
			}
			escape = escape || edge.Kind == "callback_escape" && strings.HasSuffix(edge.From, "::privateRecordArgument") && strings.HasSuffix(edge.To, "::"+owner)
		}
		if !known || !unknown || !escape {
			t.Errorf("%s interface container lost escape: known=%t unknown=%t escape=%t", owner, known, unknown, escape)
		}
	}
}

func TestPrivateArrayFactoryHasOnlyResolvedLocalInputs(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"ClosedArrayCaller"}, Depth: 3, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	read, invoked := false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::closedArrayQuery") && edge.Kind == "sql_read" {
			if edge.To != "table:orders:closed_array_records" {
				t.Fatalf("closed array factory invented outside SQL input: %+v", edge)
			}
			read = true
		}
		invoked = invoked || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::ClosedArrayCaller") && strings.HasSuffix(edge.To, "::closedArrayQuery")
		if edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.To, "::closedArrayQuery") {
			t.Fatal("private array return was treated as a public escape")
		}
	}
	if !read || !invoked {
		t.Fatalf("closed array callback lost local flow: read=%t invoked=%t", read, invoked)
	}
}

func TestSliceBuiltinsRetainCallbackFlow(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"SliceBuiltinCaller"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"appended", "copied"} {
		known, unknown, escape, invoked := false, false, false, false
		for _, edge := range r.Relationships {
			if strings.HasSuffix(edge.From, "::"+name+"Query") && edge.Kind == "sql_read" {
				known = known || edge.To == "table:orders:"+name+"_records"
				unknown = unknown || edge.To == "table:"+name+"_records"
			}
			escape = escape || edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.To, "::"+name+"Query")
			invoked = invoked || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::SliceBuiltinCaller") && strings.HasSuffix(edge.To, "::"+name+"Query")
		}
		if !known || !unknown || !escape || !invoked {
			t.Errorf("%s slice lost callback flow: known=%t unknown=%t escape=%t invoked=%t", name, known, unknown, escape, invoked)
		}
	}
}

func TestSliceBuiltinSiblingPathsRetainNestedAndDeferredFlow(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"SliceBuiltinSiblingCaller"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	for owner, table := range map[string]string{
		"chainFirstQuery": "chain_first_records", "chainSecondQuery": "chain_second_records",
		"copiedRecordQuery": "copied_record_records", "deferredCopyQuery": "deferred_copy_records",
		"goCopyQuery": "go_copy_records",
	} {
		known, unknown, escape, invoked := false, false, false, false
		for _, edge := range r.Relationships {
			if strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "sql_read" {
				known = known || edge.To == "table:orders:"+table
				unknown = unknown || edge.To == "table:"+table
			}
			escape = escape || edge.Kind == "callback_return_escape" && strings.HasSuffix(edge.To, "::"+owner)
			invoked = invoked || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::SliceBuiltinSiblingCaller") && strings.HasSuffix(edge.To, "::"+owner)
		}
		if !known || !unknown || !escape || !invoked {
			t.Errorf("%s sibling slice lost flow: known=%t unknown=%t escape=%t invoked=%t", owner, known, unknown, escape, invoked)
		}
	}
}

func TestPrivateSliceBuiltinsRetainOnlyLocalInputs(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"ClosedSliceBuiltinCaller"}, Depth: 3, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	read, invoked := false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::closedSliceBuiltinQuery") && edge.Kind == "sql_read" {
			if edge.To != "table:orders:closed_slice_builtin_records" {
				t.Fatalf("private append/copy invented outside origin: %+v", edge)
			}
			read = true
		}
		invoked = invoked || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::ClosedSliceBuiltinCaller") && strings.HasSuffix(edge.To, "::closedSliceBuiltinQuery")
	}
	for _, boundary := range r.Boundaries {
		if strings.HasSuffix(boundary.Node, "::closedSliceBuiltinQuery") && (boundary.Kind == "external_sql_input" || boundary.Kind == "escaped_sql_input") {
			t.Fatal("private append/copy callback acquired an outside-input boundary")
		}
	}
	if !read || !invoked {
		t.Fatalf("private append/copy lost local flow: read=%t invoked=%t", read, invoked)
	}
}

func TestPublicGlobalCallbacksRetainOutsideInputs(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"GlobalCallbackCaller"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	for owner, table := range map[string]string{"globalDirectQuery": "global_direct_records", "globalFieldQuery": "global_field_records", "globalPointerQuery": "global_pointer_records"} {
		known, unknown, escape := false, false, false
		for _, edge := range r.Relationships {
			if strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "sql_read" {
				known = known || edge.To == "table:orders:"+table
				unknown = unknown || edge.To == "table:"+table
			}
			escape = escape || edge.Kind == "callback_global_escape" && strings.HasSuffix(edge.To, "::"+owner) && strings.Contains(edge.Evidence.Snippet, "Public")
		}
		if !known || !unknown || !escape {
			t.Errorf("%s global hid outside inputs: known=%t unknown=%t escape=%t", owner, known, unknown, escape)
		}
	}
	privateRead := false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::privateGlobalQuery") && edge.Kind == "sql_read" {
			if edge.To != "table:orders:private_global_records" {
				t.Fatalf("private global invented outside origin: %+v", edge)
			}
			privateRead = true
		}
		if edge.Kind == "callback_global_escape" && strings.HasSuffix(edge.To, "::privateGlobalQuery") {
			t.Fatal("private global was exposed as a public variable")
		}
	}
	if !privateRead {
		t.Fatal("private global lost its local SQL origin")
	}
	pointerWrite := false
	for _, edge := range r.Relationships {
		pointerWrite = pointerWrite || edge.Kind == "global_write" && strings.HasSuffix(edge.From, "::setGlobalPointer") && strings.HasSuffix(edge.To, "::PublicPointerRecord")
	}
	if !pointerWrite {
		t.Fatal("public pointer global lost its helper writer")
	}
}

func TestPublicSQLGlobalsRetainOutsideReplacementOrigins(t *testing.T) {
	config := DefaultConfig()
	config.StorageScopes = []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/sample::OpenOrdersDatabase"}}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"GlobalDatabaseCaller"}, Depth: 4, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"global_database_records", "global_connection_records", "global_transaction_records", "global_statement_records", "global_record_database_records", "global_sql_slice_records", "global_sql_array_records", "global_sql_map_records"} {
		known, unknown := false, false
		for _, edge := range r.Relationships {
			if edge.Kind == "sql_read" {
				known = known || edge.To == "table:orders:"+table
				unknown = unknown || edge.To == "table:"+table
			}
		}
		if !known || !unknown {
			t.Errorf("public SQL global lost replacement origin for %s: known=%t unknown=%t", table, known, unknown)
		}
	}
	privateRead := false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::privateGlobalDatabaseQuery") && edge.Kind == "sql_read" {
			if edge.To != "table:orders:private_global_database_records" {
				t.Fatalf("private SQL global acquired outside origin: %+v", edge)
			}
			privateRead = true
		}
	}
	if !privateRead {
		t.Fatal("private SQL global lost its known origin")
	}
	privateFieldRead := false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::privateGlobalRecordQuery") && edge.Kind == "sql_read" {
			if edge.To != "table:orders:private_global_record_records" {
				t.Fatalf("private record field acquired an outside origin: %+v", edge)
			}
			privateFieldRead = true
		}
	}
	if !privateFieldRead {
		t.Fatal("private SQL field lost its local origin")
	}
	for _, global := range []string{"PublicGlobalDatabase", "PublicGlobalConnection", "PublicGlobalTransaction", "PublicGlobalStatement", "PublicGlobalSQLRecord", "PublicGlobalSQLSlice", "PublicGlobalSQLArray", "PublicGlobalSQLMap"} {
		boundary := false
		for _, b := range r.Boundaries {
			boundary = boundary || b.Kind == "external_sql_global" && strings.HasSuffix(b.Node, "::"+global)
		}
		if !boundary {
			t.Errorf("%s lost its SQL replacement boundary", global)
		}
	}
	preparedUnknown := false
	for _, b := range r.Boundaries {
		preparedUnknown = preparedUnknown || b.Kind == "unresolved_prepared_sql" && strings.Contains(b.Evidence.Snippet, "PublicGlobalStatement.Query")
	}
	if !preparedUnknown {
		t.Fatal("public statement lost its unknown preparation boundary")
	}
}
