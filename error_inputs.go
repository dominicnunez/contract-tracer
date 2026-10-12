package contracttrace

import (
	"fmt"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

func isStringType(typ types.Type) bool {
	basic, ok := typ.Underlying().(*types.Basic)
	return ok && basic.Kind() == types.String
}

func (a *flowAnalysis) seedFormatInput(parameter *ssa.Parameter) bool {
	if !isStringType(parameter.Type()) {
		return false
	}
	value := emptyFlow()
	value.stringUnknown = true
	return a.put(parameter, value)
}

func (a *flowAnalysis) seedErrorInput(parameter *ssa.Parameter, ix *index, reason string) bool {
	if !isErrorType(parameter.Type()) {
		return false
	}
	fn := parameter.Parent()
	owner := ix.owner(fn)
	if owner == "" {
		return false
	}
	ordinal := 0
	for i, candidate := range fn.Params {
		if candidate == parameter {
			ordinal = i
			break
		}
	}
	id := fmt.Sprintf("error-input:%s:%d", fn.String(), ordinal)
	if ix.funcs[id] == nil {
		e := ix.evidence(parameter.Pos())
		if parameter.Pos() == token.NoPos {
			e = ix.funcs[owner].node.Evidence
			e.Origin = "synthetic_declaration"
		}
		ix.funcs[id] = &function{node: Node{ID: id, Name: fmt.Sprintf("unresolved error input %d of %s", ordinal, owner), Kind: "error_input", Evidence: e}}
		ix.edges = append(ix.edges, Relationship{From: owner, To: id, Kind: "external_input", Certainty: "possible", Evidence: e, Slot: fmt.Sprintf("parameter:%d", ordinal)})
		ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "outside_error_input", Reason: reason + " Known local error origins remain alongside this unresolved candidate; this is not proof of invocation or a non-nil error.", Evidence: e})
	}
	value := emptyFlow()
	value.errors = map[string]bool{id: true}
	return a.put(parameter, value)
}

func (a *flowAnalysis) seedErrorInputs(functions []*ssa.Function, ix *index, uncalled bool) bool {
	called := map[*ssa.Function]bool{}
	if uncalled {
		for _, fn := range functions {
			for _, block := range fn.Blocks {
				for _, ins := range block.Instrs {
					if call, ok := ins.(ssa.CallInstruction); ok {
						for target := range a.targets(call.Common()) {
							called[target] = true
						}
					}
				}
			}
		}
	}
	changed := false
	for _, fn := range functions {
		public := fn.Object() != nil && fn.Object().Exported()
		if !public && (!uncalled || called[fn]) {
			continue
		}
		reason := "An exported function or method can receive error values from outside the analyzed callers."
		if !public {
			reason = "No modeled incoming call supplies this error parameter; missing or unmodeled invocation remains unresolved."
		}
		for _, parameter := range fn.Params {
			if a.seedFormatInput(parameter) {
				changed = true
			}
			if a.seedErrorInput(parameter, ix, reason) {
				changed = true
			}
		}
	}
	return changed
}
