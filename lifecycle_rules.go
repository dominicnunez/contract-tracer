package contracttrace

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func lifecycleSymbol(target *ssa.Function) string {
	obj := lifecycleDeclaredFunction(target)
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	return obj.Pkg().Path() + "::" + functionName(obj.Origin())
}

// SSA method expressions and bound-method wrappers retain the API Func object,
// while their executable signature can omit the declared receiver. Selectors
// are defined by the source declaration; invocation arguments are mapped using
// the executable call shape separately.
func lifecycleDeclaredFunction(target *ssa.Function) *types.Func {
	if target == nil {
		return nil
	}
	if obj, ok := target.Object().(*types.Func); ok && obj != nil {
		return obj.Origin()
	}
	if origin := target.Origin(); origin != nil {
		if obj, ok := origin.Object().(*types.Func); ok && obj != nil {
			return obj.Origin()
		}
	}
	return nil
}

func lifecycleDeclaredSignature(target *ssa.Function) *types.Signature {
	if target == nil {
		return nil
	}
	actual := target.Signature
	if actual == nil {
		return nil
	}
	// Ordinary functions and methods carry the best instantiated executable
	// signature. Use the declared object only for generated wrappers whose SSA
	// signature deliberately changes the receiver/parameter shape.
	if !strings.Contains(target.Synthetic, "bound method wrapper") && !strings.Contains(target.Synthetic, "thunk") {
		return actual
	}
	obj := lifecycleDeclaredFunction(target)
	if obj == nil {
		return actual
	}
	declared, _ := obj.Type().(*types.Signature)
	if declared == nil || declared.Recv() == nil {
		return actual
	}
	var receiverType types.Type
	params := actual.Params()
	switch {
	case strings.Contains(target.Synthetic, "bound method wrapper"):
		receiverType = declared.Recv().Type()
		if len(target.FreeVars) > 0 {
			receiverType = target.FreeVars[0].Type()
		}
	case strings.Contains(target.Synthetic, "thunk") && params.Len() == declared.Params().Len()+1:
		if apiMethod := lifecycleDeclaredFunction(target); apiMethod != nil {
			if _, selectedSignature, _ := lifecycleMethodSetSelection(params.At(0).Type(), apiMethod); selectedSignature != nil {
				return selectedSignature
			}
		}
		// Keep the declaration's inner receiver if the adapter's receiver path
		// cannot be selected. Never reinterpret its outer receiver as the API.
		return declared
	default:
		return actual
	}
	receiver := types.NewVar(token.NoPos, obj.Pkg(), "", receiverType)
	return types.NewSignatureType(receiver, nil, nil, params, actual.Results(), actual.Variadic())
}

func lifecycleOriginType(typ types.Type) bool {
	switch typ.Underlying().(type) {
	case *types.Pointer, *types.Struct, *types.Array, *types.Interface, *types.Map, *types.Slice, *types.Chan, *types.Signature:
		return true
	}
	return false
}

func lifecycleIdentityTypeError(identity string, typ types.Type) string {
	if identity == "origin" && !lifecycleOriginType(typ) {
		return "origin identity requires a reference or aggregate handle; scalar identity is unsupported"
	}
	if identity == "value" {
		basic, ok := typ.Underlying().(*types.Basic)
		if !ok || basic.Kind() != types.String {
			return "value identity requires a string resource key"
		}
	}
	return ""
}

func configuredLifecycleOrigin(call ssa.CallInstruction, rule LifecycleRule, symbol string, ix *index) string {
	evidence := ix.callEvidence(call)
	origin := fmt.Sprintf("configured:%s:%s:%s:%d:%d:result:%d", symbol, ix.owner(call.Parent()), evidence.File, evidence.Line, evidence.Column, *rule.Result)
	key := resourceID("lifecycle", rule.Namespace, origin)
	if ix.funcs[key] == nil {
		ix.funcs[key] = &function{node: Node{ID: key, Name: "lifecycle handle at " + evidence.File + fmt.Sprintf(":%d:%d result %d", evidence.Line, evidence.Column, *rule.Result), Kind: "lifecycle_resource", Evidence: evidence}}
	}
	return origin
}

