package contracttrace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type identityMutation struct {
	name      string
	path      string
	contents  string
	wantError string
}

func TestTraceRejectsInputsChangedByFinalGoEnv(t *testing.T) {
	mutator, realGo := buildIdentityMutator(t)
	for _, mutation := range identityMutations() {
		t.Run(mutation.name, func(t *testing.T) {
			fixture := newIdentityFixture(t)
			marker, countFile := configureIdentityMutator(t, mutator, realGo, fixture.root, mutation, 2)
			report, analysis, err := TraceWithAnalysis(context.Background(), fixture.options())
			assertIdentityMutationRan(t, marker, countFile, 2)
			if err == nil {
				t.Fatalf("Trace returned a report after final go env changed %s: report schema=%q nodes=%d analysis schema=%q", mutation.name, report.Schema, len(report.Nodes), analysis.Schema)
			}
			if !strings.Contains(err.Error(), mutation.wantError) {
				t.Fatalf("Trace rejected changed %s for the wrong reason: %v", mutation.name, err)
			}
			if report.Schema != "" || len(report.Nodes) != 0 || analysis.Schema != "" {
				t.Fatalf("Trace returned partial graph after input identity failure: report schema=%q nodes=%d analysis schema=%q", report.Schema, len(report.Nodes), analysis.Schema)
			}
		})
	}
}

func TestExploreRejectsInputsChangedByFinalGoEnv(t *testing.T) {
	mutator, realGo := buildIdentityMutator(t)
	for _, mutation := range identityMutations() {
		t.Run(mutation.name, func(t *testing.T) {
			fixture := newIdentityFixture(t)
			_, analysis, err := TraceWithAnalysis(context.Background(), fixture.options())
			if err != nil {
				t.Fatalf("create saved analysis: %v", err)
			}
			var compressed bytes.Buffer
			if err := WriteAnalysis(&compressed, analysis, true); err != nil {
				t.Fatalf("write saved analysis: %v", err)
			}
			saved, err := ReadAnalysis(&compressed)
			if err != nil {
				t.Fatalf("read saved analysis: %v", err)
			}

			call := 1
			if mutation.name == "added dependency Go source" || mutation.name == "added embedded asset match" {
				call = 2
			}
			marker, countFile := configureIdentityMutator(t, mutator, realGo, fixture.root, mutation, call)
			report, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 2, MaxNodes: 50})
			assertIdentityMutationRan(t, marker, countFile, call)
			if err == nil {
				t.Fatalf("Explore returned a stale graph after final go env changed %s", mutation.name)
			}
			if !strings.Contains(err.Error(), mutation.wantError) {
				t.Fatalf("Explore rejected changed %s for the wrong reason: %v", mutation.name, err)
			}
			if report.Schema != "" || len(report.Nodes) != 0 {
				t.Fatalf("Explore returned partial graph after input identity failure: %+v", report)
			}
		})
	}
}

func identityMutations() []identityMutation {
	return []identityMutation{
		{name: "root Go source", path: "ignored.go", contents: "//go:build identity_mutator_off\n\npackage app\n\nconst ignored = 2\n", wantError: "source changed during analysis"},
		{name: "root SQL source", path: "schema.sql", contents: "\n-- changed after identity check\n", wantError: "source changed during analysis"},
		{name: "root go.mod", path: "go.mod", contents: "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ../dependency\n\n// changed after identity check\n", wantError: "resolution input check: loaded source changed"},
		{name: "root go.sum", path: "go.sum", contents: "example.com/unused v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n", wantError: "source changed during analysis"},
		{name: "selected embedded asset", path: "asset.txt", contents: "changed asset", wantError: "embedded assets changed during analysis"},
		{name: "added embedded asset match", path: filepath.Join("assets", "late.txt"), contents: "late", wantError: "embedded asset set changed"},
		{name: "loaded dependency Go source", path: filepath.Join("..", "dependency", "dependency.go"), contents: "package dependency\nfunc Marker() int { return 2 }\n", wantError: "loaded source changed"},
		{name: "added dependency Go source", path: filepath.Join("..", "dependency", "added.go"), contents: "package dependency\nfunc Added() int { return 2 }\n", wantError: "selected Go source set changed"},
		{name: "dependency resolution manifest", path: filepath.Join("..", "dependency", "go.mod"), contents: "module example.com/dependency\n\ngo 1.27.0\n\n// changed after identity check\n", wantError: "resolution input check: loaded source changed"},
	}
}

