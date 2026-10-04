package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallbackConsumersExplainNilAndOutsideAlternativesTogether(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/mixedcallbacks\n\ngo 1.26.0\nrequire example.com/callbackapi v0.0.0\nreplace example.com/callbackapi => ./api\n",
		"api/go.mod": "module example.com/callbackapi\n\ngo 1.26.0\n",
		"api/api.go": `package callbackapi
func Subscribe(string, func()) {}
type Lease struct{}
func (*Lease) Close() {}
`,
		"client.go": `package mixedcallbacks
import "example.com/callbackapi"

func knownTask() {}
func invoke(fn func()) { fn() }

func EventKnown() { callbackapi.Subscribe("event-known", knownTask) }
func EventNilOnly() { var callback func(); callbackapi.Subscribe("event-nil-only", callback) }
func EventKnownNil(flag bool) {
    callback := knownTask
    if flag { callback = nil }
    callbackapi.Subscribe("event-known-nil", callback)
}
func EventKnownOutside(outside func(), flag bool) {
    callback := knownTask
    if flag { callback = outside }
    callbackapi.Subscribe("event-known-outside", callback)
}
func EventKnownNilOutside(outside func(), nilFlag, outsideFlag bool) {
    callback := knownTask
    if nilFlag { callback = nil }
    if outsideFlag { callback = outside }
    callbackapi.Subscribe("event-known-nil-outside", callback)
}

func BoundKnown() { callback := (&callbackapi.Lease{}).Close; callback() }
func BoundNilOnly() { var callback func(); callback() }
func BoundKnownNil(flag bool) {
    callback := (&callbackapi.Lease{}).Close
    if flag { callback = nil }
    callback()
}
func BoundKnownOutside(outside func(), flag bool) {
    callback := (&callbackapi.Lease{}).Close
    if flag { callback = outside }
    callback()
}
func BoundKnownNilOutside(outside func(), nilFlag, outsideFlag bool) {
    callback := (&callbackapi.Lease{}).Close
    if nilFlag { callback = nil }
    if outsideFlag { callback = outside }
    callback()
}
`,
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	one := 1
	config := DefaultConfig()
	config.CallRules = []CallRule{{Symbol: "example.com/callbackapi::Subscribe", Kind: "event_subscribe", Argument: 0, HandlerArgument: &one, Namespace: "notifications"}}
	config.LifecycleRules = []LifecycleRule{{Symbol: "example.com/callbackapi::Lease.Close", Role: "release", Namespace: "leases", Identity: "origin", Receiver: true}}
	functions := []string{
		"EventKnown", "EventNilOnly", "EventKnownNil", "EventKnownOutside", "EventKnownNilOutside",
		"BoundKnown", "BoundNilOnly", "BoundKnownNil", "BoundKnownOutside", "BoundKnownNilOutside",
	}
	report, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: functions, Depth: 6, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	boundaries := func(owner, kind string) []Boundary {
		var matches []Boundary
		for _, boundary := range report.Boundaries {
			if boundary.Node == "example.com/mixedcallbacks::"+owner && boundary.Kind == kind {
				matches = append(matches, boundary)
			}
		}
		return matches
	}
	assertBoundary := func(owner, kind string, wantNil, wantOutside bool) {
		t.Helper()
		matches := boundaries(owner, kind)
		if len(matches) == 0 {
			t.Fatalf("%s: missing %s boundary", owner, kind)
		}
		for _, boundary := range matches {
			if strings.Contains(boundary.Reason, "typed nil") != wantNil {
				t.Fatalf("%s: nil explanation mismatch: %+v", owner, boundary)
			}
			if strings.Contains(boundary.Reason, "outside") != wantOutside {
				t.Fatalf("%s: outside explanation mismatch: %+v", owner, boundary)
			}
		}
	}
	assertNoBoundary := func(owner, kind string) {
		t.Helper()
		if found := boundaries(owner, kind); len(found) != 0 {
			t.Fatalf("%s: unexpected %s boundary: %+v", owner, kind, found)
		}
	}
	assertBoundary("EventNilOnly", "unresolved_handler", true, false)
	assertBoundary("EventKnownNil", "unresolved_handler", true, false)
	assertBoundary("EventKnownOutside", "unresolved_handler", false, true)
	assertBoundary("EventKnownNilOutside", "unresolved_handler", true, true)
	assertNoBoundary("EventKnown", "unresolved_handler")

	assertBoundary("BoundNilOnly", "unresolved_function_call", true, false)
	assertBoundary("BoundKnownNil", "unresolved_lifecycle_dispatch", true, false)
	assertBoundary("BoundKnownOutside", "unresolved_lifecycle_dispatch", false, true)
	assertBoundary("BoundKnownNilOutside", "unresolved_lifecycle_dispatch", true, true)
	assertNoBoundary("BoundKnown", "unresolved_lifecycle_dispatch")

	eventTopics := map[string]string{
		"EventNilOnly": "event-nil-only",
		"EventKnown":   "event-known", "EventKnownNil": "event-known-nil",
		"EventKnownOutside": "event-known-outside", "EventKnownNilOutside": "event-known-nil-outside",
	}
	knownEvents := map[string]bool{}
	knownReleases := map[string]bool{}
	for owner := range eventTopics {
		knownEvents[owner] = false
	}
	for _, owner := range []string{"BoundNilOnly", "BoundKnown", "BoundKnownNil", "BoundKnownOutside", "BoundKnownNilOutside"} {
		knownReleases[owner] = false
	}
	for _, edge := range analysis.Relationships {
		for owner, topic := range eventTopics {
			knownEvents[owner] = knownEvents[owner] || edge.From == "example.com/mixedcallbacks::knownTask" && edge.To == "event:notifications:"+topic && edge.Kind == "event_handler"
		}
		for _, owner := range []string{"BoundKnown", "BoundKnownNil", "BoundKnownOutside", "BoundKnownNilOutside"} {
			knownReleases[owner] = knownReleases[owner] || edge.From == "example.com/mixedcallbacks::"+owner && strings.HasPrefix(edge.To, "lifecycle:leases:") && edge.Kind == "lifecycle_release" && strings.Contains(edge.Evidence.Snippet, "callback()")
		}
	}
	for _, owner := range []string{"EventKnown", "EventKnownNil", "EventKnownOutside", "EventKnownNilOutside"} {
		if !knownEvents[owner] {
			t.Fatalf("known callback edge was lost at %s", owner)
		}
	}
	if knownEvents["EventNilOnly"] {
		t.Fatal("nil-only event handler gained a known callback edge")
	}
	for _, owner := range []string{"BoundKnown", "BoundKnownNil", "BoundKnownOutside", "BoundKnownNilOutside"} {
		if !knownReleases[owner] {
			t.Fatalf("known bound Lease.Close edge was lost at %s", owner)
		}
	}
	if knownReleases["BoundNilOnly"] {
		t.Fatal("nil-only bound callback gained a lifecycle release edge")
	}
}
