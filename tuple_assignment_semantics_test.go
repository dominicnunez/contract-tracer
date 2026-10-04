package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTupleAssignmentRetainsEventAndConfiguredValueUncertainty(t *testing.T) {
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
	write("go.mod", "module example.com/tupleevent\n\ngo 1.27.0\n")
	write("api/api.go", `package api

func Publish(string) {}
func Query(string) bool { return true }
`)
	write("app.go", `package app

import "example.com/tupleevent/api"

type Envelope struct { EventType string }

func eventPair() (string, string) { return "orders.first", "orders.second" }
func queryPair() (string, bool) { return "SELECT id FROM tuple_records", true }

func AssignEventFirst() {
	var envelope Envelope
	envelope.EventType, _ = eventPair()
}
func AssignEventSecond() {
	var envelope Envelope
	_, envelope.EventType = eventPair()
}
func PublishTupleResults() {
	first, second := eventPair()
	api.Publish(first)
	api.Publish(second)
}
func QueryTupleResult() {
	query, ok := queryPair()
	_ = ok
	api.Query(query)
}
func DirectEventAssignment() {
	var envelope Envelope
	envelope.EventType = "orders.direct"
}

var packageQuery, packageQueryOK = queryPair()
var packageQueryResult = api.Query(packageQuery)

func queryTail() (bool, string) { return true, "SELECT id FROM tuple_records" }
func PublishTupleValueSpec() { var first, second = eventPair(); _ = second; api.Publish(first) }
func PublishTupleSecondAssignment() { eventName := "orders.stale"; _, eventName = eventPair(); api.Publish(eventName) }
func QueryTupleValueSpec() { var query, ok = queryPair(); _ = ok; api.Query(query) }
func QueryTupleSecondAssignment() { query := "SELECT id FROM stale_records"; _, query = queryTail(); api.Query(query) }
func AssignEventCommaOK(values map[string]string) { var envelope Envelope; envelope.EventType, _ = values["event"] }
func AssignEventAssertion(value any) { var envelope Envelope; envelope.EventType, _ = value.(string) }
func PlainAfterDeclaration() { var name string; name = "orders.declared"; api.Publish(name) }
`)

	report, err := Trace(context.Background(), Options{
		Root: root,
		Seeds: []string{
			"AssignEventFirst", "AssignEventSecond", "PublishTupleResults", "QueryTupleResult",
			"DirectEventAssignment", "AssignEventCommaOK", "AssignEventAssertion", "PublishTupleValueSpec", "PublishTupleSecondAssignment",
			"QueryTupleValueSpec", "QueryTupleSecondAssignment", "PlainAfterDeclaration", "example.com/tupleevent::(package init)",
		},
		Depth: 3, MaxNodes: 100,
		Config: Config{EventFields: []string{"EventType"}, CallRules: []CallRule{
			{Symbol: "example.com/tupleevent/api::Publish", Kind: "event_publish", Argument: 0},
			{Symbol: "example.com/tupleevent/api::Query", Kind: "sql_query", Argument: 0, Namespace: "primary"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !hasBoundaryAt(report, "example.com/tupleevent::AssignEventSecond", "dynamic_event", "app.go", 16) {
		t.Error("event discriminator assigned from the second tuple slot lost unresolved event evidence")
	}
	if !hasBoundaryAt(report, "example.com/tupleevent::AssignEventFirst", "dynamic_event", "app.go", 12) {
		t.Error("event discriminator assigned from a tuple call must remain explicitly unresolved")
	}
	if !hasBoundaryAt(report, "example.com/tupleevent::AssignEventCommaOK", "dynamic_event", "app.go", 41) ||
		!hasBoundaryAt(report, "example.com/tupleevent::AssignEventAssertion", "dynamic_event", "app.go", 42) {
		t.Error("event discriminator from comma-ok map or type-assertion result lost unresolved evidence")
	}
	if !hasRelationship(report, "example.com/tupleevent::DirectEventAssignment", "event:orders.direct", "event_assign", "app.go", 30) {
		t.Error("ordinary one-to-one event assignment lost its complete value")
	}

	first := hasRelationship(report, "example.com/tupleevent::PublishTupleResults", "event:orders.first", "event_publish", "app.go", 20)
	second := hasRelationship(report, "example.com/tupleevent::PublishTupleResults", "event:orders.second", "event_publish", "app.go", 21)
	if !first || !second {
		t.Errorf("tuple result candidates were not kept per slot: first=%t second=%t", first, second)
	}
	if hasRelationship(report, "example.com/tupleevent::PublishTupleResults", "event:orders.second", "event_publish", "app.go", 20) ||
		hasRelationship(report, "example.com/tupleevent::PublishTupleResults", "event:orders.first", "event_publish", "app.go", 21) {
		t.Error("tuple result candidate from one slot leaked into the other slot")
	}
	if !hasBoundaryAt(report, "example.com/tupleevent::QueryTupleResult", "dynamic_api_value", "app.go", 26) {
		t.Error("configured SQL argument derived from a tuple result lost unresolved-value evidence")
	}
	if !hasRelationship(report, "example.com/tupleevent::QueryTupleResult", "table:primary:tuple_records", "sql_read", "app.go", 26) {
		t.Error("known SQL candidate from tuple result was not retained")
	}
	if !hasBoundaryAt(report, "example.com/tupleevent::(package init)", "dynamic_api_value", "app.go", 34) {
		t.Error("package-initializer API argument derived from tuple ValueSpec lost unresolved-value evidence")
	}
	if !hasRelationship(report, "example.com/tupleevent::(package init)", "table:primary:tuple_records", "sql_read", "app.go", 34) {
		t.Error("package-initializer SQL candidate from tuple ValueSpec was not retained")
	}
	if !hasBoundaryAt(report, "example.com/tupleevent::PublishTupleValueSpec", "dynamic_api_value", "app.go", 37) ||
		!hasRelationship(report, "example.com/tupleevent::PublishTupleValueSpec", "event:orders.first", "event_publish", "app.go", 37) ||
		hasRelationship(report, "example.com/tupleevent::PublishTupleValueSpec", "event:orders.second", "event_publish", "app.go", 37) {
		t.Error("local tuple ValueSpec must retain the first event slot as a candidate with uncertainty, without importing the second slot")
	}
	if !hasBoundaryAt(report, "example.com/tupleevent::PublishTupleSecondAssignment", "dynamic_api_value", "app.go", 38) ||
		!hasRelationship(report, "example.com/tupleevent::PublishTupleSecondAssignment", "event:orders.second", "event_publish", "app.go", 38) ||
		hasRelationship(report, "example.com/tupleevent::PublishTupleSecondAssignment", "event:orders.stale", "event_publish", "app.go", 38) {
		t.Error("later tuple assignment must invalidate the stale event fallback while retaining its actual result slot")
	}
	if !hasBoundaryAt(report, "example.com/tupleevent::QueryTupleValueSpec", "dynamic_api_value", "app.go", 39) ||
		!hasRelationship(report, "example.com/tupleevent::QueryTupleValueSpec", "table:primary:tuple_records", "sql_read", "app.go", 39) {
		t.Error("local tuple ValueSpec SQL fallback lost its candidate or unresolved boundary")
	}
	if !hasBoundaryAt(report, "example.com/tupleevent::QueryTupleSecondAssignment", "dynamic_api_value", "app.go", 40) ||
		!hasRelationship(report, "example.com/tupleevent::QueryTupleSecondAssignment", "table:primary:tuple_records", "sql_read", "app.go", 40) ||
		hasRelationship(report, "example.com/tupleevent::QueryTupleSecondAssignment", "table:primary:stale_records", "sql_read", "app.go", 40) {
		t.Error("later tuple SQL assignment must invalidate the stale query fallback while retaining its actual result slot")
	}
	if !hasRelationship(report, "example.com/tupleevent::PlainAfterDeclaration", "event:orders.declared", "event_publish", "app.go", 43) ||
		hasBoundaryAt(report, "example.com/tupleevent::PlainAfterDeclaration", "dynamic_api_value", "app.go", 43) {
		t.Error("ordinary declaration followed by a plain assignment should retain its complete event argument")
	}
}
