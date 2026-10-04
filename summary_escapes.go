package contracttrace

import (
	"strings"

	"golang.org/x/tools/go/ssa"
)

func (a *flowAnalysis) callableSummaryEscapes(value ssa.Value, from, suffix string, evidence Evidence, ix *index) {
	if len(a.contextKeys) == 0 && len(a.afterFuncs) == 0 {
		return
	}
	if cached := a.callbackCache[value]; cached != nil && cached.valid(a, a.get(value)) {
		// A reusable callback walk saw no effects anywhere in the accessible
		// storage. Effect-bearing walks are volatile and never enter this cache.
		return
	}
	effects := map[string]bool{}
	a.accessibleStorageTracked(value, nil, nil, nil, nil, nil, effects)
	for _, effect := range sortedKeys(effects) {
		key, kind := "", ""
		switch {
		case strings.HasPrefix(effect, "cancel:"):
			key = strings.TrimPrefix(effect, "cancel:")
			if _, modeled := a.contextKeys[key]; modeled {
				kind = "context_cancel"
			}
		case strings.HasPrefix(effect, "afterfunc_stop:"):
			key = strings.TrimPrefix(effect, "afterfunc_stop:")
			if _, modeled := a.afterFuncs[key]; modeled {
				kind = "cancellation_stop"
			}
		case strings.HasPrefix(effect, "afterfunc_dispatch:"):
			key = strings.TrimPrefix(effect, "afterfunc_dispatch:")
			if _, modeled := a.afterFuncs[key]; modeled {
				kind = "cancellation_delivery"
			}
		}
		if kind == "" {
			continue
		}
		ix.edges = append(ix.edges, Relationship{From: from, To: key, Kind: kind + suffix, Certainty: "possible", Evidence: evidence})
		ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "callable_summary_escape", Reason: "a modeled cancellation, registration-stop or internal delivery summary crosses a dependency/outside call, exported result or public global; retention, replacement, invocation, success and timing remain unproved", Evidence: evidence, Examples: []string{kind + ":" + key}})
	}
}

func (a *flowAnalysis) callableSummaryCallEscapes(call ssa.CallInstruction, ix *index) {
	if !a.callbackOutside(call.Common(), ix) {
		return
	}
	start := len(ix.edges)
	for _, arg := range call.Common().Args {
		a.callableSummaryEscapes(arg, ix.owner(call.Parent()), "_escape", ix.callEvidence(call), ix)
	}
	for i := start; i < len(ix.edges); i++ {
		ix.edges[i].Kind = afterFuncKind(call, ix.edges[i].Kind)
	}
}

func (a *flowAnalysis) callableSummaryReturnEscapes(returned *ssa.Return, ix *index) {
	fn := returned.Parent()
	if fn.Object() == nil || !fn.Object().Exported() {
		return
	}
	for _, result := range returned.Results {
		a.callableSummaryEscapes(result, ix.owner(fn), "_return_escape", ix.evidence(returned.Pos()), ix)
	}
}
