package contracttrace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGOAMD64ChangeRejectsSavedAnalysis(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 feature build tags are only exercised on amd64 hosts")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/featureidentity\n\ngo 1.27.0\n",
		"common.go": "package app\nfunc Seed() {}\n",
		"v1.go":     "//go:build amd64.v1 && !amd64.v2\n\npackage app\nfunc SelectedV1() {}\n",
		"v2.go":     "//go:build amd64.v2\n\npackage app\nfunc SelectedV2() {}\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOEXPERIMENT", "")
	t.Setenv("GOAMD64", "v1")
	options := Options{Root: root, Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}
	v1Report, saved, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !coverageHasFile(v1Report.Coverage.Files, "v1.go") || coverageHasFile(v1Report.Coverage.Files, "v2.go") {
		t.Fatalf("GOAMD64=v1 selected unexpected source files: %v", v1Report.Coverage.Files)
	}
	var persisted bytes.Buffer
	if err := WriteAnalysis(&persisted, saved, true); err != nil {
		t.Fatal(err)
	}
	saved, err = ReadAnalysis(&persisted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err != nil {
		t.Fatalf("same-build saved analysis did not explore: %v", err)
	}
	legacy := saved
	legacy.Coverage.Build = cloneBuildEnvironment(saved.Coverage.Build)
	for _, key := range featureBuildEnvironmentKeys {
		delete(legacy.Coverage.Build, key)
	}
	rebindAnalysisSourceIdentity(t, &legacy)
	var oldSnapshot bytes.Buffer
	if err := WriteAnalysis(&oldSnapshot, legacy, true); err != nil {
		t.Fatal(err)
	}
	legacy, err = ReadAnalysis(&oldSnapshot)
	if err != nil {
		t.Fatalf("read legacy-shaped analysis: %v", err)
	}
	if _, err := Explore(context.Background(), legacy, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err == nil || !strings.Contains(err.Error(), "missing required build feature setting") {
		t.Fatalf("legacy analysis missing feature settings was not rejected for regeneration: %v", err)
	}

	t.Setenv("GOAMD64", "v2")
	v2Report, _, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !coverageHasFile(v2Report.Coverage.Files, "v2.go") || coverageHasFile(v2Report.Coverage.Files, "v1.go") {
		t.Fatalf("GOAMD64=v2 selected unexpected source files: %v", v2Report.Coverage.Files)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err == nil || !strings.Contains(err.Error(), "GOAMD64") {
		t.Fatalf("saved v1 analysis was reused under GOAMD64=v2: %v", err)
	}
}

func TestGOEXPERIMENTChangeRejectsSavedAnalysis(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOEXPERIMENT", "")
	_, saved, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOEXPERIMENT", "none")
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20}); err == nil || !strings.Contains(err.Error(), "GOEXPERIMENT") {
		t.Fatalf("saved analysis was reused after GOEXPERIMENT changed: %v", err)
	}
}

func TestBuildFeatureEnvironmentFieldsAreCapturedAndCompared(t *testing.T) {
	keys := []string{"GO386", "GOAMD64", "GOARM", "GOARM64", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOWASM", "GOEXPERIMENT", "GOFIPS140"}
	current, err := buildEnvironment(context.Background(), "testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if _, ok := current[key]; !ok {
			t.Errorf("build identity omitted recognized Go feature setting %s", key)
			continue
		}
		changed := cloneBuildEnvironment(current)
		changed[key] = current[key] + "-different"
		if err := compareBuildEnvironment(current, changed); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("changed feature setting %s was not identified: %v", key, err)
		}
		missing := cloneBuildEnvironment(current)
		delete(missing, key)
		if err := compareBuildEnvironment(current, missing); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("missing feature setting %s was not rejected: %v", key, err)
		}
	}
}

func cloneBuildEnvironment(build map[string]string) map[string]string {
	copy := make(map[string]string, len(build))
	for key, value := range build {
		copy[key] = value
	}
	return copy
}

