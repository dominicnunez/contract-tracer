package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRangeAssignmentsKeepConfiguredEventValuesDynamic(t *testing.T) {
	root := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/rangeevents\n\ngo 1.27.0\n")
	write("app.go", `package app

type Envelope struct { EventType string; Version int }

func MapKey(topics map[string]bool) {
	var envelope Envelope
	for envelope.EventType = range topics {}
}
func SliceValue(topics []string) {
	var envelope Envelope
	for _, envelope.EventType = range topics {}
}
func ChannelValue(topics <-chan string) {
	var envelope Envelope
	for envelope.EventType = range topics {}
}
func IntegerAndStringRanges() {
	var envelope Envelope
	for envelope.Version = range 3 {}
	for envelope.Version = range "orders" {}
}
func Direct() {
	var envelope Envelope
	envelope.EventType = "orders.direct"
}

var packageTopics = map[string]bool{"orders.package": true}
var packageEnvelope = Envelope{}
var packageRange = func() int {
	for packageEnvelope.EventType = range packageTopics {}
	return 0
}()

func BareRangeField(values []string) {
	var EventType string
	for _, EventType = range values {}
	_ = EventType
}
`)

	report, err := Trace(context.Background(), Options{
		Root: root,
		Seeds: []string{
			"MapKey", "SliceValue", "ChannelValue", "IntegerAndStringRanges", "Direct", "BareRangeField",
			"example.com/rangeevents::(package init)",
		},
		Depth:    2,
		MaxNodes: 100,
		Config:   Config{EventFields: []string{"EventType"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		owner string
		line  int
	}{
		{"MapKey", 7},
		{"SliceValue", 11},
		{"ChannelValue", 15},
		{"(package init)", 30},
		{"BareRangeField", 36},
	} {
		if !hasBoundaryAt(report, "example.com/rangeevents::"+expected.owner, "dynamic_event", "app.go", expected.line) {
			t.Errorf("range assignment to configured event field lacks dynamic_event at %s:%d", expected.owner, expected.line)
		}
	}
	if !hasRelationship(report, "example.com/rangeevents::Direct", "event:orders.direct", "event_assign", "app.go", 24) {
		t.Error("ordinary event assignment stopped producing its known event")
	}
	for _, line := range []int{19, 20} {
		if hasBoundaryAt(report, "example.com/rangeevents::IntegerAndStringRanges", "dynamic_event", "app.go", line) {
			t.Errorf("range on unrelated Envelope.Version field acquired dynamic_event at app.go:%d", line)
		}
	}
	for _, event := range []string{"orders.key", "orders.value", "orders.package"} {
		for _, edge := range report.Relationships {
			if edge.Kind == "event_assign" && edge.To == "event:"+event {
				t.Errorf("range source value %q was incorrectly treated as a completed event assignment: %+v", event, edge)
			}
		}
	}
	for _, owner := range []string{"MapKey", "SliceValue", "ChannelValue", "IntegerAndStringRanges", "BareRangeField"} {
		for _, edge := range report.Relationships {
			if edge.Kind == "event_assign" && edge.From == "example.com/rangeevents::"+owner {
				t.Errorf("range iteration was reported as a known event assignment: %+v", edge)
			}
		}
	}
}
