package contracttrace

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func waitGroupOperation(common *ssa.CallCommon) (string, ssa.Value) {
	target := common.StaticCallee()
	if target == nil || target.Object() == nil || target.Object().Pkg() == nil || target.Object().Pkg().Path() != "sync" || target.Signature.Recv() == nil || len(common.Args) == 0 {
		return "", nil
	}
	receiver := target.Signature.Recv().Type()
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = pointer.Elem()
	}
	named, ok := receiver.(*types.Named)
	if !ok || named.Obj().Name() != "WaitGroup" || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "sync" {
		return "", nil
	}
	switch target.Object().Name() {
	case "Add", "Done", "Wait", "Go":
		return target.Object().Name(), common.Args[0]
	}
	return "", nil
}

func (a *flowAnalysis) waitGroupResources(call ssa.CallInstruction, ix *index) []waitGroupUse {
	uses := a.waitGroupUses(call.Common())
	for _, use := range uses {
		for _, id := range use.resources {
			if ix.funcs[id] == nil {
				ix.funcs[id] = &function{node: Node{ID: id, Name: "WaitGroup receiver", Kind: "waitgroup", Evidence: ix.callEvidence(call)}}
			}
		}
	}
	return uses
}

func (a *flowAnalysis) waitGroupUse(call ssa.CallInstruction, ix *index) {
	for _, use := range a.waitGroupResources(call, ix) {
		name, resources := use.name, use.resources
		from := ix.owner(call.Parent())
		evidence := ix.callEvidence(call)
		if len(resources) == 0 || use.unknown {
			reason := "WaitGroup receiver has no modeled allocation/global/field identity; input or alias may be external"
			if len(resources) > 0 && use.outside {
				reason = "WaitGroup receiver candidates include source-backed outside input values alongside local identities"
			} else if len(resources) > 0 {
				reason = "WaitGroup receiver projection retained known embedded candidates but other receiver branches are unresolved"
			}
			ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "unresolved_waitgroup", Reason: reason, Evidence: evidence})
		}
		for _, id := range resources {
			edge := Relationship{From: from, To: id, Kind: "waitgroup_" + strings.ToLower(name), Certainty: "possible", Evidence: evidence}
			if name == "Add" && len(use.arguments) > 0 {
				if constant, ok := use.arguments[0].(*ssa.Const); ok && constant.Value != nil {
					edge.Slot = "delta"
					edge.Values = []string{"integer:" + constant.Value.ExactString()}
				}
			}
			ix.edges = append(ix.edges, edge)
		}
		if name == "Go" && len(use.arguments) > 0 {
			found := false
			taskValue := a.get(use.arguments[0])
			nilTask := taskValue.functionNil
			uncertain := taskValue.functionUnknown || nilTask
			for _, target := range sortedFunctions(a.get(use.arguments[0]).functions) {
				to := ix.owner(target)
				if to == "" {
					ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "external_waitgroup_task", Reason: "WaitGroup.Go task body is outside the indexed repository", Evidence: evidence, Examples: []string{target.String()}})
					continue
				}
				found = true
				for _, id := range resources {
					ix.edges = append(ix.edges, Relationship{From: id, To: to, Kind: "waitgroup_task", Certainty: "possible", Evidence: evidence})
				}
			}
			if !found || uncertain {
				reason := "WaitGroup.Go task has no modeled local function candidate; runtime callback selection or external bodies remain unresolved"
				if found {
					reason = "WaitGroup.Go has known local task candidates alongside an outside callback input"
					if nilTask {
						if taskValue.functionUnknown {
							reason = "WaitGroup.Go has known local task candidates alongside a possible nil and unresolved callback-value alternative"
						} else {
							reason = "WaitGroup.Go has known local task candidates alongside a nil callback alternative"
						}
					}
				} else if nilTask {
					if taskValue.functionUnknown {
						reason = "WaitGroup.Go has no known local task candidate and retains a possible nil and unresolved callback-value alternative"
					} else {
						reason = "WaitGroup.Go task is a typed nil function value; invoking it would panic, and no successful invocation is inferred"
					}
				}
				ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "unresolved_waitgroup_task", Reason: reason, Evidence: evidence})
			}
		}
	}
}
