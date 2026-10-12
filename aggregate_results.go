package contracttrace

import (
	"fmt"

	"golang.org/x/tools/go/ssa"
)

func (a *flowAnalysis) seedAggregateResult(value ssa.Value, call *ssa.Call, ix *index, unresolved bool) bool {
	if call == nil || a.special[call] != nil || iteratorFactory(call.Common()) != "" {
		return false
	}
	if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
		return false
	}
	if unresolved {
		if len(a.targets(call.Common())) != 0 {
			return false
		}
	} else if !a.outsideFunctionResult(call, ix) {
		return false
	}
	root := fmt.Sprintf("input:result:%s:%s", value.Parent().String(), value.Name())
	return a.seedAggregateOrigin(value, root, true, ix.evidence(call.Pos()), ix)
}

// Missing indirect targets are classified only after local flow converges, so
// a resolved private factory does not gain a premature outside origin.
func (a *flowAnalysis) seedUnresolvedAggregateResults(functions []*ssa.Function, ix *index) bool {
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
				if value != nil && a.seedAggregateResult(value, call, ix, true) {
					changed = true
				}
			}
		}
	}
	return changed
}
