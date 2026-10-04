package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleBoundSelectorsUseDeclaredMethodSignatures(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/boundvalidation\n\ngo 1.26.0\nrequire example.com/leases v0.0.0\nreplace example.com/leases => ./api\n",
		"api/go.mod": "module example.com/leases\n\ngo 1.26.0\n",
		"api/api.go": `package leases
type Lease struct{}
func (*Lease) Close() {}
func (*Lease) CloseArg(string) {}
func (*Lease) Reserve(string) {}
func (*Lease) Acquire() *Lease { return &Lease{} }
func Acquire() *Lease { return &Lease{} }
func Drop(*Lease) {}
type Handle struct{}
type TypedLease[T any] struct{}
func (*TypedLease[T]) Close() {}
func (*TypedLease[T]) Set(T) {}
func (*TypedLease[T]) Take() T { var zero T; return zero }
func Reserve[T ~string](T) {}
func AcquireValue[T any]() T { var zero T; return zero }
type ResourceKey string
func (ResourceKey) ReleaseKey() {}
type Closer interface { Close() }
type TextLease int
func (TextLease) CloseText() string { return "" }
`,
		"client.go": `package boundvalidation
import "example.com/leases"

func Direct() {
    lease := &leases.Lease{}
    closeLease := lease.Close
    alias := closeLease
    invoke(alias)
}

func InterfaceBound(lease leases.Closer) {
    closeLease := lease.Close
    alias := closeLease
    invoke(alias)
}

func InterfaceMixed(external leases.Closer) {
    known := &leases.Lease{}
    invoke(known.Close)
    invoke(external.Close)
}

func invoke(fn func()) { fn() }

func InvalidScalarReceiver(lease *leases.TextLease) {
    closeText := lease.CloseText
    closeText()
}
func InvalidScalarDirect(lease leases.TextLease) { lease.CloseText() }

func InvalidArgumentIndex(lease *leases.Lease) {
    closeArg := lease.CloseArg
    invokeArg(closeArg, "resource")
}
func InvalidArgumentIndexDirect(lease *leases.Lease) { lease.CloseArg("resource") }
func MaxIntDirect(lease *leases.Lease) { lease.CloseArg("resource") }
func MaxIntExpression(lease *leases.Lease) { (*leases.Lease).CloseArg(lease, "resource") }
func invokeArg(fn func(string), key string) { fn(key) }
func invokeExpression(fn func(*leases.Lease), receiver *leases.Lease) { fn(receiver) }

func MethodExpressionReceiver() { (*leases.Lease).Close(&leases.Lease{}) }
func MethodExpressionHelper() {
    expression := (*leases.Lease).Close
    alias := expression
    receiver := &leases.Lease{}
    invokeExpression(alias, receiver)
}
func MethodExpressionArgument() { (*leases.Lease).Reserve(&leases.Lease{}, "orders") }
func MethodExpressionResult() {
    lease := (*leases.Lease).Acquire(&leases.Lease{})
    leases.Drop(lease)
}
func DirectResult() {
    lease := (&leases.Lease{}).Acquire()
    leases.Drop(lease)
}
type promotedLease struct { *leases.Lease }
type deepPromotedLease struct { *promotedLease }
type keyEnvelope struct { leases.ResourceKey }
type genericStringEnvelope struct { *leases.TypedLease[string] }
type genericHandleEnvelope struct { *leases.TypedLease[*leases.Handle] }
func PromotedBound() {
    receiver := &promotedLease{Lease: &leases.Lease{}}
    closeLease := receiver.Close
    invoke(closeLease)
}
func PromotedExpression() {
    receiver := &promotedLease{Lease: &leases.Lease{}}
    (*promotedLease).Close(receiver)
}
func PromotedExpressionAcquired() {
    lease := leases.Acquire()
    wrapped := &promotedLease{Lease: lease}
    (*promotedLease).Close(wrapped)
}
func PromotedExpressionHelperAcquired() {
    lease := leases.Acquire()
    wrapped := &promotedLease{Lease: lease}
    expression := (*promotedLease).Close
    alias := expression
    invokePromotedExpression(alias, wrapped)
}
func invokePromotedExpression(fn func(*promotedLease), wrapped *promotedLease) { fn(wrapped) }
func PromotedDeepExpressionAcquired() {
    lease := leases.Acquire()
    mid := &promotedLease{Lease: lease}
    wrapped := &deepPromotedLease{promotedLease: mid}
    (*deepPromotedLease).Close(wrapped)
}
func PromotedStringReceiver() {
    wrapped := &keyEnvelope{ResourceKey: "tenant-key"}
    (*keyEnvelope).ReleaseKey(wrapped)
}
func PromotedGenericStringArgument() {
    wrapped := &genericStringEnvelope{TypedLease: &leases.TypedLease[string]{}}
    (*genericStringEnvelope).Set(wrapped, "promoted-generic-key")
}
func PromotedGenericHandleArgument() {
    wrapped := &genericHandleEnvelope{TypedLease: &leases.TypedLease[*leases.Handle]{}}
    (*genericHandleEnvelope).Set(wrapped, &leases.Handle{})
}

func GenericStringLease() {
    lease := &leases.TypedLease[string]{}
    closeLease := lease.Close
    closeLease()
    set := lease.Set
    set("generic-key")
}
func GenericHandleLease() {
    lease := &leases.TypedLease[*leases.Handle]{}
    closeLease := lease.Close
    closeLease()
    set := lease.Set
    set(&leases.Handle{})
    take := lease.Take
    handle := take()
    _ = handle
}
func GenericStringResult() {
    lease := &leases.TypedLease[string]{}
    take := lease.Take
    text := take()
    _ = text
}
func GenericFunctions() {
    leases.Reserve[string]("generic-function-key")
    _ = leases.AcquireValue[*leases.Handle]()
    _ = leases.AcquireValue[string]()
}

func DeferredAndGoroutine() {
    first := &leases.Lease{}
    second := &leases.Lease{}
    closeFirst := first.Close
    closeSecond := second.Close
    defer closeFirst()
    go closeSecond()
}
`,
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	one := 1
	zero := 0
	config := DefaultConfig()
	config.LifecycleRules = []LifecycleRule{
		{Symbol: "example.com/leases::Lease.Close", Role: "release", Namespace: "handles", Identity: "origin", Receiver: true},
		{Symbol: "example.com/leases::Closer.Close", Role: "release", Namespace: "interfaces", Identity: "origin", Receiver: true},
		{Symbol: "example.com/leases::TextLease.CloseText", Role: "release", Namespace: "invalid", Identity: "origin", Receiver: true},
		{Symbol: "example.com/leases::Lease.CloseArg", Role: "release", Namespace: "invalid", Identity: "value", Argument: &one},
		{Symbol: "example.com/leases::Lease.Reserve", Role: "acquire", Namespace: "keys", Identity: "value", Argument: &zero},
		{Symbol: "example.com/leases::Lease.Acquire", Role: "acquire", Namespace: "results", Identity: "origin", Result: &zero},
		{Symbol: "example.com/leases::Drop", Role: "release", Namespace: "results", Identity: "origin", Argument: &zero},
		{Symbol: "example.com/leases::TypedLease.Close", Role: "release", Namespace: "generic-handles", Identity: "origin", Receiver: true},
		{Symbol: "example.com/leases::TypedLease.Set", Role: "acquire", Namespace: "generic-keys", Identity: "value", Argument: &zero},
		{Symbol: "example.com/leases::TypedLease.Take", Role: "acquire", Namespace: "generic-results", Identity: "origin", Result: &zero},
		{Symbol: "example.com/leases::Reserve", Role: "acquire", Namespace: "generic-function-keys", Identity: "value", Argument: &zero},
		{Symbol: "example.com/leases::AcquireValue", Role: "acquire", Namespace: "generic-function-results", Identity: "origin", Result: &zero},
		{Symbol: "example.com/leases::Acquire", Role: "acquire", Namespace: "handles", Identity: "origin", Result: &zero},
		{Symbol: "example.com/leases::ResourceKey.ReleaseKey", Role: "release", Namespace: "keys", Identity: "value", Receiver: true},
	}
	report, analysis, err := TraceWithAnalysis(context.Background(), Options{
		Root: root,
		Seeds: []string{"Direct", "InterfaceBound", "InterfaceMixed", "InvalidScalarReceiver", "InvalidScalarDirect",
			"InvalidArgumentIndex", "InvalidArgumentIndexDirect", "MethodExpressionReceiver", "MethodExpressionArgument",
			"MethodExpressionResult", "MethodExpressionHelper", "DirectResult", "PromotedBound", "PromotedExpression",
			"PromotedExpressionAcquired", "PromotedExpressionHelperAcquired",
			"PromotedDeepExpressionAcquired", "PromotedStringReceiver", "PromotedGenericStringArgument", "PromotedGenericHandleArgument",
			"GenericStringLease", "GenericHandleLease", "GenericStringResult", "GenericFunctions", "DeferredAndGoroutine"},
		Depth: 6, MaxNodes: 200, Config: config,
	})
	if err != nil {
		t.Fatal(err)
	}
	role := func(owner, namespace, kind, snippet string) []Relationship {
		var found []Relationship
		for _, edge := range analysis.Relationships {
			if edge.From == "example.com/boundvalidation::"+owner && edge.Kind == "lifecycle_"+kind &&
				strings.HasPrefix(edge.To, "lifecycle:"+namespace+":") && (snippet == "" || strings.Contains(edge.Evidence.Snippet, snippet)) {
				found = append(found, edge)
			}
		}
		return found
	}
	invalid := func(owner, snippet, reason string) bool {
		for _, boundary := range report.Boundaries {
			if boundary.Node == "example.com/boundvalidation::"+owner && boundary.Kind == "invalid_lifecycle_rule_site" &&
				strings.Contains(boundary.Evidence.Snippet, snippet) && strings.Contains(boundary.Reason, reason) {
				return true
			}
		}
		return false
	}

	boundDirect := role("invoke", "handles", "release", "fn()")
	if len(boundDirect) == 0 || !strings.Contains(boundDirect[0].To, "boundvalidation.Direct") {
		t.Fatalf("bound pointer receiver did not preserve its captured allocation: %+v", boundDirect)
	}
	for _, boundary := range report.Boundaries {
		if boundary.Node == "example.com/boundvalidation::invoke" && boundary.Kind == "invalid_lifecycle_rule_site" &&
			(boundary.Evidence.Snippet == "func invoke(fn func()) { fn() }") &&
			(strings.Contains(boundary.Reason, "Lease.Close") || strings.Contains(boundary.Reason, "Closer.Close")) {
			t.Fatalf("valid captured method value produced a false invalid-site warning: %+v", boundary)
		}
	}
	if interfaces := role("invoke", "interfaces", "release", "fn()"); len(interfaces) == 0 {
		t.Fatal("declared interface method rule did not produce a bound receiver candidate")
	}
	if !invalid("InvalidScalarDirect", "lease.CloseText()", "scalar identity is unsupported") {
		t.Fatal("direct scalar receiver control lost its specific invalid-site boundary")
	}
	if !invalid("InvalidArgumentIndexDirect", "lease.CloseArg(\"resource\")", "outside the API call") {
		t.Fatal("direct out-of-range argument selector lost its invalid-site boundary")
	}
	if !invalid("InvalidScalarReceiver", "closeText()", "scalar identity is unsupported") {
		t.Fatal("bound scalar receiver did not retain the declaration's scalar-type validation")
	}
	if invalid("InvalidScalarReceiver", "closeText()", "function without a receiver") {
		t.Fatal("bound scalar control was misreported as missing a declared receiver")
	}
	if !invalid("invokeArg", "fn(key)", "outside the API signature") {
		t.Fatal("bound out-of-range argument control lost its declaration-signature warning")
	}

	if edges := role("MethodExpressionReceiver", "handles", "release", "(*leases.Lease).Close"); len(edges) == 0 {
		t.Fatal("method-expression receiver selector did not select its explicit receiver argument")
	}
	if edges := role("invokeExpression", "handles", "release", "fn(receiver)"); len(edges) == 0 || !strings.Contains(edges[0].To, "MethodExpressionHelper") {
		t.Fatalf("method-expression alias/helper did not preserve its explicit receiver argument: %+v", edges)
	}
	promotedBound := false
	for _, edge := range role("invoke", "handles", "release", "fn()") {
		promotedBound = promotedBound || strings.Contains(edge.To, "PromotedBound")
	}
	if !promotedBound {
		t.Fatal("promoted bound method did not preserve receiver identity")
	}
	if edges := role("PromotedExpression", "handles", "release", "(*promotedLease).Close"); len(edges) == 0 {
		t.Fatal("promoted method expression did not preserve its explicit receiver")
	}
	assertAcquiredLeaseRelease := func(acquireOwner, releaseOwner, snippet string) {
		t.Helper()
		var acquisition string
		for _, edge := range analysis.Relationships {
			if edge.From == "example.com/boundvalidation::"+acquireOwner && edge.Kind == "lifecycle_acquire" &&
				strings.HasPrefix(edge.To, "lifecycle:handles:configured%3Aexample.com%2Fleases%3A%3AAcquire%3A") {
				acquisition = edge.To
				break
			}
		}
		if acquisition == "" {
			t.Fatalf("%s did not produce configured Lease.Acquire origin", acquireOwner)
		}
		for _, edge := range analysis.Relationships {
			if edge.From == "example.com/boundvalidation::"+releaseOwner && edge.Kind == "lifecycle_release" &&
				strings.Contains(edge.Evidence.Snippet, snippet) && edge.To == acquisition {
				return
			}
		}
		t.Fatalf("%s release at %q did not retain the acquired embedded Lease origin from %s: %q", releaseOwner, snippet, acquireOwner, acquisition)
	}
	assertAcquiredLeaseRelease("PromotedExpressionAcquired", "PromotedExpressionAcquired", "(*promotedLease).Close(wrapped)")
	assertAcquiredLeaseRelease("PromotedExpressionHelperAcquired", "invokePromotedExpression", "fn(wrapped)")
	assertAcquiredLeaseRelease("PromotedDeepExpressionAcquired", "PromotedDeepExpressionAcquired", "(*deepPromotedLease).Close(wrapped)")
	if edges := role("PromotedStringReceiver", "keys", "release", "(*keyEnvelope).ReleaseKey(wrapped)"); len(edges) == 0 || edges[0].To != "lifecycle:keys:tenant-key" {
		t.Fatalf("promoted named-string receiver did not retain its field string identity: %+v", edges)
	}
	if edges := role("PromotedGenericStringArgument", "generic-keys", "acquire", "promoted-generic-key"); len(edges) == 0 || edges[0].To != "lifecycle:generic-keys:promoted-generic-key" {
		t.Fatalf("deep promoted generic selector lost instantiated string argument type: %+v", edges)
	}
	if !invalid("PromotedGenericHandleArgument", "&leases.Handle{}", "string resource key") {
		t.Fatal("deep promoted generic T=*Handle argument incorrectly passed string selector validation")
	}
	if edges := role("MethodExpressionArgument", "keys", "acquire", "(*leases.Lease).Reserve"); len(edges) == 0 || edges[0].To != "lifecycle:keys:orders" {
		t.Fatalf("method-expression argument selector did not skip the explicit receiver: %+v", edges)
	}
	acquired := role("MethodExpressionResult", "results", "acquire", "Lease).Acquire")
	if len(acquired) == 0 {
		t.Fatal("method-expression result selector did not retain the declared result slot")
	}
	joined := false
	for _, release := range role("MethodExpressionResult", "results", "release", "leases.Drop") {
		for _, acquire := range acquired {
			joined = joined || release.To == acquire.To
		}
	}
	if !joined {
		t.Fatalf("method-expression result candidate did not join Drop: acquire=%+v", acquired)
	}
	if edges := role("GenericStringLease", "generic-keys", "acquire", "set(\"generic-key\")"); len(edges) == 0 || edges[0].To != "lifecycle:generic-keys:generic-key" {
		t.Fatalf("generic T=string argument selector lost its concrete type/value: %+v", edges)
	}
	if edges := role("GenericHandleLease", "generic-handles", "release", "closeLease()"); len(edges) == 0 {
		t.Fatal("generic pointer receiver did not retain lifecycle identity")
	}
	if !invalid("GenericHandleLease", "set(&leases.Handle{})", "string resource key") {
		t.Fatal("generic T=*Handle argument incorrectly passed string selector validation")
	}
	genericHandleResult := role("GenericHandleLease", "generic-results", "acquire", "take()")
	if len(genericHandleResult) == 0 {
		t.Fatal("generic T=*Handle result incorrectly failed origin validation")
	}
	if !invalid("GenericStringResult", "take()", "scalar identity is unsupported") {
		t.Fatal("generic T=string result incorrectly passed origin validation")
	}
	if edges := role("GenericFunctions", "generic-function-keys", "acquire", `Reserve[string]("generic-function-key")`); len(edges) == 0 || edges[0].To != "lifecycle:generic-function-keys:generic-function-key" {
		t.Fatalf("generic function T=string argument lost its instantiated type: %+v", edges)
	}
	if edges := role("GenericFunctions", "generic-function-results", "acquire", "AcquireValue[*leases.Handle]()"); len(edges) == 0 {
		t.Fatal("generic function T=*Handle result failed origin validation")
	}
	if !invalid("GenericFunctions", "AcquireValue[string]()", "scalar identity is unsupported") {
		t.Fatal("generic function T=string result incorrectly passed origin validation")
	}

	deferred := role("DeferredAndGoroutine", "handles", "release", "defer closeFirst()")
	concurrent := role("DeferredAndGoroutine", "handles", "release", "go closeSecond()")
	if len(deferred) == 0 || len(concurrent) == 0 || deferred[0].To == concurrent[0].To {
		t.Fatalf("bound defer/go calls did not retain distinct captured receiver candidates: defer=%+v go=%+v", deferred, concurrent)
	}
	if report.ContractComplete {
		t.Fatal("lifecycle candidates must not claim contract completeness")
	}
	maxInt := int(^uint(0) >> 1)
	maxConfig := DefaultConfig()
	maxConfig.LifecycleRules = []LifecycleRule{{Symbol: "example.com/leases::Lease.CloseArg", Role: "release",
		Namespace: "max-index", Identity: "value", Argument: &maxInt}}
	maxReport, _, err := TraceWithAnalysis(context.Background(), Options{
		Root: root, Seeds: []string{"MaxIntDirect", "MaxIntExpression"}, Depth: 3, MaxNodes: 100, Config: maxConfig,
	})
	if err != nil {
		t.Fatalf("MaxInt argument selectors panicked or failed analysis: %v", err)
	}
	maxWarnings := 0
	for _, boundary := range maxReport.Boundaries {
		if boundary.Kind == "invalid_lifecycle_rule_site" && strings.Contains(boundary.Reason, "configured argument is outside the API call") {
			maxWarnings++
		}
	}
	if maxWarnings != 2 {
		t.Fatalf("expected safe source-site warnings for direct and method-expression MaxInt selectors, got %d: %+v", maxWarnings, maxReport.Boundaries)
	}
}
