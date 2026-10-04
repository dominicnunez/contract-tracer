package contracttrace

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Attach each captured receiver to its closure value, not the shared wrapper's
// FreeVar summary: different receivers can use the same generated wrapper.
func (a *flowAnalysis) modelWaitGroupBinding(closure *ssa.MakeClosure, result *flowValue) {
	target := closure.Fn.(*ssa.Function)
	if !strings.HasPrefix(target.Synthetic, "bound method wrapper") {
		return
	}
	for _, block := range target.Blocks {
		for _, ins := range block.Instrs {
			call, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			name, receiver := waitGroupOperation(call.Common())
			interfaceReceiver := false
			if name == "" && call.Common().IsInvoke() && waitGroupMethodName(call.Common().Method.Name()) {
				name, receiver = call.Common().Method.Name(), call.Common().Value
				interfaceReceiver = true
			}
			if name == "" {
				continue
			}
			for index, variable := range target.FreeVars {
				if receiver != variable || index >= len(closure.Bindings) {
					continue
				}
				value := emptyFlow()
				aliases := a.get(closure.Bindings[index]).addresses
				if interfaceReceiver {
					known, tagged := a.waitGroupInterfaceAliases(closure.Bindings[index])
					if !known {
						continue
					}
					aliases = tagged
				}
				value.effects["waitgroup_bound:"+name] = true
				for _, alias := range sortedKeys(aliases) {
					value.effects["waitgroup_bound:"+name+":"+alias] = true
				}
				a.merge(result, value)
			}
		}
	}
}

type waitGroupUse struct {
	name      string
	resources []string
	arguments []ssa.Value
	unknown   bool
	outside   bool
}

func (a *flowAnalysis) waitGroupUses(common *ssa.CallCommon) []waitGroupUse {
	aliases := map[string]map[string]bool{}
	partialOperations := map[string]bool{}
	outsideOperations := map[string]bool{}
	arguments := common.Args
	if name, receiver := waitGroupOperation(common); name != "" {
		aliases[name] = a.get(receiver).addresses
		arguments = common.Args[1:]
	} else if expressions := a.waitGroupMethodExpressions(common); len(expressions) > 0 {
		for name, projected := range expressions {
			aliases[name] = projected.addresses
			partialOperations[name] = projected.interfaceUnknown || projected.boundReceiverUnknown || projected.functionUnknown
			outsideOperations[name] = a.waitGroupInputCandidate(projected.addresses)
		}
		arguments = common.Args[1:]
	} else if common.IsInvoke() && waitGroupMethodName(common.Method.Name()) {
		if known, tagged := a.waitGroupInterfaceAliases(common.Value); known {
			aliases[common.Method.Name()] = tagged
		}
	} else {
		for _, effect := range sortedKeys(a.get(common.Value).effects) {
			if !strings.HasPrefix(effect, "waitgroup_bound:") {
				continue
			}
			parts := strings.SplitN(strings.TrimPrefix(effect, "waitgroup_bound:"), ":", 2)
			name := parts[0]
			if aliases[name] == nil {
				aliases[name] = map[string]bool{}
			}
			if len(parts) == 2 {
				aliases[name][parts[1]] = true
			}
		}
	}
	uses := []waitGroupUse{}
	for _, name := range sortedKeys(aliases) {
		outsideOperations[name] = outsideOperations[name] || a.waitGroupInputCandidate(aliases[name])
		partialOperations[name] = partialOperations[name] || outsideOperations[name]
		resources := []string{}
		for _, alias := range sortedKeys(aliases[name]) {
			resources = append(resources, resourceID("waitgroup", "", alias))
		}
		uses = append(uses, waitGroupUse{name, resources, arguments, partialOperations[name], outsideOperations[name]})
	}
	return uses
}

func (a *flowAnalysis) waitGroupInputCandidate(candidates map[string]bool) bool {
	for candidate := range candidates {
		if strings.HasPrefix(candidate, "input:") || a.inputRoots[candidate] {
			return true
		}
	}
	return false
}

// Method expressions for promoted methods are generated SSA thunks. Their
// first call argument is the outer receiver, while the WaitGroup receiver is
// the embedded field selected by the Go method set.
func (a *flowAnalysis) waitGroupMethodExpressions(common *ssa.CallCommon) map[string]flowValue {
	projectedByMethod := map[string]flowValue{}
	if len(common.Args) == 0 {
		return projectedByMethod
	}
	for _, target := range sortedFunctions(a.targets(common)) {
		if target.Signature == nil || target.Signature.Recv() != nil {
			continue
		}
		apiMethod, ok := target.Object().(*types.Func)
		if !ok || apiMethod.Pkg() == nil || apiMethod.Pkg().Path() != "sync" || !waitGroupMethodName(apiMethod.Name()) {
			continue
		}
		apiMethod = apiMethod.Origin()
		if apiMethod.Pkg() == nil || apiMethod.Pkg().Path() != "sync" {
			continue
		}
		receiver := apiMethod.Type().(*types.Signature).Recv().Type()
		if pointer, ok := receiver.(*types.Pointer); ok {
			receiver = pointer.Elem()
		}
		named, ok := receiver.(*types.Named)
		if !ok || named.Obj().Name() != "WaitGroup" || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "sync" {
			continue
		}
		name := apiMethod.Name()
		methodSet := types.NewMethodSet(common.Args[0].Type())
		for i := 0; i < methodSet.Len(); i++ {
			method, ok := methodSet.At(i).Obj().(*types.Func)
			if !ok || method.Name() != name {
				continue
			}
			selected, ok := methodSet.At(i).Obj().(*types.Func)
			if !ok || selected.Origin() != apiMethod {
				continue
			}
			projected, _, status := a.projectMethodExpressionReceiver(a.get(common.Args[0]), common.Args[0].Type(), apiMethod)
			if status == methodReceiverProjectionUnsupported {
				projected.interfaceUnknown = true
			}
			for candidate := range projected.addresses {
				if strings.HasPrefix(candidate, "input:") || a.inputRoots[candidate] {
					projected.interfaceUnknown = true
				}
			}
			combined := projectedByMethod[name]
			a.merge(&combined, projected)
			mergeProjectedUncertainty(&combined, a.get(common.Value))
			projectedByMethod[name] = combined
			break
		}
	}
	return projectedByMethod
}
