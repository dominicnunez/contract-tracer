package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoAndDeferDiscardedConstructorsRemainPossibleCandidates(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/instructionconstructors\n\ngo 1.27.0\n",
		"calls.go": `package app

import (
	"context"
	"database/sql"
	"time"
)

func GoContext(parent context.Context) {
	go context.WithTimeout(parent, time.Second)
}

func DeferContext(parent context.Context) {
	defer context.WithCancel(parent)
}

func GoSQL() {
	go sql.Open("sqlite", ":memory:")
}

func DeferSQL() {
	defer sql.Open("sqlite", ":memory:")
}

func DirectContext(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	_ = ctx
}

func DirectSQL() {
	db, _ := sql.Open("sqlite", ":memory:")
	_ = db
}
`,
	}
	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	owners := []string{"GoContext", "DeferContext", "GoSQL", "DeferSQL", "DirectContext", "DirectSQL"}
	seeds := make([]string, len(owners))
	for i, owner := range owners {
		seeds[i] = owner
	}
	report, err := Trace(context.Background(), Options{Root: root, Seeds: seeds, Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []struct {
		owner, kind, call string
		certainty         string
	}{
		{"GoContext", "context_create", "go context.WithTimeout", "possible"},
		{"DeferContext", "context_create", "defer context.WithCancel", "possible"},
		{"GoSQL", "sql_handle_create", "go sql.Open", "possible"},
		{"DeferSQL", "sql_handle_create", "defer sql.Open", "possible"},
		{"DirectContext", "context_create", "context.WithCancel", "fact"},
		{"DirectSQL", "sql_handle_create", "sql.Open", "fact"},
	} {
		owner := "example.com/instructionconstructors::" + want.owner
		found := false
		for _, edge := range report.Relationships {
			if edge.From == owner && edge.Kind == want.kind && edge.Evidence.File == "calls.go" &&
				strings.Contains(edge.Evidence.Snippet, want.call) {
				if edge.Certainty != want.certainty {
					t.Errorf("%s %s candidate certainty=%s, want %s", want.owner, want.kind, edge.Certainty, want.certainty)
				}
				if want.kind == "sql_handle_create" && !reportHasNode(report, edge.To, "database") {
					t.Errorf("%s SQL constructor candidate %s has no database resource node", want.owner, edge.To)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s missing %s candidate with source evidence %q", want.owner, want.kind, want.call)
		}
	}

	for _, want := range []struct{ owner, call string }{
		{"GoContext", "go context.WithTimeout"},
		{"DeferContext", "defer context.WithCancel"},
	} {
		if !reportHasBoundary(report, "example.com/instructionconstructors::"+want.owner, "unobserved_cancel", "calls.go", want.call) {
			t.Errorf("%s missing unobserved-cancel uncertainty for %s", want.owner, want.call)
		}
	}
	for _, want := range []struct{ owner, call string }{
		{"GoSQL", "go sql.Open"},
		{"DeferSQL", "defer sql.Open"},
	} {
		count := 0
		for _, boundary := range report.Boundaries {
			if boundary.Node == "example.com/instructionconstructors::"+want.owner && boundary.Kind == "discarded_sql_handle" &&
				boundary.Evidence.File == "calls.go" && strings.Contains(boundary.Evidence.Snippet, want.call) {
				count++
			}
		}
		if count != 1 {
			t.Errorf("%s has %d discarded-database-handle boundaries for %s, want exactly one", want.owner, count, want.call)
		}
	}
	if !reportHasBoundary(report, "example.com/instructionconstructors::GoSQL", "concurrency", "calls.go", "go sql.Open") {
		t.Error("go SQL constructor lost goroutine execution uncertainty")
	}
	if !reportHasBoundary(report, "example.com/instructionconstructors::DeferSQL", "cleanup", "calls.go", "defer sql.Open") {
		t.Error("deferred SQL constructor lost cleanup-registration uncertainty")
	}
}

func reportHasNode(report Report, id, kind string) bool {
	for _, node := range report.Nodes {
		if node.ID == id && node.Kind == kind {
			return true
		}
	}
	return false
}

func reportHasBoundary(report Report, owner, kind, file, snippet string) bool {
	for _, boundary := range report.Boundaries {
		if boundary.Node == owner && boundary.Kind == kind && boundary.Evidence.File == file &&
			strings.Contains(boundary.Evidence.Snippet, snippet) {
			return true
		}
	}
	return false
}
