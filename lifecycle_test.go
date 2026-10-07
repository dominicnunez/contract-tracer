package contracttrace

import (
	"context"
	"strings"
	"testing"
)

func TestContextLifecycleAcrossBoundaries(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Start", "WrapperStart", "Unreleased"}, Depth: 3, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	cancel, observe, wrapped, missing := false, false, false, false
	for _, e := range r.Relationships {
		if e.Kind == "context_cancel" && strings.HasSuffix(e.From, "::Start") {
			cancel = true
		}
		if e.Kind == "context_cancel" && strings.HasSuffix(e.From, "::WrapperStart") {
			wrapped = true
		}
		if e.Kind == "context_observe" && strings.HasSuffix(e.From, "::Worker") {
			observe = true
		}
	}
	for _, b := range r.Boundaries {
		if b.Kind == "unobserved_cancel" && strings.HasSuffix(b.Node, "::Unreleased") {
			missing = true
		}
	}
	if !cancel || !observe || !wrapped || !missing {
		t.Errorf("lifecycle relationships missing: cancel=%t observe=%t wrapped=%t missing=%t", cancel, observe, wrapped, missing)
	}
}

func TestSeparateContextCreationSitesOnSameLine(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"DualContexts"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	unreleased := false
	for _, e := range r.Relationships {
		if e.Kind == "context_create" && strings.HasSuffix(e.From, "::DualContexts") {
			keys[e.To] = true
		}
	}
	for _, b := range r.Boundaries {
		if b.Kind == "unobserved_cancel" && strings.HasSuffix(b.Node, "::DualContexts") {
			unreleased = true
		}
	}
	if len(keys) != 2 || !unreleased {
		t.Error("one canceled context concealed a separate uncanceled creation on the same line")
	}
}

func TestCancellationSignalConnectsWorkerAndCompletion(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"CancellationStart"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	contextKey, completion := "", ""
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::CancellationStart") && e.Kind == "context_cancel" {
			contextKey = e.To
		}
		if strings.HasSuffix(e.From, "::CancellationStart") && e.Kind == "channel_create" {
			completion = e.To
		}
	}
	signal := ""
	for _, e := range r.Relationships {
		if e.From == contextKey && e.Kind == "context_done_signal" {
			signal = e.To
		}
	}
	wait, close, join := false, false, false
	for _, e := range r.Relationships {
		// SSA lowers a one-arm blocking select to an ordinary receive.
		if strings.HasSuffix(e.From, "::CancellationWorker") && e.To == signal && (e.Kind == "channel_select_receive" || e.Kind == "channel_receive") {
			wait = true
		}
		if strings.HasSuffix(e.From, "::CancellationWorker") && e.To == completion && e.Kind == "channel_close" {
			close = true
		}
		if strings.HasSuffix(e.From, "::CancellationStart") && e.To == completion && e.Kind == "channel_receive" {
			join = true
		}
	}
	if contextKey == "" || signal == "" || !wait || !close || !join {
		t.Errorf("shutdown investigation path missing: context=%s signal=%s wait=%t close=%t join=%t", contextKey, signal, wait, close, join)
	}
}

func TestDoneWrapperForwardsSignalAndNilDoneIsExplicit(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"WrappedDoneStart", "NilDoneReceive", "DetachedDone"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	signal := ""
	ctx := ""
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::WrappedDoneStart") && e.Kind == "context_cancel" {
			ctx = e.To
		}
	}
	for _, e := range r.Relationships {
		if e.From == ctx && e.Kind == "context_done_signal" {
			signal = e.To
		}
	}
	forwarded, nilBoundary, detached := false, false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::WaitSignal") && e.To == signal && e.Kind == "channel_receive" {
			forwarded = true
		}
		if strings.HasSuffix(e.From, "::DetachedDone") && e.Kind == "context_detach" {
			detached = true
		}
	}
	for _, b := range r.Boundaries {
		if strings.HasSuffix(b.Node, "::NilDoneReceive") && b.Kind == "nil_context_done_wait" {
			nilBoundary = true
		}
	}
	if signal == "" || !forwarded || !nilBoundary || !detached {
		t.Errorf("Done semantics missing: signal=%s forward=%t nil=%t detached=%t", signal, forwarded, nilBoundary, detached)
	}
}

func TestDetachedContextDoesNotInheritCancellationSignal(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"DetachStart", "NilDoneSelect"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	detached := ""
	selectNil := false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::DetachedDone") && e.Kind == "context_detach" {
			detached = e.To
		}
	}
	for _, e := range r.Relationships {
		if e.From == detached && (e.Kind == "context_cancellation_parent" || e.Kind == "context_done_signal") {
			t.Errorf("WithoutCancel inherited a cancellation signal: %+v", e)
		}
	}
	for _, b := range r.Boundaries {
		if strings.HasSuffix(b.Node, "::NilDoneSelect") && b.Kind == "nil_context_done_select" {
			selectNil = true
		}
	}
	if detached == "" || !selectNil {
		t.Errorf("detachment or disabled select arm missing: detached=%s select=%t", detached, selectNil)
	}
}

func TestDeadlineChildAndWrappedNilDone(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"DeadlineChild", "WrappedNilDone"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	// Identify the independently expected constructor sites by source evidence,
	// rather than accepting any context relationship in the report.
	parent, child := "", ""
	canceled := map[string]bool{}
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::DeadlineChild") && e.Kind == "context_cancel" {
			canceled[e.To] = true
		}
	}
	for _, n := range r.Nodes {
		if n.Kind != "context" || n.Evidence.File != "cancellation.go" || !canceled[n.ID] {
			continue
		}
		if strings.Contains(n.Evidence.Snippet, "parent, cancelParent := context.WithCancel") {
			parent = n.ID
		}
		if strings.Contains(n.Evidence.Snippet, "child, cancelChild := context.WithTimeout") {
			child = n.ID
		}
	}
	parentLink, signal := false, ""
	for _, e := range r.Relationships {
		if e.From == child && e.To == parent && e.Kind == "context_cancellation_parent" {
			parentLink = true
		}
		if e.From == child && e.Kind == "context_done_signal" {
			signal = e.To
		}
	}
	wait, nilWait := false, false
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::WaitSignal") && e.To == signal && e.Kind == "channel_receive" {
			wait = true
		}
	}
	for _, b := range r.Boundaries {
		if strings.HasSuffix(b.Node, "::WrappedNilDone") && b.Kind == "nil_context_done_wait" {
			nilWait = true
		}
	}
	if parent == "" || child == "" || !parentLink || signal == "" || !wait || !nilWait {
		t.Errorf("deadline/nil inheritance missing: parent=%s child=%s link=%t signal=%s wait=%t nil=%t", parent, child, parentLink, signal, wait, nilWait)
	}
}
