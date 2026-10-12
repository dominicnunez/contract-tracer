package contracttrace

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenericAfterFuncSourceSiteMergesInstantiations(t *testing.T) {
	root := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/genericafterfunc\n\ngo 1.27.0\n")
	fixture := `package callbacks

import "context"

func register[T any](ctx context.Context, callback func()) func() bool {
	return context.AfterFunc(ctx, callback)
}

func firstCallback() {}
func secondCallback() {}
func directCallback() {}

func inspectRegistrations() {
	firstContext, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	secondContext, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	firstStop := register[int](firstContext, firstCallback)
	secondStop := register[string](secondContext, secondCallback)
	firstStop()
	secondStop()

	directContext, cancelDirect := context.WithCancel(context.Background())
	defer cancelDirect()
	directStop := context.AfterFunc(directContext, directCallback)
	directStop()
}
	`
	write("callbacks.go", fixture)
	options := Options{Root: root, Seeds: []string{"inspectRegistrations"}, Depth: 5, MaxNodes: 300}
	fset := token.NewFileSet()
	source, err := parser.ParseFile(fset, "callbacks.go", fixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	contextPositions := map[[2]int]bool{}
	sourceLines := strings.Split(fixture, "\n")
	ast.Inspect(source, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "WithCancel" {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if ok && packageName.Name == "context" {
			position := fset.Position(call.Lparen)
			if strings.Contains(sourceLines[position.Line-1], "firstContext") || strings.Contains(sourceLines[position.Line-1], "secondContext") {
				contextPositions[[2]int{position.Line, position.Column}] = true
			}
		}
		return true
	})
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
		registrations := map[string]bool{}
		contexts := map[string]bool{}
		callbacks := map[string]bool{}
		stops := map[int]bool{}
		for _, edge := range report.Relationships {
			if edge.Kind == "cancellation_callback_register" && strings.HasSuffix(edge.From, "::register") {
				registrations[edge.To] = true
			}
		}
		if len(registrations) != 1 {
			t.Fatalf("%s: generic source call should have one registration identity, got %v", name, registrations)
		}
		var registration string
		for candidate := range registrations {
			registration = candidate
		}
		for _, edge := range report.Relationships {
			if edge.From != registration {
				continue
			}
			switch edge.Kind {
			case "cancellation_callback_context":
				contexts[edge.To] = true
			case "cancellation_callback_target":
				callbacks[edge.To] = true
			}
		}
		for _, edge := range report.Relationships {
			if edge.Kind == "cancellation_callback_stop" && edge.To == registration && strings.HasSuffix(edge.From, "::inspectRegistrations") {
				stops[edge.Evidence.Line] = true
			}
		}
		wantCallbacks := map[string]bool{
			"example.com/genericafterfunc::firstCallback":  true,
			"example.com/genericafterfunc::secondCallback": true,
		}
		genericContexts := map[string]bool{}
		for _, node := range report.Nodes {
			if node.Kind == "context" && contextPositions[[2]int{node.Evidence.Line, node.Evidence.Column}] {
				genericContexts[node.ID] = true
			}
		}
		if len(contextPositions) != 2 || len(genericContexts) != 2 || len(contexts) != 2 {
			t.Errorf("%s: generic registration contexts lost: source=%v edges=%v", name, genericContexts, contexts)
		} else {
			for context := range genericContexts {
				if !contexts[context] {
					t.Errorf("%s: generic registration missing source context %s: %v", name, context, contexts)
				}
			}
		}
		for callback := range wantCallbacks {
			if !callbacks[callback] {
				t.Errorf("%s: missing callback candidate %s at %s: %v", name, callback, registration, callbacks)
			}
		}
		if len(stops) != 2 {
			t.Errorf("%s: source registration should retain both distinct stop call sites, got %v", name, stops)
		}
		direct := false
		for _, edge := range report.Relationships {
			direct = direct || edge.Kind == "cancellation_callback_target" && edge.To == "example.com/genericafterfunc::directCallback"
		}
		if !direct {
			t.Errorf("%s: non-generic AfterFunc control missing", name)
		}
	}
	check("fresh", fresh)
	check("resumed", resumed)
}
