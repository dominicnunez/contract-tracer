package contracttrace

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func isWaitGroupPointer(t types.Type) bool {
	pointer, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	return ok && named.Obj().Name() == "WaitGroup" && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "sync"
}

func (a *flowAnalysis) modelWaitGroupInterface(value ssa.Value, result *flowValue) {
	if !isWaitGroupPointer(value.Type()) {
		return
	}
	tagged := emptyFlow()
	tagged.effects["waitgroup_receiver"] = true
	for _, alias := range sortedKeys(a.get(value).addresses) {
		tagged.effects["waitgroup_receiver:"+alias] = true
	}
	a.merge(result, tagged)
}

func (a *flowAnalysis) waitGroupInterfaceAliases(value ssa.Value) (bool, map[string]bool) {
	known := false
	aliases := map[string]bool{}
	for effect := range a.get(value).effects {
		if effect == "waitgroup_receiver" {
			known = true
		}
		if strings.HasPrefix(effect, "waitgroup_receiver:") {
			known = true
			aliases[strings.TrimPrefix(effect, "waitgroup_receiver:")] = true
		}
	}
	return known, aliases
}

func waitGroupMethodName(name string) bool {
	return name == "Add" || name == "Done" || name == "Wait" || name == "Go"
}
