package contracttrace

import (
	"go/types"

	"golang.org/x/tools/go/ssa"
)

type iteratorSummary struct {
	name   string
	source ssa.Value
	call   *ssa.Call
}
type iteratorYield struct {
	value flowValue
	typ   types.Type
}

func iteratorFactory(common *ssa.CallCommon) string {
	target := common.StaticCallee()
	if target == nil || target.Object() == nil || target.Object().Pkg() == nil || len(common.Args) != 1 {
		return ""
	}
	name := target.Object().Pkg().Path() + "." + target.Object().Name()
	switch name {
	case "slices.Values", "slices.All", "slices.Backward", "maps.Keys", "maps.Values", "maps.All":
		return name
	}
	return ""
}

func (a *flowAnalysis) modelIterator(call *ssa.Call, ix *index, result *flowValue) {
	name := iteratorFactory(call.Common())
	if name == "" {
		return
	}
	if a.iterators == nil {
		a.iterators = map[string]iteratorSummary{}
	}
	id := ix.allocationID("iterator", call.Parent(), call)
	a.iterators[id] = iteratorSummary{name, call.Common().Args[0], call}
	result.effects[id] = true
}

func (a *flowAnalysis) iteratorValues(summary iteratorSummary) []iteratorYield {
	switch source := summary.source.Type().Underlying().(type) {
	case *types.Slice:
		value := iteratorYield{a.sequenceElements(summary.source), source.Elem()}
		if summary.name == "slices.Values" {
			return []iteratorYield{value}
		}
		return []iteratorYield{{emptyFlow(), types.Typ[types.Int]}, value}
	case *types.Map:
		key := iteratorYield{a.mapIterationValue(summary.source, 1, source.Key()), source.Key()}
		value := iteratorYield{a.mapIterationValue(summary.source, 2, source.Elem()), source.Elem()}
		switch summary.name {
		case "maps.Keys":
			return []iteratorYield{key}
		case "maps.Values":
			return []iteratorYield{value}
		case "maps.All":
			return []iteratorYield{key, value}
		}
	}
	return nil
}

func (a *flowAnalysis) invokeIterator(common *ssa.CallCommon, ix *index) bool {
	if len(common.Args) != 1 {
		return false
	}
	changed := false
	for _, effect := range sortedKeys(a.get(common.Value).effects) {
		summary, ok := a.iterators[effect]
		if !ok {
			continue
		}
		for _, target := range sortedFunctions(a.get(common.Args[0]).functions) {
			if ix.owner(target) == "" {
				continue
			}
			for slot, candidate := range a.iteratorValues(summary) {
				if slot < len(target.Params) && a.put(target.Params[slot], candidate.value) {
					changed = true
				}
			}
		}
	}
	return changed
}

func (a *flowAnalysis) iteratorRelationships(ix *index) {
	for _, effect := range sortedKeys(a.iterators) {
		summary := a.iterators[effect]
		evidence := ix.callEvidence(summary.call)
		ix.funcs[effect] = &function{node: Node{ID: effect, Name: summary.name, Kind: "iterator", Evidence: evidence}}
		ix.edges = append(ix.edges, Relationship{From: ix.owner(summary.call.Parent()), To: effect, Kind: "iterator_create", Certainty: "fact", Evidence: evidence})
		ix.boundaries = append(ix.boundaries, Boundary{Node: effect, Kind: "iterator_model", Reason: "standard slices/maps iterator retains yielded candidate slots through modeled local consumers; index numbers, key/value pairing, order, early termination, panic and cleanup timing are not proved", Evidence: evidence})
	}
}

func (a *flowAnalysis) iteratorUse(call ssa.CallInstruction, ix *index) {
	for _, effect := range sortedKeys(a.get(call.Common().Value).effects) {
		_, ok := a.iterators[effect]
		if !ok {
			continue
		}
		ix.edges = append(ix.edges, Relationship{From: ix.owner(call.Parent()), To: effect, Kind: "iterator_invoke", Certainty: "possible", Evidence: ix.callEvidence(call)})
		found := false
		if len(call.Common().Args) == 1 {
			for _, target := range sortedFunctions(a.get(call.Common().Args[0]).functions) {
				if owner := ix.owner(target); owner != "" {
					found = true
					ix.edges = append(ix.edges, Relationship{From: effect, To: owner, Kind: "iterator_yield", Certainty: "possible", Evidence: ix.callEvidence(call)})
				}
			}
		}
		if !found {
			ix.boundaries = append(ix.boundaries, Boundary{Node: effect, Kind: "unresolved_iterator_yield", Reason: "iterator invocation has no modeled local yield consumer; outside consumers or unsupported function flow remain open", Evidence: ix.callEvidence(call)})
		}
	}
}
