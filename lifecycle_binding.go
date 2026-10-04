package contracttrace

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Keep the receiver on each closure value. The generated wrapper's FreeVars
// are shared by distinct closures and cannot identify a captured handle.
func (a *flowAnalysis) modelLifecycleBinding(closure *ssa.MakeClosure, result *flowValue, config Config) {
	if len(config.LifecycleRules) == 0 {
		return
	}
	wrapper := closure.Fn.(*ssa.Function)
	if !strings.HasPrefix(wrapper.Synthetic, "bound method wrapper") {
		return
	}
	type apiTarget struct {
		symbol    string
		signature *types.Signature
	}
	targets := []apiTarget{}
	seenTargets := map[string]bool{}
	addTarget := func(function *ssa.Function) {
		symbol := lifecycleSymbol(function)
		if symbol == "" {
			return
		}
		signature := lifecycleDeclaredSignature(function)
		if signature == nil {
			return
		}
		if seenTargets[symbol] {
			return
		}
		seenTargets[symbol] = true
		targets = append(targets, apiTarget{symbol: symbol, signature: signature})
	}
	// For interface selectors this is the source-declared API method. Its
	// signature retains the receiver even when the generated wrapper signature
	// does not. Concrete callgraph targets below remain separate candidates.
	addTarget(wrapper)
	for _, block := range wrapper.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			for _, target := range sortedFunctions(a.targets(call.Common())) {
				addTarget(target)
			}
		}
	}
	args := []ssa.Value{}
	for _, block := range wrapper.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			args = flowArguments(call.Common())
			if len(args) > 0 {
				break
			}
		}
		if len(args) > 0 {
			break
		}
	}
	for index, rule := range config.LifecycleRules {
		id := strconv.Itoa(index)
		for _, target := range targets {
			if target.symbol != rule.Symbol {
				continue
			}
			result.effects["lifecycle-bound-seen:"+id] = true
			if reason := lifecycleBoundSignatureRuleError(rule, target.signature); reason != "" {
				result.effects["lifecycle-bound-invalid:"+id+":"+reason] = true
				continue
			}
			result.effects["lifecycle-bound-valid:"+id] = true
			if !rule.Receiver || len(args) == 0 {
				continue
			}
			for binding, variable := range wrapper.FreeVars {
				if args[0] != variable || binding >= len(closure.Bindings) {
					continue
				}
				value := a.get(closure.Bindings[binding])
				candidates := value.addresses
				if rule.Identity == "value" {
					candidates = value.strings
				}
				for _, candidate := range sortedKeys(candidates) {
					result.effects["lifecycle-bound:"+id+":"+candidate] = true
				}
				if value.stringUnknown || value.interfaceUnknown || value.sqlUnknown || value.functionUnknown {
					result.effects["lifecycle-bound-unknown:"+id] = true
				}
			}
		}
	}
}

func (a *flowAnalysis) configuredLifecycleBoundUse(call ssa.CallInstruction, from string, ix *index, config Config) map[int]bool {
	consumed := map[int]bool{}
	value := a.get(call.Common().Value)
	for index, rule := range config.LifecycleRules {
		id := strconv.Itoa(index)
		if !value.effects["lifecycle-bound-seen:"+id] {
			continue
		}
		consumed[index] = true
		if a.lifecycleMatched == nil {
			a.lifecycleMatched = map[string]bool{}
		}
		a.lifecycleMatched[rule.Symbol] = true
		invalidPrefix := "lifecycle-bound-invalid:" + id + ":"
		invalid := ""
		for _, effect := range sortedKeys(value.effects) {
			if strings.HasPrefix(effect, invalidPrefix) {
				invalid = strings.TrimPrefix(effect, invalidPrefix)
				break
			}
		}
		if invalid != "" {
			ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "invalid_lifecycle_rule_site", Reason: rule.Symbol + ": " + invalid, Evidence: ix.callEvidence(call)})
			if !value.effects["lifecycle-bound-valid:"+id] {
				continue
			}
		}
		if reason := lifecycleBoundInvocationError(rule, call); reason != "" {
			ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "invalid_lifecycle_rule_site", Reason: rule.Symbol + ": " + reason, Evidence: ix.callEvidence(call)})
			continue
		}
		outsideAlternative := value.functionUnknown || value.effects["lifecycle-bound-unknown:"+id]
		if outsideAlternative || value.functionNil {
			reason := "Configured bound method target or receiver includes outside candidates for " + rule.Symbol + "; known candidates remain possible."
			if value.functionNil && outsideAlternative {
				reason = "Configured bound method target or receiver retains typed nil and outside alternatives for " + rule.Symbol + "; known candidates remain possible but execution is not established."
			} else if value.functionNil {
				reason = "Configured bound method target retains a typed nil callable alternative for " + rule.Symbol + "; known candidates remain possible but execution is not established."
			}
			ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "unresolved_lifecycle_dispatch", Reason: reason, Evidence: ix.callEvidence(call)})
		}
		if rule.Result != nil {
			valueResult := boundLifecycleResult(a, call, *rule.Result)
			if rule.Identity == "origin" {
				origin := configuredLifecycleOrigin(call, rule, rule.Symbol, ix)
				if valueResult.addresses == nil {
					valueResult.addresses = map[string]bool{}
				}
				valueResult.addresses[origin] = true
			}
			a.emitBoundLifecycleRole(call, from, rule, valueResult, ix)
			if lifecycleResultDiscarded(call, *rule.Result) {
				ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "discarded_lifecycle_result", Reason: fmt.Sprintf("%s configured result %d has no modeled consumer; deferred/goroutine returns and unused slots cannot transfer their handle to the caller", rule.Symbol, *rule.Result), Evidence: ix.callEvidence(call)})
			}
			continue
		}
		if rule.Receiver {
			prefix := "lifecycle-bound:" + id + ":"
			selected := emptyFlow()
			for _, effect := range sortedKeys(value.effects) {
				if strings.HasPrefix(effect, prefix) {
					selected.addresses[strings.TrimPrefix(effect, prefix)] = true
				}
			}
			if value.effects["lifecycle-bound-unknown:"+id] {
				selected.stringUnknown = true
				selected.interfaceUnknown = true
			}
			if rule.Identity == "value" {
				selected.strings = selected.addresses
				selected.addresses = nil
			}
			a.emitBoundLifecycleRole(call, from, rule, selected, ix)
			continue
		}
		args := flowArguments(call.Common())
		if rule.Argument == nil || *rule.Argument >= len(args) {
			ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "invalid_lifecycle_rule_site", Reason: rule.Symbol + ": configured argument is outside the bound method invocation", Evidence: ix.callEvidence(call)})
			continue
		}
		selected := a.get(args[*rule.Argument])
		a.emitBoundLifecycleRole(call, from, rule, selected, ix)
	}
	return consumed
}

