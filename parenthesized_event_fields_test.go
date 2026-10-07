package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParenthesizedEventFieldsUseUnderlyingSelector(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/parenthesizedevents\n\ngo 1.27.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := `package app

type Envelope struct {
	EventType string
	Version   string
}

func Assign() {
	var envelope Envelope
	(envelope.EventType) = "orders.assigned"
}

func RangeValue(topics []string) {
	var envelope Envelope
	for _, ((envelope.EventType)) = range topics {}
}

func RangeUnrelated(versions []string) {
	var envelope Envelope
	for _, (envelope.Version) = range versions {}
}

func RangeBareField(topics []string) {
	var EventType string
	for _, (EventType) = range topics {}
	_ = EventType
}

func Compare() {
	var envelope Envelope
	if (envelope.EventType) == "orders.compared" {}
}

func Switch() {
	var envelope Envelope
	switch (envelope.EventType) {
	case "orders.switched":
	}
}

var packageEnvelope Envelope
var packageEvent = func() int {
	(packageEnvelope.EventType) = "orders.initializer"
	return 0
}()
`
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}

	report, err := Trace(context.Background(), Options{
		Root: root,
		Seeds: []string{
			"Assign", "RangeValue", "RangeUnrelated", "RangeBareField", "Compare", "Switch",
			"example.com/parenthesizedevents::(package init)",
		},
		Depth:    2,
		MaxNodes: 100,
		Config:   Config{EventFields: []string{"EventType"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	line := func(fragment string) int {
		t.Helper()
		index := strings.Index(source, fragment)
		if index < 0 {
			t.Fatalf("fixture does not contain %q", fragment)
		}
		return strings.Count(source[:index], "\n") + 1
	}
	owner := func(name string) string { return "example.com/parenthesizedevents::" + name }
	expected := []struct {
		from string
		to   string
		kind string
		line int
	}{
		{owner("Assign"), "event:orders.assigned", "event_assign", line(`(envelope.EventType) = "orders.assigned"`)},
		{owner("Compare"), "event:orders.compared", "event_compare", line(`if (envelope.EventType) == "orders.compared"`)},
		{owner("Switch"), "event:orders.switched", "event_case", line(`case "orders.switched":`)},
		{owner("(package init)"), "event:orders.initializer", "event_assign", line(`(packageEnvelope.EventType) = "orders.initializer"`)},
	}
	for _, want := range expected {
		if !hasRelationship(report, want.from, want.to, want.kind, "app.go", want.line) {
			t.Errorf("missing parenthesized event relationship from %s to %s (%s) at app.go:%d", want.from, want.to, want.kind, want.line)
		}
	}
	rangeOwner := owner("RangeValue")
	rangeLine := line("for _, ((envelope.EventType)) = range topics")
	if !hasBoundaryAt(report, rangeOwner, "dynamic_event", "app.go", rangeLine) {
		t.Errorf("parenthesized range target lacks dynamic_event at app.go:%d", rangeLine)
	}
	for _, edge := range report.Relationships {
		if edge.From == rangeOwner && edge.Kind == "event_assign" {
			t.Errorf("range iteration value was invented as a completed event: %+v", edge)
		}
	}
	if hasBoundaryAt(report, owner("RangeUnrelated"), "dynamic_event", "app.go", line("for _, (envelope.Version) = range versions")) {
		t.Error("parenthesized unrelated selector acquired a dynamic_event boundary")
	}
	if !hasBoundaryAt(report, owner("RangeBareField"), "dynamic_event", "app.go", line("for _, (EventType) = range topics")) {
		t.Error("parenthesized configured bare field lacks dynamic_event")
	}
}
