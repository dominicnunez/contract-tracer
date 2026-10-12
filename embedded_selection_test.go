package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExploreRejectsNewEmbeddedFSMatch(t *testing.T) {
	fixture := newEmbeddedSelectionFixture(t, `import "embed"`, "//go:embed assets/*.txt\nvar embedded embed.FS", "_ = embedded")
	saved := traceGzipSnapshot(t, fixture.root)

	if err := os.WriteFile(filepath.Join(fixture.root, "assets", "unmatched.bin"), []byte("unmatched"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err != nil {
		t.Fatalf("unmatched non-Go file changed embedded selection: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "assets", "two.txt"), []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err == nil || !strings.Contains(err.Error(), "embedded asset set changed") {
		t.Fatalf("saved graph accepted a new embedded FS match: %v", err)
	}
}

func TestExploreRejectsNewScalarEmbedMatches(t *testing.T) {
	for _, test := range []struct {
		name     string
		typeName string
	}{
		{name: "string", typeName: "string"},
		{name: "byte slice", typeName: "[]byte"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEmbeddedSelectionFixture(t, `import _ "embed"`, "//go:embed assets/single*.txt\nvar embedded "+test.typeName, "_ = embedded")
			saved := traceGzipSnapshot(t, fixture.root)
			if err := os.WriteFile(filepath.Join(fixture.root, "assets", "single-two.txt"), []byte("two"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}); err == nil || !strings.Contains(err.Error(), "embedded asset set changed") {
				t.Fatalf("saved graph accepted second %s embed match: %v", test.name, err)
			}
		})
	}
}

type embeddedSelectionFixture struct {
	root string
}

func newEmbeddedSelectionFixture(t *testing.T, importLine, embedDecl, use string) embeddedSelectionFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	source := "package app\n\n" + importLine + "\n\n" + embedDecl + "\n\nfunc Seed() { " + use + " }\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/embedselection\n\ngo 1.27.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "single-one.txt"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	return embeddedSelectionFixture{root: root}
}

func (f embeddedSelectionFixture) options() Options {
	return Options{Root: f.root, Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20}
}

func traceGzipSnapshot(t *testing.T, root string) Analysis {
	t.Helper()
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Seed"}, Depth: 1, MaxNodes: 20})
	if err != nil {
		t.Fatalf("trace embedded fixture: %v", err)
	}
	var compressed bytes.Buffer
	if err := WriteAnalysis(&compressed, analysis, true); err != nil {
		t.Fatalf("write embedded snapshot: %v", err)
	}
	saved, err := ReadAnalysis(&compressed)
	if err != nil {
		t.Fatalf("read embedded snapshot: %v", err)
	}
	return saved
}
