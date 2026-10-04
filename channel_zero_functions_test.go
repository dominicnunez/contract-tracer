package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClosedChannelZeroCallbackCandidatesKeepSourceAndKnownAlternatives(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/channelzero\n\ngo 1.27.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := `package channelzero
import "sync"
func knownTask() {}
type Holder struct { Run func() }
type Nested struct { Inner Holder }
func closeTasks(ch chan func()) { close(ch) }
func BufferedDrain() { ch:=make(chan func(),1); ch<-knownTask; close(ch); _= <-ch; group:=&sync.WaitGroup{}; group.Go(<-ch) }
func HelperDrain() { ch:=make(chan func(),1); ch<-knownTask; closeTasks(ch); _= <-ch; group:=&sync.WaitGroup{}; group.Go(<-ch) }
func EmptyClosed() { ch:=make(chan func()); close(ch); group:=&sync.WaitGroup{}; group.Go(<-ch) }
func SelectDrain() { ch:=make(chan func(),1); ch<-knownTask; close(ch); _= <-ch; group:=&sync.WaitGroup{}; select { case task:=<-ch: group.Go(task); default: } }
func ClosedHolderDrain() { ch:=make(chan Holder,1); ch<-Holder{Run:knownTask}; close(ch); _= <-ch; holder:=<-ch; group:=&sync.WaitGroup{}; group.Go(holder.Run) }
func ClosedEmptyHolder() { ch:=make(chan Holder); close(ch); holder:=<-ch; group:=&sync.WaitGroup{}; group.Go(holder.Run) }
func ClosedEmptyNested() { ch:=make(chan Nested); close(ch); holder:=<-ch; group:=&sync.WaitGroup{}; group.Go(holder.Inner.Run) }
func ClosedPointerDrain() { ch:=make(chan *Holder,1); ch<-&Holder{Run:knownTask}; close(ch); _= <-ch; holder:=<-ch; group:=&sync.WaitGroup{}; group.Go(holder.Run) }
func UnclosedKnown() { ch:=make(chan func(),1); ch<-knownTask; group:=&sync.WaitGroup{}; group.Go(<-ch) }
`
	if err := os.WriteFile(filepath.Join(root, "channels.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	seeds := []string{"BufferedDrain", "HelperDrain", "EmptyClosed", "SelectDrain", "ClosedHolderDrain", "ClosedEmptyHolder", "ClosedEmptyNested", "ClosedPointerDrain", "UnclosedKnown"}
	report, err := Trace(context.Background(), Options{Root: root, Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Truncated || report.ContractComplete {
		t.Fatalf("probe must be untruncated and incomplete: truncated=%t complete=%t", report.Coverage.Truncated, report.ContractComplete)
	}

	for _, test := range []struct {
		name        string
		snippet     string
		known       bool
		nilCallback bool
		closed      bool
	}{
		{"BufferedDrain", "group.Go(<-ch)", true, true, true},
		{"HelperDrain", "group.Go(<-ch)", true, true, true},
		{"EmptyClosed", "group.Go(<-ch)", false, true, true},
		{"SelectDrain", "group.Go(task)", true, true, true},
		{"ClosedHolderDrain", "group.Go(holder.Run)", true, true, true},
		{"ClosedEmptyHolder", "group.Go(holder.Run)", false, true, true},
		{"ClosedEmptyNested", "group.Go(holder.Inner.Run)", false, true, true},
		{"ClosedPointerDrain", "group.Go(holder.Run)", true, false, false},
		{"UnclosedKnown", "group.Go(<-ch)", true, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			line := sourceLine(source, "func "+test.name+"(")
			var resource string
			operationCount := 0
			for _, edge := range report.Relationships {
				if edge.Kind != "waitgroup_go" || !strings.HasSuffix(edge.From, "::"+test.name) || !strings.Contains(edge.Evidence.Snippet, test.snippet) {
					continue
				}
				if edge.Evidence.File != "channels.go" || edge.Evidence.Line != line {
					t.Fatalf("WaitGroup.Go edge has wrong source evidence: %+v", edge.Evidence)
				}
				resource = edge.To
				operationCount++
			}
			if operationCount != 1 {
				t.Fatalf("expected one source-backed WaitGroup.Go edge, got %d", operationCount)
			}

			known := false
			for _, edge := range report.Relationships {
				if edge.Kind == "waitgroup_task" && edge.From == resource && strings.HasSuffix(edge.To, "::knownTask") {
					if edge.Evidence.File != "channels.go" || edge.Evidence.Line != line || !strings.Contains(edge.Evidence.Snippet, test.snippet) {
						t.Fatalf("known task edge lost invocation evidence: %+v", edge.Evidence)
					}
					known = true
				}
			}
			if known != test.known {
				t.Fatalf("known task candidate=%t, want %t", known, test.known)
			}

			nilCandidate, closedBoundary := false, false
			for _, boundary := range report.Boundaries {
				if !strings.HasSuffix(boundary.Node, "::"+test.name) {
					continue
				}
				nilReason := strings.Contains(boundary.Reason, "typed nil function value") || strings.Contains(boundary.Reason, "nil callback alternative") || strings.Contains(boundary.Reason, "possible nil")
				if boundary.Kind == "unresolved_waitgroup_task" && boundary.Evidence.File == "channels.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Evidence.Snippet, test.snippet) && nilReason {
					nilCandidate = true
				}
				if boundary.Kind == "closed_function_channel_zero" && boundary.Evidence.File == "channels.go" && boundary.Evidence.Line == line && strings.Contains(boundary.Reason, "element zero value") {
					if len(boundary.Examples) == 0 {
						t.Fatalf("closed-channel boundary lacks source close evidence: %+v", boundary)
					}
					closedBoundary = true
				}
			}
			if nilCandidate != test.nilCallback {
				t.Fatalf("typed nil callback alternative=%t, want %t", nilCandidate, test.nilCallback)
			}
			if closedBoundary != test.closed {
				t.Fatalf("closed-channel zero boundary=%t, want %t", closedBoundary, test.closed)
			}
		})
	}
}

func sourceLine(source, prefix string) int {
	for number, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(line, prefix) {
			return number + 1
		}
	}
	return 0
}
