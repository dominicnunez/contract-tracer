package contracttrace

import (
	"fmt"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type afterFuncSite struct {
	call              ssa.CallInstruction
	context, callback flowValue
}

func (a *flowAnalysis) modelAfterFunc(call ssa.CallInstruction, ix *index) bool {
	common := call.Common()
	if common.IsInvoke() || len(common.Args) != 2 {
		return false
	}
	modeled, outside := false, a.get(common.Value).functionUnknown
	for target := range a.targets(common) {
		if isAfterFunc(target) {
			modeled = true
		} else if ix.owner(target) == "" || len(target.Blocks) == 0 {
			outside = true
		}
	}
	if !modeled {
		return false
	}
	evidence := ix.callEvidence(call)
	key := resourceID("afterfunc", "", fmt.Sprintf("%s:%d:%d", evidence.File, evidence.Line, evidence.Column))
	if a.afterFuncs == nil {
		a.afterFuncs = map[string]afterFuncSite{}
	}
	site := a.afterFuncs[key]
	if site.call == nil {
		site.call = call
	}
	changed := a.merge(&site.context, a.get(common.Args[0]))
	changed = a.merge(&site.callback, a.get(common.Args[1])) || changed
	a.afterFuncs[key] = site
	if result, ok := call.(*ssa.Call); ok {
		if a.special == nil {
			a.special = map[*ssa.Call][]flowValue{}
		}
		stop := emptyFlow()
		stop.effects["afterfunc_stop:"+key] = true
		stop.functionUnknown = outside
		a.special[result] = []flowValue{stop}
	}
	if ix.funcs[key] == nil {
		ix.funcs[key] = &function{node: Node{ID: key, Name: "AfterFunc at " + evidence.File + fmt.Sprintf(":%d:%d", evidence.Line, evidence.Column), Kind: "callback_registration", Evidence: evidence}}
	}
	return changed
}

func isAfterFunc(target *ssa.Function) bool {
	return target != nil && target.Object() != nil && target.Object().Pkg() != nil && target.Object().Pkg().Path() == "context" && target.Object().Name() == "AfterFunc" && target.Signature.Recv() == nil
}

func afterFuncKind(call ssa.CallInstruction, kind string) string {
	switch call.(type) {
	case *ssa.Defer:
		return "deferred_" + kind
	case *ssa.Go:
		return "goroutine_" + kind
	}
	return kind
}

func (a *flowAnalysis) afterFuncStopUse(call ssa.CallInstruction, from string, ix *index) {
	for _, effect := range sortedKeys(a.get(call.Common().Value).effects) {
		if strings.HasPrefix(effect, "afterfunc_dispatch:") {
			key := strings.TrimPrefix(effect, "afterfunc_dispatch:")
			if _, modeled := a.afterFuncs[key]; modeled {
				ix.edges = append(ix.edges, Relationship{From: from, To: key, Kind: afterFuncKind(call, "cancellation_scheduler_dispatch"), Certainty: "possible", Evidence: ix.callEvidence(call)})
			}
		}
		if !strings.HasPrefix(effect, "afterfunc_stop:") {
			continue
		}
		key := strings.TrimPrefix(effect, "afterfunc_stop:")
		if _, modeled := a.afterFuncs[key]; !modeled {
			continue
		}
		ix.edges = append(ix.edges, Relationship{From: from, To: key, Kind: afterFuncKind(call, "cancellation_callback_stop"), Certainty: "possible", Evidence: ix.callEvidence(call)})
	}
}

func (a *flowAnalysis) afterFuncRelationships(ix *index) {
	for _, key := range sortedKeys(a.afterFuncs) {
		site := a.afterFuncs[key]
		evidence := ix.callEvidence(site.call)
		certainty := "fact"
		if _, direct := site.call.(*ssa.Call); !direct || site.call.Common().StaticCallee() == nil {
			certainty = "possible"
		}
		ix.edges = append(ix.edges, Relationship{From: ix.owner(site.call.Parent()), To: key, Kind: afterFuncKind(site.call, "cancellation_callback_register"), Certainty: certainty, Evidence: evidence})
		ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "cancellation_callback_model", Reason: "source registration links a context, callback candidates and returned stop handle; runtime registration count, custom Context scheduling, cancellation/stop races, stop result, callback execution and completion ordering are not proved; calling stop does not establish callback completion", Evidence: evidence})
		a.afterFuncSchedulerRelationships(key, site, ix)
		contextValue := site.context
		known := false
		for _, context := range sortedKeys(contextValue.addresses) {
			creation, found := a.contextKeys[context]
			if !found {
				continue
			}
			known = true
			ix.edges = append(ix.edges, Relationship{From: key, To: context, Kind: "cancellation_callback_context", Certainty: "possible", Evidence: evidence})
			if creation.name == "Background" || creation.name == "TODO" || creation.name == "WithoutCancel" {
				ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "noncanceling_callback_context", Reason: "registration has a known context candidate with no cancellation signal; this candidate does not inherit parent cancellation, while other aliases or runtime values may remain possible", Evidence: evidence})
			}
		}
		if !known || contextValue.interfaceUnknown {
			ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "unresolved_callback_context", Reason: "cancellation registration retains an outside or unmodeled context candidate; known local creation sites do not close its runtime origin set", Evidence: evidence})
		}
		callback := site.callback
		known = false
		for _, effect := range sortedKeys(callback.effects) {
			if strings.HasPrefix(effect, "cancel:") {
				context := strings.TrimPrefix(effect, "cancel:")
				if _, modeled := a.contextKeys[context]; modeled {
					known = true
					ix.edges = append(ix.edges, Relationship{From: key, To: context, Kind: "cancellation_callback_cancel", Certainty: "possible", Evidence: evidence})
				}
			}
		}
		for _, target := range sortedFunctions(callback.functions) {
			owner := ix.owner(target)
			if owner == "" {
				continue
			}
			known = true
			ix.edges = append(ix.edges, Relationship{From: key, To: owner, Kind: "cancellation_callback_target", Certainty: "possible", Evidence: evidence})
		}
		if !known || callback.functionUnknown || callback.functionNil {
			reason := "registration retains an outside or unidentified callback candidate alongside any known local targets; nil safety and invocation are not established"
			if callback.functionNil && known && callback.functionUnknown {
				reason = "registration retains known callback candidates alongside typed nil and outside callback alternatives; callback invocation is not established"
			} else if callback.functionNil && known {
				reason = "registration retains known callback candidates alongside a typed nil callback alternative; callback invocation is not established"
			} else if callback.functionNil && callback.functionUnknown {
				reason = "registration retains typed nil and outside callback alternatives; callback invocation is not established"
			} else if callback.functionNil {
				reason = "registration retains only a typed nil callback candidate; callback invocation is not established"
			}
			ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "unresolved_cancellation_callback", Reason: reason, Evidence: evidence})
		}
	}
}
