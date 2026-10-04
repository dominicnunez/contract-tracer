package contracttrace

import (
	"fmt"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// Dependency calls can retain and invoke function arguments outside the local
// graph. This records escape candidates, not proof of callback invocation.
func (a *flowAnalysis) callbackEscape(call ssa.CallInstruction, ix *index) bool {
	if !a.callbackOutside(call.Common(), ix) {
		return false
	}
	changed := false
	for _, argument := range call.Common().Args {
		for _, callback := range sortedFunctions(a.accessibleCallbacks(argument)) {
			if ix.owner(callback) == "" {
				continue
			}
			if a.callbackEscapes == nil {
				a.callbackEscapes = map[ssa.CallInstruction]map[*ssa.Function]bool{}
			}
			if a.callbackEscapes[call] == nil {
				a.callbackEscapes[call] = map[*ssa.Function]bool{}
			}
			a.callbackEscapes[call][callback] = true
			if a.markCallbackInputs(callback, ix) {
				changed = true
			}
		}
	}
	return changed
}

func (a *flowAnalysis) callbackOutside(common *ssa.CallCommon, ix *index) bool {
	if iteratorFactory(common) != "" {
		return false
	}
	outside := a.get(common.Value).functionUnknown || common.IsInvoke() && a.get(common.Value).interfaceUnknown
	for target := range a.targets(common) {
		outside = outside || ix.owner(target) == "" || len(target.Blocks) == 0
	}
	return outside
}

func (a *flowAnalysis) markCallbackInputs(callback *ssa.Function, ix *index) bool {
	changed := false
	for _, parameter := range callback.Params {
		if a.seedFormatInput(parameter) {
			changed = true
		}
		if a.seedErrorInput(parameter, ix, "This callback can escape through an exported result, public storage or a dependency/unmodeled call and receive outside error values.") {
			changed = true
		}
		if a.seedAggregateInput(parameter, ix) {
			changed = true
		}
		if isFunctionType(parameter.Type()) {
			unknown := emptyFlow()
			unknown.functionUnknown = true
			if a.put(parameter, unknown) {
				changed = true
			}
		}
		if !isSQLHandleType(parameter.Type()) {
			continue
		}
		value := emptyFlow()
		value.sqlUnknown = true
		if a.put(parameter, value) {
			changed = true
		}
	}
	return changed
}

func (a *flowAnalysis) callbackReturnEscape(returned *ssa.Return, ix *index) bool {
	fn := returned.Parent()
	if fn.Object() == nil || !fn.Object().Exported() {
		return false
	}
	changed := false
	for _, result := range returned.Results {
		for _, callback := range sortedFunctions(a.accessibleCallbacks(result)) {
			if ix.owner(callback) == "" {
				continue
			}
			if a.callbackReturns == nil {
				a.callbackReturns = map[*ssa.Return]map[*ssa.Function]bool{}
			}
			if a.callbackReturns[returned] == nil {
				a.callbackReturns[returned] = map[*ssa.Function]bool{}
			}
			a.callbackReturns[returned][callback] = true
			if a.markCallbackInputs(callback, ix) {
				changed = true
			}
		}
	}
	return changed
}

// Follow public fields and container contents through modeled memory. Address/type pairs break
// pointer cycles; the total traversal budget exposes widening instead of making
// a recursive or very large record appear completely analyzed.
func (a *flowAnalysis) accessibleCallbacks(value ssa.Value) map[*ssa.Function]bool {
	roots := a.get(value)
	if cached := a.callbackCache[value]; cached != nil && cached.valid(a, roots) {
		return cached.functions
	}
	cached := &callbackStorageCache{roots: roots, reads: map[string]uint64{}, types: map[string]types.Type{}, mapSizes: map[string]int{}}
	found := a.accessibleStorageTracked(value, nil, nil, nil, nil, cached, nil)
	if !cached.volatile {
		if a.callbackCache == nil {
			a.callbackCache = map[ssa.Value]*callbackStorageCache{}
		}
		cached.functions = found
		a.callbackCache[value] = cached
	}
	return found
}

func (a *flowAnalysis) accessibleValues(value ssa.Value, sqlCells map[string]bool) map[*ssa.Function]bool {
	return a.accessibleStorage(value, sqlCells, nil, nil)
}

func (a *flowAnalysis) accessibleStorage(value ssa.Value, sqlCells, functionCells, interfaceCells map[string]bool) map[*ssa.Function]bool {
	return a.accessibleStorageTracked(value, sqlCells, functionCells, interfaceCells, nil, nil, nil)
}

func (a *flowAnalysis) accessibleStorageTracked(value ssa.Value, sqlCells, functionCells, interfaceCells map[string]bool, outsideCells map[string]types.Type, cache *callbackStorageCache, effects map[string]bool) map[*ssa.Function]bool {
	markOutsideCell := func(address string, typ types.Type) {
		if outsideCells != nil && (isErrorType(typ) || isStringType(typ) || isScalarType(typ)) {
			outsideCells[address] = typ
		}
	}
	read := func(address string) flowValue {
		if cache != nil {
			cache.reads[address] = a.memoryVersions[address]
		}
		return a.memory[address]
	}
	type entry struct {
		value flowValue
		typ   types.Type
	}
	type identity struct {
		address string
		typ     types.Type
	}
	queue := []entry{{a.get(value), value.Type()}}
	enqueue := func(contents flowValue, typ types.Type) {
		if len(contents.functions) == 0 && len(contents.addresses) == 0 && len(contents.effects) == 0 {
			return
		}
		if len(queue) >= maxFlowValues*maxFlowValues {
			a.coverage.Widened = true
			return
		}
		queue = append(queue, entry{contents, typ})
	}
	seen := map[identity]bool{}
	found := emptyFlow()
	seenIterators := map[string]bool{}
	for head := 0; head < len(queue); head++ {
		if head >= maxFlowValues*maxFlowValues {
			a.coverage.Widened = true
			break
		}
		current := queue[head]
		if cache != nil && len(current.value.effects) != 0 {
			cache.volatile = true
		}
		for _, effect := range sortedKeys(current.value.effects) {
			if effects != nil {
				effects[effect] = true
			}
			if summary, ok := a.iterators[effect]; ok && !seenIterators[effect] {
				seenIterators[effect] = true
				for _, candidate := range a.iteratorValues(summary) {
					enqueue(candidate.value, candidate.typ)
				}
			}
		}
		functions := emptyFlow()
		functions.functions = current.value.functions
		a.merge(&found, functions)
		for _, address := range sortedKeys(current.value.addresses) {
			key := identity{address, current.typ}
			if seen[key] {
				continue
			}
			seen[key] = true
			switch typ := current.typ.Underlying().(type) {
			case *types.Interface:
				if cache != nil {
					cache.types[address] = a.addressTypes[address]
				}
				if concrete := a.addressTypes[address]; concrete != nil {
					contents := emptyFlow()
					contents.addresses[address] = true
					enqueue(contents, concrete)
				}
			case *types.Pointer:
				markOutsideCell(address, typ.Elem())
				if interfaceCells != nil && isInterfaceType(typ.Elem()) {
					interfaceCells[address] = true
				}
				if functionCells != nil && isFunctionType(typ.Elem()) {
					functionCells[address] = true
				}
				pointed := emptyFlow()
				a.merge(&pointed, read(address))
				if inlineAggregate(typ.Elem()) {
					pointed.addresses[address] = true
				}
				enqueue(pointed, typ.Elem())
			case *types.Array, *types.Slice, *types.Chan:
				var element types.Type
				suffix := ".element"
				if array, ok := typ.(*types.Array); ok {
					element = array.Elem()
				} else if slice, ok := typ.(*types.Slice); ok {
					element = slice.Elem()
				} else {
					element = typ.(*types.Chan).Elem()
					suffix = ".sent"
				}
				cell := address + suffix
				markOutsideCell(cell, element)
				contents := emptyFlow()
				if interfaceCells != nil && isInterfaceType(element) {
					interfaceCells[cell] = true
				}
				if sqlCells != nil && isSQLHandleType(element) {
					sqlCells[cell] = true
				}
				if functionCells != nil && isFunctionType(element) {
					functionCells[cell] = true
				}
				a.merge(&contents, read(cell))
				if inlineAggregate(element) {
					contents.addresses[cell] = true
				}
				enqueue(contents, element)
			case *types.Map:
				markOutsideCell(address+".key:*", typ.Elem())
				markOutsideCell(address+".keys", typ.Key())
				for entryAddress := range a.mapEntries[address] {
					markOutsideCell(entryAddress, typ.Elem())
				}
				if cache != nil {
					cache.mapSizes[address] = len(a.mapEntries[address])
				}
				if interfaceCells != nil && isInterfaceType(typ.Elem()) {
					interfaceCells[address+".key:*"] = true
				}
				if interfaceCells != nil && isInterfaceType(typ.Key()) {
					interfaceCells[address+".keys"] = true
				}
				if functionCells != nil && isFunctionType(typ.Elem()) {
					functionCells[address+".key:*"] = true
				}
				if sqlCells != nil && isSQLHandleType(typ.Elem()) {
					sqlCells[address+".key:*"] = true
				}
				for _, entryAddress := range sortedKeys(a.mapEntries[address]) {
					enqueue(read(entryAddress), typ.Elem())
				}
				enqueue(read(address+".keys"), typ.Key())
			case *types.Struct:
				for field := 0; field < typ.NumFields(); field++ {
					member := typ.Field(field)
					if !member.Exported() && !embeddedRecord(member) {
						continue
					}
					fieldAddress := fmt.Sprintf("%s.field:%d", address, field)
					contents := emptyFlow()
					a.merge(&contents, read(fieldAddress))
					fieldType := typ.Field(field).Type()
					markOutsideCell(fieldAddress, fieldType)
					if interfaceCells != nil && isInterfaceType(fieldType) {
						interfaceCells[fieldAddress] = true
					}
					if functionCells != nil && isFunctionType(fieldType) {
						functionCells[fieldAddress] = true
					}
					if sqlCells != nil && isSQLHandleType(fieldType) {
						sqlCells[fieldAddress] = true
					}
					if inlineAggregate(fieldType) {
						contents.addresses[fieldAddress] = true
					}
					enqueue(contents, fieldType)
				}
			}
		}
	}
	return found.functions
}

func embeddedRecord(field *types.Var) bool {
	if !field.Embedded() {
		return false
	}
	typ := field.Type().Underlying()
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = pointer.Elem().Underlying()
	}
	_, record := typ.(*types.Struct)
	return record
}

