package contracttrace

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func validateConfig(c Config) error {
	for _, rule := range c.LifecycleRules {
		parts := strings.Split(rule.Symbol, "::")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.TrimSpace(rule.Symbol) != rule.Symbol {
			return fmt.Errorf("lifecycle rule requires a fully qualified symbol: %q", rule.Symbol)
		}
		if rule.Namespace == "" || strings.TrimSpace(rule.Namespace) != rule.Namespace {
			return fmt.Errorf("lifecycle rule namespace must be nonempty and have no surrounding whitespace")
		}
		switch rule.Role {
		case "acquire", "release", "observe", "transfer", "recover", "shutdown":
		default:
			return fmt.Errorf("unsupported lifecycle role %q", rule.Role)
		}
		if rule.Identity != "value" && rule.Identity != "origin" {
			return fmt.Errorf("lifecycle identity must be value or origin")
		}
		selectors := 0
		for _, index := range []*int{rule.Argument, rule.Result} {
			if index != nil {
				selectors++
				if *index < 0 {
					return fmt.Errorf("lifecycle selector indexes must be nonnegative")
				}
			}
		}
		if rule.Receiver {
			selectors++
		}
		if selectors != 1 {
			return fmt.Errorf("lifecycle rule requires exactly one argument, result or receiver selector")
		}
	}
	originNamespaces := map[string]string{}
	if c.SQLDialect != "sqlite" && c.SQLDialect != "inventory" {
		return fmt.Errorf("unsupported SQL dialect %q; use sqlite or explicit inventory mode", c.SQLDialect)
	}
	for _, pattern := range c.SQLFiles {
		if err := validateFileGlob(pattern); err != nil {
			return err
		}
	}
	for _, scope := range c.StorageScopes {
		if strings.TrimSpace(scope.Namespace) == "" || strings.TrimSpace(scope.Namespace) != scope.Namespace {
			return fmt.Errorf("storage namespace must be nonempty and have no surrounding whitespace")
		}
		if len(scope.GoFiles)+len(scope.SQLFiles)+len(scope.DatabaseOrigins) == 0 {
			return fmt.Errorf("storage scope %q needs file or database-origin selectors", scope.Namespace)
		}
		for _, patterns := range [][]string{scope.GoFiles, scope.SQLFiles} {
			for _, pattern := range patterns {
				if err := validateFileGlob(pattern); err != nil {
					return err
				}
			}
		}
		for _, origin := range scope.DatabaseOrigins {
			parts := strings.Split(origin, "::")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.TrimSpace(origin) != origin {
				return fmt.Errorf("database origin requires a fully qualified function symbol: %q", origin)
			}
			if previous := originNamespaces[origin]; previous != "" && previous != scope.Namespace {
				return fmt.Errorf("ambiguous database origin %s: %s and %s", origin, previous, scope.Namespace)
			}
			originNamespaces[origin] = scope.Namespace
		}
	}
	for _, list := range [][]string{c.EventFields, c.LifecycleNames, c.SQLMethods} {
		for _, name := range list {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("configuration names must not be empty")
			}
		}
	}
	for _, r := range c.CallRules {
		if strings.TrimSpace(r.Namespace) != r.Namespace {
			return fmt.Errorf("call-rule namespace must have no surrounding whitespace")
		}
		if !strings.Contains(r.Symbol, "::") {
			return fmt.Errorf("call rule requires a fully qualified symbol: %q", r.Symbol)
		}
		if r.Argument < 0 || r.HandlerArgument != nil && *r.HandlerArgument < 0 {
			return fmt.Errorf("call-rule argument indexes must be nonnegative")
		}
		switch r.Kind {
		case "sql_query", "event_publish", "event_subscribe", "event_dispatch":
		default:
			return fmt.Errorf("unsupported call-rule kind %q", r.Kind)
		}
		if r.HandlerArgument != nil && r.Kind != "event_subscribe" {
			return fmt.Errorf("handler_argument is only valid for event_subscribe")
		}
	}
	return nil
}
func callObject(expr ast.Expr, f *function) *types.Func {
	switch v := expr.(type) {
	case *ast.Ident:
		obj, _ := f.pkg.TypesInfo.Uses[v].(*types.Func)
		return obj
	case *ast.SelectorExpr:
		obj, _ := f.pkg.TypesInfo.Uses[v.Sel].(*types.Func)
		return obj
	case *ast.IndexExpr:
		return callObject(v.X, f)
	case *ast.IndexListExpr:
		return callObject(v.X, f)
	case *ast.ParenExpr:
		return callObject(v.X, f)
	}
	return nil
}
func (ix *index) applyCallRules(id string, f *function, call *ast.CallExpr, a assignments, config Config, matched map[string]bool) (bool, error) {
	if len(config.CallRules) == 0 {
		return false, nil
	}
	handledSQL := false
	objects := map[*types.Func]bool{}
	if obj := callObject(call.Fun, f); obj != nil {
		objects[obj.Origin()] = true
	}
	callable := flowValue{}
	if ix.flow != nil {
		callable = ix.flow.callTargets[call.Lparen]
		ix.flow.merge(&callable, ix.flow.positions[call.Fun.Pos()])
		for candidate := range callable.functions {
			if candidate != nil {
				if obj, ok := candidate.Object().(*types.Func); ok && obj.Pkg() != nil {
					objects[obj.Origin()] = true
				}
			}
		}
	}
	if len(objects) == 0 {
		return false, nil
	}
	for _, r := range config.CallRules {
		var matchedObject *types.Func
		matchedTargets := map[*ssa.Function]bool{}
		for obj := range objects {
			if obj.Pkg() != nil && r.Symbol == obj.Pkg().Path()+"::"+functionName(obj) {
				matchedObject = obj
				break
			}
		}
		if matchedObject == nil {
			continue
		}
		for _, target := range sortedFunctions(callable.functions) {
			if obj, ok := target.Object().(*types.Func); ok && obj.Pkg() != nil && r.Symbol == obj.Pkg().Path()+"::"+functionName(obj.Origin()) {
				matchedTargets[target] = true
			}
		}
		matched[r.Symbol] = true
		if r.Kind == "sql_query" {
			handledSQL = true
		}
		argumentOffset := callRuleReceiverOffset(call, f, matchedObject)
		argumentIndex, validArgument := callRuleArgumentIndex(r.Argument, argumentOffset, len(call.Args))
		if !validArgument {
			ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "invalid_rule_site", Reason: "configured argument does not exist for " + r.Symbol, Evidence: ix.evidence(call.Pos())})
			continue
		}
		namespace := r.Namespace
		namespaces := []string{namespace}
		if r.Kind == "sql_query" {
			var err error
			receivers, unknown := ix.callRuleSQLReceivers(f, call, matchedObject, callable, matchedTargets, argumentOffset)
			namespaces, err = ix.receiverNamespaces(id, ix.evidence(call.Pos()), receivers, unknown, r.Namespace, config)
			if err != nil {
				return handledSQL, err
			}
		}
		values, complete := ix.stringCandidates(call.Args[argumentIndex], f, a)
		if !complete {
			ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "dynamic_api_value", Reason: "configured API value uses flow candidates; additional runtime values may exist", Evidence: ix.evidence(call.Args[argumentIndex].Pos())})
		}
		for _, value := range values {
			if strings.Contains(value, unknown) {
				continue
			}
			if r.Kind == "sql_query" {
				for _, found := range ix.sqlAccesses(id, value, ix.evidence(call.Pos())) {
					access := found.access
					for _, namespace := range namespaces {
						ix.sqlTableAccess(id, resourceID("table", namespace, access.table), access.table, access.role, call)
					}
				}
				continue
			}
			key := resourceID("event", r.Namespace, value)
			ix.resource(id, key, value, "event", r.Kind, call.Pos())
			if r.HandlerArgument != nil {
				handlerIndex, validHandler := callRuleArgumentIndex(*r.HandlerArgument, argumentOffset, len(call.Args))
				if !validHandler {
					ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "invalid_rule_site", Reason: "configured handler argument does not exist for " + r.Symbol, Evidence: ix.evidence(call.Pos())})
					continue
				}
				expression := call.Args[handlerIndex]
				targets := map[string]bool{}
				if handler := callObject(expression, f); handler != nil {
					if target := ix.objects[handler.Origin()]; target != "" {
						targets[target] = true
					}
				}
				unknownHandler, nilHandler, outsideHandler := false, false, false
				if ix.flow != nil {
					flow := ix.flow.positions[expression.Pos()]
					unknownHandler = flow.functionUnknown || flow.functionNil || flow.interfaceUnknown
					nilHandler = flow.functionNil
					outsideHandler = flow.functionUnknown || flow.interfaceUnknown
					for fn := range flow.functions {
						if target := ix.owner(fn); target != "" {
							targets[target] = true
						}
					}
				}
				if len(targets) == 0 || unknownHandler {
					reason := "subscription handler target was not resolved"
					if len(targets) > 0 && unknownHandler {
						reason = "known subscription handler candidates coexist with an unresolved outside or nil handler alternative"
						if nilHandler && outsideHandler {
							reason = "known subscription handler candidates coexist with typed nil and outside handler alternatives; no successful handler invocation is inferred"
						} else if nilHandler {
							reason = "known subscription handler candidates coexist with a typed nil handler alternative; no successful handler invocation is inferred"
						}
					} else if nilHandler {
						reason = "subscription handler has a typed nil callback candidate; no successful handler invocation is inferred"
					}
					ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "unresolved_handler", Reason: reason, Evidence: ix.evidence(expression.Pos())})
				}
				for target := range targets {
					ix.edge(target, key, "event_handler", "possible", expression.Pos())
				}
			}
		}
	}
	return handledSQL, nil
}

