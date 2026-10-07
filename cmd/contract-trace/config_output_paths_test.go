package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigCannotBeReplacedByAnalysisArtifacts(t *testing.T) {
	t.Run("saved analysis", func(t *testing.T) {
		root := t.TempDir()
		writeCLIInput(t, root)
		config, original := writeCLIConfig(t, root)
		result := runCLI(t, "-root", root, "-seed", "Validate", "-config", config, "-save-analysis", config)
		requireConfigCollisionRejected(t, result, config, original, "-save-analysis")
	})

	t.Run("report only", func(t *testing.T) {
		root := t.TempDir()
		writeCLIInput(t, root)
		config, original := writeCLIConfig(t, root)
		result := runCLI(t, "-root", root, "-seed", "Validate", "-config", config, "-output", config)
		requireConfigCollisionRejected(t, result, config, original, "-output")
	})

	t.Run("paired report", func(t *testing.T) {
		root := t.TempDir()
		writeCLIInput(t, root)
		config, original := writeCLIConfig(t, root)
		snapshot := filepath.Join(root, "analysis.json")
		result := runCLI(t, "-root", root, "-seed", "Validate", "-config", config, "-save-analysis", snapshot, "-output", config)
		requireConfigCollisionRejected(t, result, config, original, "-output")
		if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
			t.Fatalf("rejected paired write left snapshot behind: %v", err)
		}
	})

	t.Run("hard-linked saved analysis", func(t *testing.T) {
		root := t.TempDir()
		writeCLIInput(t, root)
		config, original := writeCLIConfig(t, root)
		snapshot := filepath.Join(root, "analysis.json")
		if err := os.Link(config, snapshot); err != nil {
			t.Skipf("hard links unavailable: %v", err)
		}
		result := runCLI(t, "-root", root, "-seed", "Validate", "-config", config, "-save-analysis", snapshot)
		requireConfigCollisionRejected(t, result, config, original, "-save-analysis")
		if !sameFile(t, config, snapshot) {
			t.Fatal("rejected save changed the configuration hard link")
		}
	})

	t.Run("symlink parent report", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the symlink/.. parent path has platform-specific meaning")
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
		config, original := writeCLIConfig(t, root)
		if err := os.Symlink(filepath.Join(root, "child"), filepath.Join(outside, "link")); err != nil {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
		output := outside + string(os.PathSeparator) + "link" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "config.json"
		if !sameFile(t, config, output) {
			t.Skip("host does not resolve the symlink/.. fixture to the configuration file")
		}
		result := runCLI(t, "-root", root, "-seed", "Validate", "-config", config, "-output", output)
		requireConfigCollisionRejected(t, result, config, original, "-output")
	})
}

func TestConfigAndDistinctAnalysisArtifactsCanBeWritten(t *testing.T) {
	root := t.TempDir()
	writeCLIInput(t, root)
	config, original := writeCLIConfig(t, root)
	snapshot := filepath.Join(root, "analysis.json.gz")
	report := filepath.Join(root, "report.json")
	result := runCLI(t, "-root", root, "-seed", "Validate", "-config", config, "-save-analysis", snapshot, "-output", report)
	if result.err != nil {
		t.Fatalf("distinct config and artifact paths: %v: %s", result.err, result.stderr)
	}
	if after, err := os.ReadFile(config); err != nil || string(after) != string(original) {
		t.Fatalf("successful run changed its config: read err=%v", err)
	}
	for _, path := range []string{snapshot, report} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected artifact %q: %v", filepath.Base(path), err)
		}
	}
}

func writeCLIConfig(t *testing.T, root string) (string, []byte) {
	t.Helper()
	config := filepath.Join(root, "config.json")
	contents := []byte(`{"event_fields":["EventType"]}`)
	if err := os.WriteFile(config, contents, 0600); err != nil {
		t.Fatal(err)
	}
	return config, contents
}

func requireConfigCollisionRejected(t *testing.T, result cliResult, config string, original []byte, artifactFlag string) {
	t.Helper()
	if result.err == nil {
		after, err := os.ReadFile(config)
		if err != nil {
			t.Errorf("run accepted %s that aliases its config, then config read failed: %v", artifactFlag, err)
		} else {
			t.Errorf("run accepted %s that aliases its config; config now starts %q", artifactFlag, after[:min(len(after), 80)])
		}
	}
	if result.err != nil && (!strings.Contains(result.stderr, "-config") || !strings.Contains(result.stderr, artifactFlag)) {
		t.Errorf("collision error should identify config and artifact flags, got %q", result.stderr)
	}
	after, err := os.ReadFile(config)
	if err != nil || string(after) != string(original) {
		t.Errorf("rejected write changed config bytes: read err=%v", err)
	}
}

func sameFile(t *testing.T, first, second string) bool {
	t.Helper()
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(firstInfo, secondInfo)
}
