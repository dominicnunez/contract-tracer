package contracttrace

import (
	"bytes"
	"context"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadedSourceCaptureRejectsMutationAndHonorsCancellation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "dependency.go")
	contents := []byte("package dependency\nconst Marker = 1\n")
	if err := os.WriteFile(file, contents, 0644); err != nil {
		t.Fatal(err)
	}
	capture := &loadedSourceCapture{}
	if _, err := capture.parseFile(token.NewFileSet(), file, nil); err != nil {
		t.Fatal(err)
	}
	sources := capture.sources()
	if err := verifyLoadedSources(context.Background(), sources); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyLoadedSources(ctx, sources); err != context.Canceled {
		t.Fatalf("canceled verification: %v", err)
	}
	changed := []byte("package dependency\nconst Marker = 2\n")
	if err := os.WriteFile(file, changed, 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyLoadedSources(context.Background(), sources); err == nil || !strings.Contains(err.Error(), "loaded source changed") {
		t.Fatalf("mutation during analysis accepted: %v", err)
	}
	if _, err := capture.parseFile(token.NewFileSet(), file, changed); err == nil {
		t.Fatal("two versions of one loaded file accepted")
	}
}

func TestSavedLoadedSourceInventoryRejectsInvalidEntries(t *testing.T) {
	for _, sources := range [][]LoadedSource{
		{{Path: "relative.go", SHA256: strings.Repeat("0", 64)}},
		{{Path: "/dependency.go", SHA256: strings.Repeat("g", 64)}},
		{{Path: "/dependency.go", SHA256: strings.Repeat("0", 64)}, {Path: "/dependency.go", SHA256: strings.Repeat("0", 64)}},
	} {
		analysis := Analysis{Schema: analysisSchema, Root: "/repo", Coverage: Coverage{SourceSHA256: strings.Repeat("0", 64), LoadedSources: sources, LoadedSourceSHA256: loadedSourceIdentity(sources)}}
		if err := analysis.validate(); err == nil || !strings.Contains(err.Error(), "loaded-source") {
			t.Fatalf("invalid loaded source entry accepted: %+v / %v", sources, err)
		}
	}
}

func TestLocalDependencyEditChangesAnalysisIdentity(t *testing.T) {
	base := t.TempDir()
	root, dependency := filepath.Join(base, "app"), filepath.Join(base, "dependency")
	for _, dir := range []string{root, dependency} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "go.mod"):              "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ../dependency\n",
		filepath.Join(root, "app.go"):              "package app\nimport \"example.com/dependency\"\nfunc Seed() { _ = dependency.Marker() }\n",
		filepath.Join(dependency, "go.mod"):        "module example.com/dependency\n\ngo 1.27.0\n",
		filepath.Join(dependency, "dependency.go"): "package dependency\nfunc Marker() int { return 1 }\n",
	}
	for path, contents := range files {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	options := Options{Root: root, Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}
	first, saved, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Coverage.LoadedSources) < 2 || first.Coverage.LoadedSourceSHA256 == "" {
		t.Fatal("loaded source inventory missing")
	}
	if err := os.WriteFile(filepath.Join(dependency, "dependency.go"), []byte("package dependency\nfunc Marker() int { return 2 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	second, err := Trace(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Coverage.SourceSHA256 == second.Coverage.SourceSHA256 {
		t.Fatal("dependency source edit retained the old analysis identity")
	}
	_, err = Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20})
	if err == nil || !strings.Contains(err.Error(), "loaded source changed") {
		t.Fatalf("stale dependency snapshot accepted: %v", err)
	}
}

func TestExploreRejectsAddedLocalDependencySource(t *testing.T) {
	base := t.TempDir()
	root, dependency := filepath.Join(base, "app"), filepath.Join(base, "dependency")
	for _, dir := range []string{root, dependency} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "go.mod"):              "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ../dependency\n",
		filepath.Join(root, "app.go"):              "package app\nimport \"example.com/dependency\"\nfunc Seed() { _ = dependency.Marker() }\n",
		filepath.Join(root, "app_test.go"):         "package app\nimport \"testing\"\nfunc TestBuildSelection(t *testing.T) {}\n",
		filepath.Join(dependency, "go.mod"):        "module example.com/dependency\n\ngo 1.27.0\n",
		filepath.Join(dependency, "dependency.go"): "package dependency\nfunc Marker() int { return 1 }\n",
		filepath.Join(dependency, "tagged.go"):     "//go:build selection_probe\n\npackage dependency\nconst Selected = true\n",
	}
	for path, contents := range files {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", "off")
	_, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20, Tests: true, Tags: "selection_probe"})
	if err != nil {
		t.Fatalf("trace initial dependency selection: %v", err)
	}
	var compressed bytes.Buffer
	if err := WriteAnalysis(&compressed, saved, true); err != nil {
		t.Fatalf("write selected-source snapshot: %v", err)
	}
	saved, err = ReadAnalysis(&compressed)
	if err != nil {
		t.Fatalf("read selected-source snapshot: %v", err)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err != nil {
		t.Fatalf("stable gzip snapshot did not explore with saved Tests/tags: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dependency, "unselected.go"), []byte("//go:build another_selection\n\npackage dependency\nconst Unselected = true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err != nil {
		t.Fatalf("unselected build-tag file changed selected source identity: %v", err)
	}
	assertChanged := func(label string) {
		t.Helper()
		if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err == nil || !strings.Contains(err.Error(), "selected Go source set changed") {
			t.Fatalf("saved analysis accepted dependency source %s: %v", label, err)
		}
	}
	assertMissing := func(label string) {
		t.Helper()
		if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err == nil || !strings.Contains(err.Error(), "loaded source unavailable") {
			t.Fatalf("saved analysis accepted dependency source %s: %v", label, err)
		}
	}
	tagged, renamed := filepath.Join(dependency, "tagged.go"), filepath.Join(dependency, "renamed.go")
	if err := os.Rename(tagged, renamed); err != nil {
		t.Fatal(err)
	}
	assertMissing("rename")
	if err := os.Rename(renamed, tagged); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tagged); err != nil {
		t.Fatal(err)
	}
	assertMissing("removal")
	if err := os.WriteFile(tagged, []byte("//go:build selection_probe\n\npackage dependency\nconst Selected = true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(dependency, "added.go")
	if err := os.WriteFile(added, []byte("//go:build selection_probe\n\npackage dependency\nfunc Added() int { return 2 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	assertChanged("addition")
}

func TestExploreRejectsLegacyAnalysisWithoutSourceInventory(t *testing.T) {
	_, saved, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20})
	if err != nil {
		t.Fatal(err)
	}
	saved.Coverage.LoadedSources = nil
	saved.Coverage.LoadedSourceSHA256 = ""
	var compressed bytes.Buffer
	if err := WriteAnalysis(&compressed, saved, true); err != nil {
		t.Fatalf("write legacy snapshot: %v", err)
	}
	saved, err = ReadAnalysis(&compressed)
	if err != nil {
		t.Fatalf("read legacy snapshot: %v", err)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Validate"}, Depth: 1, MaxNodes: 20}); err == nil || !strings.Contains(err.Error(), "lacks the selected Go source inventory") {
		t.Fatalf("analysis without selected-source inventory was reused: %v", err)
	}
}
