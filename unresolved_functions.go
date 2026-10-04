package contracttrace

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func isFunctionType(typ types.Type) bool { _, ok := typ.Underlying().(*types.Signature); return ok }

func (a *flowAnalysis) seedFunctionInputs(functions []*ssa.Function) {
	for _, function := range functions {
		if function.Object() == nil || !function.Object().Exported() {
			continue
		}
		for _, parameter := range function.Params {
			if isFunctionType(parameter.Type()) {
				unknown := emptyFlow()
				unknown.functionUnknown = true
				a.put(parameter, unknown)
			}
		}
	}
}

func (a *flowAnalysis) outsideFunctionResult(call *ssa.Call, ix *index) bool {
	return a.special[call] == nil && iteratorFactory(call.Common()) == "" && (a.get(call.Common().Value).functionUnknown || a.sqlExternalResult(call.Common(), ix))
}

func (a *flowAnalysis) seedUnresolvedFunctionResults(functions []*ssa.Function) bool {
	changed := false
	for _, function := range functions {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				var value ssa.Value
				var call *ssa.Call
				switch candidate := instruction.(type) {
				case *ssa.Call:
					value, call = candidate, candidate
				case *ssa.Extract:
					value = candidate
					call, _ = candidate.Tuple.(*ssa.Call)
				}
				if value == nil || call == nil || !isFunctionType(value.Type()) || a.special[call] != nil || len(a.targets(call.Common())) != 0 {
					continue
				}
				modeled := false
				for effect := range a.get(call).effects {
					if _, ok := a.iterators[effect]; ok {
						modeled = true
					}
				}
				if !modeled {
					unknown := emptyFlow()
					unknown.functionUnknown = true
					if a.put(value, unknown) {
						changed = true
					}
				}
			}
		}
	}
	return changed
}

func (a *flowAnalysis) unresolvedFunctionUse(call ssa.CallInstruction, ix *index) {
	common := call.Common()
	if common.IsInvoke() || common.StaticCallee() != nil {
		return
	}
	if _, builtin := common.Value.(*ssa.Builtin); builtin {
		return
	}
	value := a.get(common.Value)
	known := len(a.targets(common)) != 0
	for effect := range value.effects {
		if strings.HasPrefix(effect, "afterfunc_dispatch:") {
			if _, modeled := a.afterFuncs[strings.TrimPrefix(effect, "afterfunc_dispatch:")]; modeled {
				known = true
			}
		}
		if strings.HasPrefix(effect, "afterfunc_stop:") {
			if _, modeled := a.afterFuncs[strings.TrimPrefix(effect, "afterfunc_stop:")]; modeled {
				known = true
			}
		}
		if _, modeled := a.iterators[effect]; modeled {
			known = true
		}
		if strings.HasPrefix(effect, "cancel:") {
			if _, modeled := a.contextKeys[strings.TrimPrefix(effect, "cancel:")]; modeled {
				known = true
			}
		}
	}
	if len(a.waitGroupUses(common)) != 0 {
		known = true
	}
	if known && !value.functionUnknown && !value.functionNil {
		return
	}
	kind := "unresolved_function_call"
	reason := "function call has no identified target in the bounded local flow model; nil, outside input or unsupported function flow cannot establish an absent caller/callee path or prove execution"
	if known {
		kind = "partial_function_call"
		reason = "known function targets or callable summaries are retained alongside an unresolved alternative; additional runtime behavior remains unidentified"
		if value.functionNil && value.functionUnknown {
			reason = "known function targets are retained alongside possible nil and unresolved function-value alternatives; nil invocation would panic, and no successful invocation is inferred"
		} else if value.functionNil {
			reason = "known function targets are retained alongside a typed nil function alternative; invoking the nil candidate would panic, and no successful invocation is inferred"
		}
	} else if value.functionNil && !value.functionUnknown {
		reason = "function call has only a typed nil function candidate; invoking it would panic, and no successful caller/callee path is inferred"
	}
	switch call.(type) {
	case *ssa.Defer:
		kind = "deferred_" + kind
	case *ssa.Go:
		kind = "goroutine_" + kind
	}
	ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(call.Parent()), Kind: kind, Reason: reason, Evidence: ix.callEvidence(call)})
}
