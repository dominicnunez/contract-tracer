package contracttrace

import (
	"go/ast"
	"go/constant"
	"go/types"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

type functionElementInitialization uint8

const (
	functionElementUnsupported functionElementInitialization = iota
	functionElementFullyInitialized
	functionElementMayBeZero
)

// possiblePositiveLength reports whether a make-slice length can be positive.
func possiblePositiveLength(length ssa.Value) bool {
	if c, ok := length.(*ssa.Const); ok && c.Value != nil {
		return constant.Sign(c.Value) > 0
	}
	return true
}

// sequenceMayHaveElement distinguishes a zero-length slice from a view that
// can address at least one element. Unknown lengths remain possible.
func sequenceMayHaveElement(value ssa.Value) bool {
	return sequenceMayHaveElementSeen(value, map[ssa.Value]bool{}, 0)
}

func sequenceMayHaveElementSeen(value ssa.Value, seen map[ssa.Value]bool, depth int) bool {
	if value == nil || depth >= maxFlowValues {
		return true
	}
	if seen[value] {
		return true
	}
	switch sequence := value.(type) {
	case *ssa.MakeSlice:
		return possiblePositiveLength(sequence.Len)
	case *ssa.Slice:
		low, lowOK := sequence.Low.(*ssa.Const)
		high, highOK := sequence.High.(*ssa.Const)
		lowValue, lowConst := int64(0), sequence.Low == nil
		if lowOK && low.Value != nil {
			lowValue, lowConst = constant.Int64Val(low.Value)
		}
		highValue, highConst := int64(0), false
		if highOK && high.Value != nil {
			highValue, highConst = constant.Int64Val(high.Value)
		}
		if lowConst && highConst {
			return highValue > lowValue
		}
		return true
	case *ssa.Phi:
		seen[value] = true
		defer delete(seen, value)
		for _, edge := range sequence.Edges {
			if sequenceMayHaveElementSeen(edge, seen, depth+1) {
				return true
			}
		}
		return false
	case *ssa.ChangeType:
		return sequenceMayHaveElementSeen(sequence.X, seen, depth+1)
	case *ssa.Convert:
		return sequenceMayHaveElementSeen(sequence.X, seen, depth+1)
	default:
		return true
	}
}

// zeroFunctionArrayAtAddress recognizes only a source-bound array allocation
// and a field path to its function-array value. Unsupported origins remain
// separate from proven zero initialization.
func (a *flowAnalysis) zeroFunctionArrayAtAddress(address string, ix *index) functionElementInitialization {
	if state := a.zeroFunctionArrayCache[address]; state != 0 {
		return functionElementInitialization(state)
	}
	root, found := longestAllocationRoot(address, a.allocations, false)
	if !found {
		return functionElementUnsupported
	}
	allocation := a.allocations[root]
	if allocation == nil || allocation.Parent() == nil {
		return functionElementUnsupported
	}
	pointer, ok := allocation.Type().Underlying().(*types.Pointer)
	if !ok {
		return functionElementUnsupported
	}
	indexes, ok := allocationFieldIndexes(root, address)
	if !ok {
		return functionElementUnsupported
	}
	arrayType, ok := functionArrayAtPath(pointer.Elem(), indexes)
	if !ok || arrayType.Len() == 0 {
		return functionElementUnsupported
	}
	owner := ix.funcs[ix.owner(allocation.Parent())]
	if owner == nil || owner.pkg == nil || owner.pkg.TypesInfo == nil {
		return functionElementUnsupported
	}
	initializer, found := allocationInitializer(allocation.Parent(), allocation, pointer.Elem(), owner.pkg, ix)
	if !found {
		if state, recognized := sliceBackingAllocationState(allocation.Parent(), allocation, owner.pkg); recognized {
			a.cacheZeroFunctionArray(address, state)
			return state
		}
		return functionElementUnsupported
	}
	if initializer.zero {
		a.cacheZeroFunctionArray(address, functionElementMayBeZero)
		return functionElementMayBeZero
	}
	state, found := arrayLiteralPathState(initializer.literal, pointer.Elem(), indexes, owner.pkg)
	if !found {
		return functionElementUnsupported
	}
	a.cacheZeroFunctionArray(address, state)
	return state
}

func sliceBackingAllocationState(function *ssa.Function, allocation *ssa.Alloc, pkg *packages.Package) (functionElementInitialization, bool) {
	if function == nil || function.Syntax() == nil || allocation == nil || pkg == nil || pkg.TypesInfo == nil {
		return functionElementUnsupported, false
	}
	var candidates []functionElementInitialization
	ast.Inspect(function.Syntax(), func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			if value.Pos() > allocation.Pos() || value.End() < allocation.Pos() || len(value.Args) < 2 {
				return true
			}
			name, ok := value.Fun.(*ast.Ident)
			if !ok || name.Name != "make" {
				return true
			}
			typ := pkg.TypesInfo.TypeOf(value)
			if typ == nil {
				return true
			}
			slice, ok := types.Unalias(typ).Underlying().(*types.Slice)
			if !ok || !inlineAggregate(slice.Elem()) && !isFunctionType(slice.Elem()) {
				return true
			}
			positive := expressionMayBePositive(value.Args[1], pkg)
			if len(value.Args) > 2 {
				positive = positive || expressionMayBePositive(value.Args[2], pkg)
			}
			if positive {
				candidates = append(candidates, functionElementMayBeZero)
			} else {
				candidates = append(candidates, functionElementFullyInitialized)
			}
		case *ast.CompositeLit:
			if value.Pos() > allocation.Pos() || value.End() < allocation.Pos() {
				return true
			}
			typ := pkg.TypesInfo.TypeOf(value)
			slice, ok := types.Unalias(typ).Underlying().(*types.Slice)
			if ok && (isFunctionType(slice.Elem()) || inlineAggregate(slice.Elem())) {
				if sliceLiteralHasOmittedElements(value, pkg) {
					candidates = append(candidates, functionElementMayBeZero)
				} else {
					candidates = append(candidates, functionElementFullyInitialized)
				}
			}
		}
		return true
	})
	if len(candidates) != 1 {
		return functionElementUnsupported, false
	}
	return candidates[0], true
}

