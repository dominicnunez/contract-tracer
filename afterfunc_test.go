package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestAliasedCancellationRetainsBoundMethodAndOutsideAlternative(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"AliasedCancellationCaller", "KnownOutsideAliasedCancellationCaller", "MixedAliasedCancellation"}, Depth: 5, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	registrations := map[string]string{}
	targets := map[string]bool{}
	channel := ""
	deferred, goroutine, localStop, mixedStop := false, false, false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::registerAliasedVoid") {
			deferred = deferred || edge.Kind == "deferred_cancellation_callback_register"
			goroutine = goroutine || edge.Kind == "goroutine_cancellation_callback_register"
		}
		if strings.HasSuffix(edge.From, "::MixedAliasedCancellation") {
			localStop = localStop || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.To, "::localAliasedStop")
			mixedStop = mixedStop || edge.Kind == "cancellation_callback_stop"
		}
		if edge.Kind == "cancellation_callback_register" {
			if edge.Certainty != "possible" {
				t.Errorf("dynamic registration is not a fact: %+v", edge)
			}
			registrations[edge.From] = edge.To
		}
		if edge.Kind == "cancellation_callback_target" {
			targets[edge.To] = true
		}
		if edge.Kind == "channel_create" && strings.HasSuffix(edge.From, "::AliasedCancellationCaller") {
			channel = edge.To
		}
	}
	for _, caller := range []string{"registerAliasedCancellation", "OutsideAliasedCancellation"} {
		if registrations["example.com/sample::"+caller] == "" {
			t.Errorf("missing aliased registration %s", caller)
		}
	}
	closed, stopped, partialRegistration, partialStop, falseUnknown := false, false, false, false, false
	for _, edge := range r.Relationships {
		closed = closed || edge.Kind == "channel_close" && strings.Contains(edge.From, "cancellationOwner.finish") && edge.To == channel
		stopped = stopped || edge.Kind == "cancellation_callback_stop" && strings.HasSuffix(edge.From, "::AliasedCancellationCaller") && edge.To == registrations["example.com/sample::registerAliasedCancellation"]
	}
	for _, boundary := range r.Boundaries {
		if strings.HasSuffix(boundary.Node, "::OutsideAliasedCancellation") {
			partialRegistration = partialRegistration || boundary.Kind == "partial_function_call" && strings.Contains(boundary.Evidence.Snippet, "register(")
			partialStop = partialStop || boundary.Kind == "partial_function_call" && boundary.Evidence.Snippet == "stop()"
		}
		falseUnknown = falseUnknown || strings.HasSuffix(boundary.Node, "::AliasedCancellationCaller") && boundary.Evidence.Snippet == "stop()" && strings.Contains(boundary.Kind, "function_call")
		falseUnknown = falseUnknown || strings.HasSuffix(boundary.Node, "::MixedAliasedCancellation") && boundary.Evidence.Snippet == "stop()" && strings.Contains(boundary.Kind, "function_call")
	}
	bound := false
	for target := range targets {
		bound = bound || strings.Contains(target, "cancellationOwner.finish")
	}
	if !deferred || !goroutine || !localStop || !mixedStop {
		t.Errorf("alias alternate paths missing: defer=%t go=%t local-stop=%t mixed-stop=%t", deferred, goroutine, localStop, mixedStop)
	}
	if !bound || channel == "" || !closed || !stopped || !partialRegistration || !partialStop || falseUnknown {
		t.Fatalf("alias/binding coverage: bound=%t channel=%s close=%t stop=%t partial-register=%t partial-stop=%t false-unknown=%t", bound, channel, closed, stopped, partialRegistration, partialStop, falseUnknown)
	}
}

func TestKnownStopMixtureAndCancelCallbackBridge(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"MixedKnownStop", "AfterFuncCancelBridge"}, Depth: 3, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	local, stop := false, false
	registration, context, canceled := "", "", ""
	for _, edge := range r.Relationships {
		local = local || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::MixedKnownStop") && strings.HasSuffix(edge.To, "::localFunctionStop")
		stop = stop || edge.Kind == "cancellation_callback_stop" && strings.HasSuffix(edge.From, "::MixedKnownStop")
		if edge.Kind == "cancellation_callback_register" && strings.HasSuffix(edge.From, "::AfterFuncCancelBridge") {
			registration = edge.To
		}
	}
	for _, edge := range r.Relationships {
		if edge.From == registration && edge.Kind == "cancellation_callback_context" {
			context = edge.To
		}
		if edge.From == registration && edge.Kind == "cancellation_callback_cancel" {
			canceled = edge.To
		}
	}
	for _, boundary := range r.Boundaries {
		if strings.HasSuffix(boundary.Node, "::MixedKnownStop") && boundary.Evidence.Snippet == "stop()" && strings.Contains(boundary.Kind, "function_call") {
			t.Errorf("fully modeled mixture became unknown: %+v", boundary)
		}
		if boundary.Node == registration && boundary.Kind == "unresolved_cancellation_callback" {
			t.Error("modeled cancel callback became unknown")
		}
	}
	if !local || !stop || registration == "" || context == "" || canceled == "" || context == canceled {
		t.Fatalf("mixture/bridge missing: local=%t stop=%t registration=%s context=%s canceled=%s", local, stop, registration, context, canceled)
	}
}

