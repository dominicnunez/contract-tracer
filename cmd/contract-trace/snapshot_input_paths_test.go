package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveAnalysisCannotReplaceRootInputs(t *testing.T) {
	for _, name := range []string{"new.go", "new.sql", "go.mod", "go.sum"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeCLIInput(t, root)
			destination := filepath.Join(root, name)
			var original []byte
			if contents, err := os.ReadFile(destination); err == nil {
				original = contents
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}

			result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", destination)
			if result.err == nil {
				t.Fatalf("save-analysis accepted root input destination %q", name)
			}
			after, err := os.ReadFile(destination)
			if len(original) == 0 {
				if !os.IsNotExist(err) {
					t.Fatalf("rejected save created %q: read err=%v", name, err)
				}
			} else if err != nil || string(after) != string(original) {
				t.Fatalf("rejected save changed %q: read err=%v", name, err)
			}
		})
	}
}

func TestReportOnlyMayWriteSourceShapedPathInRoot(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	report := filepath.Join(root, "report.go")
	result := runCLI(t, "-root", root, "-seed", "Validate", "-output", report)
	if result.err != nil {
		t.Fatalf("report-only output behavior changed: %v: %s", result.err, result.stderr)
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatalf("report-only output missing: %v", err)
	}
}

func TestSaveAndReportMayWriteExcludedFingerprintDirectory(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	vendor := filepath.Join(root, "vendor")
	if err := os.Mkdir(vendor, 0755); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "analysis.json")
	report := filepath.Join(vendor, "report.go")
	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", report)
	if result.err != nil {
		t.Fatalf("excluded fingerprint directory should remain writable: %v: %s", result.err, result.stderr)
	}
}

func TestSaveRejectsNewSourceThroughDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	snapshot := filepath.Join(alias, "new.go")
	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot)
	if result.err == nil {
		t.Fatal("save-analysis accepted a new source path through a directory alias")
	}
	if _, err := os.Stat(filepath.Join(root, "new.go")); !os.IsNotExist(err) {
		t.Fatalf("rejected source alias created a Go file: %v", err)
	}
}

func TestSaveAndReportRejectSourcePathThroughSymlinkDotDot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the symlink/.. path meaning differs on Windows")
	}
	fixture := func(t *testing.T) (string, string) {
		t.Helper()
		base := t.TempDir()
		root := filepath.Join(base, "root")
		outside := filepath.Join(base, "outside")
		if err := os.MkdirAll(filepath.Join(root, "child"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(outside, 0755); err != nil {
			t.Fatal(err)
		}
		writeCLIInput(t, root)
		if err := os.Symlink(filepath.Join(root, "child"), filepath.Join(outside, "link")); err != nil {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
		linkParent := outside + string(os.PathSeparator) + "link" + string(os.PathSeparator) + ".."
		aliasedSource := linkParent + string(os.PathSeparator) + "new.go"
		parentInfo, err := os.Stat(linkParent)
		if err != nil {
			t.Fatal(err)
		}
		rootInfo, err := os.Stat(root)
		if err != nil || !os.SameFile(parentInfo, rootInfo) {
			t.Skipf("host does not resolve the fixture path to the root: stat err=%v", err)
		}
		return root, aliasedSource
	}

	t.Run("snapshot", func(t *testing.T) {
		root, aliasedSource := fixture(t)
		result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", aliasedSource)
		if result.err == nil {
			t.Fatal("save-analysis accepted a new source path through symlink/.. traversal")
		}
		if _, err := os.Stat(filepath.Join(root, "new.go")); !os.IsNotExist(err) {
			t.Fatalf("rejected snapshot created source through symlink/.. path: %v", err)
		}
	})
	t.Run("paired report", func(t *testing.T) {
		root, aliasedSource := fixture(t)
		snapshot := filepath.Join(root, "analysis.json")
		result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", aliasedSource)
		if result.err == nil {
			t.Fatal("paired report accepted a new source path through symlink/.. traversal")
		}
		if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
			t.Fatalf("rejected paired report left snapshot behind: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "new.go")); !os.IsNotExist(err) {
			t.Fatalf("rejected report created source through symlink/.. path: %v", err)
		}
	})
}

func TestSaveAnalysisCannotReplaceCaseAliasedRootInput(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows file-name aliases")
	}
	root := t.TempDir()
	writeCLIInput(t, root)
	source := filepath.Join(root, "source.go")
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", filepath.Join(root, "SOURCE.GO"))
	if result.err == nil {
		t.Fatal("save-analysis accepted a case-aliased source destination")
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(original) {
		t.Fatalf("rejected case alias changed source: read err=%v", err)
	}
}

func TestPairedReportCannotAddRootInput(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	snapshot := filepath.Join(root, "analysis.json")
	report := filepath.Join(root, "new.go")
	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", report)
	if result.err == nil {
		t.Fatal("save-analysis and paired report accepted a new root Go input")
	}
	for _, path := range []string{snapshot, report} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("rejected paired publication left %q behind: %v", filepath.Base(path), err)
		}
	}
}