func expressionMayBePositive(expression ast.Expr, pkg *packages.Package) bool {
	if pkg == nil || pkg.TypesInfo == nil {
		return true
	}
	value := pkg.TypesInfo.Types[expression].Value
	if value == nil || value.Kind() != constant.Int {
		return true
	}
	return constant.Sign(value) > 0
}

func (a *flowAnalysis) cacheZeroFunctionArray(address string, state functionElementInitialization) {
	if a.zeroFunctionArrayCache == nil {
		a.zeroFunctionArrayCache = map[string]uint8{}
	}
	a.zeroFunctionArrayCache[address] = uint8(state)
}

func (a *flowAnalysis) hasFunctionArrayAllocation(address string) bool {
	_, found := longestAllocationRoot(address, a.allocations, true)
	return found
}

func functionArrayAtPath(typ types.Type, indexes []int) (*types.Array, bool) {
	current := types.Unalias(typ)
	for _, fieldIndex := range indexes {
		structType, ok := current.Underlying().(*types.Struct)
		if !ok || fieldIndex < 0 || fieldIndex >= structType.NumFields() {
			return nil, false
		}
		current = types.Unalias(structType.Field(fieldIndex).Type())
		if _, pointer := current.Underlying().(*types.Pointer); pointer {
			return nil, false
		}
	}
	arrayType, ok := current.Underlying().(*types.Array)
	if !ok || !isFunctionType(arrayType.Elem()) {
		return nil, false
	}
	return arrayType, true
}

