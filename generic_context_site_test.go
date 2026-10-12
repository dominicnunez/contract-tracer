package contracttrace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenericContextSiteKeepsEveryParentCandidate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/genericcontextsites\n\ngo 1.27.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := `package app

import "context"

func withValue[T any](parent context.Context, value T) context.Context {
	return context.WithValue(parent, "key", value)
}

func withCancel[T any](parent context.Context, value T) (context.Context, context.CancelFunc) {
	return context.WithCancel(parent)
}

func withSchedulerCancel[T any](parent context.Context, value T) (context.Context, context.CancelFunc) {
	return context.WithCancel(parent)
}

type schedulingContext struct {
	context.Context
	ready, stopped chan struct{}
	pending        func()
}

func (ctx *schedulingContext) Done() <-chan struct{} { return ctx.ready }
func (ctx *schedulingContext) AfterFunc(callback func()) func() bool {
	ctx.pending = callback
	return ctx.stop
}
func (ctx *schedulingContext) stop() bool { close(ctx.stopped); return true }
func (ctx *schedulingContext) fire()       { ctx.pending() }

func Observe(ctx context.Context) {
	<-ctx.Done()
}

func Scenario() {
	background := context.Background()
	cancelable, cancel := context.WithCancel(background)
	defer cancel()
	Observe(withValue(cancelable, 1))
	Observe(withValue(context.Background(), "value"))
	parent := context.Background()
	_, cancelOne := withCancel(cancelable, 1)
	_, cancelTwo := withCancel(parent, "value")
	cancelOne()
	cancelTwo()
	firstScheduler := &schedulingContext{Context: context.Background(), ready: make(chan struct{}), stopped: make(chan struct{})}
	_, cancelFirstScheduler := withSchedulerCancel(firstScheduler, 1)
	_, cancelSecondScheduler := withSchedulerCancel(context.Background(), "value")
	firstScheduler.fire()
	cancelFirstScheduler()
	cancelSecondScheduler()
}
`
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}

	options := Options{Root: root, Seeds: []string{"Scenario"}, Depth: 4, MaxNodes: 180}
	report, analysis, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}

	lineOf := func(fragment string) int {
		t.Helper()
		position := strings.Index(source, fragment)
		if position < 0 {
			t.Fatalf("fixture does not contain %q", fragment)
		}
		return strings.Count(source[:position], "\n") + 1
	}
	contextAt := func(fragment string) string {
		t.Helper()
		line := lineOf(fragment)
		for _, node := range report.Nodes {
			if node.Kind == "context" && node.Evidence.File == "app.go" && node.Evidence.Line == line {
				return node.ID
			}
		}
		t.Fatalf("no context node for %q at app.go:%d", fragment, line)
		return ""
	}

	withValueLine := lineOf("return context.WithValue")
	observeLine := lineOf("<-ctx.Done()")
	contextID := ""
	for _, node := range report.Nodes {
		if node.Kind == "context" && node.Evidence.File == "app.go" && node.Evidence.Line == withValueLine {
			contextID = node.ID
			break
		}
	}
	if contextID == "" {
		t.Fatalf("generic WithValue source site has no context node at app.go:%d", withValueLine)
	}
	outer := contextAt("cancelable, cancel := context.WithCancel(background)")
	backgroundParent := contextAt("Observe(withValue(context.Background(), \"value\"))")
	for _, parentID := range []string{outer, backgroundParent} {
		if !hasRelationship(report, contextID, parentID, "context_cancellation_parent", "app.go", withValueLine) {
			t.Errorf("generic WithValue source site lost parent context candidate %s", parentID)
		}
	}
	signal := ""
	for _, edge := range report.Relationships {
		if edge.From == outer && edge.Kind == "context_done_signal" {
			signal = edge.To
		}
	}
	if signal == "" {
		t.Errorf("generic WithValue source site lost its cancelable parent's Done signal candidate (%s)", outer)
	} else if !hasRelationship(report, "example.com/genericcontextsites::Observe", signal, "channel_receive", "app.go", observeLine) {
		t.Errorf("generic WithValue result was not connected to the cancelable parent's observed Done channel %s", signal)
	}
	if !hasBoundaryAt(report, "example.com/genericcontextsites::Observe", "nil_context_done_wait", "app.go", observeLine) {
		t.Errorf("generic WithValue source site lost its Background parent's nil Done candidate at app.go:%d", observeLine)
	}

	withCancelLine := lineOf("return context.WithCancel(parent)")
	withCancelID := ""
	for _, node := range report.Nodes {
		if node.Kind == "context" && node.Evidence.File == "app.go" && node.Evidence.Line == withCancelLine {
			withCancelID = node.ID
			break
		}
	}
	if withCancelID == "" {
		t.Fatalf("generic WithCancel source site has no context node at app.go:%d", withCancelLine)
	}
	parent := contextAt("parent := context.Background()")
	for _, parentID := range []string{outer, parent} {
		if !hasRelationship(report, withCancelID, parentID, "context_cancellation_parent", "app.go", withCancelLine) {
			t.Errorf("generic WithCancel source site lost parent candidate %s", parentID)
		}
	}

	// This generic constructor source site receives one scheduler-aware context
	// and one Background context. The scheduler body's cancellation edge shows
	// that the aggregated source-site reader retained the scheduler parent.
	schedulerCancelLine := lineOf("func withSchedulerCancel") + 1
	var schedulerContextID string
	for _, node := range report.Nodes {
		if node.Kind == "context" && node.Evidence.File == "app.go" && node.Evidence.Line == schedulerCancelLine {
			schedulerContextID = node.ID
		}
	}
	if schedulerContextID == "" {
		t.Fatalf("generic scheduler-aware cancelable site has no context node at app.go:%d", schedulerCancelLine)
	}
	fireLine := lineOf("func (ctx *schedulingContext) fire()")
	if !hasRelationship(report, "example.com/genericcontextsites::schedulingContext.fire", schedulerContextID, "context_cancel", "app.go", fireLine) {
		t.Errorf("aggregated generic cancelable parent lost the scheduler-body cancellation candidate %s", schedulerContextID)
	}
	schedulerBackgroundParent := contextAt("_, cancelSecondScheduler")
	if !hasRelationship(report, schedulerContextID, schedulerBackgroundParent, "context_cancellation_parent", "app.go", schedulerCancelLine) {
		t.Errorf("generic scheduler-aware source site lost its Background parent candidate %s", schedulerBackgroundParent)
	}
	if !hasBoundaryAt(report, "example.com/genericcontextsites::withSchedulerCancel", "context_parent", "app.go", schedulerCancelLine) {
		t.Errorf("generic scheduler-aware source site lost the unresolved custom-parent identity boundary")
	}
	if !hasRelationship(report, schedulerContextID, "example.com/genericcontextsites::schedulingContext.stop", "context_scheduler_stop_target", "app.go", schedulerCancelLine) {
		t.Errorf("generic cancelable source site lost the custom scheduler stop target")
	}

	var compressed bytes.Buffer
	if err := WriteAnalysis(&compressed, analysis, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: options.Seeds, Depth: options.Depth, MaxNodes: options.MaxNodes})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRelationship(resumed, contextID, outer, "context_cancellation_parent", "app.go", withValueLine) ||
		!hasRelationship(resumed, contextID, backgroundParent, "context_cancellation_parent", "app.go", withValueLine) ||
		!hasBoundaryAt(resumed, "example.com/genericcontextsites::Observe", "nil_context_done_wait", "app.go", observeLine) ||
		!hasRelationship(resumed, "example.com/genericcontextsites::schedulingContext.fire", schedulerContextID, "context_cancel", "app.go", fireLine) {
		t.Errorf("gzip-resumed analysis lost generic context parent, nil-Done, or scheduler candidates")
	}
}

