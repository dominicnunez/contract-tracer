package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicErrorGlobalsKeepReplacementAndAliasedContents(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/errorglobals\n\ngo 1.27.0\n",
		"global.go": `package errorglobals
import "errors"
func source() error { return errors.New("local") }
var Public error = source()
var private error = source()
type Record struct { E error; hidden error }
var Shared = &Record{E: source(), hidden: source()}
var alias = Shared
var Empty *Record
var Slice = []error{source()}
var Map = map[string]error{"key": source()}
var EmptySlice []error
var EmptyMap map[string]error
func Read() error { return Public }
func Private() error { return private }
func Field() error { return Shared.E }
func Alias() error { return alias.E }
func Hidden() error { return Shared.hidden }
func EmptyField() error { return Empty.E }
func SliceRead() error { return Slice[0] }
func MapRead() error { return Map["key"] }
func EmptySliceRead() error { return EmptySlice[0] }
func EmptyMapRead() error { return EmptyMap["key"] }
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Read", "Private", "Field", "Alias", "Hidden", "EmptyField", "SliceRead", "MapRead", "EmptySliceRead", "EmptyMapRead"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: append([]string{"Public"}, seeds...), Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: append([]string{"Public"}, seeds...), Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, owner := range seeds {
			outside, local := false, false
			for _, edge := range report.Relationships {
				if !strings.HasSuffix(edge.From, "::"+owner) || edge.Kind != "error_return" {
					continue
				}
				outside = outside || strings.HasPrefix(edge.To, "error-input:")
				for _, node := range report.Nodes {
					if node.ID == edge.To && node.Kind == "error_result" && strings.Contains(node.Evidence.Snippet, `errors.New("local")`) {
						local = true
					}
				}
			}
			if outside != (owner != "Private" && owner != "Hidden") {
				t.Errorf("%s outside=%v", owner, outside)
			}
			if owner != "EmptyField" && owner != "EmptySliceRead" && owner != "EmptyMapRead" && !local {
				t.Errorf("%s lost local error", owner)
			}
		}
		outside := false
		for _, boundary := range report.Boundaries {
			outside = outside || boundary.Kind == "outside_error_input" && boundary.Evidence.File == "global.go" && boundary.Evidence.Line > 0
		}
		if !outside {
			t.Error("missing source-backed global error boundary")
		}
	}
}
