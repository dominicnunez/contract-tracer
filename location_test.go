package contracttrace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordFieldLocationSurvivesSerializationAndRetainsUsers(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/fieldlocation\n\ngo 1.27.0\n",
		"state.go": `package fieldlocation
type Counter struct {
 remaining [
 2]int
 other int
}

func Read(c *Counter) int { return c.remaining[0] }
func Write(c *Counter, n int) { c.remaining[0] = n }
func Other(c *Counter) int { return c.other }
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Locations: []string{"state.go:4"}, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Locations: []string{"state.go:4"}, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		if len(report.Seeds) != 1 || report.Seeds[0] != "field:example.com/fieldlocation::Counter.remaining" {
			t.Errorf("wrong declaration seed: %v", report.Seeds)
		}
		if !hasName(report, "Read") || !hasName(report, "Write") || hasName(report, "Other") {
			t.Error("field location lost precise sibling scope")
		}
	}
	id := "field:example.com/fieldlocation::Counter.remaining"
	span, ok := loaded.DeclarationRanges[id]
	if !ok || span.First != 3 || span.Last != 4 {
		t.Fatalf("wrong saved field span: %+v", span)
	}
	loaded.DeclarationRanges[id] = SourceRange{4, 4}
	if _, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: []string{id}, Depth: 1, MaxNodes: 100}); err == nil {
		t.Error("range inconsistent with declaration evidence accepted")
	}
	loaded.DeclarationRanges = nil
	if _, err := Explore(context.Background(), loaded, ExploreOptions{Locations: []string{"state.go:3"}, Depth: 1, MaxNodes: 100}); err != nil {
		t.Errorf("legacy declaration-line fallback: %v", err)
	}
	if _, err := Explore(context.Background(), loaded, ExploreOptions{Locations: []string{"state.go:4"}, Depth: 1, MaxNodes: 100}); err == nil {
		t.Error("legacy snapshot invented continuation range")
	}
}

func TestSourceLocationSeed(t *testing.T) {
	r, err := Trace(context.Background(), Options{Root: "testdata/sample", Locations: []string{"sample.go:8"}, Depth: 3, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "Validate") || !hasName(r, "Recovery") {
		t.Error("source location did not start contract discovery at its containing function")
	}
	_, err = Trace(context.Background(), Options{Root: "testdata/sample", Locations: []string{"../outside.go:8"}, Depth: 3, MaxNodes: 150})
	if err == nil {
		t.Error("outside-root location accepted")
	}
}

func TestGlobalInitializerLocationSeedSurvivesSavedExploration(t *testing.T) {
	source, err := os.ReadFile("testdata/sample/databases.go")
	if err != nil {
		t.Fatal(err)
	}
	position := strings.Index(string(source), "privateTextConsumer, // location seed")
	if position < 0 {
		t.Fatal("missing independent source-location fixture")
	}
	location := fmt.Sprintf("databases.go:%d", strings.Count(string(source[:position]), "\n")+1)
	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Locations: []string{location}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(string(serialized)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Locations: []string{location}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		seed, callback := false, false
		for _, node := range report.Nodes {
			seed = seed || node.Name == "PublicLocationCallbacks" && node.Distance == 0
		}
		for _, edge := range report.Relationships {
			callback = callback || edge.Kind == "callback_global_escape" && strings.HasSuffix(edge.To, "::privateTextConsumer")
		}
		if !seed || !callback {
			t.Fatalf("global location lost scope: seed=%t callback=%t", seed, callback)
		}
	}
	for id, span := range loaded.DeclarationRanges {
		if !strings.HasSuffix(id, "::PublicLocationCallbacks") {
			continue
		}
		loaded.DeclarationRanges[id] = SourceRange{span.First, span.First - 1}
		if _, err := Explore(context.Background(), loaded, ExploreOptions{Locations: []string{location}, Depth: 2, MaxNodes: 100}); err == nil {
			t.Fatal("malformed saved declaration range accepted")
		}
		loaded.DeclarationRanges = nil // Older analyses have only declaration evidence.
		declaration := fmt.Sprintf("databases.go:%d", span.First)
		if _, err := Explore(context.Background(), loaded, ExploreOptions{Locations: []string{declaration}, Depth: 2, MaxNodes: 100}); err != nil {
			t.Fatalf("legacy declaration-line seed: %v", err)
		}
		if _, err := Explore(context.Background(), loaded, ExploreOptions{Locations: []string{location}, Depth: 2, MaxNodes: 100}); err == nil {
			t.Fatal("legacy graph invented an initializer range")
		}
		return
	}
	t.Fatal("saved graph missing global declaration range")
}

func TestGlobalLocationRejectsAmbiguousDeclarations(t *testing.T) {
	ix := &index{root: t.TempDir(), funcs: map[string]*function{}, declarationRanges: map[string]SourceRange{}}
	for _, id := range []string{"global:sample::First", "global:sample::Second"} {
		ix.funcs[id] = &function{node: Node{ID: id, Kind: "global", Evidence: Evidence{File: "globals.go", Line: 3}}}
		ix.declarationRanges[id] = SourceRange{3, 5}
	}
	if _, err := ix.locationSeeds([]string{"globals.go:4"}); err == nil || !strings.Contains(err.Error(), "ambiguous location") {
		t.Fatalf("shared declaration must not guess an owner: %v", err)
	}
}

func TestPrivateGlobalLocationDiscoversReadersAndWriters(t *testing.T) {
	source, err := os.ReadFile("testdata/sample/databases.go")
	if err != nil {
		t.Fatal(err)
	}
	position := strings.Index(string(source), "value: \"initial\", // private global location seed")
	if position < 0 {
		t.Fatal("missing private global source fixture")
	}
	location := fmt.Sprintf("databases.go:%d", strings.Count(string(source[:position]), "\n")+1)
	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Locations: []string{location}, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Locations: []string{location}, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		expected := map[string]string{"readPrivateLocationState": "global_read", "siblingPrivateLocationState": "global_read", "setPrivateLocationState": "global_write"}
		for _, edge := range report.Relationships {
			for owner, kind := range expected {
				if strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == kind && edge.To == "global:example.com/sample::privateLocationState" {
					delete(expected, owner)
				}
			}
		}
		if len(expected) != 0 {
			t.Fatalf("missing private state paths: %v", expected)
		}
		if hasName(report, "writeUnrelatedPrivateState") {
			t.Fatal("unrelated storage joined private global")
		}
		for _, boundary := range report.Boundaries {
			if boundary.Node == "global:example.com/sample::privateLocationState" && strings.HasPrefix(boundary.Kind, "external_") {
				t.Fatal("private storage classified as exported")
			}
		}
	}
}

func TestRecordFieldLocationRejectsGroupedDeclarationAmbiguity(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod":    "module example.com/groupedfields\n\ngo 1.27.0\n",
		"fields.go": "package groupedfields\ntype Pair struct {\n first, second [\n 2]int\n}\nfunc Read(p *Pair) int { return p.first[0] + p.second[0] }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Read"}, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	for _, resume := range []bool{false, true} {
		if resume {
			_, err = Explore(context.Background(), loaded, ExploreOptions{Locations: []string{"fields.go:4"}, Depth: 1, MaxNodes: 100})
		} else {
			_, err = Trace(context.Background(), Options{Root: root, Locations: []string{"fields.go:4"}, Depth: 1, MaxNodes: 100})
		}
		if err == nil || !strings.Contains(err.Error(), "ambiguous location") || !strings.Contains(err.Error(), "Pair.first") || !strings.Contains(err.Error(), "Pair.second") {
			t.Errorf("ambiguous declaration guessed an owner: %v", err)
		}
	}
	for i := range loaded.Nodes {
		if loaded.Nodes[i].ID == "field:example.com/groupedfields::Pair.first" {
			loaded.Nodes[i].Kind = "table"
		}
	}
	if _, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: []string{"Read"}, Depth: 1, MaxNodes: 100}); err == nil {
		t.Error("declaration range accepted for a non-declaration resource")
	}
}

func TestEventDiscriminatorDeclarationLineExplicitlySeedsAllUsers(t *testing.T) {
	source, err := os.ReadFile("testdata/sample/sample.go")
	if err != nil {
		t.Fatal(err)
	}
	position := strings.Index(string(source), "type Event struct")
	if position < 0 {
		t.Fatal("event declaration fixture missing")
	}
	location := fmt.Sprintf("sample.go:%d", strings.Count(string(source[:position]), "\n")+1)
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Locations: []string{location}, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Locations: []string{location}, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, name := range []string{"Publish", "Consume", "Another"} {
			if !hasName(report, name) {
				t.Errorf("field declaration location missing %s", name)
			}
		}
		if len(report.Seeds) != 1 || report.Seeds[0] != "field:example.com/sample::Event.EventType" {
			t.Errorf("wrong explicit field seed: %v", report.Seeds)
		}
	}
}