func (a *flowAnalysis) callbackReturnRelationships(returned *ssa.Return, ix *index) {
	evidence := ix.evidence(returned.Pos())
	if returned.Pos() == token.NoPos {
		evidence = ix.funcs[ix.owner(returned.Parent())].node.Evidence
		evidence.Origin = "synthetic_declaration"
	}
	for _, callback := range sortedFunctions(a.callbackReturns[returned]) {
		owner := ix.owner(callback)
		ix.edges = append(ix.edges, Relationship{From: ix.owner(returned.Parent()), To: owner, Kind: "callback_return_escape", Certainty: "possible", Evidence: evidence})
		ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "external_callback", Reason: "function-value candidate is returned by an exported function; callers outside the selected build may retain/invoke it with unknown arguments and timing", Evidence: evidence})
		for _, parameter := range callback.Params {
			if isSQLHandleType(parameter.Type()) {
				ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "escaped_sql_input", Reason: "callback with SQL-handle parameters is returned through an exported API; outside callback inputs remain possible", Evidence: evidence})
				break
			}
		}
	}
}

func (a *flowAnalysis) callbackEscapeRelationships(call ssa.CallInstruction, ix *index) {
	for _, callback := range sortedFunctions(a.callbackEscapes[call]) {
		owner := ix.owner(callback)
		ix.callEdge(ix.owner(call.Parent()), owner, "callback_escape", "possible", call)
		ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "external_callback", Reason: "function-value candidate is passed to a dependency or unmodeled body; retention, invocation, supplied arguments and timing require investigation", Evidence: ix.callEvidence(call)})
		for _, parameter := range callback.Params {
			if isSQLHandleType(parameter.Type()) {
				ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "escaped_sql_input", Reason: "callback with SQL-handle parameters is passed to a dependency or unmodeled body; unknown callback inputs remain possible, but invocation is not established", Evidence: ix.callEvidence(call)})
				break
			}
		}
	}
	if a.callbackOutside(call.Common(), ix) {
		for _, argument := range call.Common().Args {
			callback := a.get(argument)
			if callback.functionNil {
				ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(call.Parent()), Kind: "unresolved_callback_nil", Reason: "an escaping function argument retains a typed nil callback alternative alongside any local callback candidates; retention or successful invocation of the nil value is not inferred", Evidence: ix.callEvidence(call)})
				break
			}
		}
	}
}
