package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalAddressesPassedToExternalCallsRemainCandidates(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "client")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module example.com/client\n\ngo 1.27.0\n\nrequire example.com/external v0.0.0\nreplace example.com/external => ../external\n",
		"client.go": `package client
import "example.com/external"

var record external.Record
var values [1]int
var unrelated int

func Seed() {
    external.TakeRecord(&record)
    field := &record.Value
    external.TakeInt(field)
    external.TakeInt(&values[0])
    forward(&record.Value)
    defer external.TakeInt(&record.Value)
    go external.TakeInt(&values[0])
    record.Value = 1
    _ = record.Value
}

func forward(value *int) { external.TakeInt(value) }
`,
		"../external/go.mod": "module example.com/external\n\ngo 1.27.0\n",
		"../external/api.go": `package external
type Record struct { Value int }
func TakeRecord(*Record) {}
func TakeInt(*int) {}
`,
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"Seed"}, Depth: 4, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	wants := []struct {
		global, snippet string
	}{
		{"record", "external.TakeRecord(&record)"},
		{"record", "external.TakeInt(field)"},
		{"values", "external.TakeInt(&values[0])"},
		{"record", "defer external.TakeInt(&record.Value)"},
		{"values", "go external.TakeInt(&values[0])"},
		{"record", "external.TakeInt(value)"},
	}
	for _, want := range wants {
		global := "global:example.com/client::" + want.global
		found := false
		for _, edge := range report.Relationships {
			if edge.From == "example.com/client::Seed" || edge.From == "example.com/client::forward" {
				found = found || edge.To == global && edge.Kind == "global_escape" && edge.Certainty == "possible" && strings.Contains(edge.Evidence.Snippet, want.snippet)
			}
		}
		if !found {
			t.Errorf("missing possible global escape to %s at %q", global, want.snippet)
		}
	}
	helperCall, helperArgument := false, false
	var helperEdges []Relationship
	for _, edge := range report.Relationships {
		if edge.From != "example.com/client::Seed" || edge.To != "example.com/client::forward" {
			continue
		}
		helperEdges = append(helperEdges, edge)
		helperCall = helperCall || edge.Kind == "call" && strings.Contains(edge.Evidence.Snippet, "forward(&record.Value)")
		helperArgument = helperArgument || edge.Kind == "argument_flow" && strings.Contains(edge.Evidence.Snippet, "forward(&record.Value)") && contains(edge.Values, "global:example.com/client.record.field:0")
	}
	if !helperCall || !helperArgument {
		t.Fatalf("local helper path lost the global-backed argument: call=%t argument=%t edges=%+v", helperCall, helperArgument, helperEdges)
	}
	read, write := false, false
	for _, edge := range report.Relationships {
		if edge.To != "global:example.com/client::record" || edge.From != "example.com/client::Seed" {
			continue
		}
		read = read || edge.Kind == "global_read" && strings.Contains(edge.Evidence.Snippet, "_ = record.Value")
		write = write || edge.Kind == "global_write" && strings.Contains(edge.Evidence.Snippet, "record.Value = 1")
	}
	if !read || !write {
		t.Fatalf("direct global access controls missing: read=%t write=%t", read, write)
	}
	unrelated := false
	for _, edge := range report.Relationships {
		unrelated = unrelated || edge.From == "example.com/client::Seed" && edge.To == "global:example.com/client::unrelated" && edge.Kind == "global_escape"
	}
	if unrelated {
		t.Fatal("unpassed global entered the external escape candidates")
	}
	unresolved := false
	for _, boundary := range report.Boundaries {
		unresolved = unresolved || (boundary.Kind == "unresolved_global_escape" && boundary.Node == "example.com/client::Seed" && strings.Contains(boundary.Evidence.Snippet, "external.TakeRecord(&record)") && strings.Contains(boundary.Reason, "no specific mutation is established"))
	}
	if !unresolved {
		t.Fatal("possible external global mutation was not disclosed as unresolved")
	}
}
