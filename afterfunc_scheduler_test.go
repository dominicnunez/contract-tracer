package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCustomSchedulerValueAndNestedReceiverStorage(t *testing.T) {
	seeds := []string{"ValuePromotedSchedulingCaller", "NestedPromotedSchedulingCaller", "ValueReceiverSchedulingCaller"}
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	for _, caller := range []string{"ValuePromotedSchedulingCaller", "NestedPromotedSchedulingCaller", "ValueReceiverSchedulingCaller"} {
		registration, channel := "", ""
		for _, edge := range r.Relationships {
			if !strings.HasSuffix(edge.From, "::"+caller) {
				continue
			}
			if edge.Kind == "cancellation_callback_register" {
				registration = edge.To
			}
			if edge.Kind == "channel_create" && strings.Contains(edge.Evidence.Snippet, "stopped :=") {
				channel = edge.To
			}
		}
		scheduler, stop, closed, dispatched := false, false, false, caller == "ValueReceiverSchedulingCaller"
		for _, edge := range r.Relationships {
			scheduler = scheduler || edge.From == registration && edge.Kind == "cancellation_callback_scheduler"
			stop = stop || edge.From == registration && edge.Kind == "cancellation_scheduler_stop_target"
			closed = closed || edge.To == channel && edge.Kind == "channel_close"
			dispatched = dispatched || edge.To == registration && edge.Kind == "cancellation_scheduler_dispatch"
		}
		if registration == "" || channel == "" || !scheduler || !stop || !closed || !dispatched {
			t.Errorf("%s missing receiver path: registration=%s channel=%s scheduler=%t stop=%t close=%t relay=%t", caller, registration, channel, scheduler, stop, closed, dispatched)
		}
	}
}

func TestCustomCancellationSchedulerRetainsRelayReceiverAndStop(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"CustomSchedulingCaller", "WrongSchedulingCaller", "KnownOutsideCustomSchedulingCaller", "NamedSchedulingCaller", "ChildCustomSchedulingCaller", "OtherChildCustomSchedulingCaller", "PromotedSchedulingCaller"}, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	registration, wrong, outside, named, child, stoppedResource := "", "", "", "", "", ""
	for _, e := range r.Relationships {
		if e.Kind == "channel_create" && strings.HasSuffix(e.From, "::CustomSchedulingCaller") && strings.Contains(e.Evidence.Snippet, "stopped :=") {
			stoppedResource = e.To
		}
		if e.Kind == "context_create" && strings.HasSuffix(e.From, "::ChildCustomSchedulingCaller") && strings.Contains(e.Evidence.Snippet, "WithCancel(parent)") {
			child = e.To
		}
		if e.Kind != "cancellation_callback_register" {
			continue
		}
		if strings.HasSuffix(e.From, "::CustomSchedulingCaller") {
			registration = e.To
		}
		if strings.HasSuffix(e.From, "::WrongSchedulingCaller") {
			wrong = e.To
		}
		if strings.HasSuffix(e.From, "::OutsideCustomScheduling") {
			outside = e.To
		}
		if strings.HasSuffix(e.From, "::NamedSchedulingCaller") {
			named = e.To
		}
	}
	scheduler, relay, stop, stoppedChannel := false, false, false, false
	childScheduler, childCancel, childStop := false, false, false
	deferred, goroutine := false, false
	otherChildren := map[string]bool{}
	promotedRegistration, promotedChannel := "", ""
	for _, e := range r.Relationships {
		if e.Kind == "cancellation_callback_register" && strings.HasSuffix(e.From, "::PromotedSchedulingCaller") {
			promotedRegistration = e.To
		}
		if e.Kind == "channel_create" && strings.HasSuffix(e.From, "::PromotedSchedulingCaller") && strings.Contains(e.Evidence.Snippet, "stopped :=") {
			promotedChannel = e.To
		}
	}
	promotedScheduler, promotedClose := false, false
	for _, e := range r.Relationships {
		promotedScheduler = promotedScheduler || e.From == promotedRegistration && e.Kind == "cancellation_callback_scheduler"
		promotedClose = promotedClose || e.To == promotedChannel && e.Kind == "channel_close" && strings.Contains(e.From, "customSchedulingContext.stop")
		deferred = deferred || e.To == registration && e.Kind == "deferred_cancellation_scheduler_dispatch"
		goroutine = goroutine || e.To == registration && e.Kind == "goroutine_cancellation_scheduler_dispatch"
		if e.Kind == "context_cancellation_scheduler" && strings.Contains(e.Evidence.Snippet, "parent,") {
			otherChildren[e.From] = true
		}
		if e.Kind == "context_cancellation_scheduler" && strings.Contains(e.Evidence.Snippet, "WithCancelCause(parent)") {
			otherChildren[e.From] = true
		}
		childScheduler = childScheduler || e.From == child && e.Kind == "context_cancellation_scheduler" && strings.Contains(e.To, "customSchedulingContext.AfterFunc")
		childCancel = childCancel || e.To == child && e.Kind == "context_cancel" && strings.Contains(e.From, "customSchedulingContext.fire")
		childStop = childStop || e.From == child && e.Kind == "context_scheduler_stop_target" && strings.Contains(e.To, "customSchedulingContext.stop")
		scheduler = scheduler || e.From == registration && e.Kind == "cancellation_callback_scheduler" && strings.Contains(e.To, "customSchedulingContext.AfterFunc")
		relay = relay || e.To == registration && e.Kind == "cancellation_scheduler_dispatch" && strings.Contains(e.From, "customSchedulingContext.fire")
		stop = stop || e.From == registration && e.Kind == "cancellation_scheduler_stop_target" && strings.Contains(e.To, "customSchedulingContext.stop")
		stoppedChannel = stoppedChannel || e.Kind == "channel_close" && strings.Contains(e.From, "customSchedulingContext.stop") && e.To == stoppedResource
		if (e.From == wrong || e.From == named) && e.Kind == "cancellation_callback_scheduler" {
			t.Errorf("wrong signature became a scheduler: %+v", e)
		}
	}
	unknown := false
	for _, b := range r.Boundaries {
		unknown = unknown || b.Node == outside && b.Kind == "unresolved_cancellation_scheduler"
	}
	if child == "" || stoppedResource == "" || !childScheduler || !childCancel || !childStop {
		t.Errorf("child scheduler missing: child=%s stopped=%s scheduler=%t cancel=%t stop=%t", child, stoppedResource, childScheduler, childCancel, childStop)
	}
	if !deferred || !goroutine || len(otherChildren) != 5 {
		t.Errorf("scheduler sibling paths: defer=%t go=%t constructors=%d", deferred, goroutine, len(otherChildren))
	}
	if promotedRegistration == "" || promotedChannel == "" || !promotedScheduler || !promotedClose {
		t.Errorf("promoted receiver missing: registration=%s channel=%s scheduler=%t close=%t", promotedRegistration, promotedChannel, promotedScheduler, promotedClose)
	}
	if registration == "" || wrong == "" || named == "" || outside == "" || !scheduler || !relay || !stop || !stoppedChannel || !unknown {
		t.Fatalf("custom scheduling paths: register=%s wrong=%s outside=%s scheduler=%t relay=%t stop=%t receiver-close=%t unknown=%t", registration, wrong, outside, scheduler, relay, stop, stoppedChannel, unknown)
	}
}
