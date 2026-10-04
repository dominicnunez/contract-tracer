package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutsideErrorInputsKeepKnownOriginsAndPrivateControl(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/errorinputs\n\ngo 1.26.0\n",
		"input.go": `package errorinputs
import "errors"
func source() error { return errors.New("local") }
func Public(err error) bool { return err != nil }
func KnownPublic() bool { return Public(source()) }
func closed(err error) bool { return err != nil }
func Closed() bool { return closed(source()) }
func callback(err error) bool { return err != nil }
func ExportCallback() func(error) bool { return callback }
func MixedCallback() bool { return callback(source()) }
func unused(err error) bool { return err != nil }
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Public", "closed", "callback", "unused"}, Depth: 4, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"Public", "callback", "unused"} {
		outside := false
		for _, edge := range r.Relationships {
			if strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "error_compare" && strings.HasPrefix(edge.To, "error-input:") {
				outside = true
			}
		}
		if !outside {
			t.Errorf("%s missing unresolved outside error input", owner)
		}
	}
	for _, owner := range []string{"Public", "closed", "callback"} {
		known := false
		for _, edge := range r.Relationships {
			if !strings.HasSuffix(edge.From, "::"+owner) || edge.Kind != "error_compare" {
				continue
			}
			if owner == "closed" && strings.HasPrefix(edge.To, "error-input:") {
				t.Error("closed helper invented outside error input")
			}
			for _, node := range r.Nodes {
				if edge.To == node.ID && node.Kind == "error_result" && node.Evidence.Line == 3 {
					known = true
				}
			}
		}
		if !known {
			t.Errorf("%s lost known local error", owner)
		}
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Public", "callback", "unused"}, Depth: 4, MaxNodes: 150})
	if err != nil {
		t.Fatal(err)
	}
	boundary := false
	for _, item := range resumed.Boundaries {
		if item.Kind == "outside_error_input" && item.Evidence.File == "input.go" && item.Evidence.Line != 0 {
			boundary = true
		}
	}
	if !boundary {
		t.Fatal("saved exploration lost source-backed outside error boundary")
	}
}
