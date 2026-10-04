package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZeroRecordCallbacksSurviveContainerSelections(t *testing.T) {
	root := t.TempDir()
	source := `package recordnil
import "sync"
type holder struct { task func() }
type nestedHolder struct { inner holder }
func knownTask() {}
func ArrayRecordPartial(index int) { var values [2]holder; values[0].task = knownTask; var group sync.WaitGroup; group.Go(values[index].task) }
func ArrayRecordConditional(index int, flag bool) { var values [2]holder; if flag { values[0].task = knownTask }; var group sync.WaitGroup; group.Go(values[index].task) }
func SliceRecordPartial(index int) { values := make([]holder, 2); values[0].task = knownTask; var group sync.WaitGroup; group.Go(values[index].task) }
func SliceRecordDynamicPartial(length, index int) { if length <= 0 { return }; values := make([]holder, length); values[0].task = knownTask; var group sync.WaitGroup; group.Go(values[index].task) }
func SliceRecordDynamicConditional(length, index int, flag bool) { if length <= 0 { return }; values := make([]holder, length); if flag { values[0].task = knownTask }; var group sync.WaitGroup; group.Go(values[index].task) }
func SliceRecordDynamicEmpty(index int) { values := make([]holder, 0); var group sync.WaitGroup; group.Go(values[index].task) }
func MapRecordPartial(key string) { values := map[string]holder{"known": {task: knownTask}}; var group sync.WaitGroup; group.Go(values[key].task) }
func MapRecordMutated(key string, remove bool) { values := map[string]holder{"known": {task: knownTask}}; if remove { delete(values, "known") }; values[key] = holder{task: knownTask}; var group sync.WaitGroup; group.Go(values[key].task) }
func MapRecordOutside(values map[string]holder, key string) { var group sync.WaitGroup; group.Go(values[key].task) }
func NestedArrayRecordPartial(index int) { var values [2]nestedHolder; values[0].inner.task = knownTask; var group sync.WaitGroup; group.Go(values[index].inner.task) }
func ArrayRecordFull(index int) { values := [2]holder{{task: knownTask}, {task: knownTask}}; var group sync.WaitGroup; group.Go(values[index].task) }
func SliceRecordFull(index int) { values := []holder{{task: knownTask}, {task: knownTask}}; var group sync.WaitGroup; group.Go(values[index].task) }
func MapRecordKnownKey() { values := map[string]holder{"known": {task: knownTask}}; var group sync.WaitGroup; group.Go(values["known"].task) }
func ArrayRecordExplicitNil(index int) { values := [2]holder{{task: knownTask}, {task: nil}}; var group sync.WaitGroup; group.Go(values[index].task) }
func NilParentPointer() { var parent *holder; var group sync.WaitGroup; group.Go(parent.task) }
`
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recordnil\n\ngo 1.27.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "records.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	sites := map[string]string{
		"ArrayRecordPartial": "group.Go(values[index].task)", "ArrayRecordConditional": "group.Go(values[index].task)",
		"SliceRecordPartial": "group.Go(values[index].task)", "SliceRecordDynamicPartial": "group.Go(values[index].task)",
		"SliceRecordDynamicConditional": "group.Go(values[index].task)", "SliceRecordDynamicEmpty": "group.Go(values[index].task)",
		"MapRecordPartial": "group.Go(values[key].task)", "MapRecordMutated": "group.Go(values[key].task)",
		"MapRecordOutside": "group.Go(values[key].task)", "NestedArrayRecordPartial": "group.Go(values[index].inner.task)",
		"ArrayRecordFull": "group.Go(values[index].task)", "SliceRecordFull": "group.Go(values[index].task)",
		"MapRecordKnownKey": `group.Go(values["known"].task)`, "ArrayRecordExplicitNil": "group.Go(values[index].task)",
		"NilParentPointer": "group.Go(parent.task)",
	}
	seeds := make([]string, 0, len(sites))
	for name := range sites {
		seeds = append(seeds, name)
	}
	report, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 5, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]string{}
	knownTasks := map[string]bool{}
	for _, edge := range analysis.Relationships {
		for owner, snippet := range sites {
			if edge.From == "example.com/recordnil::"+owner && strings.Contains(edge.Evidence.Snippet, snippet) && edge.Kind == "waitgroup_go" {
				operations[owner] = edge.To
			}
		}
	}
	for _, edge := range analysis.Relationships {
		if edge.Kind == "waitgroup_task" && edge.To == "example.com/recordnil::knownTask" {
			for owner, operation := range operations {
				if operation == edge.From {
					knownTasks[owner] = true
				}
			}
		}
	}
	boundaries := map[string][]Boundary{}
	for _, boundary := range report.Boundaries {
		if boundary.Kind != "unresolved_waitgroup_task" {
			continue
		}
		owner := strings.TrimPrefix(boundary.Node, "example.com/recordnil::")
		boundaries[owner] = append(boundaries[owner], boundary)
	}
	for owner, snippet := range sites {
		if operations[owner] == "" {
			t.Errorf("%s lost its source-backed operation edge at %s", owner, snippet)
		}
		for _, edge := range analysis.Relationships {
			if (edge.Kind == "waitgroup_go" && edge.From == "example.com/recordnil::"+owner || edge.Kind == "waitgroup_task" && edge.From == operations[owner]) &&
				(edge.Evidence.File != "records.go" || edge.Evidence.Line == 0 || edge.Evidence.Snippet == "") {
				t.Errorf("%s edge lacks source evidence: %+v", owner, edge)
			}
		}
	}
	for _, owner := range []string{"ArrayRecordPartial", "ArrayRecordConditional", "SliceRecordPartial", "SliceRecordDynamicPartial", "SliceRecordDynamicConditional", "MapRecordPartial", "MapRecordMutated", "NestedArrayRecordPartial"} {
		if !knownTasks[owner] || !hasNilTaskBoundary(boundaries[owner]) {
			t.Errorf("%s must retain its known task and expose the zero-record nil candidate: known=%t boundaries=%+v", owner, knownTasks[owner], boundaries[owner])
		}
		if owner == "MapRecordMutated" && !hasNilAndUnknownTaskBoundary(boundaries[owner]) {
			t.Errorf("%s must retain nil and unresolved callback alternatives: %+v", owner, boundaries[owner])
		}
	}
	for _, owner := range []string{"ArrayRecordFull", "SliceRecordFull", "MapRecordKnownKey"} {
		if !knownTasks[owner] || len(boundaries[owner]) != 0 {
			t.Errorf("%s full-initialization control changed: known=%t boundaries=%+v", owner, knownTasks[owner], boundaries[owner])
		}
	}
	if !knownTasks["ArrayRecordExplicitNil"] || !hasNilTaskBoundary(boundaries["ArrayRecordExplicitNil"]) {
		t.Errorf("explicit nil field control lost its known task or nil boundary: known=%t boundaries=%+v", knownTasks["ArrayRecordExplicitNil"], boundaries["ArrayRecordExplicitNil"])
	}
	if knownTasks["SliceRecordDynamicEmpty"] || hasNilTaskBoundary(boundaries["SliceRecordDynamicEmpty"]) {
		t.Errorf("zero-length dynamic slice must not manufacture an element callback: known=%t boundaries=%+v", knownTasks["SliceRecordDynamicEmpty"], boundaries["SliceRecordDynamicEmpty"])
	}
	if knownTasks["MapRecordOutside"] || len(boundaries["MapRecordOutside"]) == 0 || hasNilTaskBoundary(boundaries["MapRecordOutside"]) {
		t.Errorf("outside map read must remain generic unresolved without typed nil: known=%t boundaries=%+v", knownTasks["MapRecordOutside"], boundaries["MapRecordOutside"])
	}
	if knownTasks["NilParentPointer"] || len(boundaries["NilParentPointer"]) == 0 || hasNilTaskBoundary(boundaries["NilParentPointer"]) {
		t.Errorf("nil-parent pointer must stay generic/unresolved without a fabricated nil callback: known=%t boundaries=%+v", knownTasks["NilParentPointer"], boundaries["NilParentPointer"])
	}
	if report.Coverage.Truncated || report.ContractComplete {
		t.Fatalf("expected untruncated but incomplete scope: coverage=%+v complete=%t", report.Coverage, report.ContractComplete)
	}
}

func hasNilTaskBoundary(boundaries []Boundary) bool {
	for _, boundary := range boundaries {
		reason := strings.ToLower(boundary.Reason)
		if strings.Contains(reason, "nil callback alternative") || strings.Contains(reason, "typed nil") || strings.Contains(reason, "possible nil and unresolved callback-value alternative") {
			return true
		}
	}
	return false
}

func hasNilAndUnknownTaskBoundary(boundaries []Boundary) bool {
	for _, boundary := range boundaries {
		reason := strings.ToLower(boundary.Reason)
		if strings.Contains(reason, "possible nil and unresolved callback-value alternative") {
			return true
		}
	}
	return false
}
