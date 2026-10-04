package contracttrace

import (
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func globalID(global *ssa.Global) string {
	return "global:" + global.Pkg.Pkg.Path() + "::" + global.Name()
}

func sqlHandleGlobal(global *ssa.Global) bool {
	pointer, ok := global.Type().Underlying().(*types.Pointer)
	return ok && isSQLHandleType(pointer.Elem())
}

func (a *flowAnalysis) callbackGlobalEscapes(ix *index) bool {
	changed := false
	for _, global := range a.publicGlobals {
		if a.seedAggregateOrigin(global, "input:global:"+global.String(), false, ix.evidence(global.Pos()), ix) {
			changed = true
		}
		cells := map[string]bool{}
		functionCells := map[string]bool{}
		interfaceCells := map[string]bool{}
		outsideCells := map[string]types.Type{}
		callbacks := a.accessibleStorageTracked(global, cells, functionCells, interfaceCells, outsideCells, nil, nil)
		for _, cell := range sortedKeys(outsideCells) {
			if isScalarType(outsideCells[cell]) {
				if a.store(cell, a.outsideScalar("global:"+global.String()+":"+cell, globalID(global), ix.evidence(global.Pos()), ix)) {
					changed = true
				}
				continue
			}
			if isStringType(outsideCells[cell]) {
				unknown := emptyFlow()
				unknown.stringUnknown = true
				if a.store(cell, unknown) {
					changed = true
					ix.boundaries = append(ix.boundaries, Boundary{Node: globalID(global), Kind: "outside_string_input", Reason: "Outside callers can replace exported string storage or mutate its accessible contents, including through private aliases. Known local strings remain candidates; runtime mutation timing and arbitrary string values are unresolved.", Evidence: ix.evidence(global.Pos())})
				}
				continue
			}
			id := "error-input:global:" + global.String() + ":" + cell
			if ix.funcs[id] == nil {
				evidence := ix.evidence(global.Pos())
				ix.funcs[id] = &function{node: Node{ID: id, Name: "outside error in " + global.Name(), Kind: "error_input", Evidence: evidence}}
				ix.edges = append(ix.edges, Relationship{From: globalID(global), To: id, Kind: "external_input", Certainty: "possible", Evidence: evidence})
				ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "outside_error_input", Reason: "Outside callers can replace exported error storage or mutate accessible error contents. Known local origins remain alongside this candidate; private aliases of shared public objects do not close its origin set. Runtime identity, mutation timing and a non-nil error are not proved.", Evidence: evidence})
			}
			unknown := emptyFlow()
			unknown.errors = map[string]bool{id: true}
			if a.store(cell, unknown) {
				changed = true
			}
		}
		for _, cell := range sortedKeys(interfaceCells) {
			unknown := emptyFlow()
			unknown.interfaceUnknown = true
			if a.store(cell, unknown) {
				changed = true
			}
		}
		for _, cell := range sortedKeys(functionCells) {
			unknown := emptyFlow()
			unknown.functionUnknown = true
			if a.store(cell, unknown) {
				changed = true
			}
		}
		for _, cell := range sortedKeys(cells) {
			if a.globalSQLFields == nil {
				a.globalSQLFields = map[*ssa.Global]bool{}
			}
			a.globalSQLFields[global] = true
			unknown := emptyFlow()
			unknown.sqlUnknown = true
			if a.store(cell, unknown) {
				changed = true
			}
		}
		for _, callback := range sortedFunctions(callbacks) {
			if ix.owner(callback) == "" {
				continue
			}
			if a.globalCallbacks == nil {
				a.globalCallbacks = map[*ssa.Global]map[*ssa.Function]bool{}
			}
			if a.globalCallbacks[global] == nil {
				a.globalCallbacks[global] = map[*ssa.Function]bool{}
			}
			a.globalCallbacks[global][callback] = true
			if a.markCallbackInputs(callback, ix) {
				changed = true
			}
		}
	}
	return changed
}

