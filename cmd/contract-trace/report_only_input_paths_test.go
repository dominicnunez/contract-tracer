package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReportOnlyCannotReplaceRootInputs(t *testing.T) {
	for _, name := range []string{"source.go", "excluded.go", "schema.sql", "go.mod", "go.sum"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeCLIInput(t, root)
			for file, contents := range map[string]string{
				"schema.sql":  "CREATE TABLE records (id INTEGER PRIMARY KEY);\n",
				"go.sum":      "example.com/unused v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n",
				"excluded.go": "//go:build report_only_never\n\npackage outputalias\nfunc excluded() {}\n",
			} {
				if err := os.WriteFile(filepath.Join(root, file), []byte(contents), 0644); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(root, name)
			before, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}

			result := runCLI(t, "-root", root, "-seed", "Validate", "-output", destination)
			if result.err == nil {
				after, _ := os.ReadFile(destination)
				t.Fatalf("report-only output replaced analyzed root input %q (bytes changed: %t)", name, string(after) != string(before))
			}
			assertReportOutputRejected(t, result)
			after, err := os.ReadFile(destination)
			if err != nil || string(after) != string(before) {
				t.Fatalf("rejected report changed root input %q: read error=%v", name, err)
			}
		})
	}
}

func TestReportOnlyCannotReplaceEmbeddedAsset(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/reportasset\n\ngo 1.27.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	asset := []byte("embedded source text")
	if err := os.WriteFile(filepath.Join(root, "asset.txt"), asset, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package reportasset\nimport _ \"embed\"\n//go:embed asset.txt\nvar content string\nfunc Validate() bool { return content != \"\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result := runCLI(t, "-root", root, "-seed", "Validate", "-output", filepath.Join(root, "asset.txt"))
	if result.err == nil {
		t.Fatal("report-only output replaced a captured embedded asset")
	}
	assertReportOutputRejected(t, result)
	after, err := os.ReadFile(filepath.Join(root, "asset.txt"))
	if err != nil || string(after) != string(asset) {
		t.Fatalf("rejected report changed embedded asset: read error=%v", err)
	}
}

func TestReportOnlyCannotReplaceExternalDependencyInputs(t *testing.T) {
	for _, name := range []string{"source.go", "go.mod"} {
		t.Run(name, func(t *testing.T) {
			root, dependency := writeExternalDependency(t)
			destination := filepath.Join(dependency, name)
			before, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}

			result := runCLI(t, "-root", root, "-seed", "Validate", "-output", destination)
			if result.err == nil {
				t.Fatalf("report-only output replaced captured dependency input %q", name)
			}
			assertReportOutputRejected(t, result)
			after, err := os.ReadFile(destination)
			if err != nil || string(after) != string(before) {
				t.Fatalf("rejected report changed dependency input %q: read error=%v", name, err)
			}
		})
	}
}

func TestReportOnlyCannotReplaceWorkspaceFile(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	dependency := filepath.Join(base, "dependency")
	for _, directory := range []string{root, dependency} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "go.mod"):          "module example.com/workspaceapp\n\ngo 1.27.0\n\nrequire example.com/workspacedep v0.0.0\n",
		filepath.Join(root, "source.go"):       "package workspaceapp\nimport \"example.com/workspacedep\"\nfunc Validate() bool { return workspacedep.Check() }\n",
		filepath.Join(dependency, "go.mod"):    "module example.com/workspacedep\n\ngo 1.27.0\n",
		filepath.Join(dependency, "source.go"): "package workspacedep\nfunc Check() bool { return true }\n",
	}
	for path, contents := range files {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	workfile := filepath.Join(base, "go.work")
	workContents := []byte("go 1.27.0\n\nuse (\n ./app\n ./dependency\n)\n")
	if err := os.WriteFile(workfile, workContents, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", workfile)

	result := runCLI(t, "-root", root, "-seed", "Validate", "-output", workfile)
	if result.err == nil {
		t.Fatal("report-only output replaced the active workspace file")
	}
	assertReportOutputRejected(t, result)
	after, err := os.ReadFile(workfile)
	if err != nil || string(after) != string(workContents) {
		t.Fatalf("rejected report changed workspace file: read error=%v", err)
	}
}

func TestReportOnlyCannotReplaceRootInputByHardLink(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	source := filepath.Join(root, "source.go")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "report.json")
	if err := os.Link(source, report); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	result := runCLI(t, "-root", root, "-seed", "Validate", "-output", report)
	if result.err == nil {
		t.Fatal("report-only output replaced a root source through a hard link")
	}
	assertReportOutputRejected(t, result)
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(before) {
		t.Fatalf("rejected hard-link report changed source: read error=%v", err)
	}
}