// Configured output origins augment ordinary flow without replacing builtin
// summaries or known local return candidates. Each site/slot/target has its own
// candidate: repeated invocations and successful acquisition are not proved.
func (a *flowAnalysis) configuredLifecycleResult(value ssa.Value, result *flowValue, ix *index, config Config) {
	if len(config.LifecycleRules) == 0 {
		return
	}
	var call *ssa.Call
	slot := 0
	switch v := value.(type) {
	case *ssa.Call:
		if _, tuple := v.Type().(*types.Tuple); tuple {
			return
		}
		call = v
	case *ssa.Extract:
		call, _ = v.Tuple.(*ssa.Call)
		slot = v.Index
	}
	if call == nil || !lifecycleOriginType(value.Type()) {
		return
	}
	bound := a.get(call.Common().Value)
	for index, rule := range config.LifecycleRules {
		id := strconv.Itoa(index)
		if rule.Identity != "origin" || rule.Result == nil || *rule.Result != slot || !bound.effects["lifecycle-bound-seen:"+id] {
			continue
		}
		if hasEffectPrefix(bound.effects, "lifecycle-bound-invalid:"+id+":") && !bound.effects["lifecycle-bound-valid:"+id] {
			continue
		}
		if lifecycleBoundInvocationError(rule, call) != "" {
			continue
		}
		origin := configuredLifecycleOrigin(call, rule, rule.Symbol, ix)
		result.addresses[origin] = true
	}
	for _, target := range sortedFunctions(a.targets(call.Common())) {
		if strings.HasPrefix(call.Parent().Synthetic, "bound method wrapper") {
			continue
		}
		symbol := lifecycleSymbol(target)
		for _, rule := range config.LifecycleRules {
			if rule.Symbol != symbol || rule.Identity != "origin" || rule.Result == nil || *rule.Result != slot {
				continue
			}
			origin := configuredLifecycleOrigin(call, rule, symbol, ix)
			result.addresses[origin] = true
		}
	}
}

func hasEffectPrefix(effects map[string]bool, prefix string) bool {
	for effect := range effects {
		if strings.HasPrefix(effect, prefix) {
			return true
		}
	}
	return false
}

func lifecycleSelected(call ssa.CallInstruction, target *ssa.Function, rule LifecycleRule, a *flowAnalysis) (flowValue, types.Type, error) {
	signature := lifecycleDeclaredSignature(target)
	if signature == nil {
		return flowValue{}, nil, fmt.Errorf("API declaration signature is unavailable")
	}
	if rule.Result != nil {
		results := signature.Results()
		if *rule.Result >= results.Len() {
			return flowValue{}, nil, fmt.Errorf("configured result index is outside the API signature")
		}
		typ := results.At(*rule.Result).Type()
		ordinary, ok := call.(*ssa.Call)
		if !ok {
			return emptyFlow(), typ, nil
		}
		if results.Len() == 1 {
			return a.get(ordinary), typ, nil
		}
		var result flowValue
		if refs := ordinary.Referrers(); refs != nil {
			for _, instruction := range *refs {
				if extract, ok := instruction.(*ssa.Extract); ok && extract.Index == *rule.Result {
					a.merge(&result, a.get(extract))
				}
			}
		}
		return result, typ, nil
	}
	method := signature.Recv() != nil
	methodExpression := lifecycleMethodExpression(call, target, signature)
	args := flowArguments(call.Common())
	var methodExpressionReceiver flowValue
	if methodExpression {
		// A method expression's receiver is an explicit first argument in SSA.
		args = call.Common().Args
		projected, selectedSignature, status := a.projectMethodExpressionReceiver(
			a.get(args[0]), args[0].Type(), lifecycleDeclaredFunction(target))
		if status == methodReceiverProjectionUnsupported || selectedSignature == nil {
			return projected, nil, errUnsupportedMethodReceiverProjection
		}
		signature = selectedSignature
		method = signature.Recv() != nil
		methodExpressionReceiver = projected
	}
	index := 0
	if rule.Receiver {
		if !method {
			return flowValue{}, nil, fmt.Errorf("receiver selector used on a function without a receiver")
		}
		if methodExpression {
			index = 0
		}
	} else {
		if rule.Argument == nil || *rule.Argument < 0 || *rule.Argument >= signature.Params().Len() {
			return flowValue{}, nil, fmt.Errorf("configured argument is outside the API call")
		}
		index = *rule.Argument
		if method {
			// The declared argument was range-checked before applying the
			// explicit receiver offset, so even MaxInt selectors cannot overflow.
			index++
		}
	}
	if index >= len(args) {
		return flowValue{}, nil, fmt.Errorf("configured argument is outside the API call")
	}
	if rule.Receiver {
		if methodExpression {
			return methodExpressionReceiver, signature.Recv().Type(), nil
		}
		return a.get(args[index]), signature.Recv().Type(), nil
	}
	selectorIndex := index
	if method {
		selectorIndex--
	}
	if selectorIndex < 0 || selectorIndex >= signature.Params().Len() {
		return flowValue{}, nil, fmt.Errorf("configured argument is outside the API signature")
	}
	return a.get(args[index]), signature.Params().At(selectorIndex).Type(), nil
}

func lifecycleMethodExpression(call ssa.CallInstruction, target *ssa.Function, declared *types.Signature) bool {
	if call == nil || target == nil || declared == nil || declared.Recv() == nil || call.Common().IsInvoke() {
		return false
	}
	callable, ok := call.Common().Value.Type().Underlying().(*types.Signature)
	if !ok || callable.Params().Len() != declared.Params().Len()+1 {
		return false
	}
	// Determine the receiver offset from the function value's callable
	// signature. This also works when aliases or helper parameters make the
	// call's StaticCallee unavailable; variadic arguments are packed into the
	// final Common.Args entry after the explicit receiver.
	return len(call.Common().Args) == callable.Params().Len()
}

