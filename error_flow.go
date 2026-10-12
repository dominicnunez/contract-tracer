package contracttrace

import (
	"fmt"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

func isErrorType(t types.Type) bool {
	return t != nil && types.Implements(t, types.Universe.Lookup("error").Type().Underlying().(*types.Interface))
}

func errorResultID(call ssa.CallInstruction, slot int, ix *index) string {
	e := ix.callEvidence(call)
	if call.Pos() == token.NoPos {
		for index, instruction := range call.Block().Instrs {
			if instruction == call {
				return fmt.Sprintf("error-result:%s:%d:%d:%s:block%d:instruction%d:%d", e.File, e.Line, e.Column, call.Parent().String(), call.Block().Index, index, slot)
			}
		}
	}
	return fmt.Sprintf("error-result:%s:%d:%d:%s:%d", e.File, e.Line, e.Column, call.Parent().String(), slot)
}

func (a *flowAnalysis) seedErrorResults(funcs []*ssa.Function, ix *index) {
	for _, fn := range funcs {
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				results := call.Common().Signature().Results()
				for slot := 0; slot < results.Len(); slot++ {
					if !isErrorType(results.At(slot).Type()) {
						continue
					}
					id := errorResultID(call, slot, ix)
					e := ix.callEvidence(call)
					ix.funcs[id] = &function{node: Node{ID: id, Name: fmt.Sprintf("error result %d at %s:%d:%d", slot, e.File, e.Line, e.Column), Kind: "error_result", Evidence: e}}
					ix.edges = append(ix.edges, Relationship{From: ix.owner(fn), To: id, Kind: "error_result", Certainty: "fact", Evidence: e, Slot: fmt.Sprintf("result:%d", slot)})
					value := emptyFlow()
					origin := id
					if target := call.Common().StaticCallee(); target != nil && len(target.Blocks) != 0 {
						if owner := ix.owner(target); owner != "" {
							origin = fmt.Sprintf("error-return:%s:%d", owner, slot)
							if ix.funcs[origin] == nil {
								declaration := ix.funcs[owner].node.Evidence
								ix.funcs[origin] = &function{node: Node{ID: origin, Name: fmt.Sprintf("error return slot %d of %s", slot, owner), Kind: "error_return_slot", Evidence: declaration}}
							}
							ix.edges = append(ix.edges, Relationship{From: id, To: origin, Kind: "error_result_contract", Certainty: "fact", Evidence: e, Slot: fmt.Sprintf("result:%d", slot)})
						}
					}
					value.errors = map[string]bool{origin: true}
					if normal, ok := call.(*ssa.Call); ok {
						if results.Len() == 1 {
							a.put(normal, value)
						} else if refs := normal.Referrers(); refs != nil {
							for _, ref := range *refs {
								if extract, ok := ref.(*ssa.Extract); ok && extract.Index == slot {
									a.put(extract, value)
								}
							}
						}
					}
				}
			}
		}
	}
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "error_flow_model", Reason: "Error-typed call results retain bounded candidate origins through local parameters, returns and modeled memory/channels. Modeled static local calls share a callee return-slot origin while retaining every source call-result site; dependency and dynamic calls retain call-site origins. A result slot does not prove a non-nil error. Comparisons and unused SSA results identify investigation sites, not correct handling; invocation correlation, wrapping in dependencies, outside error inputs and runtime failure/cleanup ordering remain unresolved."})
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "error_wrap_model", Reason: "Known errors.Join and fmt.Errorf targets retain candidate causes from modeled variadic slice elements. Closed literal argument arrays select fmt %w operands, including explicit indices and width/precision operands. Other storage shapes retain conservative candidates and pairing boundaries; successful formatting, nil filtering, outside/unmodeled slice contents, custom wrapping and runtime Unwrap/Is/As behavior remain unresolved. Candidate causes do not prove actual wrapping."})
}

func (a *flowAnalysis) errorUse(ins ssa.Instruction, ix *index) {
	owner := ix.owner(ins.Parent())
	link := func(value ssa.Value, kind string, pos token.Pos, slot string) {
		for _, origin := range sortedKeys(a.get(value).errors) {
			ix.edges = append(ix.edges, Relationship{From: owner, To: origin, Kind: kind, Certainty: "possible", Evidence: ix.evidence(pos), Slot: slot})
		}
	}
	switch v := ins.(type) {
	case *ssa.Return:
		for slot, result := range v.Results {
			link(result, "error_return", v.Pos(), fmt.Sprintf("result:%d", slot))
		}
	case *ssa.BinOp:
		if v.Op == token.EQL || v.Op == token.NEQ {
			link(v.X, "error_compare", v.Pos(), "left")
			link(v.Y, "error_compare", v.Pos(), "right")
		}
	case *ssa.Store:
		link(v.Val, "error_store", v.Pos(), "value")
	case *ssa.Send:
		link(v.X, "error_send", v.Pos(), "value")
	case *ssa.Panic:
		link(v.X, "error_panic", v.Pos(), "value")
	}
	call, ok := ins.(ssa.CallInstruction)
	if !ok {
		return
	}
	for slot, argument := range flowArguments(call.Common()) {
		link(argument, "error_argument", call.Pos(), flowArgumentSlot(call.Common(), slot))
	}
	results := call.Common().Signature().Results()
	for slot := 0; slot < results.Len(); slot++ {
		if !isErrorType(results.At(slot).Type()) {
			continue
		}
		kind := ""
		switch v := call.(type) {
		case *ssa.Defer:
			kind = "deferred_error_result_discard"
		case *ssa.Go:
			kind = "goroutine_error_result_discard"
		case *ssa.Call:
			used := false
			if refs := v.Referrers(); refs != nil {
				for _, ref := range *refs {
					if _, debug := ref.(*ssa.DebugRef); debug {
						continue
					}
					if results.Len() == 1 {
						used = true
					} else if extract, ok := ref.(*ssa.Extract); ok && extract.Index == slot {
						if consumers := extract.Referrers(); consumers != nil {
							for _, consumer := range *consumers {
								if _, debug := consumer.(*ssa.DebugRef); !debug {
									used = true
								}
							}
						}
					}
				}
			}
			if !used {
				kind = "error_result_unused"
			}
		}
		if kind != "" {
			ix.edges = append(ix.edges, Relationship{From: owner, To: errorResultID(call, slot, ix), Kind: kind, Certainty: "fact", Evidence: ix.callEvidence(call), Slot: fmt.Sprintf("result:%d", slot)})
		}
	}
}
