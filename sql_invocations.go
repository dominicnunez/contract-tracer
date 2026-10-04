package contracttrace

import (
	"fmt"
	"go/types"
	"sort"

	"golang.org/x/tools/go/ssa"
)

// sqlInvocation is the normalized call-site view shared by SQL handle
// construction, operations and cleanup. arguments contains only the declared
// API arguments; the method receiver is carried separately.
type sqlInvocation struct {
	target          *ssa.Function
	method          *types.Func
	receiverKind    string
	name            string
	receiver        flowValue
	arguments       []ssa.Value
	unknownReceiver bool
	apiFunction     bool
}

func isSQLHandleMethodTarget(target *ssa.Function) (*types.Func, bool) {
	if target == nil {
		return nil, false
	}
	method, ok := target.Object().(*types.Func)
	if !ok || method.Pkg() == nil || method.Pkg().Path() != "database/sql" {
		return nil, false
	}
	signature, ok := method.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return nil, false
	}
	switch sqlReceiver(signature.Recv().Type()) {
	case "DB", "Conn", "Stmt", "Tx":
		return method.Origin(), true
	default:
		return nil, false
	}
}

func sqlInvocationSymbol(invocation sqlInvocation) string {
	if invocation.method == nil || invocation.method.Pkg() == nil {
		return ""
	}
	return invocation.method.Pkg().Path() + "::" + functionName(invocation.method)
}

// sqlInvocations recognizes the declared database/sql API behind direct calls,
// method-expression thunks and target-keyed bound-method closures. Receiver
// candidates stay attached to their actual SSA target, so wrappers with the
// same declared method do not share arbitrary FreeVars.
func (a *flowAnalysis) sqlInvocations(common *ssa.CallCommon) ([]sqlInvocation, bool) {
	callable := a.get(common.Value)
	unknown := callable.functionUnknown || callable.functionNil || callable.interfaceUnknown || callable.boundReceiverUnknown
	if target := common.StaticCallee(); target != nil {
		if object, ok := target.Object().(*types.Func); ok && (object.Pkg() == nil || object.Pkg().Path() != "database/sql") {
			return nil, false
		}
	}
	var result []sqlInvocation
	for _, target := range sortedFunctions(a.targets(common)) {
		if target.Signature == nil {
			unknown = true
			continue
		}
		object, ok := target.Object().(*types.Func)
		if !ok || object.Pkg() == nil || object.Pkg().Path() != "database/sql" {
			continue
		}
		apiSignature, ok := object.Type().(*types.Signature)
		if !ok {
			continue
		}
		if apiSignature.Recv() == nil {
			if object.Name() != "Open" && object.Name() != "OpenDB" {
				continue
			}
			arguments := append([]ssa.Value(nil), common.Args...)
			if !sqlArgumentsFit(apiSignature, arguments) {
				unknown = true
			}
			result = append(result, sqlInvocation{target: target, method: object.Origin(), name: object.Name(),
				arguments: arguments, unknownReceiver: unknown, apiFunction: true})
			continue
		}

		receiverKind := sqlReceiver(apiSignature.Recv().Type())
		if receiverKind != "DB" && receiverKind != "Conn" && receiverKind != "Stmt" && receiverKind != "Tx" {
			continue
		}
		invocation := sqlInvocation{target: target, method: object.Origin(), receiverKind: receiverKind, name: object.Name(), unknownReceiver: unknown}
		argumentStart := -1
		switch {
		case common.IsInvoke():
			// SSA interface invokes store the receiver in Value and only explicit
			// API arguments in Args. Concrete API targets remain possible; outside
			// dispatch is preserved separately through interfaceUnknown.
			receiverType := apiSignature.Recv().Type()
			if target.Signature.Recv() != nil {
				receiverType = target.Signature.Recv().Type()
			}
			invocation.receiver, invocation.unknownReceiver = a.projectSQLReceiver(a.get(common.Value), receiverType, object.Origin(), invocation.unknownReceiver)
			argumentStart = 0
		case target.Signature.Recv() != nil:
			// A normal direct method call has the receiver as SSA argument zero.
			if len(common.Args) == 0 {
				invocation.unknownReceiver = true
				break
			}
			invocation.receiver, invocation.unknownReceiver = a.projectSQLReceiver(a.get(common.Args[0]), common.Args[0].Type(), object.Origin(), invocation.unknownReceiver)
			argumentStart = 1
		case target.Signature.Params().Len() == apiSignature.Params().Len()+1:
			// A generated method-expression thunk has an explicit receiver
			// parameter followed by the declared API parameters.
			if len(common.Args) == 0 {
				invocation.unknownReceiver = true
				break
			}
			invocation.receiver, invocation.unknownReceiver = a.projectSQLReceiver(a.get(common.Args[0]), common.Args[0].Type(), object.Origin(), invocation.unknownReceiver)
			argumentStart = 1
		case target.Signature.Params().Len() == apiSignature.Params().Len():
			// A bound method wrapper omits its receiver from invocation arguments.
			argumentStart = 0
			if captured, exists := callable.boundReceivers[target]; exists {
				invocation.receiver.addresses = snapshotCandidates(captured.addresses)
				invocation.receiver.sqlUnknown = captured.unknown
				invocation.unknownReceiver = invocation.unknownReceiver || captured.unknown || len(captured.addresses) == 0
			} else {
				invocation.unknownReceiver = true
			}
		default:
			invocation.unknownReceiver = true
		}
		if argumentStart >= 0 && argumentStart <= len(common.Args) {
			invocation.arguments = append([]ssa.Value(nil), common.Args[argumentStart:]...)
			if !sqlArgumentsFit(apiSignature, invocation.arguments) {
				invocation.unknownReceiver = true
			}
		}
		result = append(result, invocation)
	}
	if common.IsInvoke() && common.Method != nil {
		inferred, hasUnknown := a.sqlInterfaceInvocations(common, callable)
		result = append(result, inferred...)
		unknown = unknown || hasUnknown
	}
	return result, unknown
}