func boundLifecycleResult(a *flowAnalysis, call ssa.CallInstruction, slot int) flowValue {
	ordinary, ok := call.(*ssa.Call)
	if !ok {
		return emptyFlow()
	}
	if _, tuple := ordinary.Type().(*types.Tuple); !tuple {
		return a.get(ordinary)
	}
	var selected flowValue
	if refs := ordinary.Referrers(); refs != nil {
		for _, instruction := range *refs {
			if extract, ok := instruction.(*ssa.Extract); ok && extract.Index == slot {
				a.merge(&selected, a.get(extract))
			}
		}
	}
	return selected
}

func (a *flowAnalysis) emitBoundLifecycleRole(call ssa.CallInstruction, from string, rule LifecycleRule, selected flowValue, ix *index) {
	candidates := selected.addresses
	if rule.Identity == "value" {
		candidates = selected.strings
	}
	resolved := false
	outside := selected.interfaceUnknown || selected.sqlUnknown || selected.functionUnknown
	if rule.Identity == "value" {
		outside = outside || selected.stringUnknown
	}
	for _, candidate := range sortedKeys(candidates) {
		if strings.Contains(candidate, unknown) {
			continue
		}
		resolved = true
		outside = outside || strings.HasPrefix(candidate, "input:") || a.inputRoots[candidate]
		key := resourceID("lifecycle", rule.Namespace, candidate)
		if ix.funcs[key] == nil {
			ix.funcs[key] = &function{node: Node{ID: key, Name: "lifecycle " + rule.Namespace + " " + candidate, Kind: "lifecycle_resource", Evidence: ix.callEvidence(call)}}
		}
		ix.callEdge(from, key, "lifecycle_"+rule.Role, "possible", call)
	}
	if !resolved || outside {
		ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "unresolved_lifecycle_resource", Reason: fmt.Sprintf("%s bound selector candidates are missing or include outside values; ownership and dispatch remain unresolved", rule.Symbol), Evidence: ix.callEvidence(call)})
	}
}

func lifecycleBoundSignatureRuleError(rule LifecycleRule, signature *types.Signature) string {
	if signature == nil {
		return "configured API declaration signature is unavailable"
	}
	method := signature.Recv() != nil
	if rule.Result != nil {
		results := signature.Results()
		if *rule.Result >= results.Len() {
			return "configured result index is outside the API signature"
		}
		return lifecycleIdentityTypeError(rule.Identity, results.At(*rule.Result).Type())
	}
	if rule.Receiver {
		if !method {
			return "receiver selector used on a function without a receiver"
		}
		return lifecycleIdentityTypeError(rule.Identity, signature.Recv().Type())
	}
	if !method {
		return "bound selector target has no method receiver"
	}
	if rule.Argument == nil || *rule.Argument >= signature.Params().Len() {
		return "configured argument index is outside the API signature"
	}
	return lifecycleIdentityTypeError(rule.Identity, signature.Params().At(*rule.Argument).Type())
}

func lifecycleBoundInvocationError(rule LifecycleRule, call ssa.CallInstruction) string {
	signature, ok := call.Common().Value.Type().Underlying().(*types.Signature)
	if !ok {
		return "bound invocation has no function signature"
	}
	if rule.Result != nil {
		if *rule.Result >= signature.Results().Len() {
			return "configured result index is outside the bound method invocation"
		}
		return lifecycleIdentityTypeError(rule.Identity, signature.Results().At(*rule.Result).Type())
	}
	if rule.Receiver {
		return ""
	}
	if rule.Argument == nil || *rule.Argument >= signature.Params().Len() {
		return "configured argument is outside the bound method invocation"
	}
	return lifecycleIdentityTypeError(rule.Identity, signature.Params().At(*rule.Argument).Type())
}
