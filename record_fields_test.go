package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateFieldConnectsSiblingEntryPointsAndConstructors(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/fields\n\ngo 1.27.0\n",
		"fields.go": `package fields
type Counter struct { remaining int; other int }
func New(n int) *Counter {return &Counter{remaining:n}}
func (c *Counter) Acquire(n int) bool { if c.remaining<n {return false}; c.remaining-=n; return true }
func (c *Counter) Release(n int) {c.remaining+=n}
func (c *Counter) Remaining() int {return c.remaining}
func (c *Counter) Other() int {return c.other}
type Different struct {remaining int}
func (d *Different) Read() int {return d.remaining}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{Root: root, Seeds: []string{"Counter.Acquire"}, Depth: 2, MaxNodes: 100}
	fresh, saved, err := TraceWithAnalysis(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: opts.Seeds, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, name := range []string{"Counter.Release", "Counter.Remaining", "New"} {
			if !hasName(report, name) {
				t.Errorf("missing sibling/constructor %s", name)
			}
		}
		for _, name := range []string{"Counter.Other", "Different.Read"} {
			if hasName(report, name) {
				t.Errorf("unrelated field linked %s", name)
			}
		}
		reads, writes := false, false
		for _, edge := range report.Relationships {
			if edge.To == "field:example.com/fields::Counter.remaining" && edge.From == "example.com/fields::Counter.Acquire" {
				reads = reads || edge.Kind == "field_read"
				writes = writes || edge.Kind == "field_write"
			}
		}
		if !reads || !writes {
			t.Error("compound assignment/read lost typed field roles")
		}
		model := false
		for _, boundary := range report.Boundaries {
			model = model || boundary.Kind == "record_field_model"
		}
		if !model {
			t.Error("missing field-instance/temporal uncertainty")
		}
	}
}

func TestFieldShapesPreserveDefinitionsAndAccessRoles(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/fieldshapes\n\ngo 1.27.0\n",
		"shapes.go": `package fieldshapes
type State[T any] struct { count int; values []int }
type Alias = State[int]
type Wrapper struct { *State[int] }
func Promoted(w *Wrapper) {w.count++}
func ReplaceEmbedded(w *Wrapper, s *State[int]) {w.State=s}
func Generic[T any](s *State[T]) int {return s.count}
func AliasRead(s *Alias) int {return s.count}
func Positional() State[int] {return State[int]{1,nil}}
func Zero() *State[int] {return new(State[int])}
func Address(s *State[int]) *int {return &s.count}
func Contents(s *State[int]) {s.values[0]=1}
func Index(s *State[int], dst []int) {dst[s.count]=1}
func Copy(s *State[int]) State[int] {return *s}
func Replace(s *State[int]) {*s=State[int]{} }
func ValueCopy(s State[int]) State[int] {return s}
var Shared=State[int]{count:1}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Promoted", "ReplaceEmbedded", "Generic", "AliasRead", "Positional", "Zero", "Address", "Contents", "Index", "Copy", "Replace", "ValueCopy", "(package init)"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 2, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 2, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for owner, kind := range map[string]string{"Promoted": "field_write", "Generic": "field_read", "AliasRead": "field_read", "Positional": "field_write", "Zero": "field_zero_initialize", "Address": "field_address", "Index": "field_read", "Copy": "field_record_read", "Replace": "field_record_write", "ValueCopy": "field_record_read", "(package init)": "field_write"} {
			found := false
			for _, edge := range report.Relationships {
				found = found || edge.From == "example.com/fieldshapes::"+owner && edge.To == "field:example.com/fieldshapes::State.count" && edge.Kind == kind
			}
			if !found {
				t.Errorf("%s missing %s for original field declaration", owner, kind)
			}
		}
		content, promoted := false, false
		for _, edge := range report.Relationships {
			content = content || edge.From == "example.com/fieldshapes::Contents" && edge.To == "field:example.com/fieldshapes::State.values" && edge.Kind == "field_content_write"
			promoted = promoted || edge.From == "example.com/fieldshapes::Promoted" && edge.To == "field:example.com/fieldshapes::Wrapper.State" && edge.Kind == "field_embedding_access"
			if edge.From == "example.com/fieldshapes::Index" && edge.To == "field:example.com/fieldshapes::State.count" && edge.Kind == "field_write" {
				t.Error("index operand misclassified as field write")
			}
		}
		if !content || !promoted {
			t.Error("container write or implicit promoted path lost")
		}
	}
}

