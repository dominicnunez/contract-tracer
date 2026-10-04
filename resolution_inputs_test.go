package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
