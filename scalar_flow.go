package contracttrace

import (
	"fmt"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

func isScalarType(t types.Type) bool {
	if t == nil {
		return false
	}
	basic, ok := t.Underlying().(*types.Basic)
	return ok && basic.Info()&(types.IsNumeric|types.IsBoolean) != 0
}

// Only transfer this domain: a comparison must not inherit callable targets,
// string literals, error identities or pointer aliases from its operands.
func (a *flowAnalysis) mergeScalar(dst *flowValue, src flowValue) {
	a.merge(dst, flowValue{scalars: src.scalars, scalarUnknown: src.scalarUnknown})
}

func (a *flowAnalysis) seedScalarParameters(funcs []*ssa.Function, ix *index) {
	for _, fn := range funcs {
		for slot, parameter := range fn.Params {
			if !isScalarType(parameter.Type()) {
				continue
			}
			id := fmt.Sprintf("scalar-parameter:%s:%d", fn.String(), slot)
			e := ix.evidence(parameter.Pos())
			if e.Line < 1 {
				e = ix.funcs[ix.owner(fn)].node.Evidence
				e.Origin = "synthetic_declaration"
			}
			ix.funcs[id] = &function{node: Node{ID: id, Name: "scalar parameter " + parameter.Name() + " of " + fn.String(), Kind: "scalar_parameter", Evidence: e}}
			ix.edges = append(ix.edges, Relationship{From: ix.owner(fn), To: id, Kind: "scalar_parameter", Certainty: "fact", Evidence: e, Slot: fmt.Sprintf("parameter:%d", slot)})
			a.put(parameter, flowValue{scalars: map[string]bool{id: true}})
		}
	}
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "scalar_flow_model", Reason: "Numeric and boolean formal parameter origins propagate through arithmetic, comparisons, local calls, returns and modeled memory/channels as bounded context-insensitive candidates. Formal parameters do not prove outside invocation. Bounded branch influence is disclosed separately by the scalar control model. Numeric values, overflow, decision correctness, exhaustive implicit dependencies, invocation correlation, opaque operation results and runtime ownership or ordering are not proved."})
}

func (a *flowAnalysis) outsideScalar(address, root string, e Evidence, ix *index) flowValue {
	id := "scalar-input:" + address
	if ix.funcs[id] == nil {
		ix.funcs[id] = &function{node: Node{ID: id, Name: "outside scalar in " + address, Kind: "scalar_input", Evidence: e}}
		ix.edges = append(ix.edges, Relationship{From: root, To: id, Kind: "external_input", Certainty: "possible", Evidence: e})
		ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "outside_scalar_input", Reason: "Outside numeric or boolean storage may coexist with local values. Shared storage summaries do not prove runtime identity, mutation timing or numeric bounds.", Evidence: e})
	}
	return flowValue{scalars: map[string]bool{id: true}}
}

func (a *flowAnalysis) scalarUse(ins ssa.Instruction, ix *index) {
	owner := ix.owner(ins.Parent())
	e := ix.evidence(ins.Pos())
	if e.Line < 1 {
		e = ix.funcs[owner].node.Evidence
		e.Origin = "synthetic_declaration"
	}
	linkFlow := func(v flowValue, kind, slot string) {
		for _, origin := range sortedKeys(v.scalars) {
			ix.edges = append(ix.edges, Relationship{From: owner, To: origin, Kind: kind, Certainty: "possible", Evidence: e, Slot: slot})
		}
		if v.scalarUnknown {
			ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "unresolved_scalar_origins", Reason: "Additional scalar origins were omitted by bounded analysis at " + kind + " " + slot, Evidence: e})
		}
	}
	link := func(value ssa.Value, kind, slot string) { linkFlow(a.get(value), kind, slot) }
	control := a.scalarControlValue(ins.Block())
	switch v := ins.(type) {
	case *ssa.Return:
		linkFlow(control, "scalar_control_return", "execution")
		for slot, result := range v.Results {
			link(result, "scalar_return", fmt.Sprintf("result:%d", slot))
		}
	case *ssa.If:
		link(v.Cond, "scalar_condition", "condition")
	case *ssa.BinOp:
		link(v.X, "scalar_operand", "left")
		link(v.Y, "scalar_operand", "right")
	case *ssa.Store:
		linkFlow(control, "scalar_control_store", "execution")
		link(v.Val, "scalar_store", "value")
	case *ssa.Send:
		linkFlow(control, "scalar_control_send", "execution")
		link(v.X, "scalar_send", "value")
	}
	if call, ok := ins.(ssa.CallInstruction); ok {
		linkFlow(control, "scalar_control_call", "execution")
		for slot, argument := range flowArguments(call.Common()) {
			link(argument, "scalar_argument", flowArgumentSlot(call.Common(), slot))
		}
	}
}