func TestRecordAccessDistinguishesCopiesPointersAndShadowedBuiltins(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/recordroles\n\ngo 1.27.0\n",
		"roles.go": `package recordroles
type State struct { count int }
func consume(State) {}
func pointer(*State) {}
func Copies(s State, out chan State) { consume(s); out <- s; _ = s == State{} }
func Pointers(s *State) { pointer(s); _ = s }
func Shadow(s *State) *State { new := func() *State { return s }; return new() }
func Discard(s State) { _ = s }
func LocalZero() { var s State; pointer(&s) }
var Shared State
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Copies", "Pointers", "Shadow", "Discard", "LocalZero", "(package init)"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 1, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		seen := map[string]bool{}
		for _, edge := range report.Relationships {
			if edge.To != "field:example.com/recordroles::State.count" {
				continue
			}
			seen[edge.From+"/"+edge.Kind] = true
			if edge.From == "example.com/recordroles::Pointers" && edge.Kind == "field_record_read" {
				t.Error("pointer passed as whole record copy")
			}
			if edge.From == "example.com/recordroles::Shadow" && edge.Kind == "field_zero_initialize" {
				t.Error("shadowed new treated as builtin")
			}
			if edge.From == "example.com/recordroles::Discard" && edge.Kind == "field_record_write" {
				t.Error("blank discard treated as record storage write")
			}
		}
		for _, item := range []string{"Copies/field_record_read", "Discard/field_record_read", "LocalZero/field_zero_initialize", "(package init)/field_zero_initialize"} {
			if !seen["example.com/recordroles::"+item] {
				t.Errorf("missing %s", item)
			}
		}
	}
}

func TestEventDiscriminatorFieldInventoryRemainsExplicitlyExplorable(t *testing.T) {
	_, saved, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Publish"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, resume := range []bool{false, true} {
		var report Report
		if resume {
			report, err = Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Publish"}, Depth: 2, MaxNodes: 100})
		} else {
			report, err = Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Publish"}, Depth: 2, MaxNodes: 100})
		}
		if err != nil {
			t.Fatal(err)
		}
		if hasName(report, "Another") || !hasName(report, "Consume") {
			t.Error("event value scope lost")
		}
		excluded := false
		for _, boundary := range report.Boundaries {
			if boundary.Kind == "field_scope_exclusion" {
				for _, candidate := range boundary.Candidates {
					excluded = excluded || candidate == "example.com/sample::Another"
				}
				for _, candidate := range boundary.Examples {
					excluded = excluded || candidate == "example.com/sample::Another"
				}
			}
		}
		if !excluded {
			t.Error("excluded discriminator user missing from inventory")
		}
	}
	field := ""
	for _, node := range saved.Nodes {
		if node.Name == "Event.EventType" {
			field = node.ID
		}
	}
	if field == "" {
		t.Fatal("field missing from saved inventory")
	}
	for _, resume := range []bool{false, true} {
		var report Report
		if resume {
			report, err = Explore(context.Background(), saved, ExploreOptions{Seeds: []string{field}, Depth: 1, MaxNodes: 100})
		} else {
			report, err = Trace(context.Background(), Options{Root: "testdata/sample", Seeds: []string{field}, Depth: 1, MaxNodes: 100})
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"Publish", "Consume", "Another"} {
			if !hasName(report, name) {
				t.Errorf("explicit field seed missing %s", name)
			}
		}
	}
}
