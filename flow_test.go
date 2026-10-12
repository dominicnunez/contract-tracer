package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestInterproceduralStrings(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"WrappedWrite", "WrappedPublish"}, Depth: 4, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AuditRead", "WrappedConsume"} {
		if !hasName(r, name) {
			t.Errorf("missing value-flow sibling %s", name)
		}
	}
	sql, event := false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::sqlWrapper") && e.To == "table:audit_log" {
			sql = true
		}
		if strings.HasSuffix(e.From, "::publishKind") && e.To == "event:WRAPPED_COMPLETED" {
			event = true
		}
	}
	if !sql || !event {
		t.Error("strings passed through parameters and returns were not resolved at storage/event sites")
	}
}

func TestMapAndChannelFlow(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"MapCallback", "ChannelEvent", "SelectEvent"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	resolved, wrong, receive, close, selectReceive := false, false, false, false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::MapCallback") && e.Kind == "resolved_callback_call" {
			if strings.HasSuffix(e.To, "::Validate") {
				resolved = true
			}
			if strings.HasSuffix(e.To, "::OtherMapTarget") {
				wrong = true
			}
		}
		if strings.HasSuffix(e.From, "::receiveKind") && e.To == "event:CHANNEL_EVENT" {
			receive = true
		}
		if strings.HasSuffix(e.From, "::selectKind") && e.To == "event:CHANNEL_EVENT" {
			selectReceive = true
		}
		if strings.HasSuffix(e.From, "::ChannelEvent") && e.Kind == "channel_close" {
			close = true
		}
	}
	if !resolved || wrong || !receive || !selectReceive || !close || !hasName(r, "ChannelConsume") {
		t.Errorf("container flow incomplete: resolved=%t wrong=%t receive=%t select=%t close=%t", resolved, wrong, receive, selectReceive, close)
	}
}

func TestContainerIdentityAndTupleSlots(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"IntegerMapCallback", "SecondSelectEvent", "CommaReceiveEvent"}, Depth: 3, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	callback, second, comma := false, false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::IntegerMapCallback") && e.Kind == "resolved_callback_call" {
			if strings.HasSuffix(e.To, "::Validate") {
				callback = true
			}
			if strings.HasSuffix(e.To, "::OtherMapTarget") {
				t.Error("distinct integer map keys merged")
			}
		}
		if strings.HasSuffix(e.From, "::SecondSelectEvent") && e.To == "event:SECOND_CHANNEL" {
			second = true
		}
		if strings.HasSuffix(e.From, "::SecondSelectEvent") && e.To == "event:FIRST_CHANNEL" {
			t.Error("distinct select receive slots merged")
		}
		if strings.HasSuffix(e.From, "::CommaReceiveEvent") && e.To == "event:COMMA_EVENT" {
			comma = true
		}
	}
	if !callback || !second || !comma {
		t.Errorf("tuple flow missing: callback=%t second=%t comma=%t", callback, second, comma)
	}
}
func TestCallbackValueFlow(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"FlowEntry", "RegisteredEntry", "FieldEntry"}, Depth: 4, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{"runCheck": false, "RegisteredEntry": false, "FieldEntry": false}
	for _, e := range r.Relationships {
		if e.Kind == "resolved_callback_call" && strings.HasSuffix(e.To, "::Validate") {
			for name := range expected {
				if strings.HasSuffix(e.From, "::"+name) {
					expected[name] = true
				}
			}
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("callback flow through %s not resolved", name)
		}
	}
}