func arrayLiteralPathState(literal *ast.CompositeLit, typ types.Type, indexes []int, pkg *packages.Package) (functionElementInitialization, bool) {
	if len(indexes) == 0 {
		arrayType, ok := types.Unalias(typ).Underlying().(*types.Array)
		if !ok {
			return functionElementUnsupported, false
		}
		if arrayLiteralHasOmittedElements(literal, arrayType, pkg) {
			return functionElementMayBeZero, true
		}
		return functionElementFullyInitialized, true
	}
	current := types.Unalias(typ)
	for depth, fieldIndex := range indexes {
		structType, ok := current.Underlying().(*types.Struct)
		if !ok || fieldIndex < 0 || fieldIndex >= structType.NumFields() {
			return functionElementUnsupported, false
		}
		fieldType := types.Unalias(structType.Field(fieldIndex).Type())
		if _, pointer := fieldType.Underlying().(*types.Pointer); pointer {
			return functionElementUnsupported, false
		}
		fieldArray, terminal := fieldType.Underlying().(*types.Array)
		if depth == len(indexes)-1 {
			if !terminal || !isFunctionType(fieldArray.Elem()) {
				return functionElementUnsupported, false
			}
			value, present := compositeFieldValue(literal, fieldIndex, current, pkg)
			if !present {
				return functionElementMayBeZero, true
			}
			nested, ok := unparenExpr(value).(*ast.CompositeLit)
			if !ok {
				return functionElementUnsupported, false
			}
			return arrayLiteralPathState(nested, fieldType, nil, pkg)
		}
		if terminal {
			return functionElementUnsupported, false
		}
		value, present := compositeFieldValue(literal, fieldIndex, current, pkg)
		if !present {
			if _, ok := functionArrayAtPath(fieldType, indexes[depth+1:]); ok {
				return functionElementMayBeZero, true
			}
			return functionElementUnsupported, false
		}
		nested, ok := unparenExpr(value).(*ast.CompositeLit)
		if !ok {
			return functionElementUnsupported, false
		}
		literal, current = nested, fieldType
	}
	return functionElementUnsupported, false
}

func arrayLiteralHasOmittedElements(literal *ast.CompositeLit, array *types.Array, pkg *packages.Package) bool {
	if literal == nil || array == nil || pkg == nil || pkg.TypesInfo == nil {
		return false
	}
	covered := map[int64]bool{}
	nextIndex := int64(0)
	for _, element := range literal.Elts {
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			value := pkg.TypesInfo.Types[keyed.Key].Value
			if value == nil || value.Kind() != constant.Int {
				return true
			}
			constantIndex, ok := constant.Int64Val(value)
			if !ok || constantIndex < 0 || constantIndex >= array.Len() {
				return true
			}
			covered[constantIndex] = true
			nextIndex = constantIndex + 1
			continue
		}
		if nextIndex < array.Len() {
			covered[nextIndex] = true
		}
		nextIndex++
	}
	return int64(len(covered)) < array.Len()
}

// makeSliceFunctionElements source-binds MakeSlice either to a slice literal,
// whose keyed holes can be proven, or a make call with its SSA length. All
// other construction paths stay unresolved.
func makeSliceFunctionElements(function *ssa.Function, makeSlice *ssa.MakeSlice, ix *index) functionElementInitialization {
	if function == nil || function.Syntax() == nil || makeSlice == nil {
		return functionElementUnsupported
	}
	owner := ix.funcs[ix.owner(function)]
	if owner == nil || owner.pkg == nil || owner.pkg.TypesInfo == nil {
		return functionElementUnsupported
	}
	var candidates []functionElementInitialization
	ast.Inspect(function.Syntax(), func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CompositeLit:
			if value.Pos() > makeSlice.Pos() || value.End() < makeSlice.Pos() {
				return true
			}
			typ := owner.pkg.TypesInfo.TypeOf(value)
			if typ == nil {
				return true
			}
			slice, ok := types.Unalias(typ).Underlying().(*types.Slice)
			if !ok || !isFunctionType(slice.Elem()) && !inlineAggregate(slice.Elem()) || !types.Identical(types.Unalias(typ), types.Unalias(makeSlice.Type())) {
				return true
			}
			if sliceLiteralHasOmittedElements(value, owner.pkg) {
				candidates = append(candidates, functionElementMayBeZero)
			} else {
				candidates = append(candidates, functionElementFullyInitialized)
			}
		case *ast.CallExpr:
			if value.Pos() > makeSlice.Pos() || value.End() < makeSlice.Pos() || len(value.Args) < 1 {
				return true
			}
			name, ok := value.Fun.(*ast.Ident)
			if !ok || name.Name != "make" {
				return true
			}
			typ := owner.pkg.TypesInfo.TypeOf(value)
			if typ == nil {
				return true
			}
			slice, ok := types.Unalias(typ).Underlying().(*types.Slice)
			if ok && (isFunctionType(slice.Elem()) || inlineAggregate(slice.Elem())) && types.Identical(types.Unalias(typ), types.Unalias(makeSlice.Type())) {
				if possiblePositiveLength(makeSlice.Len) || possiblePositiveLength(makeSlice.Cap) {
					candidates = append(candidates, functionElementMayBeZero)
				} else {
					candidates = append(candidates, functionElementFullyInitialized)
				}
			}
		}
		return true
	})
	if len(candidates) != 1 {
		return functionElementUnsupported
	}
	return candidates[0]
}

