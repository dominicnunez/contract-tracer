package main

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
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

type cliResult struct {
	err    error
	stderr string
}

func runCLI(t *testing.T, args ...string) cliResult {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=TestCLIProcess")
	command.Env = append(os.Environ(), "CONTRACT_TRACE_TEST_ARGS="+string(payload))
	var stderr strings.Builder
	command.Stderr = &stderr
	err = command.Run()
	return cliResult{err: err, stderr: stderr.String()}
}

func TestCLIProcess(t *testing.T) {
	payload := os.Getenv("CONTRACT_TRACE_TEST_ARGS")
	if payload == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(payload), &args); err != nil {
		t.Fatalf("decode CLI test arguments: %v", err)
	}
	os.Args = append([]string{"contract-trace"}, args...)
	flag.CommandLine = flag.NewFlagSet("contract-trace", flag.ContinueOnError)
	os.Exit(run())
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
