package contracttrace

import (
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
