package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWaitGroupInterfaceCandidatesRetainOutsideAlternatives(t *testing.T) {
	root := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/waitgroup-interface\n\ngo 1.27.0\n")
	write("waitgroup.go", `package app

import "sync"

type Group interface {
	Add(int)
	Done()
	Wait()
	Go(func())
}

func knownTask() {}

func PublicMixed(input Group, chooseOutside bool, task func()) {
	var local sync.WaitGroup
	var selected Group = &local
	if chooseOutside {
		selected = input
	}
	selected.Add(1)
	done := selected.Done
	done()
	selected.Wait()
	selected.Go(knownTask)
	selected.Go(task)
}

func PublicOutsideOnly(input *sync.WaitGroup) {
	input.Add(1)
	done := input.Done
	done()
}

func UnmarkedInterfaceOnly(input Group) {
	input.Add(1)
	done := input.Done
	done()
	input.Wait()
}

func closedKnownOnly() {
	var local sync.WaitGroup
	var selected Group = &local
	selected.Add(1)
	done := selected.Done
	done()
	selected.Wait()
}
`)

	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{
		Root:     root,
		Seeds:    []string{"PublicMixed", "PublicOutsideOnly", "UnmarkedInterfaceOnly", "closedKnownOnly"},
		Depth:    4,
		MaxNodes: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err := WriteAnalysis(&saved, analysis, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(bytes.NewReader(saved.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{
		Seeds:    []string{"PublicMixed", "PublicOutsideOnly", "UnmarkedInterfaceOnly", "closedKnownOnly"},
		Depth:    4,
		MaxNodes: 200,
	})
	if err != nil {
		t.Fatal(err)
	}

	for reportName, report := range map[string]Report{"fresh": fresh, "resumed": resumed} {
		mixedGroup := ""
		outsideGroup := ""
		known := map[string]bool{"waitgroup_add": false, "waitgroup_done": false, "waitgroup_wait": false, "waitgroup_go": false}
		for _, edge := range report.Relationships {
			if strings.HasSuffix(edge.From, "::PublicMixed") && edge.Kind == "waitgroup_add" && strings.Contains(edge.Evidence.Snippet, "selected.Add") {
				mixedGroup = edge.To
			}
			if strings.HasSuffix(edge.From, "::PublicOutsideOnly") && edge.Kind == "waitgroup_add" && strings.Contains(edge.Evidence.Snippet, "input.Add") {
				outsideGroup = edge.To
			}
		}
		if mixedGroup == "" || outsideGroup == "" {
			t.Fatalf("%s report omitted source-backed known WaitGroup candidates: mixed=%q outside=%q", reportName, mixedGroup, outsideGroup)
		}
		for _, edge := range report.Relationships {
			if strings.HasSuffix(edge.From, "::PublicMixed") && edge.To == mixedGroup {
				if _, ok := known[edge.Kind]; ok {
					known[edge.Kind] = true
				}
			}
		}
		knownTask := false
		for _, edge := range report.Relationships {
			knownTask = knownTask || edge.Kind == "waitgroup_task" && edge.From == mixedGroup && strings.HasSuffix(edge.To, "::knownTask")
		}
		if !knownTask {
			t.Errorf("%s report lost the local Go task candidate for the known receiver", reportName)
		}
		for kind, found := range known {
			if !found {
				t.Errorf("%s report missing known interface-backed %s edge", reportName, kind)
			}
		}
		for _, snippet := range []string{"selected.Add(1)", "done()", "selected.Wait()", "selected.Go(knownTask)"} {
			found := false
			for _, boundary := range report.Boundaries {
				found = found || boundary.Kind == "unresolved_waitgroup" && strings.HasSuffix(boundary.Node, "::PublicMixed") && strings.Contains(boundary.Evidence.Snippet, snippet)
			}
			if !found {
				t.Errorf("%s known-plus-outside operation %q lacks its unresolved boundary", reportName, snippet)
			}
		}
		goBoundary, taskBoundary := false, false
		for _, boundary := range report.Boundaries {
			goBoundary = goBoundary || boundary.Kind == "unresolved_waitgroup" && strings.HasSuffix(boundary.Node, "::PublicMixed") && strings.Contains(boundary.Evidence.Snippet, "selected.Go(task)")
			taskBoundary = taskBoundary || boundary.Kind == "unresolved_waitgroup_task" && strings.HasSuffix(boundary.Node, "::PublicMixed") && strings.Contains(boundary.Evidence.Snippet, "selected.Go(task)")
		}
		if !goBoundary || !taskBoundary {
			t.Errorf("%s WaitGroup.Go lost receiver/task outside uncertainty: operation=%t task=%t", reportName, goBoundary, taskBoundary)
		}
		for _, snippet := range []string{"input.Add(1)", "done()"} {
			found := false
			for _, boundary := range report.Boundaries {
				found = found || boundary.Kind == "unresolved_waitgroup" && strings.HasSuffix(boundary.Node, "::PublicOutsideOnly") && strings.Contains(boundary.Evidence.Snippet, snippet)
			}
			if !found {
				t.Errorf("%s outside-only operation %q lacks its input boundary", reportName, snippet)
			}
		}
		for _, boundary := range report.Boundaries {
			if strings.HasSuffix(boundary.Node, "::closedKnownOnly") && strings.HasPrefix(boundary.Kind, "unresolved_waitgroup") {
				t.Errorf("%s closed known-only interface flow acquired an outside boundary: %+v", reportName, boundary)
			}
			if strings.HasSuffix(boundary.Node, "::UnmarkedInterfaceOnly") && strings.HasPrefix(boundary.Kind, "unresolved_waitgroup") {
				t.Errorf("%s untagged interface method names were inferred as WaitGroup: %+v", reportName, boundary)
			}
		}
		for _, edge := range report.Relationships {
			if strings.HasSuffix(edge.From, "::UnmarkedInterfaceOnly") && strings.HasPrefix(edge.Kind, "waitgroup_") {
				t.Errorf("%s untagged interface method names produced a WaitGroup edge: %+v", reportName, edge)
			}
		}
	}
}
