package contracttrace

import (
	"fmt"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

func sensitiveInputType(typ types.Type, seen map[types.Type]bool) bool {
	if isSyncWaitGroupType(typ) {
		return true
	}
	if seen[typ] {
		return false
	}
	seen[typ] = true
	if isFunctionType(typ) || isSQLHandleType(typ) || isErrorType(typ) || isStringType(typ) || isScalarType(typ) {
		return true
	}
	switch shape := typ.Underlying().(type) {
	case *types.Interface:
		return true
	case *types.Chan:
		return true
	case *types.Pointer:
		return sensitiveInputType(shape.Elem(), seen)
	case *types.Array:
		return sensitiveInputType(shape.Elem(), seen)
	case *types.Slice:
		return sensitiveInputType(shape.Elem(), seen)
	case *types.Map:
		return sensitiveInputType(shape.Key(), seen) || sensitiveInputType(shape.Elem(), seen)
	case *types.Struct:
		for i := 0; i < shape.NumFields(); i++ {
			field := shape.Field(i)
			if (field.Exported() || embeddedRecord(field)) && sensitiveInputType(field.Type(), seen) {
				return true
			}
		}
	}
	return false
}

func isSyncWaitGroupType(typ types.Type) bool {
	for {
		unaliased := types.Unalias(typ)
		if pointer, ok := unaliased.(*types.Pointer); ok {
			typ = pointer.Elem()
			continue
		}
		named, ok := unaliased.(*types.Named)
		return ok && named.Obj().Name() == "WaitGroup" && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "sync"
	}
}

// Input roots are source-backed type summaries, not runtime allocations. Reuse
// pointee type roots to bound cycles; concrete local candidates remain separate.
func (a *flowAnalysis) seedAggregateInput(parameter *ssa.Parameter, ix *index) bool {
	root := fmt.Sprintf("input:%s:%s", parameter.Parent().String(), parameter.Name())
	return a.seedAggregateOrigin(parameter, root, false, ix.evidence(parameter.Pos()), ix)
}

func (a *flowAnalysis) seedAggregateOrigin(source ssa.Value, root string, result bool, evidence Evidence, ix *index) bool {
	typ := source.Type()
	if isFunctionType(typ) || isSQLHandleType(typ) || !sensitiveInputType(typ, map[types.Type]bool{}) && !a.lifecycleSensitiveType(typ, map[types.Type]bool{}) {
		return false
	}
	if a.inputRoots == nil {
		a.inputRoots = map[string]bool{}
	}
	if a.inputRoots[root] {
		return false
	}
	a.inputRoots[root] = true
	owner := ""
	if global, ok := source.(*ssa.Global); ok {
		owner = globalID(global)
	} else {
		owner = ix.owner(source.Parent())
	}
	if evidence.Line < 1 {
		evidence = ix.funcs[owner].node.Evidence
		evidence.Origin = "synthetic_declaration"
	}
	changed := false
	representatives := map[types.Type]string{}
	type identity struct {
		address string
		typ     types.Type
	}
	visited := map[identity]bool{}
	work := 0
	var shape func(string, types.Type)
	var cell func(string, types.Type)
	containerCell := func(address string, typ types.Type) {
		if target := representatives[typ]; target != "" && inlineAggregate(typ) {
			value := emptyFlow()
			value.addresses[target] = true
			if a.store(address, value) {
				changed = true
			}
		} else {
			cell(address, typ)
		}
	}
	cell = func(address string, typ types.Type) {
		value := emptyFlow()
		if isScalarType(typ) {
			value = a.outsideScalar(address, root, evidence, ix)
		}
		if isErrorType(typ) {
			id := "error-input:" + address
			value.errors = map[string]bool{id: true}
			value.interfaceUnknown = isInterfaceType(typ)
			if ix.funcs[id] == nil {
				ix.funcs[id] = &function{node: Node{ID: id, Name: "unresolved error in " + address, Kind: "error_input", Evidence: evidence}}
				ix.edges = append(ix.edges, Relationship{From: root, To: id, Kind: "external_input", Certainty: "possible", Evidence: evidence})
				ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "outside_error_input", Reason: "Public error-typed aggregate storage can contain outside error values alongside modeled local origins. Shared type/container summaries do not prove invocation, object identity, a non-nil error or correct handling.", Evidence: evidence})
			}
		}
		switch {
		case isStringType(typ):
			value.stringUnknown = true
			ix.boundaries = append(ix.boundaries, Boundary{Node: root, Kind: "outside_string_input", Reason: "Outside string storage at " + address + " retains unknown values alongside local strings; known local formats, queries or event names do not close its origin set.", Evidence: evidence})
		case isInterfaceType(typ):
			value.interfaceUnknown = true
		case isFunctionType(typ):
			value.functionUnknown = true
		case isSQLHandleType(typ):
			value.sqlUnknown = true
		default:
			switch concrete := typ.Underlying().(type) {
			case *types.Pointer:
				target := representatives[concrete.Elem()]
				if target == "" {
					target = address + ".pointee"
					shape(target, concrete.Elem())
				}
				value.addresses[target] = true
			case *types.Chan:
				target := representatives[typ]
				if target == "" {
					target = address + ".channel"
					shape(target, typ)
				}
				value.addresses["channel:outside:"+target] = true
			case *types.Struct, *types.Array, *types.Slice, *types.Map:
				shape(address, typ)
				value.addresses[address] = true
			}
		}
		if a.store(address, value) {
			changed = true
		}
	}
	shape = func(address string, typ types.Type) {
		work++
		if work > maxFlowValues*maxFlowValues {
			a.coverage.Widened = true
			return
		}
		key := identity{address, typ}
		if visited[key] {
			return
		}
		visited[key] = true
		if representatives[typ] == "" {
			representatives[typ] = address
		}
		a.addressTypes[address] = typ
		if isFunctionType(typ) || isSQLHandleType(typ) || isStringType(typ) || isScalarType(typ) {
			cell(address, typ)
			return
		}
		// Implementing error does not make a record's public fields opaque.
		if isErrorType(typ) {
			cell(address, typ)
		}
		// A pointer to a slice/map root must load the summarized container,
		// including when there is no local allocation or initializer.
		switch typ.Underlying().(type) {
		case *types.Slice, *types.Map:
			alias := emptyFlow()
			alias.addresses[address] = true
			if a.store(address, alias) {
				changed = true
			}
		}
		switch concrete := typ.Underlying().(type) {
		case *types.Interface:
			cell(address, typ)
		case *types.Pointer:
			cell(address, typ)
		case *types.Struct:
			for i := 0; i < concrete.NumFields(); i++ {
				field := concrete.Field(i)
				if field.Exported() || embeddedRecord(field) {
					cell(fmt.Sprintf("%s.field:%d", address, i), field.Type())
				}
			}
		case *types.Array:
			containerCell(address+".element", concrete.Elem())
		case *types.Slice:
			containerCell(address+".element", concrete.Elem())
		case *types.Map:
			containerCell(address+".keys", concrete.Key())
			containerCell(address+".key:*", concrete.Elem())
		case *types.Chan:
			key := "channel:outside:" + address
			alias := emptyFlow()
			alias.addresses[key] = true
			if a.store(address, alias) {
				changed = true
			}
			if ix.funcs[key] == nil {
				ix.funcs[key] = &function{node: Node{ID: key, Name: "outside channel " + address, Kind: "channel", Evidence: evidence}}
				ix.edges = append(ix.edges, Relationship{From: root, To: key, Kind: "external_input", Certainty: "possible", Evidence: evidence})
				ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "outside_channel", Reason: "Outside channel identity and typed contents remain candidates alongside local channels. Nil values, send/receive pairing, closure, callback execution and temporal ownership are not proved; repeated channel types share bounded summary storage.", Evidence: evidence})
			}
			containerCell(key+".sent", concrete.Elem())
		}
	}
	// The source pointer names its pointee object. Pointers stored inside that
	// object retain their own cells, so each dereference consumes one layer.
	if pointer, ok := typ.Underlying().(*types.Pointer); ok {
		shape(root, pointer.Elem())
	} else {
		shape(root, typ)
	}
	value := emptyFlow()
	if isScalarType(typ) {
		a.mergeScalar(&value, a.memory[root])
	}
	value.interfaceUnknown = isInterfaceType(typ)
	value.stringUnknown = isStringType(typ)
	if _, channel := typ.Underlying().(*types.Chan); channel {
		value.addresses["channel:outside:"+root] = true
	} else {
		value.addresses[root] = true
	}
	if a.put(source, value) {
		changed = true
	}
	kind, edgeKind, boundaryKind := "input", "external_input", "aggregate_input_model"
	reason := "outside aggregate/interface input through a public API or escaped callback retains synthetic public function/SQL/interface/error storage candidates alongside local values; pointee types are shared to bound cycles; private fields, other outside value domains and runtime object identity are not established"
	if result {
		kind, edgeKind, boundaryKind = "result", "external_result", "aggregate_result_model"
		reason = "outside or unresolved aggregate/interface result retains synthetic public function/SQL/interface/error storage candidates alongside local results; pointee types are shared to bound cycles; private fields, other outside value domains and runtime object identity are not established"
	}
	name := source.Name()
	if _, global := source.(*ssa.Global); global {
		name = "outside storage of " + name
	}
	ix.funcs[root] = &function{node: Node{ID: root, Name: name, Kind: kind, Evidence: evidence}}
	ix.edges = append(ix.edges, Relationship{From: owner, To: root, Kind: edgeKind, Certainty: "possible", Evidence: evidence})
	ix.boundaries = append(ix.boundaries, Boundary{Node: root, Kind: boundaryKind, Reason: reason, Evidence: evidence})
	return changed
}
