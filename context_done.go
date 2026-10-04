package contracttrace

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func contextAdapterName(method *types.Func) string {
	if method == nil {
		return ""
	}
	name := method.Name()
	for _, suffix := range []string{"$bound", "$thunk"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}

// contextMethod identifies the standard context interface method by its
// declared object. A same-named method on a concrete context is not enough.
func contextMethod(method *types.Func, name string) bool {
	if method == nil || method.Pkg() == nil || method.Pkg().Path() != "context" || method.Name() != name {
		return false
	}
	object, ok := method.Origin().Type().(*types.Signature)
	if !ok || object.Recv() == nil {
		return false
	}
	named, ok := method.Pkg().Scope().Lookup("Context").Type().Underlying().(*types.Interface)
	if !ok {
		return false
	}
	named.Complete()
	for i := 0; i < named.NumMethods(); i++ {
		candidate := named.Method(i)
		if candidate.Name() == name && candidate.Origin() == method.Origin() {
			return true
		}
	}
	return false
}

func contextSelectedMethod(receiverType types.Type, name string) *types.Func {
	if receiverType == nil || (name != "Done" && name != "Err") {
		return nil
	}
	selection := types.NewMethodSet(receiverType).Lookup(nil, name)
	if selection == nil {
		return nil
	}
	method, ok := selection.Obj().(*types.Func)
	if !ok || !contextMethod(method, name) {
		return nil
	}
	return method
}

// contextCallReceiver returns the actual receiver flow for a standard
// context method call, including generated method expressions and bound
// method values. The method projection helper follows only the SSA adapter's
// declared selector path and never falls back to its outer wrapper value.
func (a *flowAnalysis) contextCallReceiver(common *ssa.CallCommon, name string) (flowValue, bool, bool) {
	if common == nil {
		return emptyFlow(), false, false
	}
	if common.IsInvoke() {
		method := common.Method
		if !contextMethod(method, name) {
			method = contextSelectedMethod(common.Value.Type(), name)
		}
		if method == nil || !contextMethod(method, name) {
			return emptyFlow(), false, false
		}
		receiver := a.get(common.Value)
		projected, _, status := a.projectMethodExpressionReceiver(receiver, common.Value.Type(), method)
		if status == methodReceiverProjectionUnsupported {
			projected = unresolvedProjectedReceiver()
		}
		return projected, true, status == methodReceiverProjectionUnsupported
	}
	targets := a.targets(common)
	result := emptyFlow()
	matched, uncertain := false, false
	calleeFlow := a.get(common.Value)
	uncertain = calleeFlow.functionUnknown || calleeFlow.functionNil || calleeFlow.boundReceiverUnknown
	for _, target := range sortedFunctions(targets) {
		functionObject, ok := target.Object().(*types.Func)
		if !ok || contextAdapterName(functionObject) != name {
			uncertain = true
			continue
		}
		declaredMethod := functionObject
		declared, ok := declaredMethod.Type().(*types.Signature)
		if !ok || declared.Recv() == nil || target.Signature == nil {
			uncertain = true
			continue
		}
		callable := target.Signature
		if len(common.Args) > callable.Params().Len() {
			uncertain = true
			continue
		}
		// A method expression carries the receiver as its first explicit
		// argument. A bound method wrapper instead recovers its captured receiver.
		if callable.Params().Len() > declared.Params().Len() {
			if len(common.Args) == 0 {
				uncertain = true
				continue
			}
			receiver := a.get(common.Args[0])
			method := contextSelectedMethod(common.Args[0].Type(), name)
			if method == nil {
				continue
			}
			matched = true
			projected, _, status := a.projectMethodExpressionReceiver(receiver, common.Args[0].Type(), method)
			if status == methodReceiverProjectionUnsupported {
				projected = unresolvedProjectedReceiver()
				uncertain = true
			}
			a.merge(&result, projected)
			continue
		}
		bound := a.get(common.Value)
		receiver, exists := bound.boundReceivers[target]
		if !exists {
			continue
		}
		matched = true
		for address := range receiver.addresses {
			result.addresses[address] = true
		}
		uncertain = uncertain || receiver.unknown || bound.boundReceiverUnknown || bound.interfaceUnknown || bound.sqlUnknown || bound.functionUnknown || bound.functionNil
	}
	if !matched {
		return emptyFlow(), false, false
	}
	result.interfaceUnknown = result.interfaceUnknown || uncertain || len(result.addresses) == 0
	return result, true, result.interfaceUnknown
}

func (a *flowAnalysis) modelDone(call *ssa.Call, ix *index) {
	common := call.Common()
	receiver, matched, uncertain := a.contextCallReceiver(common, "Done")
	if !matched {
		return
	}
	if a.special == nil {
		a.special = map[*ssa.Call][]flowValue{}
	}
	result := emptyFlow()
	result.interfaceUnknown = uncertain || receiver.interfaceUnknown || receiver.boundReceiverUnknown || receiver.sqlUnknown
	visited := map[string]bool{}
	queue := []string{}
	for _, key := range sortedKeys(receiver.addresses) {
		if strings.HasPrefix(key, "context:") {
			queue = append(queue, key)
		} else {
			result.interfaceUnknown = true
		}
	}
	resolved := false
	for head := 0; head < len(queue); head++ {
		key := queue[head]
		if visited[key] {
			continue
		}
		visited[key] = true
		site, known := a.contextKeys[key]
		if !known {
			result.interfaceUnknown = true
			continue
		}
		resolved = true
		if site.needsCancel {
			channel := resourceID("channel", "context_done", key)
			value := emptyFlow()
			value.addresses[channel] = true
			a.merge(&result, value)
			if a.doneChannels == nil {
				a.doneChannels = map[string]string{}
			}
			a.doneChannels[channel] = key
			if ix.funcs[channel] == nil {
				ix.funcs[channel] = &function{node: Node{ID: channel, Name: "Done signal for " + site.name, Kind: "channel", Evidence: ix.evidence(site.position)}}
			}
		}
		hasNilDone := false
		hasWithValue := false
		for _, constructor := range site.names {
			hasNilDone = hasNilDone || constructor == "Background" || constructor == "TODO" || constructor == "WithoutCancel"
			hasWithValue = hasWithValue || constructor == "WithValue"
		}
		if hasNilDone {
			value := emptyFlow()
			value.effects["nil_done:"+key] = true
			a.merge(&result, value)
			if site.needsCancel || hasWithValue {
				result.interfaceUnknown = true
			}
		}
		if hasWithValue {
			parentFlow := a.get(site.parent)
			if parentFlow.interfaceUnknown || parentFlow.sqlUnknown || len(parentFlow.addresses) == 0 {
				result.interfaceUnknown = true
			}
			for _, parent := range sortedKeys(parentFlow.addresses) {
				if strings.HasPrefix(parent, "context:") {
					queue = append(queue, parent)
				} else {
					result.interfaceUnknown = true
				}
			}
			if site.needsCancel || hasNilDone {
				result.interfaceUnknown = true
			}
		}
	}
	if !resolved {
		result.interfaceUnknown = true
	}
	a.special[call] = []flowValue{result}
}

func (a *flowAnalysis) doneRelationships(ix *index) {
	for _, channel := range sortedKeys(a.doneChannels) {
		key := a.doneChannels[channel]
		site := a.contextKeys[key]
		ix.edge(key, channel, "context_done_signal", "possible", site.position)
	}
}