func TestGenericWithoutCancelWideningKeepsKnownNilDone(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/genericcontextwidening\n\ngo 1.27.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	source.WriteString("package app\n\nimport \"context\"\n\n")
	for i := 0; i <= maxFlowValues; i++ {
		fmt.Fprintf(&source, "type token%d struct{}\n", i)
	}
	source.WriteString(`
func detach[T any](parent context.Context, value T) {
	detached := context.WithoutCancel(parent)
	<-detached.Done()
}

func inherit[T any](parent context.Context, value T) {
	inherited := context.WithValue(parent, "key", value)
	<-inherited.Done()
}

func Scenario() {
`)
	for i := 0; i <= maxFlowValues; i++ {
		fmt.Fprintf(&source, "\tparent%d := context.Background()\n\tdetach(parent%d, token%d{})\n\tinherit(parent%d, token%d{})\n", i, i, i, i, i)
	}
	source.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}

	report, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"Scenario"}, Depth: 4, MaxNodes: 600})
	if err != nil {
		t.Fatal(err)
	}
	detachPos := strings.Index(source.String(), "detached := context.WithoutCancel")
	detachLine := strings.Count(source.String()[:detachPos], "\n") + 1
	detachReceivePos := strings.Index(source.String(), "<-detached.Done()")
	detachReceiveLine := strings.Count(source.String()[:detachReceivePos], "\n") + 1
	inheritPos := strings.Index(source.String(), "inherited := context.WithValue")
	inheritLine := strings.Count(source.String()[:inheritPos], "\n") + 1
	inheritReceivePos := strings.Index(source.String(), "<-inherited.Done()")
	inheritReceiveLine := strings.Count(source.String()[:inheritReceivePos], "\n") + 1
	parentWidenedAt := func(owner string, line int) bool {
		for _, boundary := range report.Boundaries {
			if boundary.Node == owner && boundary.Kind == "context_parent" && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Reason, "widened") {
				return true
			}
		}
		return false
	}
	if !parentWidenedAt("example.com/genericcontextwidening::detach", detachLine) {
		t.Errorf("bounded generic parent candidates were not reported at detach source line %d", detachLine)
	}
	if !hasBoundaryAt(report, "example.com/genericcontextwidening::detach", "nil_context_done_wait", "app.go", detachReceiveLine) {
		t.Errorf("WithoutCancel lost its known nil Done candidate after parent widening at line %d", detachReceiveLine)
	}
	if !parentWidenedAt("example.com/genericcontextwidening::inherit", inheritLine) ||
		!hasBoundaryAt(report, "example.com/genericcontextwidening::inherit", "nil_context_done_wait", "app.go", inheritReceiveLine) {
		t.Errorf("WithValue lost its bounded parent uncertainty or nil Done candidate at lines %d/%d", inheritLine, inheritReceiveLine)
	}
	if hasBoundaryAt(report, "example.com/genericcontextwidening::detach", "unresolved_channel", "app.go", detachReceiveLine) {
		for _, boundary := range report.Boundaries {
			if boundary.Node == "example.com/genericcontextwidening::detach" && boundary.Kind == "unresolved_channel" && boundary.Evidence.File == "app.go" && boundary.Evidence.Line == detachReceiveLine {
				t.Logf("detached unresolved boundary: %+v", boundary)
			}
		}
		t.Errorf("parent widening incorrectly made the known WithoutCancel Done result unresolved")
	}
	if !hasBoundaryAt(report, "example.com/genericcontextwidening::inherit", "unresolved_channel", "app.go", inheritReceiveLine) {
		t.Errorf("WithValue parent widening did not retain unresolved Done alternatives")
	}
	for _, edge := range report.Relationships {
		if edge.Kind == "context_done_signal" && edge.Evidence.File == "app.go" && edge.Evidence.Line == detachLine {
			t.Errorf("WithoutCancel inherited a parent Done signal despite parent widening: %+v", edge)
		}
	}
}
