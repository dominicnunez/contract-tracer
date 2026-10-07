package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUncalledPrivateCallbackInputsStayUnresolved(t *testing.T) {
	root := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/privatecallbacks\n\ngo 1.27.0\n")
	write("callbacks.go", `package callbacks

func knownCallback() {}
func closedCallback() {}

func uncalledPrivate(callback func(), chooseKnown bool) {
	if chooseKnown {
		callback = knownCallback
	}
	callback()
}

func calledPrivate(callback func()) { callback() }
func closedCaller() { calledPrivate(closedCallback) }

func ExportedCallback(callback func()) { callback() }
`)
	options := Options{Root: root, Seeds: []string{"uncalledPrivate", "closedCaller", "ExportedCallback"}, Depth: 4, MaxNodes: 250}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	if err := WriteAnalysis(&compressed, analysis, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: options.Seeds, Depth: options.Depth, MaxNodes: options.MaxNodes})
	if err != nil {
		t.Fatal(err)
	}

	check := func(name string, report Report) {
		t.Helper()
		knownTarget, openPrivateBoundary, exportedBoundary := false, false, false
		for _, edge := range report.Relationships {
			knownTarget = knownTarget || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::uncalledPrivate") && strings.HasSuffix(edge.To, "::knownCallback") && edge.Evidence.File == "callbacks.go" && edge.Evidence.Line == 10 && strings.Contains(edge.Evidence.Snippet, "callback()")
		}
		for _, boundary := range report.Boundaries {
			openPrivateBoundary = openPrivateBoundary || boundary.Kind == "partial_function_call" && strings.HasSuffix(boundary.Node, "::uncalledPrivate") && boundary.Evidence.Line == 10 && strings.Contains(boundary.Evidence.Snippet, "callback()")
			exportedBoundary = exportedBoundary || boundary.Kind == "unresolved_function_call" && strings.HasSuffix(boundary.Node, "::ExportedCallback") && boundary.Evidence.Line == 16 && strings.Contains(boundary.Evidence.Snippet, "callback()")
			if (boundary.Kind == "partial_function_call" || strings.Contains(boundary.Kind, "unresolved_function_call")) && strings.HasSuffix(boundary.Node, "::calledPrivate") && boundary.Evidence.File == "callbacks.go" && boundary.Evidence.Line == 13 {
				t.Errorf("%s: closed private caller acquired an outside callback alternative: %+v", name, boundary)
			}
		}
		closedTarget := false
		for _, edge := range report.Relationships {
			closedTarget = closedTarget || edge.Kind == "resolved_callback_call" && strings.HasSuffix(edge.From, "::calledPrivate") && strings.HasSuffix(edge.To, "::closedCallback") && edge.Evidence.File == "callbacks.go" && edge.Evidence.Line == 13 && strings.Contains(edge.Evidence.Snippet, "callback()")
		}
		if !knownTarget || !openPrivateBoundary || !exportedBoundary || !closedTarget {
			t.Errorf("%s: callback input contract incomplete: known=%t private-unknown=%t exported=%t closed=%t", name, knownTarget, openPrivateBoundary, exportedBoundary, closedTarget)
		}
	}
	check("fresh", fresh)
	check("resumed", resumed)
}
