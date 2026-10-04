package contracttrace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScalarOutsideResultsAndAssertionsKeepOrigins(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/scalaroutside\n\ngo 1.26.0\n",
		"outside.go": `package scalaroutside
import "strconv"
type Count int
func Factory(factory func() Count) bool { return factory() > 0 }
func Pair(factory func() (Count, bool)) bool { count, ok := factory(); return count > 0 && ok }
func KnownFactory() Count { return 3 }
func FactoryCaller() bool { return Factory(KnownFactory) }
func closedFactory(factory func() Count) bool { return factory() > 0 }
func ClosedFactoryCaller() bool { return closedFactory(KnownFactory) }
func Assertion(value any) bool { return value.(Count) > 0 }
func AssertPair(value any) bool { count, ok := value.(Count); return ok && count > 0 }
func closedAssertion(value any) bool { return value.(Count) > 0 }
func ClosedAssertionCaller() bool { return closedAssertion(Count(3)) }
func Dependency(text string) bool { count, _ := strconv.Atoi(text); return count > 0 }
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Factory", "Pair", "FactoryCaller", "closedFactory", "ClosedFactoryCaller", "Assertion", "AssertPair", "closedAssertion", "ClosedAssertionCaller", "Dependency"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 5, MaxNodes: 400})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 5, MaxNodes: 400})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, owner := range seeds {
			found := false
			for _, edge := range report.Relationships {
				found = found || strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "scalar_return" && strings.HasPrefix(edge.To, "scalar-input:")
			}
			want := owner != "closedFactory" && owner != "ClosedFactoryCaller" && owner != "closedAssertion" && owner != "ClosedAssertionCaller"
			if found != want {
				t.Errorf("%s outside scalar result=%v, want %v", owner, found, want)
			}
		}
	}
}

func TestScalarOverflowDisclosesLostOriginsAtConsumer(t *testing.T) {
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			root := t.TempDir()
			parameters, terms, args := []string{}, []string{}, []string{}
			for i := 0; i < count; i++ {
				name := fmt.Sprintf("p%d", i)
				parameters = append(parameters, name+" int")
				terms = append(terms, name)
				args = append(args, "1")
			}
			source := "package scalarcap\nfunc total(" + strings.Join(parameters, ",") + ") int { return " + strings.Join(terms, "+") + " }\nfunc Entry() int { return total(" + strings.Join(args, ",") + ") }\n"
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/scalarcap\n\ngo 1.26.0\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "cap.go"), []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
			fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Entry", "total"}, Depth: 4, MaxNodes: 300})
			if err != nil {
				t.Fatal(err)
			}
			resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"Entry", "total"}, Depth: 4, MaxNodes: 300})
			if err != nil {
				t.Fatal(err)
			}
			for _, report := range []Report{fresh, resumed} {
				for _, owner := range []string{"total", "Entry"} {
					unknown := false
					for _, boundary := range report.Boundaries {
						unknown = unknown || strings.HasSuffix(boundary.Node, "::"+owner) && boundary.Kind == "unresolved_scalar_origins"
					}
					if unknown != (count > 64) {
						t.Errorf("%s count=%d truncation boundary=%v", owner, count, unknown)
					}
				}
				origins := 0
				for _, node := range report.Nodes {
					if node.Kind == "scalar_parameter" {
						origins++
					}
				}
				if origins != count {
					t.Errorf("source input inventory lost origins: got %d want %d", origins, count)
				}
			}
		})
	}
}

func TestScalarBuiltinResultsAndNumericArguments(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/scalarbuiltins\n\ngo 1.26.0\n",
		"builtin.go": `package scalarbuiltins
func Length(values []int) int { return len(values) }
func Capacity(ch chan int) int { return cap(ch) }
func Copy(dst, src []int) int { return copy(dst,src) }
func Minimum(n int) int { return min(n,10) }
func Maximum(n float64) float64 { return max(n,10) }
func Complex(n float64) float64 { return real(complex(n,1)) }
func Imaginary(n float64) float64 { return imag(complex(1,n)) }
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := []string{"Length", "Capacity", "Copy", "Minimum", "Maximum", "Complex", "Imaginary"}
	fresh, saved, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 5, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		for _, owner := range seeds {
			found := false
			for _, edge := range report.Relationships {
				found = found || strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "scalar_return" && strings.HasPrefix(edge.To, "scalar-builtin-result:")
			}
			if !found {
				t.Errorf("%s lacks builtin scalar result origin", owner)
			}
		}
		for _, owner := range []string{"Minimum", "Maximum", "Complex", "Imaginary"} {
			found := false
			for _, edge := range report.Relationships {
				found = found || strings.HasSuffix(edge.From, "::"+owner) && edge.Kind == "scalar_return" && edge.To == "scalar-parameter:example.com/scalarbuiltins."+owner+":0"
			}
			if !found {
				t.Errorf("%s lost numeric operand provenance", owner)
			}
		}
		model := false
		for _, boundary := range report.Boundaries {
			model = model || boundary.Kind == "scalar_builtin_model"
		}
		if !model {
			t.Error("missing builtin result limitations")
		}
	}
}
