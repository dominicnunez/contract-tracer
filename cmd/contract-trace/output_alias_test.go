package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOutputCannotReplaceSavedAnalysis(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	snapshot := filepath.Join(root, "analysis.json")
	original := []byte("preserve this snapshot")
	if err := os.WriteFile(snapshot, original, 0644); err != nil {
		t.Fatal(err)
	}

	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", snapshot)
	if result.err == nil {
		after, err := os.ReadFile(snapshot)
		if err != nil {
			t.Fatalf("read overwritten snapshot: %v", err)
		}
		t.Fatalf("run accepted output that aliases the new saved analysis; snapshot now starts %q", after[:min(len(after), 80)])
	}
	after, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("rejected collision changed snapshot bytes: got %q", after)
	}
	if !strings.Contains(result.stderr, "-output") || !strings.Contains(result.stderr, "-save-analysis") {
		t.Fatalf("collision error should name both flags, got %q", result.stderr)
	}
}

func TestOutputCollisionPrecedesAnalysis(t *testing.T) {
	root := t.TempDir()
	snapshot := filepath.Join(root, "analysis.json")
	result := runCLI(t, "-root", filepath.Join(root, "missing"), "-seed", "Validate", "-save-analysis", snapshot, "-output", snapshot)
	if result.err == nil {
		t.Fatal("run accepted colliding output and snapshot paths")
	}
	if !strings.Contains(result.stderr, "-output") || !strings.Contains(result.stderr, "-save-analysis") {
		t.Fatalf("collision should be rejected before root analysis, got %q", result.stderr)
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("collision should be rejected before creating snapshot, stat error=%v", err)
	}
}

func TestOutputCannotReplaceResumedAnalysis(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	snapshot := filepath.Join(root, "analysis.json")
	created := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot)
	if created.err != nil {
		t.Fatalf("create valid saved analysis: %v: %s", created.err, created.stderr)
	}
	original, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	result := runCLI(t, "-resume", snapshot, "-seed", "Validate", "-output", snapshot)
	if result.err == nil {
		t.Fatal("run accepted output that aliases the resumed analysis")
	}
	after, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("rejected collision changed snapshot bytes: got %q", after)
	}
	if !strings.Contains(result.stderr, "-output") || !strings.Contains(result.stderr, "-resume") {
		t.Fatalf("collision error should name both flags, got %q", result.stderr)
	}
}

func TestOutputCannotReplaceSymlinkedSavedAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("filesystem alias integration test")
	}
	root := t.TempDir()
	writeCLIInput(t, root)
	snapshot := filepath.Join(root, "analysis.json")
	created := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot)
	if created.err != nil {
		t.Fatalf("create valid saved analysis: %v: %s", created.err, created.stderr)
	}
	original, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	outputAlias := filepath.Join(root, "report.json")
	if err := os.Symlink(snapshot, outputAlias); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}

	result := runCLI(t, "-resume", snapshot, "-seed", "Validate", "-output", outputAlias)
	if result.err == nil {
		t.Fatal("run accepted output symlink that aliases the resumed analysis")
	}
	after, err := os.ReadFile(snapshot)
	if err != nil || string(after) != string(original) {
		t.Fatalf("rejected symlink collision changed snapshot: read err=%v", err)
	}
}

func TestOutputCannotReplaceAnalysisViaSymlinkParentTraversal(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(other, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	writeCLIInput(t, root)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(other, "nested"), alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	snapshot := filepath.Join(other, "analysis.json")
	created := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot)
	if created.err != nil {
		t.Fatalf("create valid saved analysis: %v: %s", created.err, created.stderr)
	}
	original, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	output := root + string(os.PathSeparator) + "alias" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "analysis.json"
	outputInfo, err := os.Stat(output)
	if err != nil {
		t.Skipf("this platform does not resolve symlink/.. to the target's parent: %v", err)
	}
	snapshotInfo, err := os.Stat(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(outputInfo, snapshotInfo) {
		t.Skip("this platform resolves symlink/.. differently from the POSIX alias under test")
	}

	result := runCLI(t, "-resume", snapshot, "-seed", "Validate", "-output", output)
	if result.err == nil {
		t.Fatal("run accepted output that reaches the resumed analysis through symlink/.. traversal")
	}
	after, err := os.ReadFile(snapshot)
	if err != nil || string(after) != string(original) {
		t.Fatalf("rejected symlink traversal changed snapshot: read err=%v", err)
	}

	freshSnapshot := filepath.Join(other, "fresh-analysis.json")
	freshOutput := root + string(os.PathSeparator) + "alias" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "fresh-analysis.json"
	fresh := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", freshSnapshot, "-output", freshOutput)
	if fresh.err == nil {
		t.Fatal("run accepted fresh output that reaches the new snapshot through symlink/.. traversal")
	}
	if _, err := os.Stat(freshSnapshot); !os.IsNotExist(err) {
		t.Fatalf("fresh symlink traversal should be rejected before snapshot creation, stat error=%v", err)
	}
}

func TestOutputCannotAliasUncreatedSaveThroughDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	aliasDir := filepath.Join(root, "alias")
	if err := os.Symlink(root, aliasDir); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	snapshot := filepath.Join(root, "analysis.json")
	output := filepath.Join(aliasDir, "nested", "..", "analysis.json")
	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", output)
	if result.err == nil {
		t.Fatal("run accepted uncreated output path through a directory symlink")
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("collision should be rejected before creating snapshot, stat error=%v", err)
	}
}

