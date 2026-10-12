package contracttrace

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

// zeroFunctionFieldAtAddress recognizes only a source-bound zero-initialized
// aggregate allocation and a field path omitted by its initializer. It never
// treats absent flow candidates as evidence of a nil value.
func (a *flowAnalysis) zeroFunctionFieldAtAddress(address string, ix *index) bool {
	if a.zeroFunctionFieldCache != nil {
		if result, exists := a.zeroFunctionFieldCache[address]; exists {
			return result == 1
		}
	}
	if len(a.allocations) == 0 {
		return false
	}
	root, found := longestAllocationRoot(address, a.allocations, false)
	if !found {
		return false
	}
	indexes, ok := allocationFieldIndexes(root, address)
	if !ok || len(indexes) == 0 {
		return false
	}
	allocation := a.allocations[root]
	if allocation == nil {
		return false
	}
	function := allocation.Parent()
	owner := ix.funcs[ix.owner(function)]
	if owner == nil || owner.pkg == nil || owner.pkg.TypesInfo == nil {
		return false
	}
	typ := allocation.Type()
	if pointer, ok := typ.Underlying().(*types.Pointer); ok {
		typ = pointer.Elem()
	}
	if !functionFieldAtPath(typ, indexes) {
		a.cacheZeroFunctionField(address, false)
		return false
	}
	var result bool
	if len(indexes) == 1 {
		result = omittedFunctionFieldInitializer(function, allocation, indexes[0], typ, owner.pkg, ix)
	} else {
		result = omittedNestedFunctionField(function, allocation, indexes, typ, owner.pkg, ix)
	}
	a.cacheZeroFunctionField(address, result)
	return result
}

func (a *flowAnalysis) cacheZeroFunctionField(address string, result bool) {
	if a.zeroFunctionFieldCache == nil {
		a.zeroFunctionFieldCache = map[string]uint8{}
	}
	if result {
		a.zeroFunctionFieldCache[address] = 1
	} else {
		a.zeroFunctionFieldCache[address] = 2
	}
}

func functionFieldAtPath(typ types.Type, indexes []int) bool {
	current := typ
	for depth, index := range indexes {
		structType, ok := current.Underlying().(*types.Struct)
		if !ok || index < 0 || index >= structType.NumFields() {
			return false
		}
		fieldType := structType.Field(index).Type()
		if depth == len(indexes)-1 {
			return isFunctionType(fieldType)
		}
		if _, pointer := fieldType.Underlying().(*types.Pointer); pointer {
			return false
		}
		current = fieldType
	}
	return false
}

func allocationFieldIndexes(root, address string) ([]int, bool) {
	suffix := strings.TrimPrefix(address, root)
	indexes := []int{}
	for len(suffix) > 0 {
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
		if err != nil {
			return nil, false
		}
		indexes = append(indexes, index)
	}
	return indexes, true
}

type aggregateInitializer struct {
	literal *ast.CompositeLit
	zero    bool
}

func omittedFunctionFieldInitializer(function *ssa.Function, allocation *ssa.Alloc, field int, typ types.Type, pkg *packages.Package, ix *index) bool {
	initializer, found := allocationInitializer(function, allocation, typ, pkg, ix)
	if !found {
		return false
	}
	if initializer.zero {
		return true
	}
	_, present := compositeFieldValue(initializer.literal, field, typ, pkg)
	return !present
}

func omittedNestedFunctionField(function *ssa.Function, allocation *ssa.Alloc, indexes []int, typ types.Type, pkg *packages.Package, ix *index) bool {
	initializer, found := allocationInitializer(function, allocation, typ, pkg, ix)
	if !found {
		return false
	}
	if initializer.zero {
		return true
	}
	literal := initializer.literal
	current := typ
	for depth, fieldIndex := range indexes {
		structType, ok := current.Underlying().(*types.Struct)
		if !ok || fieldIndex < 0 || fieldIndex >= structType.NumFields() {
			return false
		}
		value, present := compositeFieldValue(literal, fieldIndex, current, pkg)
		if !present {
			return true
		}
		fieldType := structType.Field(fieldIndex).Type()
		if depth == len(indexes)-1 {
			return false
		}
		if _, ok := fieldType.Underlying().(*types.Pointer); ok {
			return false
		}
		current = fieldType
		nested, ok := unparenExpr(value).(*ast.CompositeLit)
		if !ok {
			return false
		}
		literal = nested
	}
	return false
}

