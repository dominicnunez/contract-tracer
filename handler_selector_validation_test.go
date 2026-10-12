package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscribeHandlerSelectorValidatesWithoutEventCandidate(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/handlerselectors\n\ngo 1.27.0\nrequire example.com/handlerapi v0.0.0\nreplace example.com/handlerapi => ./api\n",
		"api/go.mod": "module example.com/handlerapi\n\ngo 1.27.0\n",
		"api/api.go": `package handlerapi

type Bus struct{}

func (*Bus) Subscribe(string, func(string)) {}
func (*Bus) SubscribeValid(string, func(string)) {}
func (*Bus) SubscribeBoth(string, func(string)) {}
`,
		"app.go": `package app

import "example.com/handlerapi"

func Handler(string) {}

func InvalidDynamic(bus *handlerapi.Bus, topic string) {
	bus.Subscribe(topic, Handler)
}
func InvalidKnown(bus *handlerapi.Bus) {
	bus.Subscribe("orders.known", Handler)
}
func InvalidMultiple(bus *handlerapi.Bus, choose bool) {
	topic := "orders.first"
	if choose { topic = "orders.second" }
	bus.Subscribe(topic, Handler)
}
func InvalidMethodExpression(bus *handlerapi.Bus, topic string) {
	(*handlerapi.Bus).Subscribe(bus, topic, Handler)
}
func ValidSubscriber(bus *handlerapi.Bus) {
	bus.SubscribeValid("orders.valid", Handler)
}
func InvalidBoth(bus *handlerapi.Bus) {
	bus.SubscribeBoth("orders.both", Handler)
}
func InvalidBound(bus *handlerapi.Bus, topic string) {
	subscribe := bus.Subscribe
	subscribe(topic, Handler)
}
`,
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
	invalidHandler := 2
	validHandler := 1
	invalidArgument := 2
	invalidBothHandler := 3
	config := DefaultConfig()
	config.CallRules = []CallRule{
		{Symbol: "example.com/handlerapi::Bus.Subscribe", Kind: "event_subscribe", Argument: 0, HandlerArgument: &invalidHandler, Namespace: "orders"},
		{Symbol: "example.com/handlerapi::Bus.SubscribeValid", Kind: "event_subscribe", Argument: 0, HandlerArgument: &validHandler, Namespace: "orders"},
		{Symbol: "example.com/handlerapi::Bus.SubscribeBoth", Kind: "event_subscribe", Argument: invalidArgument, HandlerArgument: &invalidBothHandler, Namespace: "orders"},
	}

	report, err := Trace(context.Background(), Options{
		Root: root,
		Seeds: []string{
			"InvalidDynamic", "InvalidKnown", "InvalidMultiple", "InvalidMethodExpression", "ValidSubscriber", "InvalidBoth", "InvalidBound",
		},
		Depth: 4, MaxNodes: 180, Config: config,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		owner   string
		line    int
		snippet string
	}{
		{"InvalidDynamic", 8, "bus.Subscribe(topic"},
		{"InvalidKnown", 11, "bus.Subscribe(\"orders.known\""},
		{"InvalidMultiple", 16, "bus.Subscribe(topic"},
		{"InvalidMethodExpression", 19, "(*handlerapi.Bus).Subscribe"},
		{"InvalidBound", 29, "subscribe(topic, Handler)"},
	} {
		count := 0
		for _, boundary := range report.Boundaries {
			if boundary.Node == "example.com/handlerselectors::"+expected.owner && boundary.Kind == "invalid_rule_site" &&
				strings.Contains(boundary.Reason, "configured handler") && boundary.Evidence.File == "app.go" &&
				boundary.Evidence.Line == expected.line && strings.Contains(boundary.Evidence.Snippet, expected.snippet) {
				count++
			}
		}
		if count != 1 {
			t.Errorf("%s: invalid handler selector boundaries=%d, want one at app.go:%d", expected.owner, count, expected.line)
		}
	}
	for _, reason := range []string{"configured argument", "configured handler"} {
		count := 0
		for _, boundary := range report.Boundaries {
			if boundary.Node == "example.com/handlerselectors::InvalidBoth" && boundary.Kind == "invalid_rule_site" &&
				strings.Contains(boundary.Reason, reason) && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == 25 {
				count++
			}
		}
		if count != 1 {
			t.Errorf("combined-invalid site lost exactly one %s boundary: count=%d", reason, count)
		}
	}
	for _, ownerAndLine := range []struct {
		owner string
		line  int
	}{
		{"InvalidDynamic", 8},
		{"InvalidMultiple", 16},
		{"InvalidMethodExpression", 19},
		{"InvalidBound", 29},
	} {
		if !hasBoundaryAt(report, "example.com/handlerselectors::"+ownerAndLine.owner, "dynamic_api_value", "app.go", ownerAndLine.line) {
			t.Errorf("%s: unresolved event input was not retained at the subscription site", ownerAndLine.owner)
		}
	}
	if !hasRelationship(report, "example.com/handlerselectors::InvalidKnown", "event:orders:orders.known", "event_subscribe", "app.go", 11) {
		t.Error("invalid handler selector suppressed the independent known subscription event candidate")
	}
	for _, value := range []string{"orders.first", "orders.second"} {
		if !hasRelationship(report, "example.com/handlerselectors::InvalidMultiple", "event:orders:"+value, "event_subscribe", "app.go", 16) {
			t.Errorf("multi-candidate subscription lost known event %q", value)
		}
	}
	if !hasRelationship(report, "example.com/handlerselectors::ValidSubscriber", "event:orders:orders.valid", "event_subscribe", "app.go", 22) ||
		!hasRelationship(report, "example.com/handlerselectors::Handler", "event:orders:orders.valid", "event_handler", "app.go", 22) {
		t.Error("valid subscriber rule lost its registration or handler target")
	}
	for _, boundary := range report.Boundaries {
		if boundary.Node == "example.com/handlerselectors::ValidSubscriber" && boundary.Kind == "invalid_rule_site" {
			t.Errorf("valid direct subscriber was incorrectly marked as an invalid rule site: %+v", boundary)
		}
	}
}
