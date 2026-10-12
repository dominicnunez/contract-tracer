package contracttrace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestGlobalFunctionSourceClassificationDistinguishesZeroInitializedAndUnknown(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "globals.go", `package globals
type holder struct {
	callback func()
}
type nestedHolder struct { inner struct { callback func() } }
var zero holder
var omitted = holder{}
var nested nestedHolder
var nestedOmitted = nestedHolder{}
var nestedInlineOmitted = nestedHolder{inner: struct { callback func() }{}}
var explicit = holder{callback: known}
var nilExplicit = holder{callback: nil}
var direct func()
var directNil func() = nil
var directSet = known
var dynamic = choose()
var pointer *holder
var sameA, sameB = holder{}, holder{callback: known}
func known() {}
func choose() holder { return holder{} }
`, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	checked, err := new(types.Config).Check("example.com/globals", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	pkg := &packages.Package{PkgPath: checked.Path(), Types: checked, TypesInfo: info, Syntax: []*ast.File{file}}
	state := func(name string, fields ...int) globalFunctionState {
		t.Helper()
		variable := sourceGlobalVariable(t, pkg, name)
		return classifyGlobalFunctionSource(variable, fields, pkg)
	}

	for _, name := range []string{"zero", "omitted"} {
		if got := state(name, 0); got != globalFunctionZero {
			t.Errorf("%s callback field state = %v, want source-proven zero", name, got)
		}
	}
	if got := state("nested", 0, 0); got != globalFunctionZero {
		t.Errorf("nested inline callback field state = %v, want source-proven zero", got)
	}
	if got := state("nestedOmitted", 0, 0); got != globalFunctionZero {
		t.Errorf("omitted nested callback field state = %v, want source-proven zero", got)
	}
	if got := state("nestedInlineOmitted", 0, 0); got != globalFunctionZero {
		t.Errorf("elided nested literal callback field state = %v, want source-proven zero", got)
	}
	if got := state("explicit", 0); got != globalFunctionInitialized {
		t.Errorf("explicit known callback state = %v, want initialized", got)
	}
	if got := state("nilExplicit", 0); got != globalFunctionZero {
		t.Errorf("explicit nil callback state = %v, want zero", got)
	}
	if got := state("direct"); got != globalFunctionZero {
		t.Errorf("direct function global state = %v, want source-proven zero", got)
	}
	if got := state("directNil"); got != globalFunctionZero {
		t.Errorf("explicit nil function global state = %v, want zero", got)
	}
	if got := state("directSet"); got != globalFunctionInitialized {
		t.Errorf("direct initialized function global state = %v, want initialized", got)
	}
	if got := state("dynamic", 0); got != globalFunctionUnknown {
		t.Errorf("call-initialized callback field state = %v, want unknown", got)
	}
	if got := state("pointer", 0); got != globalFunctionUnknown {
		t.Errorf("field under zero pointer state = %v, want unknown (the parent pointer may panic)", got)
	}
	if got := state("sameA", 0); got != globalFunctionZero {
		t.Errorf("same-line zero global state = %v, want source-proven zero", got)
	}
	if got := state("sameB", 0); got != globalFunctionInitialized {
		t.Errorf("same-line initialized global state = %v, want initialized", got)
	}
}

func sourceGlobalVariable(t *testing.T, pkg *packages.Package, name string) *types.Var {
	t.Helper()
	for _, file := range pkg.Syntax {
		for _, declaration := range file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, raw := range gen.Specs {
				spec, ok := raw.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, identifier := range spec.Names {
					if identifier.Name == name {
						variable, _ := pkg.TypesInfo.Defs[identifier].(*types.Var)
						if variable != nil {
							return variable
						}
					}
				}
			}
		}
	}
	t.Fatalf("package fixture has no variable %q", name)
	return nil
}