func TestResumeReportCannotAddRootInput(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	snapshot := filepath.Join(root, "analysis.json")
	created := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot)
	if created.err != nil {
		t.Fatalf("create saved analysis: %v: %s", created.err, created.stderr)
	}
	original, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "new.sql")
	result := runCLI(t, "-resume", snapshot, "-seed", "Validate", "-output", report)
	if result.err == nil {
		t.Fatal("resume accepted a report destination that adds a root SQL input")
	}
	if _, err := os.Stat(report); !os.IsNotExist(err) {
		t.Fatalf("rejected resume left report behind: %v", err)
	}
	after, err := os.ReadFile(snapshot)
	if err != nil || string(after) != string(original) {
		t.Fatalf("rejected resume changed snapshot: read err=%v", err)
	}
}

func TestSaveAnalysisCannotReplaceEmbeddedAsset(t *testing.T) {
	root := t.TempDir()
	asset := []byte("asset contents that define the analysis")
	if err := os.WriteFile(filepath.Join(root, "asset.txt"), asset, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package outputalias\nimport _ \"embed\"\n//go:embed asset.txt\nvar embedded string\nfunc Validate() bool { return embedded != \"\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/outputalias\n\ngo 1.27.0\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", filepath.Join(root, "asset.txt"))
	if result.err == nil {
		t.Fatal("save-analysis accepted a captured embedded asset")
	}
	after, err := os.ReadFile(filepath.Join(root, "asset.txt"))
	if err != nil || string(after) != string(asset) {
		t.Fatalf("rejected save changed embedded asset: read err=%v", err)
	}
}

func TestPairedReportCannotAliasRootSourceByHardLink(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	source := filepath.Join(root, "source.go")
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "report.json")
	if err := os.Link(source, report); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	snapshot := filepath.Join(root, "analysis.json")
	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", report)
	if result.err == nil {
		t.Fatal("paired report accepted a hard link to an analyzed Go source")
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("rejected paired publication left snapshot behind: %v", err)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(original) {
		t.Fatalf("rejected hard-link report changed source: read err=%v", err)
	}
}

func TestSaveAndReportMayUseExternalSourceShapedName(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	external := t.TempDir()
	snapshot := filepath.Join(root, "analysis.json.gz")
	report := filepath.Join(external, "report.go")
	created := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", report)
	if created.err != nil {
		t.Fatalf("save with external Go-named report: %v: %s", created.err, created.stderr)
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatalf("external report missing: %v", err)
	}
	resumed := runCLI(t, "-resume", snapshot, "-seed", "Validate", "-output", filepath.Join(external, "resumed.sql"))
	if resumed.err != nil {
		t.Fatalf("resume with external SQL-named report: %v: %s", resumed.err, resumed.stderr)
	}
}

func TestSaveAnalysisCannotReplaceExternalModuleInputs(t *testing.T) {
	for _, destinationName := range []string{"source.go", "go.mod"} {
		t.Run(destinationName, func(t *testing.T) {
			root, dependency := writeExternalDependency(t)
			destination := filepath.Join(dependency, destinationName)
			original, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", destination)
			if result.err == nil {
				t.Fatalf("save-analysis accepted captured external input %q", destinationName)
			}
			after, err := os.ReadFile(destination)
			if err != nil || string(after) != string(original) {
				t.Fatalf("rejected save changed external input: read err=%v", err)
			}
		})
	}

	root, dependency := writeExternalDependency(t)
	snapshot := filepath.Join(root, "analysis.json")
	output := filepath.Join(dependency, "source.go")
	original, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", output)
	if result.err == nil {
		t.Fatal("paired report accepted a loaded external Go source")
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("rejected paired publication left snapshot behind: %v", err)
	}
	after, err := os.ReadFile(output)
	if err != nil || string(after) != string(original) {
		t.Fatalf("rejected paired publication changed external source: read err=%v", err)
	}
}

func writeExternalDependency(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "app")
	dependency := filepath.Join(base, "dependency")
	for _, directory := range []string{root, dependency} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "go.mod"):          "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ../dependency\n",
		filepath.Join(root, "source.go"):       "package app\nimport \"example.com/dependency\"\nfunc Validate() bool { return dependency.Check() }\n",
		filepath.Join(dependency, "go.mod"):    "module example.com/dependency\n\ngo 1.27.0\n",
		filepath.Join(dependency, "source.go"): "package dependency\nfunc Check() bool { return true }\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root, dependency
}