func sliceLiteralHasOmittedElements(literal *ast.CompositeLit, pkg *packages.Package) bool {
	if literal == nil || pkg == nil || pkg.TypesInfo == nil {
		return false
	}
	covered := map[int64]bool{}
	nextIndex, maxIndex := int64(0), int64(-1)
	for _, element := range literal.Elts {
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			value := pkg.TypesInfo.Types[keyed.Key].Value
			if value == nil || value.Kind() != constant.Int {
				return true
			}
			index, ok := constant.Int64Val(value)
			if !ok || index < 0 {
				return true
			}
			covered[index] = true
			nextIndex, maxIndex = index+1, max64(maxIndex, index)
			continue
		}
		covered[nextIndex] = true
		maxIndex, nextIndex = max64(maxIndex, nextIndex), nextIndex+1
	}
	return int64(len(covered)) < maxIndex+1
}

func sliceElementsHaveZero(length, capacity ssa.Value) bool {
	return possiblePositiveLength(length) || possiblePositiveLength(capacity)
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func cacheSequenceElement(a *flowAnalysis, address string, state functionElementInitialization) {
	if state == functionElementUnsupported {
		return
	}
	if a.knownFunctionElements == nil {
		a.knownFunctionElements = map[string]bool{}
	}
	a.knownFunctionElements[address] = true
	if state == functionElementMayBeZero {
		if a.zeroFunctionElements == nil {
			a.zeroFunctionElements = map[string]bool{}
		}
		a.zeroFunctionElements[address] = true
	}
}

// functionElementValue returns the zero/unknown alternative for one candidate
// sequence root. Callers merge one result per root so source branch ordering
// cannot erase another root's alternative.
func (a *flowAnalysis) functionElementValue(sequence ssa.Value, root, address string, ix *index) flowValue {
	result := emptyFlow()
	if _, isSlice := sequence.Type().Underlying().(*types.Slice); isSlice {
		if !a.knownFunctionElements[address] && !a.unknownFunctionElements[address] {
			state := a.zeroFunctionArrayAtAddress(root, ix)
			if state != functionElementUnsupported || a.hasFunctionArrayAllocation(root) {
				if state == functionElementUnsupported {
					if a.unknownFunctionElements == nil {
						a.unknownFunctionElements = map[string]bool{}
					}
					a.unknownFunctionElements[address] = true
				} else {
					cacheSequenceElement(a, address, state)
				}
			}
		}
		if sequenceMayHaveElement(sequence) {
			result.functionNil = a.zeroFunctionElements[address]
			result.functionUnknown = a.unknownFunctionElements[address] || a.get(sequence).functionUnknown
		}
		return result
	}
	if !a.knownFunctionElements[address] && !a.unknownFunctionElements[address] {
		state := a.zeroFunctionArrayAtAddress(root, ix)
		if state != functionElementUnsupported || a.hasFunctionArrayAllocation(root) {
			if state == functionElementUnsupported {
				if a.unknownFunctionElements == nil {
					a.unknownFunctionElements = map[string]bool{}
				}
				a.unknownFunctionElements[address] = true
			} else {
				if a.knownFunctionElements == nil {
					a.knownFunctionElements = map[string]bool{}
				}
				a.knownFunctionElements[address] = true
				if state == functionElementMayBeZero {
					if a.zeroFunctionArrayElements == nil {
						a.zeroFunctionArrayElements = map[string]bool{}
					}
					a.zeroFunctionArrayElements[address] = true
				}
			}
		}
	}
	result.functionNil = a.zeroFunctionArrayElements[address]
	result.functionUnknown = a.unknownFunctionElements[address] || a.get(sequence).functionUnknown
	return result
}
