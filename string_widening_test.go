package contracttrace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStringWideningKeepsPossibleWrappingCause(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprint(overflow), func(t *testing.T) {
			root := t.TempDir()
			var source strings.Builder
			source.WriteString("package widening\nimport (\"errors\"; \"fmt\")\nfunc cause() error { return errors.New(\"cause\") }\nfunc AFormats(n int) string { switch n {\n")
			count := maxFlowValues - 1
			if overflow {
				count++
			}
			for i := 0; i < count; i++ {
				fmt.Fprintf(&source, "case %d: return %q\n", i, fmt.Sprintf("display %02d: %%v", i))
			}
			last := "display default: %v"
			if overflow {
				last = "z wrapping: %w"
			}
			fmt.Fprintf(&source, "default: return %q\n} }\nfunc Wrap(n int) error { return fmt.Errorf(AFormats(n), cause()) }\n", last)
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/widening\n\ngo 1.26.0\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source.String()), 0644); err != nil {
				t.Fatal(err)
			}
			fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"cause"}, Depth: 4, MaxNodes: 250})
			if err != nil {
				t.Fatal(err)
			}
			resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"cause"}, Depth: 4, MaxNodes: 250})
			if err != nil {
				t.Fatal(err)
			}
			for _, report := range []Report{fresh, resumed} {
				origins := map[string]bool{}
				for _, node := range report.Nodes {
					if node.Kind == "error_result" && node.Evidence.Line == 3 {
						origins[node.ID] = true
					}
				}
				if len(origins) != 1 {
					t.Fatal("missing cause origin")
				}
				wrapped, unknown := false, false
				for _, edge := range report.Relationships {
					wrapped = wrapped || edge.Kind == "error_return" && strings.HasSuffix(edge.From, "::Wrap") && origins[edge.To]
				}
				for _, boundary := range report.Boundaries {
					unknown = unknown || boundary.Kind == "error_wrap_format" && strings.HasSuffix(boundary.Node, "::Wrap")
				}
				if wrapped != overflow || unknown != overflow || report.Coverage.ValueFlow.Widened != overflow {
					t.Errorf("overflow=%v wrapped=%v unknown=%v widened=%v", overflow, wrapped, unknown, report.Coverage.ValueFlow.Widened)
				}
			}
		})
	}
}
