package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextDoneTracksBoundAndMethodExpressionReceivers(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/contextdoneprobe\n\ngo 1.26.0\n",
		"context.go": `package contextdoneprobe
import (
	"context"
	"time"
)

func DirectDone(ctx context.Context) { <-ctx.Done() }
func BoundDone(ctx context.Context) {
	done := ctx.Done
	alias := done
	<-alias()
}
func ConsumeDone(fn func() <-chan struct{}) { <-fn() }
func BoundDoneHelper(ctx context.Context) {
	done := ctx.Done
	ConsumeDone(done)
}
func ExpressionDone(ctx context.Context) { <-context.Context.Done(ctx) }
func ConsumeExpression(fn func(context.Context) <-chan struct{}, ctx context.Context) { <-fn(ctx) }
func ExpressionDoneHelper(ctx context.Context) {
	done := context.Context.Done
	ConsumeExpression(done, ctx)
}
type contextWrapper struct { context.Context }
func PromotedDirectDone(ctx context.Context) {
	wrapper := contextWrapper{Context: ctx}
	<-wrapper.Done()
}
func PromotedBoundDone(ctx context.Context) {
	wrapper := contextWrapper{Context: ctx}
	done := wrapper.Done
	<-done()
}
func PromotedExpressionDone(ctx context.Context) {
	wrapper := contextWrapper{Context: ctx}
	<-(*contextWrapper).Done(&wrapper)
}
func DirectErr(ctx context.Context) { _ = ctx.Err() }
func BoundErr(ctx context.Context) { err := ctx.Err; _ = err() }
func ExpressionErr(ctx context.Context) { _ = context.Context.Err(ctx) }
func ConsumeErr(fn func() error) { _ = fn() }
func BoundErrHelper(ctx context.Context) { err := ctx.Err; ConsumeErr(err) }
func ConsumeExpressionErr(fn func(context.Context) error, ctx context.Context) { _ = fn(ctx) }
func ExpressionErrHelper(ctx context.Context) {
	err := context.Context.Err
	ConsumeExpressionErr(err, ctx)
}
func BoundValueDone(ctx context.Context) {
	value := context.WithValue(ctx, "key", "value")
	done := value.Done
	<-done()
}
func NilBackgroundBound() {
	background := context.Background()
	done := background.Done
	<-done()
}
func NilTodoExpression() { <-context.Context.Done(context.TODO()) }
func NilWithValueBound() {
	todo := context.WithValue(context.TODO(), "key", "value")
	done := todo.Done
	<-done()
}
func NilWithoutCancelBound(parent context.Context) {
	detached := context.WithoutCancel(parent)
	done := detached.Done
	<-done()
}

type customContext struct { done <-chan struct{} }
func (customContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c customContext) Done() <-chan struct{} { return c.done }
func (customContext) Err() error { return nil }
func (customContext) Value(any) any { return nil }
type unrelatedContextName struct{}
func (unrelatedContextName) Done() <-chan struct{} { return nil }
func (unrelatedContextName) Err() error { return nil }
func UnrelatedNames() {
	var value unrelatedContextName
	_ = value.Done()
	_ = value.Err()
}
func CustomContext() {
	done := make(chan struct{})
	var ctx context.Context = customContext{done: done}
	<-ctx.Done()
}

func MixedBound(known, outside context.Context) {
	selected := known
	if externalChoice { selected = outside }
	done := selected.Done
	<-done()
}
func UnknownBoundHelper(ctx context.Context) {
	done := ctx.Done
	ConsumeDone(done)
}
func UnknownExpression(ctx context.Context) { <-context.Context.Done(ctx) }

var externalChoice bool

func ConstructorAliasDone() {
	background := context.Background
	base := background()
	makeContext := context.WithCancel
	ctx, cancel := makeContext(base)
	defer cancel()
	<-ctx.Done()
}
func invokeContextFactory(factory func(context.Context) (context.Context, context.CancelFunc), parent context.Context) (context.Context, context.CancelFunc) {
	return factory(parent)
}
func ConstructorHelperDone() {
	makeContext := context.WithCancel
	ctx, cancel := invokeContextFactory(makeContext, context.Background())
	defer cancel()
	<-ctx.Done()
}
func chooseBackground(external bool) func() context.Context {
	if external { return context.TODO }
	return context.Background
}
func ConstructorCompatibleUnion() {
	ctx := chooseBackground(externalChoice)()
	<-ctx.Done()
}

func Scenario(outside context.Context) {
	base := context.Background()
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	DirectDone(ctx)
	BoundDone(ctx)
	BoundDoneHelper(ctx)
	ExpressionDone(ctx)
	ExpressionDoneHelper(ctx)
	PromotedDirectDone(ctx)
	PromotedBoundDone(ctx)
	PromotedExpressionDone(ctx)
	DirectErr(ctx)
	BoundErr(ctx)
	BoundErrHelper(ctx)
	ExpressionErr(ctx)
	ExpressionErrHelper(ctx)
	BoundValueDone(ctx)
	NilBackgroundBound()
	NilTodoExpression()
	NilWithValueBound()
	NilWithoutCancelBound(ctx)
	MixedBound(ctx, outside)
}
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}

	report, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"Scenario", "UnknownExpression", "UnknownBoundHelper", "CustomContext", "UnrelatedNames", "ConstructorAliasDone", "ConstructorHelperDone", "ConstructorCompatibleUnion"}, Depth: 7, MaxNodes: 500})
	if err != nil {
		t.Fatal(err)
	}
	contextID := ""
	for _, edge := range report.Relationships {
		if edge.From == "example.com/contextdoneprobe::Scenario" && edge.Kind == "context_create" && strings.Contains(edge.Evidence.Snippet, "context.WithCancel(base)") {
			contextID = edge.To
		}
	}
	if contextID == "" {
		t.Fatalf("fixture did not establish its source-backed WithCancel context: %+v", report.Relationships)
	}
	signal := ""
	for _, edge := range report.Relationships {
		if edge.From == contextID && edge.Kind == "context_done_signal" {
			signal = edge.To
		}
	}
	if signal == "" {
		t.Fatalf("direct Done control did not establish its cancellation signal for %s", contextID)
	}

	owners := []string{
		"DirectDone",
		"BoundDone",
		"ConsumeDone",
		"ExpressionDone",
		"ConsumeExpression",
		"PromotedDirectDone",
		"PromotedBoundDone",
		"PromotedExpressionDone",
		"BoundValueDone",
		"MixedBound",
	}
	observed := map[string]map[string]bool{}
	for _, owner := range owners {
		observed[owner] = map[string]bool{}
	}
	for _, edge := range report.Relationships {
		if edge.Kind != "channel_receive" && edge.Kind != "channel_select_receive" {
			continue
		}
		for _, owner := range owners {
			if edge.From == "example.com/contextdoneprobe::"+owner {
				observed[owner][edge.To] = true
			}
		}
	}
	for _, owner := range owners {
		if !observed[owner][signal] {
			t.Errorf("%s did not receive the exact known Done signal %s; observed=%v", owner, signal, observed[owner])
		}
	}

	observedContext := map[string]bool{}
	for _, edge := range report.Relationships {
		if edge.Kind == "context_observe" && edge.To == contextID {
			observedContext[edge.From] = true
		}
	}
	for _, owner := range []string{"DirectErr", "BoundErr", "ExpressionErr", "ConsumeErr", "BoundErrHelper", "ConsumeExpressionErr", "ExpressionErrHelper"} {
		if owner == "ConsumeErr" || owner == "ConsumeExpressionErr" || owner == "BoundErrHelper" || owner == "ExpressionErrHelper" {
			continue
		}
		if !observedContext["example.com/contextdoneprobe::"+owner] {
			t.Errorf("%s did not report context observation of %s", owner, contextID)
		}
	}
	for _, owner := range []string{"ConsumeErr", "ConsumeExpressionErr"} {
		if !observedContext["example.com/contextdoneprobe::"+owner] {
			t.Errorf("context Err callback invocation in %s did not retain the context observation", owner)
		}
	}

	nilOwners := map[string]bool{"NilBackgroundBound": false, "NilTodoExpression": false, "NilWithValueBound": false, "NilWithoutCancelBound": false}
	for _, boundary := range report.Boundaries {
		if boundary.Kind != "nil_context_done_wait" {
			continue
		}
		for owner := range nilOwners {
			if boundary.Node == "example.com/contextdoneprobe::"+owner {
				nilOwners[owner] = true
			}
		}
	}
	for owner, found := range nilOwners {
		if !found {
			t.Errorf("%s lost its source-backed nil Done boundary", owner)
		}
	}
	if observedContext["example.com/contextdoneprobe::CustomContext"] {
		t.Error("custom concrete Done implementation was misclassified as a standard context identity")
	}
	if observedContext["example.com/contextdoneprobe::UnrelatedNames"] {
		t.Error("unrelated same-name methods were misclassified as standard context observations")
	}
	for _, boundary := range report.Boundaries {
		if boundary.Node == "example.com/contextdoneprobe::UnrelatedNames" && boundary.Kind == "unresolved_context" {
			t.Errorf("unrelated same-name methods received a context boundary: %+v", boundary)
		}
	}
	customChannels := map[string]bool{}
	customReceives := map[string]bool{}
	for _, edge := range report.Relationships {
		if edge.From != "example.com/contextdoneprobe::CustomContext" {
			continue
		}
		if edge.Kind == "channel_create" {
			customChannels[edge.To] = true
		}
		if edge.Kind == "channel_receive" || edge.Kind == "channel_select_receive" {
			customReceives[edge.To] = true
		}
	}
	customReturn := false
	for channel := range customChannels {
		customReturn = customReturn || customReceives[channel]
	}
	if !customReturn {
		t.Errorf("custom context implementation's ordinary local channel return was lost: created=%v received=%v", customChannels, customReceives)
	}
	unresolvedCustom := false
	for _, boundary := range report.Boundaries {
		unresolvedCustom = unresolvedCustom || boundary.Kind == "unresolved_context" && boundary.Node == "example.com/contextdoneprobe::CustomContext"
	}
	if !unresolvedCustom {
		t.Error("custom context interface dispatch did not retain its outside standard-context identity boundary")
	}
	unresolvedMixed := false
	for _, boundary := range report.Boundaries {
		unresolvedMixed = unresolvedMixed || boundary.Kind == "unresolved_context" && boundary.Node == "example.com/contextdoneprobe::MixedBound"
	}
	if !unresolvedMixed {
		t.Error("known plus outside bound receiver did not retain an explicit unresolved context boundary")
	}
	unresolvedBoundHelper := false
	for _, boundary := range report.Boundaries {
		unresolvedBoundHelper = unresolvedBoundHelper || boundary.Kind == "unresolved_context" && boundary.Node == "example.com/contextdoneprobe::ConsumeDone"
	}
	if !unresolvedBoundHelper {
		t.Error("outside bound receiver passed through a helper did not retain an explicit context boundary")
	}
	unresolvedExpression := false
	for _, boundary := range report.Boundaries {
		unresolvedExpression = unresolvedExpression || boundary.Kind == "unresolved_context" && boundary.Node == "example.com/contextdoneprobe::UnknownExpression"
	}
	if !unresolvedExpression {
		t.Error("unknown method-expression receiver did not retain an explicit context boundary")
	}
	unionNilBoundary := false
	for _, boundary := range report.Boundaries {
		unionNilBoundary = unionNilBoundary || boundary.Kind == "nil_context_done_wait" && boundary.Node == "example.com/contextdoneprobe::ConstructorCompatibleUnion"
	}
	if !unionNilBoundary {
		t.Error("compatible Background/TODO callback candidates did not retain their shared nil Done behavior")
	}
	for _, constructor := range []struct{ callOwner, siteOwner, snippet string }{
		{callOwner: "ConstructorAliasDone", siteOwner: "ConstructorAliasDone", snippet: "makeContext(base)"},
		{callOwner: "ConstructorHelperDone", siteOwner: "invokeContextFactory", snippet: "factory(parent)"},
	} {
		var constructorContext string
		for _, edge := range report.Relationships {
			if edge.From == "example.com/contextdoneprobe::"+constructor.siteOwner && edge.Kind == "context_create" && strings.Contains(edge.Evidence.Snippet, constructor.snippet) {
				constructorContext = edge.To
			}
		}
		if constructorContext == "" {
			t.Errorf("%s lost exact context constructor identity through its alias/helper", constructor.callOwner)
			continue
		}
		constructorSignal := ""
		for _, edge := range report.Relationships {
			if edge.From == constructorContext && edge.Kind == "context_done_signal" {
				constructorSignal = edge.To
			}
		}
		foundReceive := false
		for _, edge := range report.Relationships {
			if edge.From == "example.com/contextdoneprobe::"+constructor.callOwner && edge.To == constructorSignal && edge.Kind == "channel_receive" {
				foundReceive = true
			}
		}
		if constructorSignal == "" || !foundReceive {
			t.Errorf("%s failed to connect its exact alias/helper constructor to its Done receive; context=%s signal=%s", constructor.callOwner, constructorContext, constructorSignal)
		}
	}
}
