package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorWrappingRetainsCausesAndFormattingControls(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/errorwrap\n\ngo 1.27.0\n",
		"wrap.go": `package errorwrap
import ("errors"; "fmt")
func cause() error { return errors.New("cause") }
func Wrap() error { return fmt.Errorf("operation: %w", cause()) }
func Joined() error { return errors.Join(nil, Wrap(), cause()) }
func Spread() error { values := []error{cause()}; values = append(values, Wrap()); return errors.Join(values...) }
func Check() bool { return Joined() != nil }
func Display() error { return fmt.Errorf("display: %v", cause()) }
func Escaped() error { return fmt.Errorf("literal %%w: %v", cause()) }
func Indexed() error { return fmt.Errorf("%[2]w %[1]v", "message", cause()) }
func Dynamic(format string) error { return fmt.Errorf(format, cause()) }
var Join = errors.Join
func Alias() error { return Join(cause()) }
func PublicFormat(format string) error { return fmt.Errorf(format, cause()) }
func FormatCaller() error { return PublicFormat("%v") }
func closedFormat(format string) error { return fmt.Errorf(format, cause()) }
func ClosedFormatCaller() error { return closedFormat("%v") }
func ConcatFormat(format string) error { return fmt.Errorf("prefix: " + format, cause()) }
func ConcatCaller() error { return ConcatFormat("%v") }
func other() error { return errors.New("other") }
func Selected() error { return fmt.Errorf("%v %w", other(), cause()) }
func SelectedIndex() error { return fmt.Errorf("%[2]w %[1]v", other(), cause()) }
func Width() error { return fmt.Errorf("%*w %v", 8, cause(), other()) }
func Precision() error { return fmt.Errorf("%.*w %v", 3, cause(), other()) }
func Reordered() error { return fmt.Errorf("%[3]*.[2]*[1]w %[4]v", cause(), 3, 8, other()) }
func Mutated() error { values := []any{other(), cause()}; values[0] = cause(); return fmt.Errorf("%w %v", values...) }
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"cause"}, Depth: 8, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	original := ""
	for _, node := range r.Nodes {
		if node.Kind == "error_result" && node.Evidence.Line == 3 {
			original = node.ID
		}
	}
	if original == "" {
		t.Fatal("missing cause result")
	}
	for _, owner := range []string{"Wrap", "Joined", "Spread", "Check", "Indexed", "Dynamic", "Alias", "PublicFormat", "ConcatFormat", "Selected", "SelectedIndex", "Width", "Precision", "Reordered", "Mutated"} {
		found := false
		for _, edge := range r.Relationships {
			if strings.HasSuffix(edge.From, "::"+owner) && (edge.Kind == "error_return" || edge.Kind == "error_compare") && edge.To == original {
				found = true
			}
		}
		if !found {
			t.Errorf("%s lost wrapped cause", owner)
		}
	}
	for _, edge := range r.Relationships {
		if (strings.HasSuffix(edge.From, "::Display") || strings.HasSuffix(edge.From, "::Escaped") || strings.HasSuffix(edge.From, "::closedFormat")) && edge.Kind == "error_return" && edge.To == original {
			t.Errorf("formatting-only function retained wrapped cause: %s", edge.From)
		}
	}
	other := ""
	for _, node := range r.Nodes {
		if node.Kind == "error_result" && strings.Contains(node.Evidence.Snippet, `errors.New("other")`) {
			other = node.ID
		}
	}
	if other == "" {
		t.Fatal("missing formatting-only source error")
	}
	for _, edge := range r.Relationships {
		for _, owner := range []string{"Selected", "SelectedIndex", "Width", "Precision", "Reordered"} {
			if strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "error_return" && edge.To == other {
				t.Errorf("%s wrapped the formatting-only argument", owner)
			}
		}
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Check"}, Depth: 8, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	unknown := false
	for _, boundary := range resumed.Boundaries {
		if boundary.Kind == "error_wrap_format" && boundary.Evidence.Line == 11 {
			unknown = true
		}
	}
	if !unknown {
		t.Fatal("saved graph hid unresolved wrapping format")
	}
	mutation := false
	for _, boundary := range resumed.Boundaries {
		if boundary.Kind == "error_wrap_format" && strings.Contains(boundary.Evidence.Snippet, "values[0] = cause()") {
			mutation = true
		}
	}
	if !mutation {
		t.Fatal("mutated argument storage had no pairing boundary")
	}
}
