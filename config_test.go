package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestTypedAPIRules(t *testing.T) {
	handler := 1
	config := Config{CallRules: []CallRule{
		{Symbol: "example.com/sample::execute", Kind: "sql_query", Argument: 2},
		{Symbol: "example.com/sample::sendAPI", Kind: "event_publish", Argument: 1, Namespace: "orders"},
		{Symbol: "example.com/sample::subscribeAPI", Kind: "event_subscribe", Argument: 0, HandlerArgument: &handler, Namespace: "orders"},
	}}
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"APIWrite", "APIPublish"}, Depth: 4, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"APIRead", "APISetup", "APIHandler"} {
		if !hasName(r, name) {
			t.Errorf("missing API contract path %s", name)
		}
	}
	registered := false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::APIHandler") && e.To == "event:orders:order.ready" && e.Kind == "event_handler" {
			registered = true
		}
	}
	if !registered {
		t.Error("subscription did not connect its callback handler to the event resource")
	}
}
func TestInvalidCallRule(t *testing.T) {
	_, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 2, MaxNodes: 50, Config: Config{CallRules: []CallRule{{Symbol: "anything", Kind: "invented", Argument: -1}}}})
	if err == nil {
		t.Error("invalid rule accepted")
	}
}

func TestStrictConfigAndResourceIdentity(t *testing.T) {
	for _, text := range []string{`null`, `{"typo":true}`, `{} {}`, `{} trailing`, `{"call_rules":[{"symbol":"x::y","kind":"invented","argument":0}]}`} {
		if _, err := ReadConfig(strings.NewReader(text)); err == nil {
			t.Errorf("invalid config accepted: %s", text)
		}
	}
	c, err := ReadConfig(strings.NewReader(`{"event_fields":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.EventFields) != 0 || len(c.SQLMethods) == 0 {
		t.Error("empty arrays and default fields were not distinguished")
	}
	if resourceID("event", "orders", "ready") == resourceID("event", "", "orders:ready") {
		t.Error("namespace collided with an event value")
	}
}

func TestSQLFilePatternsRejectSilentExclusions(t *testing.T) {
	for _, pattern := range []string{"[", "../schema.sql", "/schema.sql", "C:/schema.sql", "", "a//b.sql", `a\b.sql`} {
		config := DefaultConfig()
		config.SQLFiles = []string{pattern}
		if err := validateConfig(config); err == nil {
			t.Errorf("invalid SQL file pattern accepted: %q", pattern)
		}
	}
	for _, pattern := range []string{"**/*.sql", "migrations/**/*.sql", "schema.sql", "migrations/[0-9]*.sql"} {
		config := DefaultConfig()
		config.SQLFiles = []string{pattern}
		if err := validateConfig(config); err != nil {
			t.Errorf("valid SQL file pattern rejected: %q: %v", pattern, err)
		}
	}
}