func allocationInitializer(function *ssa.Function, allocation *ssa.Alloc, typ types.Type, pkg *packages.Package, ix *index) (aggregateInitializer, bool) {
	if function == nil || function.Syntax() == nil || allocation == nil || pkg == nil || pkg.TypesInfo == nil || ix == nil || ix.fset == nil {
		return aggregateInitializer{}, false
	}
	position := ix.fset.Position(allocation.Pos())
	if position.Line == 0 {
		return aggregateInitializer{}, false
	}
	var candidates []aggregateInitializer
	ast.Inspect(function.Syntax(), func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CompositeLit:
			literalType := pkg.TypesInfo.TypeOf(value)
			if literalType == nil || !types.Identical(types.Unalias(literalType), types.Unalias(typ)) {
				return true
			}
			contains := value.Pos() <= allocation.Pos() && allocation.Pos() <= value.End()
			if contains {
				candidates = append(candidates, aggregateInitializer{literal: value})
			}
		case *ast.ValueSpec:
			for nameIndex, name := range value.Names {
				if name.Pos() != allocation.Pos() {
					continue
				}
				object := pkg.TypesInfo.Defs[name]
				variable, ok := object.(*types.Var)
				if !ok || !types.Identical(types.Unalias(variable.Type()), types.Unalias(typ)) {
					continue
				}
				if len(value.Values) == 0 {
					candidates = append(candidates, aggregateInitializer{zero: true})
					continue
				}
				if len(value.Values) != len(value.Names) || nameIndex >= len(value.Values) {
					continue
				}
				if literal, ok := unparenExpr(value.Values[nameIndex]).(*ast.CompositeLit); ok {
					literalType := pkg.TypesInfo.TypeOf(literal)
					if literalType != nil && types.Identical(types.Unalias(literalType), types.Unalias(typ)) {
						candidates = append(candidates, aggregateInitializer{literal: literal})
					}
				}
			}
		case *ast.AssignStmt:
			if value.Tok != token.DEFINE || len(value.Lhs) != len(value.Rhs) {
				return true
			}
			for targetIndex, target := range value.Lhs {
				identifier, ok := target.(*ast.Ident)
				if !ok || identifier.Pos() != allocation.Pos() {
					continue
				}
				variable, ok := pkg.TypesInfo.Defs[identifier].(*types.Var)
				if !ok || !types.Identical(types.Unalias(variable.Type()), types.Unalias(typ)) {
					continue
				}
				literal, ok := unparenExpr(value.Rhs[targetIndex]).(*ast.CompositeLit)
				if !ok {
					continue
				}
				literalType := pkg.TypesInfo.TypeOf(literal)
				if literalType != nil && types.Identical(types.Unalias(literalType), types.Unalias(typ)) {
					candidates = append(candidates, aggregateInitializer{literal: literal})
				}
			}
		case *ast.CallExpr:
			if value.Pos() > allocation.Pos() || value.End() < allocation.Pos() || len(value.Args) != 1 {
				return true
			}
			name, ok := value.Fun.(*ast.Ident)
			if !ok || name.Name != "new" {
				return true
			}
			allocatedType := pkg.TypesInfo.TypeOf(value.Args[0])
			if allocatedType != nil && types.Identical(types.Unalias(allocatedType), types.Unalias(typ)) {
				candidates = append(candidates, aggregateInitializer{zero: true})
			}
		}
		return true
	})
	if len(candidates) != 1 {
		return aggregateInitializer{}, false
	}
	return candidates[0], true
}

func compositeFieldValue(literal *ast.CompositeLit, fieldIndex int, typ types.Type, pkg *packages.Package) (ast.Expr, bool) {
	structType, ok := typ.Underlying().(*types.Struct)
	if !ok || fieldIndex < 0 || fieldIndex >= structType.NumFields() {
		return nil, false
	}
	field := structType.Field(fieldIndex)
	for index, element := range literal.Elts {
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			identifier, ok := keyed.Key.(*ast.Ident)
			if ok {
				object, _ := pkg.TypesInfo.Uses[identifier].(*types.Var)
				if object == field || identifier.Name == field.Name() {
					return keyed.Value, true
				}
			}
			continue
		}
		if index == fieldIndex {
			return element, true
		}
	}
	return nil, false
}

func unparenExpr(expression ast.Expr) ast.Expr {
	for {
		paren, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = paren.X
	}
}
