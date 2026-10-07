package contracttrace

import (
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func cancellationSchedulerSignature(fn *ssa.Function) bool {
	return fn.Object() != nil && fn.Object().Name() == "AfterFunc" && schedulerSignature(fn.Signature)
}

func schedulerSignature(signature *types.Signature) bool {
	if signature.Recv() == nil || signature.Params().Len() != 1 || signature.Results().Len() != 1 {
		return false
	}
	callback, ok := types.Unalias(signature.Params().At(0).Type()).(*types.Signature)
	if !ok || callback.Params().Len() != 0 || callback.Results().Len() != 0 || callback.Variadic() {
		return false
	}
	stop, ok := types.Unalias(signature.Results().At(0).Type()).(*types.Signature)
	return ok && stop.Params().Len() == 0 && stop.Results().Len() == 1 && !stop.Variadic() && types.Identical(stop.Results().At(0).Type(), types.Typ[types.Bool]) && !signature.Variadic()
}

func (a *flowAnalysis) modelAfterFuncInterface(value ssa.Value, result *flowValue) {
	if a.afterFuncTypeCache == nil {
		a.afterFuncTypeCache = map[types.Type]string{}
	}
	if effect, cached := a.afterFuncTypeCache[value.Type()]; cached {
		if effect != "" {
			result.effects[effect] = true
		}
		return
	}
	a.afterFuncTypeCache[value.Type()] = ""
	selection := types.NewMethodSet(value.Type()).Lookup(nil, "AfterFunc")
	if selection == nil || !schedulerSignature(selection.Obj().Type().(*types.Signature)) {
		return
	}
	for _, id := range sortedKeys(a.afterFuncSchedulers) {
		target := a.afterFuncSchedulers[id]
		if target.Object() != selection.Obj() {
			continue
		}
		result.effects["afterfunc_scheduler:"+id] = true
		a.afterFuncTypeCache[value.Type()] = "afterfunc_scheduler:" + id
		return
	}
	result.effects["afterfunc_scheduler_unmodeled"] = true
	a.afterFuncTypeCache[value.Type()] = "afterfunc_scheduler_unmodeled"
}

// The standard-library hook receives an internal delivery wrapper rather than
// the user's callback. Preserve it as a callable relay summary, not as the same
// function value. Receiver and stored relay candidates join the normal flow.
func (a *flowAnalysis) afterFuncSchedulerFlow() bool {
	changed := false
	for _, key := range sortedKeys(a.afterFuncs) {
		site := a.afterFuncs[key]
		if a.bindCancellationScheduler(site.context, "afterfunc_dispatch:"+key) {
			changed = true
		}
	}
	for _, key := range sortedKeys(a.contextKeys) {
		site := a.contextKeys[key]
		if !site.needsCancel {
			continue
		}
		parent := emptyFlow()
		for _, name := range site.names {
			if contextConstructorClass(name) != "cancelable" {
				continue
			}
			for _, value := range sortedSSAValues(site.parents[name]) {
				a.merge(&parent, a.get(value))
			}
		}
		if site.parentUnknown || site.unknown {
			parent.interfaceUnknown = true
		}
		if a.bindCancellationScheduler(parent, "cancel:"+key) {
			changed = true
		}
	}
	return changed
}

func (a *flowAnalysis) bindCancellationScheduler(receiver flowValue, delivery string) bool {
	changed := false
	for _, effect := range sortedKeys(receiver.effects) {
		target := a.afterFuncSchedulers[strings.TrimPrefix(effect, "afterfunc_scheduler:")]
		if !strings.HasPrefix(effect, "afterfunc_scheduler:") || target == nil || len(target.Params) != 2 {
			continue
		}
		if a.put(target.Params[0], a.schedulerReceiver(receiver, target)) {
			changed = true
		}
		relay := emptyFlow()
		relay.effects[delivery] = true
		if a.put(target.Params[1], relay) {
			changed = true
		}
	}
	return changed
}

// Optional methods can be promoted through embedded fields. Resolve each known
// root's method selection separately; unrelated context candidates must not be
// bound as this method's receiver. Pointer fields use their stored aliases and
// value fields keep their own field address.
func (a *flowAnalysis) schedulerReceiver(value flowValue, target *ssa.Function) flowValue {
	result := emptyFlow()
	for _, root := range sortedKeys(value.addresses) {
		typ := a.addressTypes[root]
		if typ == nil {
			continue
		}
		selection := types.NewMethodSet(typ).Lookup(nil, "AfterFunc")
		if selection == nil || selection.Obj() != target.Object() {
			continue
		}
		addresses := map[string]bool{root: true}
		path := selection.Index()
		for _, field := range path[:len(path)-1] {
			if pointer, ok := typ.Underlying().(*types.Pointer); ok {
				typ = pointer.Elem()
			}
			record, ok := typ.Underlying().(*types.Struct)
			if !ok || field >= record.NumFields() {
				addresses = nil
				break
			}
			typ = record.Field(field).Type()
			next := map[string]bool{}
			for _, address := range sortedKeys(addresses) {
				cell := fmt.Sprintf("%s.field:%d", address, field)
				if _, pointer := typ.Underlying().(*types.Pointer); pointer {
					for _, alias := range sortedKeys(a.memory[cell].addresses) {
						next[alias] = true
					}
				} else {
					next[cell] = true
				}
			}
			addresses = next
		}
		candidate := emptyFlow()
		candidate.addresses = addresses
		a.merge(&result, candidate)
	}
	return result
}

func (a *flowAnalysis) afterFuncSchedulerRelationships(key string, site afterFuncSite, ix *index) {
	receiver := site.context
	evidence := ix.callEvidence(site.call)
	a.cancellationSchedulerRelationships(key, receiver, evidence, "cancellation_callback_scheduler", "cancellation_scheduler_stop_target", ix)
}

func (a *flowAnalysis) cancellationSchedulerRelationships(key string, receiver flowValue, evidence Evidence, schedulerKind, stopKind string, ix *index) {
	for _, effect := range sortedKeys(receiver.effects) {
		if !strings.HasPrefix(effect, "afterfunc_scheduler:") {
			continue
		}
		target := a.afterFuncSchedulers[strings.TrimPrefix(effect, "afterfunc_scheduler:")]
		if target == nil {
			continue
		}
		ix.edges = append(ix.edges, Relationship{From: key, To: ix.owner(target), Kind: schedulerKind, Certainty: "possible", Evidence: evidence})
		ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "cancellation_scheduler_model", Reason: "a known context type supplies the optional AfterFunc hook; receiver and internal delivery-wrapper relay candidates are modeled, but parent Done/Err conditions, scheduler selection, callback execution, returned-stop invocation and ordering are not proved; the internal wrapper is not the user's callback value", Evidence: evidence})
		if len(a.returns[target]) == 0 {
			continue
		}
		stopCandidates := a.returns[target][0]
		outsideStop := stopCandidates.functionUnknown
		knownStop := len(stopCandidates.functions) > 0
		for _, stop := range sortedFunctions(a.returns[target][0].functions) {
			if owner := ix.owner(stop); owner != "" {
				ix.edges = append(ix.edges, Relationship{From: key, To: owner, Kind: stopKind, Certainty: "possible", Evidence: evidence})
			} else {
				outsideStop = true
			}
		}
		if outsideStop || stopCandidates.functionNil {
			reason := "custom scheduler retains an outside returned-stop candidate alongside known targets"
			if stopCandidates.functionNil && knownStop && !stopCandidates.functionUnknown && !outsideStop {
				reason = "custom scheduler retains known returned-stop targets alongside a typed nil stop-function alternative; successful stopping is not established"
			} else if stopCandidates.functionNil && !knownStop && !stopCandidates.functionUnknown && !outsideStop {
				reason = "custom scheduler returns only a typed nil stop-function candidate; successful stopping is not established"
			} else if stopCandidates.functionNil {
				reason = "custom scheduler retains typed nil and outside returned-stop alternatives; successful stopping is not established"
			}
			ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "unresolved_cancellation_scheduler_stop", Reason: reason, Evidence: evidence})
		}
	}
	if receiver.interfaceUnknown || receiver.effects["afterfunc_scheduler_unmodeled"] {
		ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "unresolved_cancellation_scheduler", Reason: "outside context types, promoted or adapted scheduler receivers and omitted dependency implementations can supply additional optional AfterFunc hooks; known candidates do not establish the complete scheduler set", Evidence: evidence})
	}
}
