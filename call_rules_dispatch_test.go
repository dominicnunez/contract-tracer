package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredCallRulesFollowTypedAliasesAndMethodExpressions(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/callclient\n\ngo 1.26.0\nrequire example.com/callapi v0.0.0\nreplace example.com/callapi => ./api\n",
		"api/go.mod": "module example.com/callapi\n\ngo 1.26.0\n",
		"api/api.go": `package callapi
type Bus struct{}
type Publisher interface { Publish(string, string) }
type ArchiveBus struct{}
type DB struct{}
func (*Bus) Publish(string, string) {}
func (*Bus) Subscribe(string, func(string)) {}
func (*ArchiveBus) Publish(string, string) {}
func (*DB) Query(string) {}
func Dispatch(string, string) {}
`,
		"client.go": `package callclient
import (
    "database/sql"
    "example.com/callapi"
)
var bus callapi.Bus
var archive callapi.ArchiveBus
var db callapi.DB
var flag bool
func Scenario() {
    bus.Publish("docs.ready", "payload")
    publish := bus.Publish
    invokePublish(publish, "docs.ready", "payload")
    (*callapi.Bus).Publish(&bus, "docs.ready", "payload")
    (*callapi.Bus).Subscribe(&bus, "docs.ready", Handler)
    var publisher callapi.Publisher = &bus
    interfacePublish := publisher.Publish
    interfacePublish("docs.ready", "payload")
    dispatch := callapi.Dispatch
    invokePublish(dispatch, "docs.ready", "payload")
    subscribe := bus.Subscribe
    invokeSubscribe(subscribe, "docs.ready", Handler)
    PublishMaybe(bus.Publish, "docs.ready", "payload")
    SubscribeMaybe(bus.Subscribe, "docs.ready", Handler)
    unrelated := archive.Publish
    unrelated("docs.ready", "payload")
    query := db.Query
    query("SELECT id FROM documents")
    (*callapi.DB).Query(&db, "SELECT id FROM documents")
    QueryPrimary()
    QueryPrimaryExpr()
    QueryArchive()
    QueryMixed(nil)
    PublicOutside(nil)
}
func Handler(string) {}
func invokePublish(fn func(string, string), topic, payload string) { fn(topic, payload) }
func invokeSubscribe(fn func(string, func(string)), topic string, handler func(string)) { fn(topic, handler) }
func PublishMaybe(fn func(string, string), topic, payload string) { fn(topic, payload) }
func SubscribeMaybe(fn func(string, func(string)), topic string, handler func(string)) { fn(topic, handler) }
func OpenPrimary() *sql.DB { db, _ := sql.Open("sqlite3", ":memory:"); return db }
func OpenArchive() *sql.DB { db, _ := sql.Open("sqlite3", ":memory:"); return db }
func QueryPrimary() { db := OpenPrimary(); queryPrimary(db.Exec) }
func QueryPrimaryExpr() { db := OpenPrimary(); (*sql.DB).Exec(db, "SELECT id FROM documents") }
func QueryArchive() { db := OpenArchive(); queryArchive(db.Exec) }
func queryPrimary(exec func(string, ...any) (sql.Result, error)) { exec("SELECT id FROM documents") }
func queryArchive(exec func(string, ...any) (sql.Result, error)) { exec("SELECT id FROM documents") }
func QueryMixed(outside *sql.DB) {
    exec := outside.Exec
    if flag { exec = OpenPrimary().Exec }
    exec("SELECT id FROM documents")
}
func PublicOutside(db *sql.DB) { exec := db.Exec; exec("SELECT id FROM documents") }
`,
		"migrations/schema.sql":  "CREATE TABLE documents (id INTEGER PRIMARY KEY);\n",
		"migrations/archive.sql": "CREATE TABLE documents (id INTEGER PRIMARY KEY);\n",
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	one := 1
	config := DefaultConfig()
	config.SQLFiles = []string{"migrations/*.sql"}
	config.StorageScopes = []StorageScope{
		{Namespace: "primary", SQLFiles: []string{"migrations/schema.sql"}, DatabaseOrigins: []string{"example.com/callclient::OpenPrimary"}},
		{Namespace: "archive", SQLFiles: []string{"migrations/archive.sql"}, DatabaseOrigins: []string{"example.com/callclient::OpenArchive"}},
	}
	config.CallRules = []CallRule{
		{Symbol: "example.com/callapi::Bus.Publish", Kind: "event_publish", Argument: 0, Namespace: "documents"},
		{Symbol: "example.com/callapi::Publisher.Publish", Kind: "event_publish", Argument: 0, Namespace: "documents"},
		{Symbol: "example.com/callapi::Bus.Subscribe", Kind: "event_subscribe", Argument: 0, HandlerArgument: &one, Namespace: "documents"},
		{Symbol: "example.com/callapi::Dispatch", Kind: "event_dispatch", Argument: 0, Namespace: "documents"},
		{Symbol: "example.com/callapi::DB.Query", Kind: "sql_query", Argument: 0, Namespace: "primary"},
		{Symbol: "database/sql::DB.Exec", Kind: "sql_query", Argument: 0},
	}
	maxSelector := int(^uint(0) >> 1)
	config.CallRules = append(config.CallRules,
		CallRule{Symbol: "example.com/callapi::Bus.Publish", Kind: "event_publish", Argument: maxSelector, Namespace: "documents"},
		CallRule{Symbol: "example.com/callapi::Bus.Subscribe", Kind: "event_subscribe", Argument: 0, HandlerArgument: &maxSelector, Namespace: "documents"},
	)
	report, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Scenario"}, Depth: 8, MaxNodes: 300, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	topic := "event:documents:docs.ready"
	table := "table:primary:documents"
	require := func(from, to, kind, snippet string) bool {
		for _, edge := range analysis.Relationships {
			if edge.From == from && edge.To == to && edge.Kind == kind && edge.Certainty == "possible" && strings.Contains(edge.Evidence.Snippet, snippet) {
				return true
			}
		}
		return false
	}
	if !require("example.com/callclient::invokePublish", topic, "event_publish", "fn(topic, payload)") {
		t.Fatal("typed CallRule did not follow a bound-method alias through its helper to the publish target")
	}
	if !require("example.com/callclient::Scenario", topic, "event_publish", `bus.Publish("docs.ready"`) {
		t.Fatal("direct configured method call lost its source-site event role")
	}
	if !require("example.com/callclient::Scenario", topic, "event_publish", "(*callapi.Bus).Publish") {
		t.Fatal("method-expression CallRule did not skip its explicit receiver when selecting argument zero")
	}
	if !require("example.com/callclient::Scenario", topic, "event_subscribe", "(*callapi.Bus).Subscribe") || !require("example.com/callclient::Handler", topic, "event_handler", "(*callapi.Bus).Subscribe") {
		t.Fatal("method-expression Subscribe selectors did not skip its explicit receiver")
	}
	if !require("example.com/callclient::Scenario", topic, "event_publish", "interfacePublish(\"docs.ready\"") {
		t.Fatal("bound interface method did not preserve its declared Publisher.Publish API identity")
	}
	if !require("example.com/callclient::invokePublish", topic, "event_dispatch", "fn(topic, payload)") {
		t.Fatal("typed event_dispatch CallRule did not follow a package function alias through its helper")
	}
	if !require("example.com/callclient::invokeSubscribe", topic, "event_subscribe", "fn(topic, handler)") {
		t.Fatal("bound Subscribe alias did not publish a registration candidate at the API invocation")
	}
	if !require("example.com/callclient::Handler", topic, "event_handler", "handler") {
		t.Fatal("bound Subscribe handler target was not connected from its declaration to the event resource")
	}
	if !require("example.com/callclient::Scenario", table, "sql_read", "query(\"SELECT id FROM documents\")") {
		t.Fatal("configured SQL alias did not use its invocation query and storage namespace")
	}
	if !require("example.com/callclient::Scenario", table, "sql_read", "(*callapi.DB).Query") {
		t.Fatal("SQL method-expression selector did not skip its explicit receiver")
	}
	if !require("example.com/callclient::queryPrimary", table, "sql_read", "exec(\"SELECT id FROM documents\")") {
		t.Fatal("aliased database/sql method did not retain its configured constructor namespace and schema")
	}
	if !require("example.com/callclient::QueryPrimaryExpr", table, "sql_read", "(*sql.DB).Exec") {
		t.Fatal("database/sql method-expression query or receiver selector offset was lost")
	}
	if !require("example.com/callclient::queryArchive", "table:archive:documents", "sql_read", "exec(\"SELECT id FROM documents\")") {
		t.Fatal("second bound database alias did not retain its own configured namespace")
	}
	if require("example.com/callclient::queryPrimary", "table:archive:documents", "sql_read", "exec(\"SELECT id FROM documents\")") || require("example.com/callclient::queryArchive", table, "sql_read", "exec(\"SELECT id FROM documents\")") {
		t.Fatal("distinct bound database closures cross-joined primary and archive namespaces")
	}
	if !require("example.com/callclient::QueryMixed", table, "sql_read", "exec(\"SELECT id FROM documents\")") {
		t.Fatal("known receiver candidate was lost when merged with an outside receiver")
	}
	if require("example.com/callclient::Scenario", topic, "event_publish", "unrelated(") {
		t.Fatal("same-named method on a different receiver type entered the configured rule")
	}
	knownOutsideHandler := false
	for _, boundary := range report.Boundaries {
		knownOutsideHandler = knownOutsideHandler || boundary.Node == "example.com/callclient::SubscribeMaybe" && boundary.Kind == "unresolved_handler"
	}
	if !knownOutsideHandler {
		t.Fatal("known callback plus public outside handler alternatives lack unresolved_handler boundary")
	}
	invalidArgument, invalidHandler := false, false
	for _, boundary := range report.Boundaries {
		if boundary.Node != "example.com/callclient::Scenario" || boundary.Kind != "invalid_rule_site" {
			continue
		}
		invalidArgument = invalidArgument || strings.Contains(boundary.Reason, "configured argument") && strings.Contains(boundary.Evidence.Snippet, "(*callapi.Bus).Publish")
		invalidHandler = invalidHandler || strings.Contains(boundary.Reason, "configured handler") && strings.Contains(boundary.Evidence.Snippet, "(*callapi.Bus).Subscribe")
	}
	if !invalidArgument || !invalidHandler {
		t.Fatalf("overflowing method-expression selectors lacked invalid_rule_site evidence: argument=%t handler=%t", invalidArgument, invalidHandler)
	}
	unknownNamespace := false
	for _, boundary := range report.Boundaries {
		unknownNamespace = unknownNamespace || boundary.Node == "example.com/callclient::PublicOutside" && boundary.Kind == "unresolved_database_namespace"
	}
	if !unknownNamespace {
		t.Fatal("public opaque database receiver lacked an unresolved namespace boundary")
	}
	knownOutsideNamespace := false
	for _, boundary := range report.Boundaries {
		knownOutsideNamespace = knownOutsideNamespace || boundary.Node == "example.com/callclient::QueryMixed" && boundary.Kind == "unresolved_database_namespace"
	}
	if !knownOutsideNamespace {
		t.Fatal("known and outside bound database receivers lost their uncertainty boundary")
	}
	if report.ContractComplete {
		t.Fatal("configured call rules must retain incomplete contract coverage")
	}
}
