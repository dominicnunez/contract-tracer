package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	trace "contract-tracer"
)

func TestSaveSnapshotCompressionAndFailedReplacement(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{"go.mod": "module example.com/snapshotcli\n\ngo 1.27.0\n", "source.go": "package snapshotcli\nfunc Validate(n int) bool {return n>0}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_, analysis, err := trace.TraceWithAnalysis(context.Background(), trace.Options{Root: root, Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"snapshot.json", "snapshot.json.gz", "snapshot.GZ"} {
		destination := filepath.Join(root, name)
		if err := saveSnapshot(destination, analysis); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(destination)
		if err != nil {
			t.Fatal(err)
		}
		compressed := len(before) > 2 && before[0] == 0x1f && before[1] == 0x8b
		if compressed != (name != "snapshot.json") {
			t.Fatalf("unexpected compression for %s", name)
		}
		if err := saveSnapshot(destination, trace.Analysis{}); err == nil {
			t.Fatal("invalid snapshot replaced destination")
		}
		after, err := os.ReadFile(destination)
		if err != nil || string(after) != string(before) {
			t.Fatal("failed write damaged existing snapshot")
		}
		if err := saveSnapshot(destination, analysis); err != nil {
			t.Fatal("valid replacement failed", err)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, ".contract-analysis-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary snapshots leaked", files, err)
	}
}