func TestCancellationCallbacksRetainRegistrationContextAndStop(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"AfterFuncCaller"}, Depth: 4, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	registrations := map[string]bool{}
	targets := map[string]bool{}
	contexts := map[string]bool{}
	stops := map[string]bool{}
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.Kind, "cancellation_callback_register") {
			registrations[edge.To] = true
		}
		if edge.Kind == "cancellation_callback_target" {
			targets[edge.To] = true
		}
		if edge.Kind == "cancellation_callback_context" {
			contexts[edge.From] = true
		}
		if strings.HasSuffix(edge.Kind, "cancellation_callback_stop") {
			stops[edge.Kind] = true
		}
	}
	if len(registrations) != 6 || len(contexts) != 6 {
		t.Errorf("independent registration/context sites: %v / %v", registrations, contexts)
	}
	for _, target := range []string{"firstCancellationCallback", "secondCancellationCallback", "detachedCancellationCallback", "outsideCancellationCallback", "deferredRegistrationCallback", "goroutineRegistrationCallback"} {
		if !targets["example.com/sample::"+target] {
			t.Errorf("missing cancellation target %s", target)
		}
	}
	for _, kind := range []string{"cancellation_callback_stop", "deferred_cancellation_callback_stop", "goroutine_cancellation_callback_stop"} {
		if !stops[kind] {
			t.Errorf("missing stop relationship %s", kind)
		}
	}
	unknownContext, unknownCallback, detached, ordering := false, false, false, false
	for _, boundary := range r.Boundaries {
		unknownContext = unknownContext || boundary.Kind == "unresolved_callback_context"
		unknownCallback = unknownCallback || boundary.Kind == "unresolved_cancellation_callback"
		detached = detached || boundary.Kind == "noncanceling_callback_context"
		ordering = ordering || boundary.Kind == "cancellation_callback_model"
		if strings.Contains(boundary.Kind, "unresolved_function_call") && (boundary.Evidence.Snippet == "record.Stop()" || boundary.Evidence.Snippet == "defer second()" || boundary.Evidence.Snippet == "go third()") {
			t.Errorf("modeled stop looks unresolved: %+v", boundary)
		}
	}
	if !unknownContext || !unknownCallback || !detached || !ordering {
		t.Errorf("callback limits: context=%t callback=%t detached=%t ordering=%t", unknownContext, unknownCallback, detached, ordering)
	}
}

func TestCancellationCallbackCompletionKeepsSeparateChannelPath(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"AfterFuncCompletionCaller"}, Depth: 4, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	registration, channel := "", ""
	for _, edge := range r.Relationships {
		if edge.Kind == "cancellation_callback_register" && strings.HasSuffix(edge.From, "::AfterFuncCompletionCaller") {
			registration = edge.To
		}
		if edge.Kind == "channel_create" && strings.HasSuffix(edge.From, "::AfterFuncCompletionCaller") {
			channel = edge.To
		}
	}
	targets := map[string]bool{}
	for _, edge := range r.Relationships {
		if edge.Kind == "cancellation_callback_target" && edge.From == registration {
			targets[edge.To] = true
		}
	}
	closed, waited, stopped := false, false, false
	for _, edge := range r.Relationships {
		closed = closed || edge.Kind == "channel_close" && targets[edge.From] && edge.To == channel
		waited = waited || edge.Kind == "channel_receive" && strings.HasSuffix(edge.From, "::AfterFuncCompletionCaller") && edge.To == channel
		stopped = stopped || edge.Kind == "cancellation_callback_stop" && edge.To == registration
	}
	if registration == "" || channel == "" || !closed || !waited || !stopped {
		t.Fatalf("separate completion path: registration=%s channel=%s close=%t wait=%t stop=%t", registration, channel, closed, waited, stopped)
	}
}
