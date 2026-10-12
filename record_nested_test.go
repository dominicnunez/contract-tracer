package contracttrace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedValueCopiesRetainOverwriteScopeAndInitialization(t *testing.T) {
	root := t.TempDir()
	source := `package nested
type Budget struct { remaining int }
type Account struct { budget Budget; other int }
type Alias = Account
type Batch struct { values [2]Account }
type Generic[T any] struct { value T }
type References struct { pointer *Budget; slice []Budget; lookup map[int]Budget; empty [0]Budget; _ Budget }
func Reserve(b *Budget) { b.remaining-- }
func Release(b *Budget) { b.remaining++ }
func Reset(a *Account, next Account) { *a = next }
func ReadOther(a *Account) int { return a.other }
func CopyAlias(a Alias) Alias { return a }
func CopyBatch(b Batch) Batch { return b }
func CopyGeneric(g Generic[Budget]) Generic[Budget] { return g }
func Forward(a Account, ch chan Account) Account { ch <- a; return a }
func Bind(a Account) { b := a; _ = b }
func Named() (a Account) { return }
func Zero() *Account { return new(Account) }
func Empty() *Account { return &Account{} }
func NestedOmitted() *Batch { return &Batch{} }
func Explicit(a Account) *Account { return &Account{budget:a.budget} }
func ArrayNew() *[2]Account { return new([2]Account) }
func ArrayEmpty() [2]Account { return [2]Account{} }
func ArrayPartial(a Account) [3]Account { return [3]Account{1:a} }
func ArrayFull(a Account) [2]Account { return [2]Account{0:a, 1:a} }
func (Account) Value() {}
func (a *Account) Bound() func() { return a.Value }
func CopyReferences(r References) References { return r }
func Pointer(b *Budget) *Budget { return b }
func Slice(b []Budget) []Budget { return b }
func EmptyArray(b [0]Budget) [0]Budget { return b }
`
	for name, contents := range map[string]string{"go.mod": "module example.com/nested\n\ngo 1.27.0\n", "nested.go": source} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Reserve"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Reserve"}, Depth: 2, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, name := range []string{"Reserve", "Release", "Reset", "CopyAlias", "CopyBatch", "CopyGeneric", "Forward", "Bind", "Named", "Zero", "Empty", "NestedOmitted", "Explicit", "ArrayNew", "ArrayEmpty", "ArrayPartial", "ArrayFull", "Account.Value", "Account.Bound"} {
			if !hasName(report, name) {
				t.Errorf("nested state investigation missed %s", name)
			}
		}
		for _, name := range []string{"ReadOther", "CopyReferences", "Pointer", "Slice", "EmptyArray"} {
			if hasName(report, name) {
				t.Errorf("descriptor/blank/empty/unrelated copy invented nested contents: %s", name)
			}
		}
	}
	target := "field:example.com/nested::Budget.remaining"
	for _, expected := range []struct{ owner, kind string }{
		{"Reset", "field_record_write"}, {"CopyBatch", "field_record_read"}, {"CopyGeneric", "field_record_read"},
		{"Bind", "field_record_write"}, {"Named", "field_zero_initialize"}, {"Zero", "field_zero_initialize"},
		{"Empty", "field_zero_initialize"}, {"NestedOmitted", "field_zero_initialize"},
		{"Explicit", "field_write"}, {"ArrayNew", "field_zero_initialize"}, {"ArrayEmpty", "field_zero_initialize"},
		{"ArrayPartial", "field_zero_initialize"}, {"ArrayPartial", "field_write"}, {"ArrayFull", "field_write"},
		{"Account.Value", "field_receiver_parameter"}, {"Account.Bound", "field_receiver_copy"},
	} {
		found := false
		for _, edge := range saved.Relationships {
			if edge.From == "example.com/nested::"+expected.owner && edge.To == target && edge.Kind == expected.kind && edge.Certainty == "fact" && edge.Evidence.File == "nested.go" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing source-backed nested role %s %s", expected.owner, expected.kind)
		}
	}
	for _, edge := range saved.Relationships {
		if edge.From == "example.com/nested::ArrayFull" && edge.To == target && edge.Kind == "field_zero_initialize" {
			t.Error("fully specified fixed array invented omitted element initialization")
		}
	}
}
