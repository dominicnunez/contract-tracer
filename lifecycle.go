package contracttrace

import (
	"fmt"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type contextSite struct {
	key, owner, name string
	names            []string
	position         token.Pos
	certainty        string
	needsCancel      bool
	parent           ssa.Value
	parents          map[string]map[ssa.Value]bool
	parentUnknown    bool
	unknown          bool
}

func (a *flowAnalysis) modelContext(call ssa.CallInstruction, ix *index) bool {
	changed := false
	if direct, ok := call.(*ssa.Call); ok {
		changed = a.modelAfterFunc(direct, ix)
		a.modelDone(direct, ix)
	}
	common := call.Common()
	targets := a.targets(common)
	names := []string{}
	knownTargets := 0
	for _, target := range sortedFunctions(targets) {
		if candidate := contextConstructorName(target); candidate != "" {
			seen := false
			for _, current := range names {
				seen = seen || current == candidate
			}
			if !seen {
				names = append(names, candidate)
			}
			knownTargets++
		}
	}
	if len(names) == 0 {
		return changed
	}
	name := names[0]
	needsCancel := false
	for _, candidate := range names {
		switch candidate {
		case "WithCancel", "WithCancelCause", "WithDeadline", "WithDeadlineCause", "WithTimeout", "WithTimeoutCause":
			needsCancel = true
		case "Background", "TODO", "WithValue", "WithoutCancel":
		default:
			return changed
		}
	}
	if a.contexts == nil {
		a.contexts = map[ssa.CallInstruction]contextSite{}
	}
	if a.contextKeys == nil {
		a.contextKeys = map[string]contextSite{}
	}
	evidence := ix.evidence(call.Pos())
	key := resourceID("context", "", fmt.Sprintf("%s:%d:%d", evidence.File, evidence.Line, evidence.Column))
	contextValue := emptyFlow()
	contextValue.addresses[key] = true
	callee := a.get(common.Value)
	if knownTargets != len(targets) || callee.functionUnknown || callee.functionNil || callee.boundReceiverUnknown {
		contextValue.interfaceUnknown = true
	}
	for i := 1; i < len(names); i++ {
		if contextConstructorClass(names[0]) != contextConstructorClass(names[i]) {
			contextValue.interfaceUnknown = true
		}
	}
	results := []flowValue{contextValue}
	if needsCancel {
		cancel := emptyFlow()
		cancel.effects["cancel:"+key] = true
		results = append(results, cancel)
	}
	certainty := "fact"
	if _, ok := call.(*ssa.Call); !ok {
		certainty = "possible"
	}
	site := contextSite{key: key, owner: ix.owner(call.Parent()), name: name, names: names, position: call.Pos(), certainty: certainty, needsCancel: needsCancel}
	needsParent := false
	for _, candidate := range names {
		needsParent = needsParent || candidate != "Background" && candidate != "TODO"
	}
	if needsParent && len(call.Common().Args) > 0 {
		site.parent = call.Common().Args[0]
	}
	site.unknown = contextValue.interfaceUnknown
	a.contexts[call] = site
	changed = a.mergeContextSite(site) || changed
	if direct, ok := call.(*ssa.Call); ok {
		if a.special == nil {
			a.special = map[*ssa.Call][]flowValue{}
		}
		a.special[direct] = results
	}
	if ix.funcs[key] == nil {
		ix.funcs[key] = &function{node: Node{ID: key, Name: "context at " + evidence.File + fmt.Sprintf(":%d:%d", evidence.Line, evidence.Column), Kind: "context", Evidence: evidence}}
	}
	return changed
}

// mergeContextSite keeps the candidates for one source creation site when Go
// generic instantiations have separate SSA call instructions at the same
// position. Parent values stay as SSA identities and are resolved with get at
// each use, so later flow growth is visible. The per-call contexts map remains
// separate for source-owner and cancellation reporting.
func (a *flowAnalysis) mergeContextSite(site contextSite) bool {
	current, exists := a.contextKeys[site.key]
	changed := false
	if !exists {
		current = contextSite{
			key: site.key, owner: site.owner, position: site.position,
			certainty: site.certainty, parents: map[string]map[ssa.Value]bool{},
		}
		changed = true
	}
	if site.owner != "" && (current.owner == "" || site.owner < current.owner) {
		current.owner = site.owner
		changed = true
	}
	if current.name == "" || site.name != "" && site.name < current.name {
		current.name = site.name
		changed = true
	}
	nameSet := make(map[string]bool, len(current.names)+len(site.names))
	for _, name := range current.names {
		nameSet[name] = true
	}
	for _, name := range site.names {
		if !nameSet[name] {
			nameSet[name] = true
			changed = true
		}
	}
	current.names = sortedKeys(nameSet)
	if site.needsCancel && !current.needsCancel {
		current.needsCancel = true
		changed = true
	}
	if site.unknown && !current.unknown {
		current.unknown = true
		changed = true
	}
	if site.parentUnknown && !current.parentUnknown {
		current.parentUnknown = true
		changed = true
	}
	if site.parent != nil {
		for _, name := range site.names {
			if name == "Background" || name == "TODO" {
				continue
			}
			parents := current.parents[name]
			if parents[site.parent] {
				continue
			}
			if len(parents) >= maxFlowValues {
				if !current.parentUnknown {
					current.parentUnknown = true
					changed = true
				}
				a.coverage.Widened = true
				continue
			}
			if parents == nil {
				parents = map[ssa.Value]bool{}
				current.parents[name] = parents
			}
			parents[site.parent] = true
			changed = true
		}
	}
	a.contextKeys[site.key] = current
	return changed
}

func contextHasName(site contextSite, name string) bool {
	for _, candidate := range site.names {
		if candidate == name {
			return true
		}
	}
	return site.name == name
}

func contextConstructorName(target *ssa.Function) string {
	if target == nil {
		return ""
	}
	method, ok := target.Object().(*types.Func)
	if !ok || method.Pkg() == nil || method.Pkg().Path() != "context" {
		return ""
	}
	name := method.Name()
	switch name {
	case "WithCancel", "WithCancelCause", "WithDeadline", "WithDeadlineCause", "WithTimeout", "WithTimeoutCause", "Background", "TODO", "WithValue", "WithoutCancel":
	default:
		return ""
	}
	canonical, ok := method.Pkg().Scope().Lookup(name).(*types.Func)
	if !ok || canonical.Origin() != method.Origin() {
		return ""
	}
	return name
}

func contextConstructorClass(name string) string {
	switch name {
	case "Background", "TODO", "WithoutCancel":
		return "nil_done"
	case "WithValue":
		return "with_value"
	case "WithCancel", "WithCancelCause", "WithDeadline", "WithDeadlineCause", "WithTimeout", "WithTimeoutCause":
		return "cancelable"
	default:
		return "unknown"
	}
}

func (a *flowAnalysis) contextUse(call ssa.CallInstruction, from string, ix *index) {
	a.afterFuncStopUse(call, from, ix)
	common := call.Common()
	for effect := range a.get(common.Value).effects {
		if strings.HasPrefix(effect, "cancel:") {
			ix.edge(from, strings.TrimPrefix(effect, "cancel:"), "context_cancel", "possible", call.Pos())
		}
	}
	methodNames := map[string]bool{}
	if common.IsInvoke() {
		if common.Method != nil && common.Method.Name() == "Done" {
			methodNames["Done"] = true
		} else if common.Method != nil && common.Method.Name() == "Err" {
			methodNames["Err"] = true
		}
	} else {
		for target := range a.targets(common) {
			if method, ok := target.Object().(*types.Func); ok {
				if method.Name() == "Done" || method.Name() == "Err" {
					methodNames[method.Name()] = true
				}
			}
		}
	}
	for _, methodName := range []string{"Done", "Err"} {
		if !methodNames[methodName] {
			continue
		}
		receiver, matched, unknown := a.contextCallReceiver(common, methodName)
		if !matched {
			continue
		}
		found := false
		for key := range receiver.addresses {
			if _, modeled := a.contextKeys[key]; modeled {
				ix.edge(from, key, "context_observe", "possible", call.Pos())
				found = true
			}
		}
		if !found || unknown || receiver.interfaceUnknown || receiver.boundReceiverUnknown {
			ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "unresolved_context", Reason: "context observation has no modeled creation site; input or alias may be external", Evidence: ix.evidence(call.Pos())})
		}
	}
	for _, arg := range common.Args {
		for key := range a.get(arg).addresses {
			if _, modeled := a.contextKeys[key]; modeled {
				ix.edge(from, key, "context_pass", "possible", call.Pos())
			}
		}
	}
}
func (a *flowAnalysis) contextBoundaries(ix *index) {
	canceled := map[string]bool{}
	for _, edge := range ix.edges {
		if edge.Kind == "context_cancel" {
			canceled[edge.To] = true
		}
	}
	for _, site := range a.contexts {
		summary := a.contextKeys[site.key]
		if site.needsCancel && site.parent != nil {
			parent := a.get(site.parent)
			if summary.parentUnknown || summary.unknown {
				parent.interfaceUnknown = true
			}
			a.cancellationSchedulerRelationships(site.key, parent, ix.evidence(site.position), "context_cancellation_scheduler", "context_scheduler_stop_target", ix)
		}
		ix.edge(site.owner, site.key, "context_create", site.certainty, site.position)
		if site.name == "WithoutCancel" {
			ix.edge(site.owner, site.key, "context_detach", site.certainty, site.position)
		}
		if site.needsCancel && !canceled[site.key] {
			ix.boundaries = append(ix.boundaries, Boundary{Node: site.owner, Kind: "unobserved_cancel", Reason: "no modeled call to the cancel function for " + site.key + "; it may escape to external code or rely on deadline expiry, so this is an investigation candidate", Evidence: ix.evidence(site.position)})
		}
		if site.parent != nil {
			parentKnown := false
			parentFlow := a.get(site.parent)
			for parent := range parentFlow.addresses {
				if _, modeled := a.contextKeys[parent]; modeled {
					ix.edge(site.key, parent, "context_parent", "possible", site.position)
					if site.name != "WithoutCancel" {
						ix.edge(site.key, parent, "context_cancellation_parent", "possible", site.position)
					}
					parentKnown = true
				}
			}
			if !parentKnown || parentFlow.interfaceUnknown || parentFlow.sqlUnknown || parentFlow.boundReceiverUnknown || summary.parentUnknown || summary.unknown {
				reason := "parent context creation is outside the modeled flow"
				if parentKnown {
					reason = "known parent context candidates coexist with unresolved or widened parent alternatives"
				}
				ix.boundaries = append(ix.boundaries, Boundary{Node: site.owner, Kind: "context_parent", Reason: reason, Evidence: ix.evidence(site.position)})
			}
		} else if summary.parentUnknown {
			ix.boundaries = append(ix.boundaries, Boundary{Node: site.owner, Kind: "context_parent", Reason: "bounded parent candidates omit additional context origins", Evidence: ix.evidence(site.position)})
		}
	}
}