// A CHA graph may omit implementations supplied by a dependency when an
// interface is declared locally. Recover only candidates whose actual receiver
// already has a modeled database/sql handle origin and whose method-set
// signature exactly implements the invoked interface method.
func (a *flowAnalysis) sqlInterfaceInvocations(common *ssa.CallCommon, callable flowValue) ([]sqlInvocation, bool) {
	interfaceMethod := common.Method
	interfaceSignature, ok := interfaceMethod.Type().(*types.Signature)
	if !ok || interfaceSignature.Recv() == nil {
		return nil, false
	}
	interfaceType, ok := types.Unalias(interfaceSignature.Recv().Type()).Underlying().(*types.Interface)
	if !ok {
		return nil, false
	}
	interfaceType.Complete()
	receiver := a.get(common.Value)
	unknown := callable.interfaceUnknown || receiver.interfaceUnknown || receiver.sqlUnknown || receiver.boundReceiverUnknown
	var result []sqlInvocation
	for _, address := range sortedKeys(receiver.addresses) {
		site, exists := a.sqlHandles[address]
		if !exists || site.typ == nil || !types.Implements(site.typ, interfaceType) {
			continue
		}
		selection := types.NewMethodSet(site.typ).Lookup(nil, interfaceMethod.Name())
		if selection == nil {
			continue
		}
		method, ok := selection.Obj().(*types.Func)
		if !ok || method.Pkg() == nil || method.Pkg().Path() != "database/sql" {
			continue
		}
		apiSignature, ok := method.Type().(*types.Signature)
		if !ok || apiSignature.Recv() == nil || !sqlMethodSignaturesMatch(apiSignature, interfaceSignature) {
			continue
		}
		receiverKind := sqlReceiver(apiSignature.Recv().Type())
		if receiverKind != "DB" && receiverKind != "Conn" && receiverKind != "Stmt" && receiverKind != "Tx" {
			continue
		}
		projected, projectionUnknown := a.projectSQLReceiver(flowValue{addresses: map[string]bool{address: true}}, site.typ, method.Origin(), false)
		arguments := append([]ssa.Value(nil), common.Args...)
		result = append(result, sqlInvocation{method: method.Origin(), receiverKind: receiverKind, name: method.Name(), receiver: projected,
			arguments: arguments, unknownReceiver: unknown || projectionUnknown || !sqlArgumentsFit(apiSignature, arguments)})
	}
	return result, unknown
}

func sqlMethodSignaturesMatch(api, invoked *types.Signature) bool {
	if api.Variadic() != invoked.Variadic() || api.Params().Len() != invoked.Params().Len() || api.Results().Len() != invoked.Results().Len() {
		return false
	}
	for i := 0; i < api.Params().Len(); i++ {
		if !types.Identical(api.Params().At(i).Type(), invoked.Params().At(i).Type()) {
			return false
		}
	}
	for i := 0; i < api.Results().Len(); i++ {
		if !types.Identical(api.Results().At(i).Type(), invoked.Results().At(i).Type()) {
			return false
		}
	}
	return true
}

func (a *flowAnalysis) projectSQLReceiver(receiver flowValue, receiverType types.Type, method *types.Func, unknown bool) (flowValue, bool) {
	projected, _, status := a.projectMethodExpressionReceiver(receiver, receiverType, method)
	if status == methodReceiverProjectionUnsupported {
		// An unrecognized wrapper/type shape must not be substituted with the
		// outer value as if it were the database/sql receiver.
		unknown = true
	}
	unknown = unknown || projected.sqlUnknown || projected.interfaceUnknown || projected.boundReceiverUnknown
	return projected, unknown
}

func sqlArgumentsFit(signature *types.Signature, arguments []ssa.Value) bool {
	if signature == nil {
		return false
	}
	parameters := signature.Params()
	fixed := parameters.Len()
	if signature.Variadic() {
		if fixed == 0 {
			return false
		}
		fixed--
	} else if len(arguments) != fixed {
		return false
	}
	if len(arguments) < fixed {
		return false
	}
	for i, argument := range arguments {
		if argument == nil {
			return false
		}
		var formal types.Type
		switch {
		case i < fixed:
			formal = parameters.At(i).Type()
		case !signature.Variadic():
			return false
		case i == len(arguments)-1 && types.AssignableTo(argument.Type(), parameters.At(parameters.Len()-1).Type()):
			// A source-level ... call passes the variadic slice as one argument.
			continue
		default:
			variadic, ok := types.Unalias(parameters.At(parameters.Len() - 1).Type()).Underlying().(*types.Slice)
			if !ok {
				return false
			}
			formal = variadic.Elem()
		}
		if !types.AssignableTo(argument.Type(), formal) {
			return false
		}
	}
	return true
}

func sortedSSAValues(values map[ssa.Value]bool) []ssa.Value {
	result := make([]ssa.Value, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	key := func(value ssa.Value) string {
		parent := ""
		if function := value.Parent(); function != nil {
			parent = function.String()
		}
		return fmt.Sprintf("%s\x00%010d\x00%s\x00%s", parent, value.Pos(), value.Type(), value.String())
	}
	sort.Slice(result, func(i, j int) bool { return key(result[i]) < key(result[j]) })
	return result
}
