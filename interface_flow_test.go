package contracttrace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInterfaceArgumentsResultsAndReceiverFlow(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"InterfaceWrite", "InterfaceEvent", "PairEntry", "InterfaceCallback"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	sql, event, pair, callback := false, false, false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::SQLRunner.Run") && e.To == "table:interface_records" {
			sql = true
		}
		if strings.HasSuffix(e.From, "::throughProvider") && e.To == "event:INTERFACE_EVENT" {
			event = true
		}
		if strings.HasSuffix(e.From, "::InterfacePair") && e.To == "event:PAIR_SECOND" {
			pair = true
		}
		if strings.HasSuffix(e.From, "::InterfacePair") && e.To == "event:PAIR_FIRST" {
			t.Error("interface result slots were merged")
		}
		if strings.HasSuffix(e.From, "::CheckFunc.Check") && strings.HasSuffix(e.To, "::Validate") && e.Kind == "resolved_callback_call" {
			callback = true
		}
	}
	if !sql || !event || !pair || !callback || !hasName(r, "InterfaceRead") || !hasName(r, "InterfaceConsume") {
		t.Errorf("interface flow missing: sql=%t event=%t pair=%t callback=%t", sql, event, pair, callback)
	}
}

func TestInterfaceCopiesPromotedMethodsAndAsyncCalls(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"NestedInterfaceEvent", "PromotedInterfaceEvent", "AsyncEntry"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	nested, promoted, async, deferred, argument := false, false, false, false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::throughProvider") && e.To == "event:NESTED_INTERFACE" {
			nested = true
		}
		if strings.HasSuffix(e.From, "::throughProvider") && e.To == "event:PROMOTED_INTERFACE" {
			promoted = true
		}
		if strings.HasSuffix(e.From, "::SQLRunner.Run") && e.To == "table:async_records" {
			async = true
		}
		if strings.HasSuffix(e.From, "::SQLRunner.Run") && e.To == "table:deferred_records" {
			deferred = true
		}
		if strings.HasSuffix(e.From, "::InterfaceAsync") && e.Kind == "argument_flow" && e.Slot == "argument:0" {
			for _, v := range e.Values {
				if strings.HasPrefix(v, "string:INSERT INTO async_records") {
					argument = true
				}
			}
		}
	}
	if !nested || !promoted || !async || !deferred || !argument || !hasName(r, "AsyncRead") || !hasName(r, "DeferredRead") {
		t.Errorf("interface sibling flow missing: nested=%t promoted=%t async=%t deferred=%t argument=%t", nested, promoted, async, deferred, argument)
	}
}

func TestSyntheticInterfaceCallsHaveAttributedSourceEvidence(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"throughProvider"}, Depth: 3, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	synthetic := false
	for _, e := range r.Relationships {
		if e.Evidence.File == "" || e.Evidence.Line < 1 {
			t.Errorf("relationship has no source attribution: %s -> %s (%s)", e.From, e.To, e.Kind)
		}
		if e.Kind == "call" && strings.HasSuffix(e.From, "::topicValue.Topic") && e.From == e.To {
			synthetic = true
			if e.Evidence.Origin != "synthetic_declaration" {
				t.Error("generated wrapper looked like a source call expression")
			}
		}
	}
	if !synthetic {
		t.Fatal("fixture did not exercise the generated pointer receiver wrapper")
	}
}

func TestInterfaceWithoutLocalImplementationExposesBoundary(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"ExternalInterface"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range r.Boundaries {
		if b.Node == "example.com/sample::ExternalInterface" && b.Kind == "unresolved_interface_flow" && b.Evidence.Line > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("interface with no loaded local implementation silently ended analysis")
	}
}

func TestInterfaceCandidateBudgetIsDisclosed(t *testing.T) {
	root := t.TempDir()
	module := "module example.com/interfacebudget\n\ngo 1.27.0\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	var code strings.Builder
	code.WriteString("package interfacebudget\ntype Event struct{EventType string}\ntype Sink interface{Send(string)}\ntype S struct{}\nfunc(S)Send(topic string){_=Event{EventType:topic}}\nfunc bridge(s Sink,topic string){s.Send(topic)}\nfunc Entry(){\n")
	for i := 0; i < maxFlowValues+20; i++ {
		fmt.Fprintf(&code, "bridge(S{},%q)\n", fmt.Sprintf("TOPIC_%03d", i))
	}
	code.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte(code.String()), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"S.Send"}, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	limit := false
	for _, n := range r.Nodes {
		if n.Kind == "event" {
			events++
		}
	}
	for _, b := range r.Boundaries {
		if b.Kind == "value_flow_limit" {
			limit = true
		}
	}
	if events == 0 || events > maxFlowValues || !limit || !r.Coverage.ValueFlow.Widened || r.ContractComplete {
		t.Errorf("interface budget looked exhaustive: events=%d limit=%t coverage=%+v", events, limit, r.Coverage.ValueFlow)
	}
}
