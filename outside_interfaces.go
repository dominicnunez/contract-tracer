package contracttrace

import (
	"fmt"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

func isInterfaceType(typ types.Type) bool {
	_, ok := typ.Underlying().(*types.Interface)
	return ok
}

// Materialize only the asserted shape. This does not enumerate all outside
// dynamic types or prove that an assertion succeeds.
func (a *flowAnalysis) seedInterfaceAssertion(value ssa.Value, assertion *ssa.TypeAssert, ix *index) bool {
	if a.interfaceAssertions == nil {
		a.interfaceAssertions = map[ssa.Value]bool{}
	}
	if !a.interfaceAssertions[value] {
		a.interfaceAssertions[value] = true
		ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(value.Parent()), Kind: "outside_interface_assertion", Reason: "outside interface value retains possible asserted function/SQL/aggregate candidates; dynamic type compatibility, assertion success, nil safety and complete outside type enumeration are not proved", Evidence: ix.evidence(assertion.Pos())})
	}
	unknown := emptyFlow()
	unknown.interfaceUnknown = isInterfaceType(value.Type())
	unknown.functionUnknown = isFunctionType(value.Type())
	unknown.sqlUnknown = isSQLHandleType(value.Type())
	changed := a.put(value, unknown)
	root := fmt.Sprintf("input:assertion:%s:%s", value.Parent().String(), value.Name())
	if a.seedAggregateOrigin(value, root, true, ix.evidence(assertion.Pos()), ix) {
		changed = true
	}
	return changed
}
