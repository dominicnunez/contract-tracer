package contracttrace

import (
	"context"
	"fmt"
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

func TestConfigurationNameWhitespaceRejectedByBothEntrypoints(t *testing.T) {
	type nameList struct {
		field     string
		canonical string
		set       func(*Config, string)
	}
	lists := []nameList{
		{"sql_methods", "Query", func(c *Config, value string) { c.SQLMethods = []string{value} }},
		{"event_fields", "EventType", func(c *Config, value string) { c.EventFields = []string{value} }},
		{"lifecycle_names", "shutdown", func(c *Config, value string) { c.LifecycleNames = []string{value} }},
	}
	for _, list := range lists {
		for _, value := range []string{"", " " + list.canonical, list.canonical + " "} {
			t.Run(list.field+fmt.Sprintf("/%q", value), func(t *testing.T) {
				text := fmt.Sprintf(`{"%s":[%q]}`, list.field, value)
				if _, err := ReadConfig(strings.NewReader(text)); err == nil {
					t.Errorf("ReadConfig accepted whitespace-padded %s name %q", list.field, value)
				}

				config := DefaultConfig()
				list.set(&config, value)
				_, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20, Config: config})
				if err == nil {
					t.Errorf("Trace accepted whitespace-padded %s name %q", list.field, value)
				}
			})
		}

		text := fmt.Sprintf(`{"%s":[%q]}`, list.field, list.canonical)
		if _, err := ReadConfig(strings.NewReader(text)); err != nil {
			t.Errorf("ReadConfig canonical %s name: %v", list.field, err)
		}
	}

	config := DefaultConfig()
	config.SQLMethods = []string{"Query"}
	config.EventFields = []string{"EventType"}
	config.LifecycleNames = []string{"shutdown"}
	if _, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20, Config: config}); err != nil {
		t.Fatalf("Trace with canonical configured names: %v", err)
	}
}

func TestCallRuleSymbolMustHaveTrimmedQualifiedParts(t *testing.T) {
	for _, symbol := range []string{
		" example.com/sample::sendAPI",
		"example.com/sample::sendAPI ",
		"example.com/sample ::sendAPI",
		"example.com/sample:: sendAPI",
		"::sendAPI",
		"example.com/sample::",
		"example.com/sample::sendAPI::extra",
	} {
		t.Run(fmt.Sprintf("%q", symbol), func(t *testing.T) {
			text := fmt.Sprintf(`{"call_rules":[{"symbol":%q,"kind":"event_publish","argument":0}]}`, symbol)
			if _, err := ReadConfig(strings.NewReader(text)); err == nil {
				t.Errorf("ReadConfig accepted malformed call-rule symbol %q", symbol)
			}
			_, err := Trace(context.Background(), Options{
				Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20,
				Config: Config{CallRules: []CallRule{{Symbol: symbol, Kind: "event_publish", Argument: 0}}},
			})
			if err == nil {
				t.Errorf("Trace accepted malformed call-rule symbol %q", symbol)
			}
		})
	}

	const canonical = "example.com/sample::sendAPI"
	text := fmt.Sprintf(`{"call_rules":[{"symbol":%q,"kind":"event_publish","argument":0}]}`, canonical)
	if _, err := ReadConfig(strings.NewReader(text)); err != nil {
		t.Fatalf("ReadConfig canonical call-rule symbol: %v", err)
	}
	if _, err := Trace(context.Background(), Options{
		Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20,
		Config: Config{CallRules: []CallRule{{Symbol: canonical, Kind: "event_publish", Argument: 0}}},
	}); err != nil {
		t.Fatalf("Trace with canonical call-rule symbol: %v", err)
	}
}

func TestOtherConfiguredSymbolsRejectWhitespaceWithinQualifiedParts(t *testing.T) {
	tests := []struct {
		name      string
		canonical string
		json      string
		configure func(string) Config
	}{
		{
			name:      "lifecycle rule",
			canonical: "example.com/sample::Acquire",
			json:      `{"lifecycle_rules":[{"symbol":%q,"role":"acquire","namespace":"leases","identity":"origin","result":0}]}`,
			configure: func(symbol string) Config {
				return Config{LifecycleRules: []LifecycleRule{{Symbol: symbol, Role: "acquire", Namespace: "leases", Identity: "origin", Result: intPointer(0)}}}
			},
		},
		{
			name:      "database origin",
			canonical: "example.com/sample::OpenPrimary",
			json:      `{"storage_scopes":[{"namespace":"primary","database_origins":[%q]}]}`,
			configure: func(symbol string) Config {
				return Config{StorageScopes: []StorageScope{{Namespace: "primary", DatabaseOrigins: []string{symbol}}}}
			},
		},
	}
	invalid := []string{
		" example.com/sample::Acquire",
		"example.com/sample::Acquire ",
		"example.com/sample ::Acquire",
		"example.com/sample:: Acquire",
		"example.com/sample::Ac quire",
		"::Acquire",
		"example.com/sample::",
		"example.com/sample::Acquire::extra",
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, symbol := range invalid {
				t.Run(fmt.Sprintf("%q", symbol), func(t *testing.T) {
					text := fmt.Sprintf(tt.json, symbol)
					if _, err := ReadConfig(strings.NewReader(text)); err == nil {
						t.Errorf("ReadConfig accepted malformed %s symbol %q", tt.name, symbol)
					}
					_, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20, Config: tt.configure(symbol)})
					if err == nil {
						t.Errorf("Trace accepted malformed %s symbol %q", tt.name, symbol)
					}
				})
			}
			text := fmt.Sprintf(tt.json, tt.canonical)
			if _, err := ReadConfig(strings.NewReader(text)); err != nil {
				t.Errorf("ReadConfig rejected canonical %s symbol: %v", tt.name, err)
			}
			if _, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20, Config: tt.configure(tt.canonical)}); err != nil {
				t.Errorf("Trace rejected canonical %s symbol: %v", tt.name, err)
			}
		})
	}
}

func intPointer(value int) *int { return &value }
