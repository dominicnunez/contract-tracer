package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallbackAndInterfaceScope(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Invariant: "all accepted values are validated", Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Validate", "Live", "Recovery", "Store.Read", "Entry"} {
		if !hasName(r, name) {
			t.Errorf("missing independently expected path %s", name)
		}
	}
	if hasName(r, "unrelated") {
		t.Error("disconnected function included")
	}
	if hasName(r, "UnusedCheck") {
		t.Error("same-signature callback candidate should remain an unresolved boundary by default")
	}
	possible := false
	for _, b := range r.Boundaries {
		if b.Kind == "callback_candidates" {
			possible = true
		}
	}
	if !possible {
		t.Error("nonexpanded callback candidates must be disclosed")
	}
	found := false
	for _, e := range r.Relationships {
		if e.Kind == "function_reference" && e.Evidence.Snippet == "func Recovery() bool { return apply(Validate) }" {
			found = true
		}
	}
	if !found {
		t.Error("callback use must be retained as source-backed reference")
	}
	if r.ContractComplete {
		t.Error("structural discovery cannot certify the invariant")
	}
}

func TestFingerprintIncludesOrdinaryPackageDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reports")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "source.go")
	if err := os.WriteFile(file, []byte("package reports\nconst Value=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _, err := fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package reports\nconst Value=2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, _, err := fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Error("ordinary Go package named reports was omitted from source identity")
	}
}

func TestInvalidTargetAndCanceledAnalysis(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/broken\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package broken\nfunc Seed() { missingSymbol() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Trace(context.Background(), Options{Root: dir, Seeds: []string{"Seed"}, Depth: 2, MaxNodes: 10})
	if err == nil || !strings.Contains(err.Error(), "package loading incomplete") || len(r.Nodes) != 0 {
		t.Error("unloadable code must fail instead of emitting apparently successful scope")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Trace(ctx, Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 2, MaxNodes: 10})
	if err == nil {
		t.Error("canceled analysis was allowed")
	}
}

func TestConfigurationAndScopeLimits(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"CustomPublish"}, Depth: 2, MaxNodes: 100, Config: Config{EventFields: []string{"Topic"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "CustomConsume") {
		t.Error("configured field convention did not connect consumer")
	}
	r, err = Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Nodes) > 2 || !r.Coverage.Truncated {
		t.Error("budget must be enforced and reported")
	}
	found := false
	for _, b := range r.Boundaries {
		if b.Kind == "scope_frontier" {
			found = true
		}
	}
	if !found {
		t.Error("scope omission lacks an explicit boundary")
	}
}

func TestBuildCoverageAndUnknownSeed(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	excluded := false
	for _, f := range r.Coverage.ExcludedGoFiles {
		if f == "tagged.go" {
			excluded = true
		}
	}
	if !excluded || hasName(r, "Tagged") {
		t.Error("unselected build-tag path was not correctly reported")
	}
	r, err = Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100, Tags: "traceextra"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "Tagged") {
		t.Error("selected build-tag caller missing")
	}
	_, err = Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"DoesNotExist"}, Depth: 3, MaxNodes: 100})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Error("unknown seed must fail explicitly")
	}
}

func TestStorageSiblingsAndDynamicBoundary(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Write", "Dynamic"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Read", "Rebuild"} {
		if !hasName(r, name) {
			t.Errorf("missing state sibling %s", name)
		}
	}
	if hasName(r, "Other") {
		t.Error("unrelated table was joined")
	}
	found := false
	for _, b := range r.Boundaries {
		if b.Kind == "dynamic_sql" {
			found = true
		}
	}
	if !found {
		t.Error("dynamic SQL was silently dropped")
	}
}

func TestEventProducerConsumer(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Publish"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "Consume") {
		t.Error("consumer of the same constant event missing")
	}
	if hasName(r, "Another") {
		t.Error("different event joined")
	}
}

func hasName(r Report, name string) bool {
	for _, n := range r.Nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}