func lifecycleResultDiscarded(call ssa.CallInstruction, slot int) bool {
	ordinary, ok := call.(*ssa.Call)
	if !ok {
		return true
	}
	refs := ordinary.Referrers()
	if refs == nil {
		return true
	}
	_, tuple := ordinary.Type().(*types.Tuple)
	for _, instruction := range *refs {
		if _, debug := instruction.(*ssa.DebugRef); debug {
			continue
		}
		if !tuple {
			return false
		}
		if extract, ok := instruction.(*ssa.Extract); ok && extract.Index == slot {
			if uses := extract.Referrers(); uses != nil {
				for _, use := range *uses {
					if _, debug := use.(*ssa.DebugRef); !debug {
						return false
					}
				}
			}
		}
	}
	return true
}

func (a *flowAnalysis) configuredLifecycleUse(call ssa.CallInstruction, from string, ix *index, config Config) {
	if len(config.LifecycleRules) == 0 {
		return
	}
	boundRules := a.configuredLifecycleBoundUse(call, from, ix, config)
	for _, target := range sortedFunctions(a.targets(call.Common())) {
		symbol := lifecycleSymbol(target)
		for index, rule := range config.LifecycleRules {
			if symbol != rule.Symbol {
				continue
			}
			// A captured wrapper invocation was already validated and selected
			// against the declared method signature above. Its SSA wrapper target
			// intentionally has no receiver, so treating it as an ordinary method
			// call would emit a duplicate and false invalid-site warning.
			if boundRules[index] {
				continue
			}
			if a.lifecycleMatched == nil {
				a.lifecycleMatched = map[string]bool{}
			}
			a.lifecycleMatched[symbol] = true
			value, typ, err := lifecycleSelected(call, target, rule, a)
			if err == nil {
				if typeErr := lifecycleIdentityTypeError(rule.Identity, typ); typeErr != "" {
					err = fmt.Errorf("%s", typeErr)
				}
			}
			if err != nil {
				if errors.Is(err, errUnsupportedMethodReceiverProjection) {
					ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "unresolved_lifecycle_dispatch", Reason: symbol + ": " + err.Error() + "; no outer wrapper resource identity was substituted", Evidence: ix.callEvidence(call)})
					continue
				}
				ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "invalid_lifecycle_rule_site", Reason: symbol + ": " + err.Error(), Evidence: ix.callEvidence(call)})
				continue
			}
			if rule.Result != nil {
				if rule.Identity == "origin" {
					candidate := emptyFlow()
					candidate.addresses[configuredLifecycleOrigin(call, rule, symbol, ix)] = true
					a.merge(&value, candidate)
				}
				if lifecycleResultDiscarded(call, *rule.Result) {
					ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "discarded_lifecycle_result", Reason: fmt.Sprintf("%s configured result %d has no modeled consumer; deferred/goroutine returns and unused slots cannot transfer their handle to the caller", symbol, *rule.Result), Evidence: ix.callEvidence(call)})
				}
			}
			candidates := value.addresses
			if rule.Identity == "value" {
				candidates = value.strings
			}
			resolved := false
			outside := value.interfaceUnknown || value.sqlUnknown || value.functionUnknown
			for _, candidate := range sortedKeys(candidates) {
				resolved = true
				outside = outside || strings.HasPrefix(candidate, "input:") || a.inputRoots[candidate]
				key := resourceID("lifecycle", rule.Namespace, candidate)
				if ix.funcs[key] == nil {
					ix.funcs[key] = &function{node: Node{ID: key, Name: "lifecycle " + rule.Namespace + " " + candidate, Kind: "lifecycle_resource", Evidence: ix.callEvidence(call)}}
				}
				ix.callEdge(from, key, "lifecycle_"+rule.Role, "possible", call)
			}
			if !resolved || outside || rule.Identity == "value" && value.stringUnknown {
				ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "unresolved_lifecycle_resource", Reason: symbol + ": configured resource candidates are missing or include outside values; ownership and runtime identity remain unresolved", Evidence: ix.callEvidence(call)})
			}
		}
	}
}

func (a *flowAnalysis) configuredLifecycleBoundaries(ix *index, config Config) {
	if len(config.LifecycleRules) == 0 {
		return
	}
	for _, rule := range config.LifecycleRules {
		if !a.lifecycleMatched[rule.Symbol] {
			ix.boundaries = append(ix.boundaries, Boundary{Kind: "unmatched_lifecycle_rule", Reason: "No modeled API call matched lifecycle rule " + rule.Symbol + "; absent, unloaded or unsupported dispatch must be investigated."})
		}
	}
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "lifecycle_rule_model", Reason: "Typed configured API roles connect string-key candidates or modeled handle origins through bounded value flow. Result origins identify a call site, slot and candidate target, not a successful acquisition or a unique runtime invocation. Outside inputs, aliasing, discarded results, unsupported dispatch and widening can add behavior. Roles, release success, ordering, ownership transfer and eventual cleanup are not proved."})
}