func TestOutputCannotAliasUncreatedSaveThroughDanglingFileSymlink(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	snapshot := filepath.Join(root, "analysis.json")
	outputAlias := filepath.Join(root, "report.json")
	if err := os.Symlink(snapshot, outputAlias); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}

	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", outputAlias)
	if result.err == nil {
		t.Fatal("run accepted output symlink to the not-yet-created saved analysis")
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("collision should be rejected before creating snapshot, stat error=%v", err)
	}
	linkInfo, err := os.Lstat(outputAlias)
	if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("rejection should leave output symlink intact, lstat error=%v", err)
	}
}

func TestOutputCannotAliasSavedAnalysisThroughCleanedRelativePath(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	snapshot := filepath.Join(root, "analysis.json")
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(workingDir, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(filepath.Dir(snapshot), "unused"), 0755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Dir(relative) + string(os.PathSeparator) + "unused" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(relative)
	result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", output)
	if result.err == nil {
		t.Fatal("run accepted output with a cleaned relative path alias")
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("collision should be rejected before creating snapshot, stat error=%v", err)
	}
}

func TestOutputCannotReplaceHardLinkedResumedAnalysis(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	snapshot := filepath.Join(root, "analysis.json")
	created := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot)
	if created.err != nil {
		t.Fatalf("create valid saved analysis: %v: %s", created.err, created.stderr)
	}
	original, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	outputAlias := filepath.Join(root, "report.json")
	if err := os.Link(snapshot, outputAlias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	result := runCLI(t, "-resume", snapshot, "-seed", "Validate", "-output", outputAlias)
	if result.err == nil {
		t.Fatal("run accepted output hard link that aliases the resumed analysis")
	}
	after, err := os.ReadFile(snapshot)
	if err != nil || string(after) != string(original) {
		t.Fatalf("rejected hard-link collision changed snapshot: read err=%v", err)
	}
}

func TestDistinctReportAndSavedAnalysisPathsWork(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	snapshot := filepath.Join(root, "analysis.json.gz")
	report := filepath.Join(root, "report.json")
	created := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", report)
	if created.err != nil {
		t.Fatalf("save and report: %v: %s", created.err, created.stderr)
	}
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("saved analysis missing: %v", err)
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatalf("report missing: %v", err)
	}

	resumedReport := filepath.Join(root, "resumed.json")
	resumed := runCLI(t, "-resume", snapshot, "-seed", "Validate", "-output", resumedReport)
	if resumed.err != nil {
		t.Fatalf("resume and report: %v: %s", resumed.err, resumed.stderr)
	}
	if _, err := os.Stat(resumedReport); err != nil {
		t.Fatalf("resumed report missing: %v", err)
	}
}

func TestWindowsUnusualOutputNamesCannotReplaceNewAnalysis(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path alias behavior")
	}
	for _, suffix := range []string{".", " ", "::$DATA"} {
		t.Run(strings.ReplaceAll(suffix, ":", "_"), func(t *testing.T) {
			root := t.TempDir()
			writeCLIInput(t, root)
			snapshot := filepath.Join(root, "analysis.json")
			output := snapshot + suffix
			if _, err := os.Lstat(snapshot); !os.IsNotExist(err) {
				t.Fatalf("snapshot unexpectedly exists before run: %v", err)
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("output unexpectedly exists before run: %v", err)
			}

			result := runCLI(t, "-root", root, "-seed", "Validate", "-save-analysis", snapshot, "-output", output)
			if result.err == nil {
				contents, readErr := os.ReadFile(snapshot)
				t.Fatalf("run accepted ambiguous Windows output %q (snapshot read error %v, bytes %q)", output, readErr, contents[:min(len(contents), 80)])
			}
			if !strings.Contains(result.stderr, "ambiguous Windows file name") {
				t.Fatalf("error should explain the unsupported Windows name, got %q", result.stderr)
			}
			if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
				t.Fatalf("ambiguous output should be rejected before creating snapshot, stat error=%v", err)
			}
		})
	}
}

type cliResult struct {
	err    error
	stderr string
}

func runCLI(t *testing.T, args ...string) cliResult {
	t.Helper()
	stdout, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	oldArgs, oldFlags := os.Args, flag.CommandLine
	oldStdout, oldStderr := os.Stdout, os.Stderr
	defer func() {
		os.Args, flag.CommandLine = oldArgs, oldFlags
		os.Stdout, os.Stderr = oldStdout, oldStderr
	}()
	os.Args = append([]string{"contract-trace"}, args...)
	flag.CommandLine = flag.NewFlagSet("contract-trace", flag.ContinueOnError)
	flag.CommandLine.SetOutput(stderr)
	os.Stdout, os.Stderr = stdout, stderr
	code := run()
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}
	message, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		return cliResult{err: fmt.Errorf("run returned %d", code), stderr: string(message)}
	}
	return cliResult{stderr: string(message)}
}

func writeCLIInput(t *testing.T, root string) {
	t.Helper()
	for name, content := range map[string]string{
		"go.mod":    "module example.com/outputalias\n\ngo 1.27.0\n",
		"source.go": "package outputalias\nfunc Validate() bool { return true }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
