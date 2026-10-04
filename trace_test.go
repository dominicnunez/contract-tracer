package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallbackAndInterfaceScope(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Invariant: "all accepted values are validated", Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Validate", "Live", "Recovery", "Store.Read", "Entry"} {
		if !hasName(r, name) {
			t.Errorf("missing independently expected path %s", name)
		}
	}
	if hasName(r, "unrelated") {
		t.Error("disconnected function included")
	}
	if hasName(r, "UnusedCheck") {
		t.Error("same-signature callback candidate should remain an unresolved boundary by default")
	}
	possible := false
	for _, b := range r.Boundaries {
		if b.Kind == "callback_candidates" {
			possible = true
		}
	}
	if !possible {
		t.Error("nonexpanded callback candidates must be disclosed")
	}
	found := false
	for _, e := range r.Relationships {
		if e.Kind == "function_reference" && e.Evidence.Snippet == "func Recovery() bool { return apply(Validate) }" {
			found = true
		}
	}
	if !found {
		t.Error("callback use must be retained as source-backed reference")
	}
	if r.ContractComplete {
		t.Error("structural discovery cannot certify the invariant")
	}
}

func TestFingerprintIncludesOrdinaryPackageDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reports")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "source.go")
	if err := os.WriteFile(file, []byte("package reports\nconst Value=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _, err := fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package reports\nconst Value=2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, _, err := fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Error("ordinary Go package named reports was omitted from source identity")
	}
}

func TestInvalidTargetAndCanceledAnalysis(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/broken\n\ngo 1.27.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package broken\nfunc Seed() { missingSymbol() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Trace(context.Background(), Options{Root: dir, Seeds: []string{"Seed"}, Depth: 2, MaxNodes: 10})
	if err == nil || !strings.Contains(err.Error(), "package loading incomplete") || len(r.Nodes) != 0 {
		t.Error("unloadable code must fail instead of emitting apparently successful scope")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Trace(ctx, Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 2, MaxNodes: 10})
	if err == nil {
		t.Error("canceled analysis was allowed")
	}
}

func TestConfigurationAndScopeLimits(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"CustomPublish"}, Depth: 2, MaxNodes: 100, Config: Config{EventFields: []string{"Topic"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "CustomConsume") {
		t.Error("configured field convention did not connect consumer")
	}
	r, err = Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Nodes) > 2 || !r.Coverage.Truncated {
		t.Error("budget must be enforced and reported")
	}
	found := false
	for _, b := range r.Boundaries {
		if b.Kind == "scope_frontier" {
			found = true
		}
	}
	if !found {
		t.Error("scope omission lacks an explicit boundary")
	}
}

func TestBuildCoverageAndUnknownSeed(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	excluded := false
	for _, f := range r.Coverage.ExcludedGoFiles {
		if f == "tagged.go" {
			excluded = true
		}
	}
	if !excluded || hasName(r, "Tagged") {
		t.Error("unselected build-tag path was not correctly reported")
	}
	r, err = Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100, Tags: "traceextra"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "Tagged") {
		t.Error("selected build-tag caller missing")
	}
	_, err = Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"DoesNotExist"}, Depth: 3, MaxNodes: 100})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Error("unknown seed must fail explicitly")
	}
}

func TestStorageSiblingsAndDynamicBoundary(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Write", "Dynamic"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Read", "Rebuild"} {
		if !hasName(r, name) {
			t.Errorf("missing state sibling %s", name)
		}
	}
	if hasName(r, "Other") {
		t.Error("unrelated table was joined")
	}
	found := false
	for _, b := range r.Boundaries {
		if b.Kind == "dynamic_sql" {
			found = true
		}
	}
	if !found {
		t.Error("dynamic SQL was silently dropped")
	}
}

func TestEventProducerConsumer(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Publish"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "Consume") {
		t.Error("consumer of the same constant event missing")
	}
	if hasName(r, "Another") {
		t.Error("different event joined")
	}
}