type identityFixture struct {
	root string
}

func newIdentityFixture(t *testing.T) identityFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "app")
	dependency := filepath.Join(base, "dependency")
	for _, directory := range []string{root, dependency} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "go.mod"):              "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ../dependency\n",
		filepath.Join(root, "go.sum"):              "",
		filepath.Join(root, "app.go"):              "package app\n\nimport (\n\t\"embed\"\n\t\"example.com/dependency\"\n)\n\n//go:embed asset.txt\nvar asset []byte\n\n//go:embed assets/*.txt\nvar allAssets embed.FS\n\nfunc Seed() { _ = dependency.Marker(); _ = asset; _ = allAssets }\n",
		filepath.Join(root, "ignored.go"):          "//go:build identity_mutator_off\n\npackage app\n\nconst ignored = 1\n",
		filepath.Join(root, "asset.txt"):           "embedded asset",
		filepath.Join(root, "schema.sql"):          "CREATE TABLE records (id INTEGER PRIMARY KEY);\n",
		filepath.Join(dependency, "go.mod"):        "module example.com/dependency\n\ngo 1.27.0\n",
		filepath.Join(dependency, "dependency.go"): "package dependency\nfunc Marker() int { return 1 }\n",
	}
	for path, contents := range files {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "initial.txt"), []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	return identityFixture{root: root}
}

func (f identityFixture) options() Options {
	return Options{Root: f.root, Seeds: []string{"Seed", "schema.sql"}, Depth: 2, MaxNodes: 50}
}

func buildIdentityMutator(t *testing.T) (string, string) {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("find real Go command: %v", err)
	}
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	root := filepath.Dir(testFile)
	outputDir := t.TempDir()
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	mutator := filepath.Join(outputDir, name)
	command := exec.Command(realGo, "build", "-buildvcs=false", "-o", mutator, "./testdata/go-env-mutator")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build local go env mutator: %v\n%s", err, output)
	}
	return mutator, realGo
}

func configureIdentityMutator(t *testing.T, mutator, realGo, root string, mutation identityMutation, call int) (string, string) {
	t.Helper()
	binDir := filepath.Dir(mutator)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CONTRACT_TRACE_REAL_GO", realGo)
	countFile := filepath.Join(t.TempDir(), "env-count")
	marker := filepath.Join(t.TempDir(), "mutated")
	t.Setenv("CONTRACT_TRACE_ENV_COUNT", countFile)
	t.Setenv("CONTRACT_TRACE_MUTATE_ON", strconv.Itoa(call))
	t.Setenv("CONTRACT_TRACE_MUTATE_PATH", filepath.Join(root, mutation.path))
	t.Setenv("CONTRACT_TRACE_MUTATE_CONTENT", mutation.contents)
	t.Setenv("CONTRACT_TRACE_MUTATION_MARKER", marker)
	return marker, countFile
}

func assertIdentityMutationRan(t *testing.T, marker, countFile string, expectedCall int) {
	t.Helper()
	markerContents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("controlled go env did not mutate on full environment call %d: %v", expectedCall, err)
	}
	contents, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read controlled go env call count: %v", err)
	}
	count, err := strconv.Atoi(string(contents))
	if err != nil || count < expectedCall {
		t.Fatalf("controlled go env call count %q, want at least %d", contents, expectedCall)
	}
	mutationCall, err := strconv.Atoi(string(markerContents))
	if err != nil || mutationCall != expectedCall {
		t.Fatalf("controlled mutation happened on go env call %q, want call %d", markerContents, expectedCall)
	}
}
