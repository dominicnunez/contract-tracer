package contracttrace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTraceAndSavedAnalysisCanonicalizeSymlinkRoots(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "modules")
	root := filepath.Join(parent, "app")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "go.mod"), "module example.com/rootlink\n\ngo 1.27.0\n")
	write(filepath.Join(root, "app.go"), "package app\nfunc Seed() {}\n")
	write(filepath.Join(root, "ignored.go"), "//go:build rootlink_disabled\n\npackage app\nconst ignored = 1\n")
	schema := "CREATE TABLE root_link_records (id INTEGER PRIMARY KEY);\n"
	write(filepath.Join(root, "schema.sql"), schema)

	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	rootAlias := filepath.Join(base, "root-alias")
	if err := os.Symlink(root, rootAlias); err != nil {
		t.Skipf("platform does not permit creating a directory symlink: %v", err)
	}
	parentAlias := filepath.Join(base, "parent-alias")
	if err := os.Symlink(parent, parentAlias); err != nil {
		t.Skipf("platform does not permit creating an ancestor directory symlink: %v", err)
	}
	ancestorAlias := filepath.Join(parentAlias, "app")

	options := func(selectedRoot string) Options {
		return Options{Root: selectedRoot, Seeds: []string{"Seed", "schema.sql"}, Depth: 2, MaxNodes: 100}
	}
	realReport, analysis, err := TraceWithAnalysis(context.Background(), options(root))
	if err != nil {
		t.Fatalf("TraceWithAnalysis on real root: %v", err)
	}
	if analysis.Root != canonical {
		t.Errorf("saved analysis root is not canonical: got %q, want %q", analysis.Root, canonical)
	}

	var compressed bytes.Buffer
	if err := WriteAnalysis(&compressed, analysis, true); err != nil {
		t.Fatalf("write compressed analysis: %v", err)
	}
	saved, err := ReadAnalysis(&compressed)
	if err != nil {
		t.Fatalf("read compressed analysis: %v", err)
	}
	legacy := saved
	legacy.Root = rootAlias
	legacy.Coverage.Storage.Files = nil
	legacy.Nodes = filterRootSymlinkLegacySQLNodes(legacy.Nodes)
	legacy.Relationships = filterRootSymlinkLegacySQLRelationships(legacy.Relationships)
	legacyFingerprint, _, err := fingerprint(rootAlias)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := json.Marshal(struct {
		Build  map[string]string
		Config Config
		Tests  bool
		Tags   string
	}{legacy.Coverage.Build, legacy.Config, legacy.Coverage.Tests, legacy.Coverage.Tags})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := hashFiles(rootAlias, legacy.Coverage.EmbeddedFiles)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Coverage.SourceSHA256 = hashText(legacyFingerprint + string(identity) + assets + legacy.Coverage.LoadedSourceSHA256 + legacy.Coverage.ResolutionSHA256)
	if err := legacy.validate(); err != nil {
		t.Fatalf("constructed legacy snapshot is not graph-valid: %v", err)
	}
	if _, err := Explore(context.Background(), legacy, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 50}); err == nil {
		t.Error("saved analysis with legacy symlink-root fingerprint and missing SQL inventory was accepted")
	} else if !strings.Contains(err.Error(), "source changed since saved analysis") {
		t.Errorf("legacy snapshot was rejected for the wrong reason: %v", err)
	}

	aliasReport, analysis, err := TraceWithAnalysis(context.Background(), options(rootAlias))
	if err != nil {
		t.Fatalf("TraceWithAnalysis on symlink root: %v", err)
	}
	ancestorReport, err := Trace(context.Background(), options(ancestorAlias))
	if err != nil {
		t.Fatalf("Trace on root reached through ancestor symlink: %v", err)
	}
	for label, report := range map[string]Report{"real": realReport, "direct symlink": aliasReport, "ancestor symlink": ancestorReport} {
		if report.Root != canonical {
			t.Errorf("%s root was not canonicalized: got %q, want %q", label, report.Root, canonical)
		}
		if len(report.Coverage.Storage.Files) != 1 || report.Coverage.Storage.Files[0] != "schema.sql" {
			t.Errorf("%s root omitted its SQL inventory: %v", label, report.Coverage.Storage.Files)
		}
		if !rootSymlinkHasSchemaNode(report) {
			t.Errorf("%s root omitted schema SQL evidence", label)
		}
		if !contains(report.Coverage.ExcludedGoFiles, "ignored.go") {
			t.Errorf("%s root omitted excluded Go source from the descendant source inventory: %v", label, report.Coverage.ExcludedGoFiles)
		}
	}
	if aliasReport.Coverage.SourceSHA256 != realReport.Coverage.SourceSHA256 || ancestorReport.Coverage.SourceSHA256 != realReport.Coverage.SourceSHA256 {
		t.Error("real and symlink root selections produced different source identities")
	}
	if analysis.Root != canonical {
		t.Errorf("saved analysis root is not canonical: got %q, want %q", analysis.Root, canonical)
	}
	compressed.Reset()
	if err := WriteAnalysis(&compressed, analysis, true); err != nil {
		t.Fatalf("write compressed symlink-root analysis: %v", err)
	}
	saved, err = ReadAnalysis(&compressed)
	if err != nil {
		t.Fatalf("read compressed symlink-root analysis: %v", err)
	}

	decoy := filepath.Join(base, "decoy")
	if err := os.MkdirAll(decoy, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(decoy, "go.mod"), "module example.com/decoy\n\ngo 1.27.0\n")
	write(filepath.Join(decoy, "app.go"), "package app\nfunc Seed() {}\n")
	if err := os.Remove(rootAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoy, rootAlias); err != nil {
		t.Skipf("platform cannot retarget directory symlinks: %v", err)
	}
	explored, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed", "schema.sql"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatalf("saved canonical analysis did not survive retargeting the original alias: %v", err)
	}
	if explored.Root != canonical || !rootSymlinkHasSchemaNode(explored) {
		t.Errorf("Explore followed the changed input alias instead of its recorded canonical root: root=%q", explored.Root)
	}

	write(filepath.Join(root, "schema.sql"), schema+"-- changed\n")
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 50}); err == nil {
		t.Error("compressed saved analysis accepted a changed SQL file")
	}
	write(filepath.Join(root, "schema.sql"), schema)
	write(filepath.Join(root, "ignored.go"), "//go:build rootlink_disabled\n\npackage app\nconst ignored = 2\n")
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 50}); err == nil {
		t.Error("compressed saved analysis accepted a changed excluded Go file")
	}
}

func rootSymlinkHasSchemaNode(report Report) bool {
	for _, node := range report.Nodes {
		if node.Kind == "sql_file" && node.Evidence.File == "schema.sql" && node.Evidence.Snippet != "" {
			return true
		}
	}
	return false
}

func filterRootSymlinkLegacySQLNodes(nodes []Node) []Node {
	filtered := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		if node.Kind == "sql_file" || node.ID == "table:root_link_records" {
			continue
		}
		filtered = append(filtered, node)
	}
	return filtered
}

func filterRootSymlinkLegacySQLRelationships(edges []Relationship) []Relationship {
	filtered := make([]Relationship, 0, len(edges))
	for _, edge := range edges {
		if edge.Evidence.File == "schema.sql" || edge.To == "table:root_link_records" {
			continue
		}
		filtered = append(filtered, edge)
	}
	return filtered
}
