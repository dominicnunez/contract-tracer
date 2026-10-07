package contracttrace

import (
	"go/ast"
	"go/types"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

// globalFunctionState distinguishes source-proven zero function values from
// definitely initialized callbacks and initializers the model cannot classify.
type globalFunctionState uint8

const (
	globalFunctionUnknown globalFunctionState = iota
	globalFunctionZero
	globalFunctionInitialized
)

// globalFunctionStateAtAddress maps a modeled global or inline struct field
// address back to its source declaration. It deliberately does not interpret
// a zero pointer-to-struct as a zero function field: dereferencing that pointer
// can panic before the function-valued field is read.
func (a *flowAnalysis) globalFunctionStateAtAddress(address string, ix *index) globalFunctionState {
	if a == nil || ix == nil || address == "" || len(a.localGlobals) == 0 {
		return globalFunctionUnknown
	}

	globals := append([]*ssa.Global(nil), a.localGlobals...)
	sort.Slice(globals, func(i, j int) bool { return globals[i].String() < globals[j].String() })
	var selected *ssa.Global
	selectedAddress := ""
	for _, global := range globals {
		if global == nil {
			continue
		}
		candidate := "global:" + global.String()
		if address != candidate && !strings.HasPrefix(address, candidate+".field:") {
			continue
		}
		if len(candidate) > len(selectedAddress) {
			selected, selectedAddress = global, candidate
		}
	}
	if selected == nil {
		return globalFunctionUnknown
	}

	variable, ok := selected.Object().(*types.Var)
	if !ok || variable.Type() == nil || selected.Pkg == nil || selected.Pkg.Pkg == nil {
		return globalFunctionUnknown
	}
	fieldPath, ok := globalFieldPath(selectedAddress, address)
	if !ok || !globalFunctionFieldType(variable.Type(), fieldPath) {
		return globalFunctionUnknown
	}
	pkg := globalSourcePackage(ix, selected.Pkg.Pkg)
	if pkg == nil || pkg.TypesInfo == nil {
		return globalFunctionUnknown
	}
	return classifyGlobalFunctionSource(variable, fieldPath, pkg)
}

func classifyGlobalFunctionSource(variable *types.Var, fieldPath []int, pkg *packages.Package) globalFunctionState {
	if variable == nil || variable.Type() == nil || pkg == nil || pkg.TypesInfo == nil || !globalFunctionFieldType(variable.Type(), fieldPath) {
		return globalFunctionUnknown
	}
	initializer, found := globalInitializer(variable, pkg)
	if !found {
		return globalFunctionUnknown
	}
	if initializer == nil {
		return globalFunctionZero
	}
	return classifyGlobalFunctionInitializer(initializer, variable.Type(), fieldPath, pkg)
}

func globalFieldPath(root, address string) ([]int, bool) {
	suffix := strings.TrimPrefix(address, root)
	if suffix == "" {
		return nil, true
	}
	indexes := []int{}
	for len(suffix) != 0 {
		if !strings.HasPrefix(suffix, ".field:") {
			return nil, false
		}
		suffix = strings.TrimPrefix(suffix, ".field:")
		end := strings.IndexByte(suffix, '.')
		segment := suffix
		if end >= 0 {
			segment, suffix = suffix[:end], suffix[end:]
		} else {
			suffix = ""
		}
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 {
			return nil, false
		}
		indexes = append(indexes, index)
	}
	return indexes, true
}

func globalFunctionFieldType(typ types.Type, indexes []int) bool {
	current := types.Unalias(typ)
	if _, pointer := current.Underlying().(*types.Pointer); pointer {
		return false
	}
	for depth, index := range indexes {
		structure, ok := current.Underlying().(*types.Struct)
		if !ok || index < 0 || index >= structure.NumFields() {
			return false
		}
		fieldType := types.Unalias(structure.Field(index).Type())
		if depth == len(indexes)-1 {
			return isFunctionType(fieldType)
		}
		if _, pointer := fieldType.Underlying().(*types.Pointer); pointer {
			return false
		}
		current = fieldType
	}
	return len(indexes) == 0 && isFunctionType(current)
}

func globalSourcePackage(ix *index, target *types.Package) *packages.Package {
	ids := make([]string, 0, len(ix.funcs))
	for id := range ix.funcs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fn := ix.funcs[id]
		if fn != nil && fn.pkg != nil && fn.pkg.Types == target && fn.pkg.TypesInfo != nil {
			return fn.pkg
		}
	}
	return nil
}

func globalInitializer(variable *types.Var, pkg *packages.Package) (ast.Expr, bool) {
	if variable == nil || pkg == nil || pkg.TypesInfo == nil {
		return nil, false
	}
	var initializer ast.Expr
	found := false
	for _, file := range pkg.Syntax {
		for _, declaration := range file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok || gen.Tok.String() != "var" {
				continue
			}
			for _, raw := range gen.Specs {
				spec, ok := raw.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for nameIndex, name := range spec.Names {
					if pkg.TypesInfo.Defs[name] != variable {
						continue
					}
					if found {
						return nil, false
					}
					found = true
					if len(spec.Values) == 0 {
						initializer = nil
					} else if len(spec.Values) == len(spec.Names) {
						initializer = spec.Values[nameIndex]
					} else if len(spec.Names) == 1 && len(spec.Values) == 1 {
						initializer = spec.Values[0]
					} else {
						return nil, false
					}
				}
			}
		}
	}
	return initializer, found
}

func classifyGlobalFunctionInitializer(expression ast.Expr, typ types.Type, indexes []int, pkg *packages.Package) globalFunctionState {
	if expression == nil || typ == nil || pkg == nil || pkg.TypesInfo == nil {
		return globalFunctionUnknown
	}
	expression = unparenExpr(expression)
	typ = types.Unalias(typ)
	if len(indexes) == 0 {
		if !isFunctionType(typ) {
			return globalFunctionUnknown
		}
		if value, ok := pkg.TypesInfo.Types[expression]; ok && value.IsNil() {
			return globalFunctionZero
		}
		switch value := expression.(type) {
		case *ast.FuncLit:
			return globalFunctionInitialized
		case *ast.Ident:
			if _, ok := pkg.TypesInfo.Uses[value].(*types.Func); ok {
				return globalFunctionInitialized
			}
		case *ast.SelectorExpr:
			if _, ok := pkg.TypesInfo.Uses[value.Sel].(*types.Func); ok {
				return globalFunctionInitialized
			}
		}
		return globalFunctionUnknown
	}

	structure, ok := typ.Underlying().(*types.Struct)
	if !ok {
		return globalFunctionUnknown
	}
	literal, ok := expression.(*ast.CompositeLit)
	if !ok {
		return globalFunctionUnknown
	}
	literalType := pkg.TypesInfo.TypeOf(literal)
	if literalType == nil || !types.Identical(types.Unalias(literalType), typ) {
		return globalFunctionUnknown
	}
	fieldIndex := indexes[0]
	if fieldIndex < 0 || fieldIndex >= structure.NumFields() {
		return globalFunctionUnknown
	}
	fieldValue, present := compositeFieldValue(literal, fieldIndex, typ, pkg)
	if !present {
		return globalFunctionZero
	}
	return classifyGlobalFunctionInitializer(fieldValue, structure.Field(fieldIndex).Type(), indexes[1:], pkg)
}