func rebindAnalysisSourceIdentity(t *testing.T, analysis *Analysis) {
	t.Helper()
	rootFingerprint, _, err := fingerprint(analysis.Root)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := hashFiles(analysis.Root, analysis.Coverage.EmbeddedFiles)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := json.Marshal(struct {
		Build  map[string]string
		Config Config
		Tests  bool
		Tags   string
	}{analysis.Coverage.Build, analysis.Config, analysis.Coverage.Tests, analysis.Coverage.Tags})
	if err != nil {
		t.Fatal(err)
	}
	analysis.Coverage.SourceSHA256 = hashText(rootFingerprint + string(identity) + assets + analysis.Coverage.LoadedSourceSHA256 + analysis.Coverage.ResolutionSHA256)
}

func coverageHasFile(files []string, name string) bool {
	for _, file := range files {
		if filepath.Base(filepath.FromSlash(file)) == name {
			return true
		}
	}
	return false
}

func TestWorkspaceDependencySwitchRejectsSavedAnalysis(t *testing.T) {
	base := t.TempDir()
	for _, dir := range []string{"app", "first", "second"} {
		if err := os.Mkdir(filepath.Join(base, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"app/go.mod":           "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/dependency v0.0.0\n",
		"app/app.go":           "package app\nimport \"example.com/dependency\"\nfunc Seed() { _ = dependency.Marker() }\n",
		"first/go.mod":         "module example.com/dependency\n\ngo 1.27.0\n",
		"first/dependency.go":  "package dependency\nfunc Marker() int { return 1 }\n",
		"second/go.mod":        "module example.com/dependency\n\ngo 1.27.0\n",
		"second/dependency.go": "package dependency\nfunc Marker() int { return 2 }\n",
		"go.work":              "go 1.27.0\nuse (\n ./app\n ./first\n)\n",
	}
	for file, contents := range files {
		if err := os.WriteFile(filepath.Join(base, filepath.FromSlash(file)), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	work := filepath.Join(base, "go.work")
	t.Setenv("GOWORK", work)
	t.Setenv("GOFLAGS", "")
	options := Options{Root: filepath.Join(base, "app"), Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}
	_, saved, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Coverage.Build["GOWORK"]; got != work {
		t.Fatalf("saved build identity rewrote supplied GOWORK: got %q, want %q", got, work)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err != nil {
		t.Fatalf("stable workspace analysis did not explore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "first", "added.go"), []byte("package dependency\nconst Added = 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err == nil || !strings.Contains(err.Error(), "selected Go source set changed") {
		t.Fatalf("saved workspace analysis accepted newly selected dependency source: %v", err)
	}
	for _, change := range []struct{ key, value string }{{"GOFLAGS", "-tags=contract_probe"}, {"GOWORK", "off"}} {
		t.Run(change.key, func(t *testing.T) {
			t.Setenv(change.key, change.value)
			_, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20})
			if err == nil || !strings.Contains(err.Error(), change.key) {
				t.Fatalf("changed selection setting accepted: %v", err)
			}
		})
	}
	corrupt := saved
	corrupt.Coverage.ResolutionInputs = nil
	if err := corrupt.validate(); err == nil {
		t.Fatal("workspace inventory removed without rejection")
	}
	if err := os.WriteFile(work, []byte("go 1.27.0\nuse (\n ./app\n ./second\n)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("workspace dependency switch accepted: %v", err)
	}
}

func TestPackageLoaderSkipsWorkspacePathThroughWindowsJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows junction path behavior")
	}
	base := t.TempDir()
	app := filepath.Join(base, "app")
	other := filepath.Join(base, "other")
	nested := filepath.Join(other, "nested")
	for _, dir := range []string{app, nested} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	appDependency := filepath.Join(app, "dependency")
	otherDependency := filepath.Join(other, "dependency")
	for _, dir := range []string{appDependency, otherDependency} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	junction := filepath.Join(app, "alias")
	output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, nested).CombinedOutput()
	if err != nil {
		t.Skipf("directory junction unavailable: %v: %s", err, output)
	}
	if !hasPathReparsePoint(junction) {
		t.Fatal("Windows directory junction was not recognized as a reparse point")
	}
	work := filepath.Join(junction, "go.work")
	if err := os.WriteFile(filepath.Join(nested, "go.work"), []byte("go 1.27.0\nuse ../dependency\n"), 0644); err != nil {
		t.Fatal(err)
	}
	useThroughJunction := filepath.Dir(work) + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "dependency"
	useAfterParentResolution := filepath.Join(nested, "..", "dependency")
	selected, err := os.Stat(useThroughJunction)
	if err != nil {
		t.Skipf("OS cannot resolve the workspace use path through the junction: %v", err)
	}
	redirected, err := os.Stat(useAfterParentResolution)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(selected, redirected) {
		t.Skip("this Windows version resolves the relative workspace use identically after parent resolution")
	}
	t.Setenv("GOWORK", work)
	loaderEnv := packageLoaderEnvironment(map[string]string{"GOWORK": work})
	if loaderEnv != nil {
		t.Fatalf("loader environment rewrote GOWORK through a junction parent: %#v", loaderEnv)
	}
}

func TestWorkspaceModuleReplacementRejectsSavedAnalysis(t *testing.T) {
	base := t.TempDir()
	files := map[string]string{
		"app/go.mod":               "module example.com/app\n\ngo 1.27.0\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ../dependency\n",
		"app/app.go":               "package app\nimport \"example.com/dependency\"\nfunc Seed() { _ = dependency.Marker() }\n",
		"dependency/go.mod":        "module example.com/dependency\n\ngo 1.27.0\nrequire example.com/leaf v0.0.0\nreplace example.com/leaf => ../first\n",
		"dependency/dependency.go": "package dependency\nimport \"example.com/leaf\"\nfunc Marker() int { return leaf.Value() }\n",
		"first/go.mod":             "module example.com/leaf\n\ngo 1.27.0\n",
		"first/leaf.go":            "package leaf\nfunc Value() int { return 1 }\n",
		"second/go.mod":            "module example.com/leaf\n\ngo 1.27.0\n",
		"second/leaf.go":           "package leaf\nfunc Value() int { return 2 }\n",
		"go.work":                  "go 1.27.0\nuse (\n ./app\n ./dependency\n)\n",
	}
	for file, contents := range files {
		path := filepath.Join(base, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", filepath.Join(base, "go.work"))
	t.Setenv("GOFLAGS", "")
	options := Options{Root: filepath.Join(base, "app"), Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}
	_, saved, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "dependency/go.mod"), []byte(strings.ReplaceAll(files["dependency/go.mod"], "../first", "../second")), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err == nil {
		t.Fatal("saved investigation accepted changed sibling module replacement")
	}
	_, fresh, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Coverage.ResolutionSHA256 == fresh.Coverage.ResolutionSHA256 {
		t.Fatal("module replacement did not change resolution identity")
	}
}

func TestDependencyLanguageDirectiveRejectsSavedAnalysis(t *testing.T) {
	base := t.TempDir()
	files := map[string]string{
		"app/go.mod":               "module example.com/app\n\ngo 1.27.0\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ../dependency\n",
		"app/app.go":               "package app\nimport \"example.com/dependency\"\nfunc Seed() { _ = dependency.Marker[int](1) }\n",
		"dependency/go.mod":        "module example.com/dependency\n\ngo 1.27.0\n",
		"dependency/dependency.go": "package dependency\nfunc Marker[T any](value T) T { return value }\n",
	}
	for file, contents := range files {
		path := filepath.Join(base, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	options := Options{Root: filepath.Join(base, "app"), Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}
	_, saved, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "dependency/go.mod"), []byte("module example.com/dependency\n\ngo 1.17\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err == nil {
		t.Fatal("saved investigation accepted dependency language version change")
	}
	if _, err := Trace(context.Background(), options); err == nil || !strings.Contains(err.Error(), "go1.18") {
		t.Fatalf("lowered language version did not invalidate generic dependency: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "dependency/go.mod"), []byte("module example.com/dependency\n\ngo 1.25.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, fresh, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Coverage.LoadedSourceSHA256 != fresh.Coverage.LoadedSourceSHA256 || saved.Coverage.ResolutionSHA256 == fresh.Coverage.ResolutionSHA256 {
		t.Fatal("module-only change did not retain Go byte identity and change resolution identity")
	}
}