func callRuleArgumentIndex(selector, receiverOffset, argumentCount int) (int, bool) {
	if selector < 0 || receiverOffset < 0 || receiverOffset > argumentCount || selector >= argumentCount-receiverOffset {
		return 0, false
	}
	return selector + receiverOffset, true
}

// A method expression includes its receiver in the function value signature and
// invocation arguments. Method values and SSA bound-method wrappers do not.
func callRuleReceiverOffset(call *ast.CallExpr, f *function, method *types.Func) int {
	if method == nil || method.Type() == nil {
		return 0
	}
	methodSig, ok := method.Type().(*types.Signature)
	if !ok || methodSig.Recv() == nil {
		return 0
	}
	callType := f.pkg.TypesInfo.TypeOf(call.Fun)
	if callType == nil {
		return 0
	}
	callSig, ok := callType.Underlying().(*types.Signature)
	if !ok {
		return 0
	}
	if callSig.Params().Len() == methodSig.Params().Len()+1 {
		return 1
	}
	return 0
}

func (ix *index) callRuleSQLReceivers(f *function, call *ast.CallExpr, method *types.Func, callable flowValue, targets map[*ssa.Function]bool, offset int) ([]string, bool) {
	if ix.flow == nil {
		return nil, true
	}
	combined := emptyFlow()
	if modeled := ix.flow.sqlReceivers[call.Lparen]; len(modeled.addresses) != 0 || modeled.sqlUnknown {
		ix.flow.merge(&combined, modeled)
	}
	unknown := callable.functionUnknown || callable.functionNil || callable.interfaceUnknown || callable.boundReceiverUnknown
	for _, target := range sortedFunctions(callable.functions) {
		if !targets[target] {
			continue
		}
		if receiver, ok := callable.boundReceivers[target]; ok {
			for _, address := range sortedKeys(receiver.addresses) {
				if len(combined.addresses) >= maxFlowValues && !combined.addresses[address] {
					unknown = true
					ix.flow.coverage.Widened = true
					continue
				}
				combined.addresses[address] = true
			}
			unknown = unknown || receiver.unknown
		}
	}
	if offset > 0 && offset-1 < len(call.Args) {
		receiverExpr := call.Args[offset-1]
		receiver := ix.flow.positions[receiverExpr.Pos()]
		receiverType := f.pkg.TypesInfo.TypeOf(receiverExpr)
		projected, _, status := ix.flow.projectMethodExpressionReceiver(receiver, receiverType, method)
		ix.flow.merge(&combined, projected)
		if status == methodReceiverProjectionUnsupported {
			// A method-expression receiver must be evaluated through its selected
			// method-set path. Keep this unresolved when metadata cannot establish
			// that path; treating the outer wrapper as the database would invent a
			// namespace candidate.
			unknown = true
		}
	}
	unknown = unknown || combined.sqlUnknown || combined.interfaceUnknown || combined.boundReceiverUnknown || len(combined.addresses) == 0
	return sortedKeys(combined.addresses), unknown
}
