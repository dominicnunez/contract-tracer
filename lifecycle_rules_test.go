package contracttrace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredLifecycleKeysAndHandlesSurviveWrappersAndSnapshots(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/lifeclient\n\ngo 1.26.0\nrequire example.com/lifeapi v0.0.0\nreplace example.com/lifeapi => ./api\n",
		"api/go.mod": "module example.com/lifeapi\n\ngo 1.26.0\n",
		"api/api.go": `package lifeapi
type Lease struct{}
func Acquire() *Lease {return &Lease{}}
func Pair() (*Lease,*Lease) {return &Lease{},&Lease{}}
func Drop(*Lease) {}
func (*Lease) Close() {}
func Reserve(string) {}
func ReleaseKey(string) {}
func RecoverKey(string) {}
`,
		"client.go": `package lifeclient
import "example.com/lifeapi"
func Reserve() {lifeapi.Reserve("orders")}
func Cancel() {lifeapi.ReleaseKey("orders")}
func Recover() {lifeapi.RecoverKey("orders")}
func Other() {lifeapi.Reserve("billing")}
func Unknown(key string) {lifeapi.ReleaseKey(key)}
func External(h *lifeapi.Lease) {lifeapi.Drop(h)}
func KnownExternal() {h:=lifeapi.Acquire(); External(h)}
func private(h *lifeapi.Lease) {lifeapi.Drop(h)}
func Closed() {private(&lifeapi.Lease{})}
func StartHandle() {h:=lifeapi.Acquire(); defer lifeapi.Drop(h)}
func Wrap() *lifeapi.Lease {return lifeapi.Acquire()}
func UseWrap() {h:=Wrap(); go lifeapi.Drop(h)}
func Alias() {acquire:=lifeapi.Acquire; h:=acquire(); drop:=lifeapi.Drop; drop(h)}
func Receiver() {h:=lifeapi.Acquire(); h.Close()}
func Bound() {h:=lifeapi.Acquire(); close:=h.Close; close()}
func Tuple() {first,second:=lifeapi.Pair(); lifeapi.Drop(first); _=second}
func Discard() {lifeapi.Pair()}
func DeferredAcquire() {defer lifeapi.Acquire()}
func Channel() {h:=lifeapi.Acquire(); ch:=make(chan *lifeapi.Lease,1); ch<-h; lifeapi.Drop(<-ch)}
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
	zero := 0
	one := 1
	config := DefaultConfig()
	config.LifecycleRules = []LifecycleRule{
		{Symbol: "example.com/lifeapi::Reserve", Role: "acquire", Namespace: "keys", Identity: "value", Argument: &zero},
		{Symbol: "example.com/lifeapi::ReleaseKey", Role: "release", Namespace: "keys", Identity: "value", Argument: &zero},
		{Symbol: "example.com/lifeapi::RecoverKey", Role: "recover", Namespace: "keys", Identity: "value", Argument: &zero},
		{Symbol: "example.com/lifeapi::Acquire", Role: "acquire", Namespace: "handles", Identity: "origin", Result: &zero},
		{Symbol: "example.com/lifeapi::Drop", Role: "release", Namespace: "handles", Identity: "origin", Argument: &zero},
		{Symbol: "example.com/lifeapi::Lease.Close", Role: "release", Namespace: "handles", Identity: "origin", Receiver: true},
		{Symbol: "example.com/lifeapi::Pair", Role: "acquire", Namespace: "handles", Identity: "origin", Result: &zero},
		{Symbol: "example.com/lifeapi::Pair", Role: "acquire", Namespace: "handles", Identity: "origin", Result: &one},
	}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Reserve"}, Depth: 2, MaxNodes: 100, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Reserve"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, name := range []string{"Reserve", "Cancel", "Recover"} {
			if !hasName(report, name) {
				t.Errorf("configured lifecycle scope missed %s", name)
			}
		}
		if hasName(report, "Other") {
			t.Error("distinct resource value merged")
		}
		if report.ContractComplete {
			t.Error("API mapping certified execution")
		}
	}
	for _, pair := range [][2]string{{"StartHandle", "StartHandle"}, {"Wrap", "UseWrap"}, {"Alias", "Alias"}, {"Receiver", "Receiver"}, {"Bound", "Bound"}, {"Channel", "Channel"}} {
		acquired := map[string]bool{}
		for _, edge := range saved.Relationships {
			if edge.From == "example.com/lifeclient::"+pair[0] && edge.Kind == "lifecycle_acquire" {
				acquired[edge.To] = true
			}
		}
		shared := false
		for _, edge := range saved.Relationships {
			if edge.From == "example.com/lifeclient::"+pair[1] && edge.Kind == "lifecycle_release" && acquired[edge.To] && edge.Certainty == "possible" && edge.Evidence.File == "client.go" {
				shared = true
			}
		}
		if !shared {
			t.Errorf("lost acquire/release handle lineage %v", pair)
		}
	}
	unknown := false
	for _, boundary := range saved.Boundaries {
		if boundary.Node == "example.com/lifeclient::Unknown" && boundary.Kind == "unresolved_lifecycle_resource" {
			unknown = true
		}
	}
	if !unknown {
		t.Error("outside key input lacks unresolved lifecycle boundary")
	}
	external := false
	for _, boundary := range saved.Boundaries {
		if boundary.Kind == "unresolved_lifecycle_resource" && boundary.Node == "example.com/lifeclient::External" {
			external = true
		}
		if boundary.Kind == "unresolved_lifecycle_resource" && boundary.Node == "example.com/lifeclient::private" {
			t.Error("closed private handle input invented outside origin")
		}
	}
	if !external {
		t.Error("known caller concealed outside handles accepted by exported entry point")
	}
	for _, owner := range []string{"Discard", "DeferredAcquire"} {
		acquired, discarded := false, false
		for _, edge := range saved.Relationships {
			if edge.From == "example.com/lifeclient::"+owner && edge.Kind == "lifecycle_acquire" {
				acquired = true
			}
		}
		for _, b := range saved.Boundaries {
			if b.Node == "example.com/lifeclient::"+owner && b.Kind == "discarded_lifecycle_result" {
				discarded = true
			}
		}
		if !acquired || !discarded {
			t.Errorf("discarded acquisition lost source role/boundary %s", owner)
		}
	}
	first, second := "", ""
	for _, node := range saved.Nodes {
		if strings.Contains(node.ID, "configured") && strings.Contains(node.Name, "result") && strings.Contains(node.ID, "Tuple") {
			if strings.HasSuffix(node.Name, "result 0") {
				first = node.ID
			}
			if strings.HasSuffix(node.Name, "result 1") {
				second = node.ID
			}
		}
	}
	firstReleased := false
	for _, edge := range saved.Relationships {
		if edge.From == "example.com/lifeclient::Tuple" && edge.Kind == "lifecycle_release" {
			firstReleased = firstReleased || edge.To == first
			if second != "" && edge.To == second {
				t.Error("tuple result identities merged")
			}
		}
	}
	if first == "" || second == "" || !firstReleased {
		t.Error("tuple acquisition/result-slot oracle missing")
	}
}

func TestLifecycleRuleConfigurationRejectsAmbiguousSelectors(t *testing.T) {
	zero, negative := 0, -1
	base := LifecycleRule{Symbol: "example.com/api::Acquire", Role: "acquire", Namespace: "leases", Identity: "origin", Result: &zero}
	for _, change := range []func(*LifecycleRule){
		func(r *LifecycleRule) { r.Argument = &zero }, func(r *LifecycleRule) { r.Receiver = true },
		func(r *LifecycleRule) { r.Result = nil }, func(r *LifecycleRule) { r.Result = &negative },
		func(r *LifecycleRule) { r.Namespace = "" }, func(r *LifecycleRule) { r.Identity = "guess" },
		func(r *LifecycleRule) { r.Role = "proven" }, func(r *LifecycleRule) { r.Symbol = "::Acquire" },
	} {
		rule := base
		change(&rule)
		config := DefaultConfig()
		config.LifecycleRules = []LifecycleRule{rule}
		if err := validateConfig(config); err == nil {
			t.Errorf("invalid lifecycle rule accepted: %+v", rule)
		}
	}
}

func TestBoundLifecycleArgumentSelectorsPreserveDispatchAndOutsideInputs(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/boundclient\n\ngo 1.26.0\nrequire example.com/boundapi v0.0.0\nreplace example.com/boundapi => ./api\n",
		"api/go.mod": "module example.com/boundapi\n\ngo 1.26.0\n",
		"api/api.go": `package boundapi
type Manager struct{}
func (*Manager) Reserve(string) {}
func (*Manager) Release(string) {}
`,
		"client.go": `package boundclient
import "example.com/boundapi"
var manager boundapi.Manager
func BoundArgument() {
    manager.Reserve("orders")
    release := manager.Release
    alias := release
    invoke(alias, "orders")
}
func invoke(fn func(string), key string) { fn(key) }
func BoundPublic(key string) {
    release := manager.Release
    release(key)
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
	zero := 0
	config := DefaultConfig()
	config.LifecycleRules = []LifecycleRule{
		{Symbol: "example.com/boundapi::Manager.Reserve", Role: "acquire", Namespace: "keys", Identity: "value", Argument: &zero},
		{Symbol: "example.com/boundapi::Manager.Release", Role: "release", Namespace: "keys", Identity: "value", Argument: &zero},
	}
	report, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"BoundArgument"}, Depth: 6, MaxNodes: 120, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	resource := ""
	for _, edge := range analysis.Relationships {
		if edge.From == "example.com/boundclient::BoundArgument" && edge.Kind == "lifecycle_acquire" && edge.Certainty == "possible" && edge.Evidence.File == "client.go" {
			resource = edge.To
		}
	}
	if resource == "" {
		t.Fatal("direct key acquisition candidate missing from bound argument fixture")
	}
	joined := false
	for _, edge := range analysis.Relationships {
		if edge.From == "example.com/boundclient::invoke" && edge.To == resource && edge.Kind == "lifecycle_release" && edge.Certainty == "possible" && edge.Evidence.File == "client.go" {
			joined = true
		}
	}
	if !joined {
		t.Fatalf("bound Release argument through alias/helper did not join direct acquisition candidate %q", resource)
	}
	for _, boundary := range report.Boundaries {
		if boundary.Node == "example.com/boundclient::invoke" && boundary.Kind == "unresolved_lifecycle_dispatch" {
			t.Fatalf("recognized configured bound argument selector remains explicitly unsupported at %s:%d: %s", boundary.Evidence.File, boundary.Evidence.Line, boundary.Reason)
		}
	}
	public, _, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"BoundPublic"}, Depth: 6, MaxNodes: 120, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	outside := false
	for _, boundary := range public.Boundaries {
		outside = outside || boundary.Node == "example.com/boundclient::BoundPublic" && boundary.Kind == "unresolved_lifecycle_resource" && boundary.Evidence.File == "client.go"
	}
	if !outside {
		t.Fatal("public outside bound key input lacks unresolved lifecycle-resource boundary")
	}
}