func TestPackageInitializersUseConfiguredSemanticAdapters(t *testing.T) {
	configJSON := `{"storage_scopes":[{"namespace":"initializer-store","database_origins":["example.com/initfixture::(package init)"]}]}`
	if _, err := ReadConfig(strings.NewReader(configJSON)); err != nil {
		t.Fatalf("ReadConfig rejected the modeled package-initializer database owner: %v", err)
	}
	for _, invalid := range []string{
		"example.com/initfixture::(package init)::Open",
		"example.com/initfixture:: (package init)",
	} {
		if _, err := ReadConfig(strings.NewReader(`{"storage_scopes":[{"namespace":"initializer-store","database_origins":["` + invalid + `"]}]}`)); err == nil {
			t.Errorf("ReadConfig accepted malformed package-initializer database owner %q", invalid)
		}
	}
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
	write("go.mod", "module example.com/initfixture\n\ngo 1.27.0\n")
	write("api/api.go", `package api

func Publish(string) bool { return true }
func Subscribe(string, func()) bool { return true }
func Query(string) bool { return true }
`)
	write("events.go", `package app

import "example.com/initfixture/api"

type Envelope struct { EventType string }
const orderCreated = "orders.created"
var eventName = orderCreated
var eventNameMutation = ChangeEventName()
var publishResult = api.Publish(eventName)
var eventEnvelope = Envelope{EventType: orderCreated}
var immediateResult = func() bool { return api.Publish("iife.event") }()
`)
	write("mutator.go", `package app

func ChangeEventName() bool {
	eventName = "orders.changed"
	return true
}
`)
	write("consumers.go", `package app

import (
	"database/sql"
	"example.com/initfixture/api"
)

func HandleOrder() {}
var subscriptionResult = api.Subscribe(eventName, HandleOrder)
var queryResult = api.Query("SELECT id FROM records")
var openedDB, openErr = sql.Open("sqlite", ":memory:")
var openedRows, rowsErr = openedDB.Query("SELECT id FROM opened_records")
`)
	write("dynamic.go", `package app

import "example.com/initfixture/api"

var dynamicEvent string
var dynamicResult = api.Publish(dynamicEvent)
`)
	write("callbacks.go", `package app

import "example.com/initfixture/api"

var deferredPublisher = func() { api.Publish("closure.body") }
`)
	write("functions.go", `package app

import "example.com/initfixture/api"

func OrdinaryFunction() bool { return api.Publish("body.event") }
func init() { api.Publish("runtime.init") }
`)

	r, err := Trace(context.Background(), Options{
		Root: root, Seeds: []string{"example.com/initfixture::(package init)"}, Depth: 3, MaxNodes: 100,
		Config: Config{CallRules: []CallRule{
			{Symbol: "example.com/initfixture/api::Publish", Kind: "event_publish", Argument: 0},
			{Symbol: "example.com/initfixture/api::Subscribe", Kind: "event_subscribe", Argument: 0, HandlerArgument: intPointer(1)},
			{Symbol: "example.com/initfixture/api::Query", Kind: "sql_query", Argument: 0, Namespace: "primary"},
		}, StorageScopes: []StorageScope{{
			Namespace: "initializer-store", DatabaseOrigins: []string{"example.com/initfixture::(package init)"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	initializer := "example.com/initfixture::(package init)"
	requires := []struct {
		to, kind string
		line     int
	}{
		{"event:orders.changed", "event_publish", 9},
		{"event:orders.created", "event_construct", 10},
		{"event:iife.event", "event_publish", 11},
		{"event:orders.changed", "event_subscribe", 9},
		{"table:primary:records", "sql_read", 10},
		{"table:initializer-store:opened_records", "sql_read", 12},
	}
	for _, expected := range requires {
		found := false
		for _, edge := range r.Relationships {
			if edge.From == initializer && edge.To == expected.to && edge.Kind == expected.kind &&
				edge.Evidence.File == map[string]string{
					"event_publish": "events.go", "event_construct": "events.go",
					"event_subscribe": "consumers.go", "sql_read": "consumers.go",
				}[expected.kind] && edge.Evidence.Line == expected.line {
				found = true
			}
		}
		if !found {
			t.Errorf("package initializer is missing %s from %s at line %d", expected.kind, expected.to, expected.line)
		}
	}
	if !hasRelationship(r, "example.com/initfixture::HandleOrder", "event:orders.changed", "event_handler", "consumers.go", 9) {
		t.Error("subscription handler declaration must connect through initializer registration evidence")
	}
	if hasRelationship(r, initializer, "event:body.event", "event_publish", "functions.go", 0) ||
		hasRelationship(r, initializer, "event:runtime.init", "event_publish", "functions.go", 0) ||
		hasRelationship(r, initializer, "event:closure.body", "event_publish", "callbacks.go", 0) {
		t.Error("ordinary function bodies were incorrectly attributed to package variable initialization")
	}
	if !hasRelationship(r, "example.com/initfixture::OrdinaryFunction", "event:body.event", "event_publish", "functions.go", 5) {
		t.Error("ordinary function call lost its semantic owner")
	}
	initOwner := ""
	for _, node := range r.Nodes {
		if node.Kind == "function" && node.Name == "init" && strings.HasPrefix(node.ID, "example.com/initfixture::init@functions.go:") {
			initOwner = node.ID
		}
	}
	if initOwner == "" || !hasRelationship(r, initOwner, "event:runtime.init", "event_publish", "functions.go", 6) {
		t.Error("explicit init function call lost its source owner")
	}
	if !hasBoundaryAt(r, initializer, "dynamic_api_value", "dynamic.go", 6) ||
		!hasBoundaryAt(r, initializer, "dynamic_api_value", "events.go", 9) {
		t.Error("dynamic package initializer API value needs its unresolved boundary")
	}
	for _, rule := range []string{
		"example.com/initfixture/api::Publish", "example.com/initfixture/api::Subscribe", "example.com/initfixture/api::Query",
	} {
		for _, boundary := range r.Boundaries {
			if boundary.Kind == "unmatched_rule" && strings.Contains(boundary.Reason, rule) {
				t.Errorf("configured API used only by package initialization was reported unmatched: %s", rule)
			}
		}
	}
	if r.ContractComplete {
		t.Error("package initializer resource evidence must not certify the contract")
	}
}

func TestCompoundEventFieldAssignmentRemainsUnresolved(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/compoundevent\n\ngo 1.27.0\n",
		"api/api.go": `package api

func Publish(string) bool { return true }
func Query(string) bool { return true }
`,
		"app.go": `package app

import "example.com/compoundevent/api"

type Envelope struct { EventType string }

func Send(e *Envelope) {
	e.EventType = "orders.changed"
	e.EventType += ".updated"
}

func Define() {
	EventType := "orders.local"
	EventType = "orders.replaced"
	_ = EventType
}

func PublishPartial(EventType string) {
	EventType += ".published"
	api.Publish(EventType)
}

func QueryPartial(query string) {
	query += "SELECT id FROM records"
	api.Query(query)
}
`,
	}
	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Trace(context.Background(), Options{
		Root: root, Seeds: []string{"Send", "Define", "PublishPartial", "QueryPartial"}, Depth: 2, MaxNodes: 50,
		Config: Config{CallRules: []CallRule{
			{Symbol: "example.com/compoundevent/api::Publish", Kind: "event_publish", Argument: 0},
			{Symbol: "example.com/compoundevent/api::Query", Kind: "sql_query", Argument: 0, Namespace: "primary"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRelationship(r, "example.com/compoundevent::Send", "event:orders.changed", "event_assign", "app.go", 8) {
		t.Error("plain assignment to a configured event field lost its complete event candidate")
	}
	if hasRelationship(r, "example.com/compoundevent::Send", "event:.updated", "event_assign", "app.go", 9) {
		t.Error("compound assignment incorrectly treated only its suffix as the complete event")
	}
	if !hasBoundaryAt(r, "example.com/compoundevent::Send", "dynamic_event", "app.go", 9) {
		t.Error("compound event assignment needs an unresolved-value boundary")
	}
	if !hasRelationship(r, "example.com/compoundevent::Define", "event:orders.local", "event_assign", "app.go", 13) ||
		!hasRelationship(r, "example.com/compoundevent::Define", "event:orders.replaced", "event_assign", "app.go", 14) {
		t.Error("plain short and reassignment event values should remain recognized")
	}
	if !hasBoundaryAt(r, "example.com/compoundevent::PublishPartial", "dynamic_event", "app.go", 19) {
		t.Error("compound assignment to an event-valued parameter needs an unresolved event boundary")
	}
	if !hasBoundaryAt(r, "example.com/compoundevent::PublishPartial", "dynamic_api_value", "app.go", 20) {
		t.Error("configured API argument needs an unresolved-value boundary after a compound write")
	}
	if !hasBoundaryAt(r, "example.com/compoundevent::QueryPartial", "dynamic_api_value", "app.go", 25) {
		t.Error("configured SQL API argument needs an unresolved-value boundary after a compound write")
	}
}

func hasRelationship(r Report, from, to, kind, file string, line int) bool {
	for _, edge := range r.Relationships {
		if edge.From == from && edge.To == to && edge.Kind == kind && edge.Evidence.File == file &&
			(line == 0 || edge.Evidence.Line == line) {
			return true
		}
	}
	return false
}

func hasBoundaryAt(r Report, owner, kind, file string, line int) bool {
	for _, boundary := range r.Boundaries {
		if boundary.Node == owner && boundary.Kind == kind && boundary.Evidence.File == file && boundary.Evidence.Line == line {
			return true
		}
	}
	return false
}

func hasName(r Report, name string) bool {
	for _, n := range r.Nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}
