package contracttrace

import (
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Each numeric builtin result is an operation source, independent of numeric
// truth. Reuse call-slot identity, including synthetic block/instruction sites.
func (a *flowAnalysis) seedScalarBuiltinResults(funcs []*ssa.Function, ix *index) {
	for _, fn := range funcs {
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*ssa.Call)
				if !ok || !isScalarType(call.Type()) {
					continue
				}
				builtin, ok := call.Common().Value.(*ssa.Builtin)
				if !ok {
					continue
				}
				id := "scalar-builtin-result:" + strings.TrimPrefix(errorResultID(call, 0, ix), "error-result:")
				e := ix.callEvidence(call)
				ix.funcs[id] = &function{node: Node{ID: id, Name: builtin.Name() + " scalar result", Kind: "scalar_builtin_result", Evidence: e}}
				ix.edges = append(ix.edges, Relationship{From: ix.owner(fn), To: id, Kind: "scalar_builtin_result", Certainty: "fact", Evidence: e, Slot: "result:0"})
				a.put(call, flowValue{scalars: map[string]bool{id: true}})
			}
		}
	}
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "scalar_builtin_model", Reason: "Numeric builtin calls retain source-backed result-operation origins. min, max, complex, real and imag also retain numeric argument provenance. len, cap and copy identify investigation operations without inferring container shape, length, capacity, copied count, successful execution or numeric correctness; storage and operation relationships must be inspected separately. Compile-time constants, implicit control and runtime timing are not established by these result origins."})
}

func (a *flowAnalysis) scalarBuiltinFlow(call *ssa.Call, result *flowValue) {
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || !isScalarType(call.Type()) {
		return
	}
	switch builtin.Name() {
	case "min", "max", "complex", "real", "imag":
		for _, argument := range call.Common().Args {
			a.mergeScalar(result, a.get(argument))
		}
	}
}