func (a *flowAnalysis) globalRelationships(ix *index) {
	a.globalAddressOwners = map[string]map[string]bool{}
	for _, global := range a.localGlobals {
		id := globalID(global)
		evidence := ix.evidence(global.Pos())
		ix.funcs[id] = &function{node: Node{ID: id, Name: global.Name(), Kind: "global", Evidence: evidence}}
		if span, ok := ix.globalRanges[global.Pos()]; ok {
			ix.declarationRanges[id] = span
		}
		for _, root := range a.recordAddresses(global) {
			if a.globalAddressOwners[root] == nil {
				a.globalAddressOwners[root] = map[string]bool{}
			}
			a.globalAddressOwners[root][id] = true
		}
		if !global.Object().Exported() {
			continue
		}
		a.callableSummaryEscapes(global, id, "_global_escape", evidence, ix)
		ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "external_global", Reason: "exported variable and accessible contents can be read or replaced by callers outside the selected build; modeled local values do not close its runtime origin set", Evidence: evidence})
		if sqlHandleGlobal(global) || a.globalSQLFields[global] {
			ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "external_sql_global", Reason: "exported SQL handle or reachable public SQL-handle storage can be replaced outside the selected build; known local origins are retained alongside unknown input candidates", Evidence: evidence})
		}
		for _, callback := range sortedFunctions(a.globalCallbacks[global]) {
			owner := ix.owner(callback)
			ix.edges = append(ix.edges, Relationship{From: id, To: owner, Kind: "callback_global_escape", Certainty: "possible", Evidence: evidence})
			ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "external_callback", Reason: "callback candidate is reachable through an exported variable; outside retention, invocation, inputs and timing remain possible", Evidence: evidence})
			for _, parameter := range callback.Params {
				if isSQLHandleType(parameter.Type()) {
					ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "escaped_sql_input", Reason: "SQL-handle callback inputs remain open through an exported variable", Evidence: evidence})
					break
				}
			}
		}
	}
}

func (a *flowAnalysis) globalUse(instruction ssa.Instruction, ix *index) {
	link := func(address ssa.Value, kind string) { a.globalAccess(instruction, address, kind, ix) }
	switch use := instruction.(type) {
	case *ssa.Store:
		link(use.Addr, "global_write")
	case *ssa.UnOp:
		if use.Op == token.MUL {
			link(use.X, "global_read")
		}
	case *ssa.MapUpdate:
		link(use.Map, "global_write")
	case *ssa.Lookup:
		link(use.X, "global_read")
	case *ssa.Range:
		link(use.X, "global_read")
	case *ssa.Index:
		link(use.X, "global_read")
	case ssa.CallInstruction:
		common := use.Common()
		builtin, ok := common.Value.(*ssa.Builtin)
		if !ok || len(common.Args) == 0 {
			return
		}
		switch builtin.Name() {
		case "delete", "clear":
			link(common.Args[0], "global_write")
		case "copy":
			if len(common.Args) == 2 {
				link(common.Args[0], "global_write")
				link(common.Args[1], "global_read")
			}
		case "append":
			link(common.Args[0], "global_write")
			for _, argument := range common.Args {
				link(argument, "global_read")
			}
		case "len", "cap":
			link(common.Args[0], "global_read")
		}
	}
}

func (a *flowAnalysis) globalAccess(instruction ssa.Instruction, address ssa.Value, kind string, ix *index) {
	if address == nil {
		return
	}
	matched := map[string]bool{}
	for candidate := range a.get(address).addresses {
		// Field/element addresses extend a storage root with dot-separated slots.
		// Look up the root and its prefixes instead of rescanning every global.
		for {
			for id := range a.globalAddressOwners[candidate] {
				matched[id] = true
			}
			separator := strings.LastIndex(candidate, ".")
			if separator < 0 {
				break
			}
			candidate = candidate[:separator]
		}
	}
	owner := ix.owner(instruction.Parent())
	if owner == "" {
		return
	}
	for _, id := range sortedKeys(matched) {
		evidence := ix.evidence(instruction.Pos())
		if instruction.Pos() == token.NoPos {
			evidence = ix.funcs[owner].node.Evidence
			evidence.Origin = "synthetic_declaration"
		}
		ix.edges = append(ix.edges, Relationship{From: owner, To: id, Kind: kind, Certainty: "possible", Evidence: evidence})
	}
}
