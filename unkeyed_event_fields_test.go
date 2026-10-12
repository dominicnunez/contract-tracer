package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnkeyedStructLiteralsUseResolvedEventFieldPositions(t *testing.T) {
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
	write("go.mod", "module example.com/unkeyedevent\n\ngo 1.27.0\n")
	write("app.go", `package app

type Envelope struct { EventType string; Version int }
type Alias = Envelope
type Generic[T any] struct { Version T; EventType string }

func Positional() {
	_ = Envelope{"orders.positional", 1}
	_ = Alias{"orders.alias", 2}
	_ = Generic[string]{"v1", "orders.generic"}
}

func Keyed() { _ = Envelope{EventType: "orders.keyed", Version: 1} }
func Dynamic(name string) { _ = Envelope{name, 1} }
func NonStructLiterals() {
	_ = []string{"orders.slice"}
	_ = map[string]string{EventType: "orders.map"}
	_ = [1]string{"orders.array"}
}

var packageEnvelope = Envelope{"orders.package", 3}
var dormant = func() { _ = Envelope{"orders.dormant", 4} }

func init() { _ = Envelope{"orders.explicit_init", 5} }
func Elided() { _ = []Envelope{{"orders.elided", 6}} }
func ElidedPointer() { _ = []*Envelope{{"orders.pointer", 7}} }
const EventType = "EventType"
`)

	report, err := Trace(context.Background(), Options{
		Root:  root,
		Seeds: []string{"Positional", "Keyed", "Dynamic", "NonStructLiterals", "Elided", "ElidedPointer", "example.com/unkeyedevent::(package init)"},
		Depth: 2, MaxNodes: 100,
		Config: Config{EventFields: []string{"EventType"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, expected := range []struct {
		owner, event string
		line         int
	}{
		{"example.com/unkeyedevent::Positional", "orders.positional", 8},
		{"example.com/unkeyedevent::Positional", "orders.alias", 9},
		{"example.com/unkeyedevent::Positional", "orders.generic", 10},
		{"example.com/unkeyedevent::Keyed", "orders.keyed", 13},
		{"example.com/unkeyedevent::(package init)", "orders.package", 21},
		{"example.com/unkeyedevent::Elided", "orders.elided", 25},
		{"example.com/unkeyedevent::ElidedPointer", "orders.pointer", 26},
	} {
		if !hasRelationship(report, expected.owner, "event:"+expected.event, "event_construct", "app.go", expected.line) {
			t.Errorf("missing event construct %q from %s at app.go:%d", expected.event, expected.owner, expected.line)
		}
	}
	if !hasBoundaryAt(report, "example.com/unkeyedevent::Dynamic", "dynamic_event", "app.go", 14) {
		t.Error("unresolved unkeyed event discriminator should retain a dynamic_event boundary")
	}
	if hasRelationship(report, "example.com/unkeyedevent::NonStructLiterals", "event:orders.map", "event_construct", "app.go", 17) {
		t.Error("a map key named EventType must not be interpreted as a struct event field")
	}
	explicitInitFound := false
	for _, edge := range report.Relationships {
		if strings.Contains(edge.To, "orders.dormant") {
			t.Errorf("dormant initializer closure was attributed to package initialization: %+v", edge)
		}
		if strings.Contains(edge.To, "orders.explicit_init") {
			if edge.Kind != "event_construct" || edge.Evidence.File != "app.go" || edge.Evidence.Line != 24 ||
				!strings.Contains(edge.From, "example.com/unkeyedevent::init@") {
				t.Errorf("explicit init event has incorrect owner or source evidence: %+v", edge)
			} else {
				explicitInitFound = true
			}
		}
	}
	if !explicitInitFound {
		t.Error("explicit init function's unkeyed event construct was not retained")
	}
}