func TestReportOnlyCannotReplaceSourceThroughSymlinkDotDot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the symlink/.. path meaning differs on Windows")
	}
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
	alias := outside + string(os.PathSeparator) + "link" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "source.go"
	source := filepath.Join(root, "source.go")
	aliasInfo, err := os.Stat(alias)
	if err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil || !os.SameFile(aliasInfo, sourceInfo) {
		t.Skipf("host does not resolve symlink/.. output to the root source: stat error=%v", err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}

	result := runCLI(t, "-root", root, "-seed", "Validate", "-output", alias)
	if result.err == nil {
		t.Fatal("report-only output replaced source through symlink/.. traversal")
	}
	assertReportOutputRejected(t, result)
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(before) {
		t.Fatalf("rejected symlink/.. report changed source: read error=%v", err)
	}
}

func TestReportOnlyRejectsAmbiguousWindowsName(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows file-name aliases")
	}
	root := t.TempDir()
	writeCLIInput(t, root)
	source := filepath.Join(root, "source.go")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	result := runCLI(t, "-root", root, "-seed", "Validate", "-output", source+".")
	if result.err == nil || !strings.Contains(result.stderr, "ambiguous Windows file name") {
		t.Fatalf("report-only accepted an ambiguous Windows output path: %v %q", result.err, result.stderr)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(before) {
		t.Fatalf("rejected ambiguous report changed source: read error=%v", err)
	}
}

func assertReportOutputRejected(t *testing.T, result cliResult) {
	t.Helper()
	if !strings.Contains(result.stderr, "-output") || !(strings.Contains(result.stderr, "analyzed input") || strings.Contains(result.stderr, "analyzed source fingerprint") || strings.Contains(result.stderr, "cannot safely inspect")) {
		t.Fatalf("report-only output was rejected for an unrelated reason: %v: %q", result.err, result.stderr)
	}
}

func TestReportOnlyCanWriteDistinctOutputAndStdout(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	external := t.TempDir()
	report := filepath.Join(external, "report.go")
	result := runCLI(t, "-root", root, "-seed", "Validate", "-output", report)
	if result.err != nil {
		t.Fatalf("distinct external report was rejected: %v: %s", result.err, result.stderr)
	}
	data, err := os.ReadFile(report)
	if err != nil || !json.Valid(data) {
		t.Fatalf("external report is not valid JSON: read error=%v", err)
	}
	stdout := runCLI(t, "-root", root, "-seed", "Validate")
	if stdout.err != nil || !strings.Contains(stdout.stdout, `"schema": "contract-tracer.v0.1"`) {
		t.Fatalf("stdout report was rejected or missing JSON: err=%v stdout=%q", stdout.err, stdout.stdout)
	}
}

func TestReportOnlyCanReplaceExistingJSONReport(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	report := filepath.Join(root, "report.json")
	if err := os.WriteFile(report, []byte(`{"old":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	result := runCLI(t, "-root", root, "-seed", "Validate", "-output", report)
	if result.err != nil {
		t.Fatalf("existing non-input report was rejected: %v: %s", result.err, result.stderr)
	}
	data, err := os.ReadFile(report)
	if err != nil || !json.Valid(data) || strings.Contains(string(data), `"old"`) {
		t.Fatalf("existing report was not replaced with valid JSON: read error=%v contents=%q", err, data)
	}
}
