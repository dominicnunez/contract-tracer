package contracttrace

import (
	"context"
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// A bounded, monotone, context-insensitive value-flow model. Results are
// candidates: unsupported memory/dispatch and external callers remain boundaries.
const maxFlowValues = 64

type flowValue struct {
	strings              map[string]bool
	functions            map[*ssa.Function]bool
	boundReceivers       map[*ssa.Function]boundReceiverCandidates
	boundReceiverUnknown bool
	addresses            map[string]bool
	effects              map[string]bool
	errors               map[string]bool
	scalars              map[string]bool
	scalarUnknown        bool
	sqlUnknown           bool
	functionUnknown      bool
	functionNil          bool
	zeroAggregates       map[types.Type]bool
	zeroAggregateUnknown bool
	interfaceUnknown     bool
	stringUnknown        bool
}

type boundReceiverCandidates struct {
	addresses map[string]bool
	unknown   bool
}

func emptyFlow() flowValue {
	return flowValue{strings: map[string]bool{}, functions: map[*ssa.Function]bool{}, addresses: map[string]bool{}, effects: map[string]bool{}}
}

func captureBoundMethodReceiver(result *flowValue, target *ssa.Function, receiver flowValue, enabled bool) {
	if !enabled || target == nil || result == nil {
		return
	}
	method, ok := target.Object().(*types.Func)
	if !ok {
		return
	}
	signature, ok := method.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return
	}
	result.boundReceivers = map[*ssa.Function]boundReceiverCandidates{
		target: {addresses: snapshotCandidates(receiver.addresses), unknown: receiver.sqlUnknown || receiver.boundReceiverUnknown || receiver.interfaceUnknown},
	}
}

type flowAnalysis struct {
	values                    map[ssa.Value]flowValue
	memory                    map[string]flowValue
	addressTypes              map[string]types.Type
	allocations               map[string]*ssa.Alloc
	zeroFunctionFields        map[string]bool
	zeroFunctionFieldCache    map[string]uint8
	zeroFunctionElements      map[string]bool
	knownFunctionElements     map[string]bool
	zeroFunctionArrayElements map[string]bool
	unknownFunctionElements   map[string]bool
	zeroFunctionArrayCache    map[string]uint8
	globalFunctionStates      map[string]globalFunctionState
	channelClosures           *channelClosureIndex
	aliasVersions             map[string]uint64
	recordCache               map[ssa.Value]recordAddressCache
	mapEntries                map[string]map[string]bool
	mapClosed                 map[string]bool
	mapOpen                   map[string]bool
	returns                   map[*ssa.Function][]flowValue
	positions                 map[token.Pos]flowValue
	callTargets               map[token.Pos]flowValue
	coverage                  FlowCoverage
	special                   map[*ssa.Call][]flowValue
	contexts                  map[*ssa.Call]contextSite
	contextKeys               map[string]contextSite
	doneChannels              map[string]string
	invokes                   map[*ssa.CallCommon]map[*ssa.Function]bool
	sqlHandles                map[string]sqlHandle
	statementExecutions       map[token.Pos]flowValue
	sqlOperations             map[token.Pos]map[string]bool
	sqlReceivers              map[token.Pos]flowValue
	callbackEscapes           map[ssa.CallInstruction]map[*ssa.Function]bool
	callbackReturns           map[*ssa.Return]map[*ssa.Function]bool
	publicGlobals             []*ssa.Global
	localGlobals              []*ssa.Global
	globalAddressOwners       map[string]map[string]bool
	globalCallbacks           map[*ssa.Global]map[*ssa.Function]bool
	globalSQLFields           map[*ssa.Global]bool
	iterators                 map[string]iteratorSummary
	inputRoots                map[string]bool
	interfaceAssertions       map[ssa.Value]bool
	memoryVersions            map[string]uint64
	callbackCache             map[ssa.Value]*callbackStorageCache
	afterFuncs                map[string]afterFuncSite
	afterFuncSchedulers       map[string]*ssa.Function
	afterFuncTypeCache        map[types.Type]string
	scalarControls            map[*ssa.BasicBlock][]*ssa.If
	scalarControlCache        map[*ssa.BasicBlock]flowValue
	lifecycleMatched          map[string]bool
	lifecycleTypes            []types.Type
}

func (a *flowAnalysis) addGlobalFunctionState(result *flowValue, address string, ix *index) {
	if result == nil || a == nil || !strings.HasPrefix(address, "global:") {
		return
	}
	if a.globalFunctionStates == nil {
		a.globalFunctionStates = map[string]globalFunctionState{}
	}
	state, cached := a.globalFunctionStates[address]
	if !cached {
		state = a.globalFunctionStateAtAddress(address, ix)
		a.globalFunctionStates[address] = state
	}
	switch state {
	case globalFunctionZero:
		result.functionNil = true
	case globalFunctionUnknown:
		result.functionUnknown = true
	}
}

func aggregateSequenceStorageType(sequenceType types.Type, address string, addressTypes map[string]types.Type) types.Type {
	if sequenceType == nil {
		return nil
	}
	sequenceType = types.Unalias(sequenceType)
	if pointer, ok := sequenceType.Underlying().(*types.Pointer); ok {
		sequenceType = types.Unalias(pointer.Elem())
	}
	if _, ok := sequenceType.Underlying().(*types.Array); ok {
		return sequenceType
	}
	if _, ok := sequenceType.Underlying().(*types.Slice); !ok {
		return nil
	}
	addressType := addressTypes[address]
	if addressType == nil {
		return nil
	}
	storage := types.Unalias(addressType)
	pointer, ok := storage.Underlying().(*types.Pointer)
	if !ok {
		return nil
	}
	if _, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array); !ok {
		return nil
	}
	return pointer.Elem()
}

func aggregateSelectedElement(a *flowAnalysis, value flowValue, sequenceType types.Type, address string) flowValue {
	result := emptyFlow()
	if a == nil {
		return result
	}
	storageType := aggregateSequenceStorageType(sequenceType, address, a.addressTypes)
	if storageType == nil {
		return result
	}
	a.merge(&result, a.zeroAggregateIndex(value, storageType))
	return result
}

func (a *flowAnalysis) merge(dst *flowValue, src flowValue) bool {
	changed := false
	if dst.strings == nil {
		unknown := dst.sqlUnknown
		unknownFunction := dst.functionUnknown
		nilFunction := dst.functionNil
		unknownInterface := dst.interfaceUnknown
		unknownString := dst.stringUnknown
		errorOrigins := dst.errors
		scalarOrigins, unknownScalar := dst.scalars, dst.scalarUnknown
		boundReceivers, unknownBoundReceiver := dst.boundReceivers, dst.boundReceiverUnknown
		zeroAggregates, unknownZeroAggregate := dst.zeroAggregates, dst.zeroAggregateUnknown
		*dst = emptyFlow()
		dst.sqlUnknown = unknown
		dst.functionUnknown = unknownFunction
		dst.functionNil = nilFunction
		dst.interfaceUnknown = unknownInterface
		dst.stringUnknown = unknownString
		dst.errors = errorOrigins
		dst.scalars, dst.scalarUnknown = scalarOrigins, unknownScalar
		dst.boundReceivers, dst.boundReceiverUnknown = boundReceivers, unknownBoundReceiver
		dst.zeroAggregates, dst.zeroAggregateUnknown = zeroAggregates, unknownZeroAggregate
	}
	if src.scalarUnknown && !dst.scalarUnknown {
		dst.scalarUnknown = true
		changed = true
	}
	for _, origin := range mergeKeys(src.scalars, dst.scalars) {
		if dst.scalars[origin] {
			continue
		}
		if len(dst.scalars) >= maxFlowValues {
			a.coverage.Widened = true
			if !dst.scalarUnknown {
				dst.scalarUnknown = true
				changed = true
			}
			continue
		}
		if dst.scalars == nil {
			dst.scalars = map[string]bool{}
		}
		dst.scalars[origin] = true
		changed = true
	}
	if src.sqlUnknown && !dst.sqlUnknown {
		dst.sqlUnknown = true
		changed = true
	}
	if src.functionUnknown && !dst.functionUnknown {
		dst.functionUnknown = true
		changed = true
	}
	if src.functionNil && !dst.functionNil {
		dst.functionNil = true
		changed = true
	}
	if src.zeroAggregateUnknown && !dst.zeroAggregateUnknown {
		dst.zeroAggregateUnknown = true
		changed = true
	}
	zeroTypes := make([]types.Type, 0, len(src.zeroAggregates))
	for typ := range src.zeroAggregates {
		zeroTypes = append(zeroTypes, typ)
	}
	sort.Slice(zeroTypes, func(i, j int) bool { return types.TypeString(zeroTypes[i], nil) < types.TypeString(zeroTypes[j], nil) })
	for _, typ := range zeroTypes {
		if dst.zeroAggregates[typ] {
			continue
		}
		if len(dst.zeroAggregates) >= maxFlowValues {
			a.coverage.Widened = true
			if !dst.zeroAggregateUnknown {
				dst.zeroAggregateUnknown = true
				changed = true
			}
			continue
		}
		if dst.zeroAggregates == nil {
			dst.zeroAggregates = map[types.Type]bool{}
		}
		dst.zeroAggregates[typ] = true
		changed = true
	}
	if src.interfaceUnknown && !dst.interfaceUnknown {
		dst.interfaceUnknown = true
		changed = true
	}
	if src.stringUnknown && !dst.stringUnknown {
		dst.stringUnknown = true
		changed = true
	}
	for _, origin := range mergeKeys(src.errors, dst.errors) {
		if !dst.errors[origin] {
			if len(dst.errors) >= maxFlowValues {
				a.coverage.Widened = true
				continue
			}
			if dst.errors == nil {
				dst.errors = map[string]bool{}
			}
			dst.errors[origin] = true
			changed = true
		}
	}
	for _, s := range mergeKeys(src.strings, dst.strings) {
		if !dst.strings[s] {
			if len(dst.strings) >= maxFlowValues {
				a.coverage.Widened = true
				if !dst.stringUnknown {
					dst.stringUnknown = true
					changed = true
				}
				continue
			}
			dst.strings[s] = true
			changed = true
		}
	}
	for _, f := range mergeFunctions(src.functions, dst.functions) {
		if !dst.functions[f] {
			if len(dst.functions) >= maxFlowValues {
				a.coverage.Widened = true
				continue
			}
			dst.functions[f] = true
			changed = true
		}
	}
	if len(src.boundReceivers) > 0 || src.boundReceiverUnknown {
		boundFunctionSet := make(map[*ssa.Function]bool, len(src.boundReceivers))
		for fn := range src.boundReceivers {
			boundFunctionSet[fn] = true
		}
		boundFunctions := sortedFunctions(boundFunctionSet)
		if src.boundReceiverUnknown && !dst.boundReceiverUnknown {
			dst.boundReceiverUnknown = true
			changed = true
		}
		for _, fn := range boundFunctions {
			receivers := src.boundReceivers[fn]
			current, exists := dst.boundReceivers[fn]
			if !exists && len(dst.boundReceivers) >= maxFlowValues {
				a.coverage.Widened = true
				if !dst.boundReceiverUnknown {
					dst.boundReceiverUnknown = true
					changed = true
				}
				continue
			}
			localChanged := false
			if current.addresses == nil {
				current.addresses = map[string]bool{}
			}
			for _, address := range sortedKeys(receivers.addresses) {
				if current.addresses[address] {
					continue
				}
				if len(current.addresses) >= maxFlowValues {
					a.coverage.Widened = true
					if !current.unknown {
						current.unknown = true
						localChanged = true
					}
					continue
				}
				current.addresses[address] = true
				localChanged = true
			}
			if receivers.unknown && !current.unknown {
				current.unknown = true
				localChanged = true
			}
			if dst.boundReceivers == nil {
				dst.boundReceivers = map[*ssa.Function]boundReceiverCandidates{}
			}
			if !exists || localChanged {
				dst.boundReceivers[fn] = current
				changed = changed || !exists || localChanged
			}
		}
	}
	for _, p := range mergeKeys(src.addresses, dst.addresses) {
		if !dst.addresses[p] {
			if len(dst.addresses) >= maxFlowValues {
				a.coverage.Widened = true
				continue
			}
			dst.addresses[p] = true
			changed = true
		}
	}
	for _, effect := range mergeKeys(src.effects, dst.effects) {
		if !dst.effects[effect] {
			if len(dst.effects) >= maxFlowValues {
				a.coverage.Widened = true
				continue
			}
			dst.effects[effect] = true
			changed = true
		}
	}
	return changed
}

func (a *flowAnalysis) get(v ssa.Value) flowValue {
	if v != nil {
		switch v.(type) {
		case *ssa.Const, *ssa.Function, *ssa.Global:
			// These values add intrinsic candidates before bounded merging.
		default:
			stored := a.values[v]
			if len(stored.strings) <= maxFlowValues && len(stored.functions) <= maxFlowValues && len(stored.addresses) <= maxFlowValues && len(stored.boundReceivers) <= maxFlowValues && len(stored.effects) <= maxFlowValues && len(stored.errors) <= maxFlowValues && len(stored.scalars) <= maxFlowValues && len(stored.zeroAggregates) <= maxFlowValues {
				return snapshotFlow(stored)
			}
		}
	}
	result := emptyFlow()
	if v == nil {
		return result
	}
	switch x := v.(type) {
	case *ssa.Const:
		if x.IsNil() && isSQLHandleType(x.Type()) {
			result.sqlUnknown = true
		}
		if x.IsNil() && isFunctionType(x.Type()) {
			result.functionNil = true
		}
		if x.Value != nil && x.Value.Kind() == constant.String {
			result.strings[constant.StringVal(x.Value)] = true
		}
	case *ssa.Function:
		result.functions[x] = true
	case *ssa.Global:
		result.addresses["global:"+x.String()] = true
	}
	a.merge(&result, a.values[v])
	return result
}
func (a *flowAnalysis) put(v ssa.Value, f flowValue) bool {
	old := a.values[v]
	changed := a.merge(&old, f)
	a.values[v] = old
	return changed
}
func (ix *index) registerInitializers(prog *ssa.Program, pkgs []*packages.Package) {
	ix.ssaOwners = map[*ssa.Function]string{}
	for _, p := range pkgs {
		sp := prog.Package(p.Types)
		if sp == nil || len(p.Syntax) == 0 {
			continue
		}
		fn, ok := sp.Members["init"].(*ssa.Function)
		if !ok {
			continue
		}
		evidence := ix.evidence(p.Syntax[0].Pos())
		id := p.PkgPath + "::(package init)"
		ix.ssaOwners[fn] = id
		ix.funcs[id] = &function{node: Node{ID: id, Name: "(package init)", Kind: "initializer", Evidence: evidence}, pkg: p}
	}
}
func (ix *index) analyzeFlow(ctx context.Context, prog *ssa.Program, graph *callgraph.Graph, config Config) (*flowAnalysis, error) {
	a := &flowAnalysis{values: map[ssa.Value]flowValue{}, memory: map[string]flowValue{}, addressTypes: map[string]types.Type{}, returns: map[*ssa.Function][]flowValue{}, positions: map[token.Pos]flowValue{}, mapOpen: map[string]bool{}, coverage: FlowCoverage{MaxValues: maxFlowValues}}
	if len(config.CallRules) > 0 {
		a.callTargets = map[token.Pos]flowValue{}
	}
	a.invokes = map[*ssa.CallCommon]map[*ssa.Function]bool{}
	for fn, node := range graph.Nodes {
		if ix.owner(fn) == "" {
			continue
		}
		for _, edge := range node.Out {
			if edge.Site == nil || !edge.Site.Common().IsInvoke() || ix.owner(edge.Callee.Func) == "" {
				continue
			}
			common := edge.Site.Common()
			if a.invokes[common] == nil {
				a.invokes[common] = map[*ssa.Function]bool{}
			}
			a.invokes[common][edge.Callee.Func] = true
		}
	}
	for _, p := range prog.AllPackages() {
		for _, member := range p.Members {
			if global, ok := member.(*ssa.Global); ok {
				a.addressTypes["global:"+global.String()] = global.Type()
				if variable, ok := global.Object().(*types.Var); ok {
					if _, inside := relative(ix.root, ix.fset.Position(global.Pos()).Filename); inside {
						a.localGlobals = append(a.localGlobals, global)
					}
					if _, inside := relative(ix.root, ix.fset.Position(global.Pos()).Filename); inside && variable.Exported() {
						a.publicGlobals = append(a.publicGlobals, global)
						if sqlHandleGlobal(global) {
							unknown := emptyFlow()
							unknown.sqlUnknown = true
							a.store("global:"+global.String(), unknown)
						}
					}
					if text, present := ix.embedded[variable]; present {
						value := emptyFlow()
						value.strings[text] = true
						a.memory["global:"+global.String()] = value
					}
				}
			}
		}
	}
	sort.Slice(a.publicGlobals, func(i, j int) bool { return a.publicGlobals[i].String() < a.publicGlobals[j].String() })
	funcs := []*ssa.Function{}
	for fn := range ssautil.AllFunctions(prog) {
		a.collectLifecycleTypes(fn, config)
		if ix.owner(fn) != "" && len(fn.Blocks) > 0 {
			funcs = append(funcs, fn)
		}
	}
	sort.Slice(funcs, func(i, j int) bool { return funcs[i].String() < funcs[j].String() })
	a.channelClosures = newChannelClosureIndex(funcs)
	a.seedErrorResults(funcs, ix)
	a.seedScalarParameters(funcs, ix)
	a.seedScalarBuiltinResults(funcs, ix)
	a.seedScalarControl(funcs, ix)
	a.afterFuncSchedulers = map[string]*ssa.Function{}
	for _, fn := range funcs {
		if cancellationSchedulerSignature(fn) {
			id := ix.owner(fn)
			if previous := a.afterFuncSchedulers[id]; previous == nil || previous.Synthetic != "" && fn.Synthetic == "" {
				a.afterFuncSchedulers[id] = fn
			}
		}
	}
	a.seedSQLInputs(funcs, ix, false)
	a.seedFunctionInputs(funcs)
	a.seedErrorInputs(funcs, ix, false)
	for _, function := range funcs {
		if function.Object() != nil && function.Object().Exported() {
			for _, parameter := range function.Params {
				a.seedAggregateInput(parameter, ix)
			}
		}
	}
	uncalledInputsSeeded := false
	for round := 0; round < 100; round++ {
		// Control predicates can acquire origins during this round. Recompute
		// summaries next round, while sharing them across instructions here.
		a.scalarControlCache = map[*ssa.BasicBlock]flowValue{}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		changed := false
		for _, fn := range funcs {
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					result := emptyFlow()
					switch v := instruction.(type) {
					case *ssa.MakeMap:
						address := ix.allocationID("map", fn, v)
						result.addresses[address] = true
						a.addressTypes[address] = v.Type()
						if a.mapClosed == nil {
							a.mapClosed = map[string]bool{}
						}
						a.mapClosed[address] = true
					case *ssa.MakeSlice:
						address := ix.allocationID("slice", fn, v)
						result.addresses[address] = true
						a.addressTypes[address] = v.Type()
						if slice, ok := types.Unalias(v.Type()).Underlying().(*types.Slice); ok && isFunctionType(slice.Elem()) {
							elementAddress := address + ".element"
							state := makeSliceFunctionElements(fn, v, ix)
							cacheSequenceElement(a, elementAddress, state)
							if state == functionElementUnsupported {
								if a.unknownFunctionElements == nil {
									a.unknownFunctionElements = map[string]bool{}
								}
								a.unknownFunctionElements[elementAddress] = true
							}
						} else if slice, ok := types.Unalias(v.Type()).Underlying().(*types.Slice); ok && inlineAggregate(slice.Elem()) {
							state := makeSliceFunctionElements(fn, v, ix)
							switch state {
							case functionElementMayBeZero:
								a.addZeroAggregate(&result, slice.Elem())
							case functionElementUnsupported:
								a.markZeroAggregateUnknown(&result)
							}
						}
					case *ssa.MakeChan:
						result.addresses[ix.channelID(v.Pos())] = true
					case *ssa.MapUpdate:
						stored := a.get(v.Value)
						if isScalarType(v.Value.Type()) {
							a.mergeScalar(&stored, a.scalarControlValue(block))
						}
						for _, p := range sortedKeys(a.get(v.Map).addresses) {
							if a.mapUpdateOpens(fn, v, ix) && a.openMap(p) {
								changed = true
							}
							if a.store(p+".keys", a.get(v.Key)) {
								changed = true
							}
							for _, key := range a.mapKeys(v.Key) {
								if a.storeMap(p, key, stored) {
									changed = true
								}
							}
						}
					case *ssa.Lookup:
						result = a.mapLookup(v.X, v.Index)
					case *ssa.Range:
						if _, ok := v.X.Type().Underlying().(*types.Map); ok {
							a.merge(&result, a.get(v.X))
						}
					case *ssa.Send:
						if a.send(v.Chan, v.X, block) {
							changed = true
						}
					case *ssa.Select:
						for _, state := range v.States {
							if state.Dir == types.SendOnly && a.send(state.Chan, state.Send, block) {
								changed = true
							}
						}
					case *ssa.Alloc:
						address := ix.allocationID("alloc", fn, v)
						result.addresses[address] = true
						a.addressTypes[address] = v.Type()
						if a.allocations == nil {
							a.allocations = map[string]*ssa.Alloc{}
						}
						a.allocations[address] = v
						if pointer, ok := v.Type().Underlying().(*types.Pointer); ok && inlineAggregate(pointer.Elem()) {
							if owner := ix.funcs[ix.owner(fn)]; owner != nil && owner.pkg != nil {
								initializer, found := allocationInitializer(fn, v, pointer.Elem(), owner.pkg, ix)
								if found && initializer.zero {
									a.addZeroAggregate(&result, pointer.Elem())
								} else if found && initializer.literal != nil {
									if array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array); ok && inlineAggregate(array.Elem()) && arrayLiteralHasOmittedElements(initializer.literal, array, owner.pkg) {
										a.addZeroAggregate(&result, pointer.Elem())
									}
								} else if state, recognized := sliceBackingAllocationState(fn, v, owner.pkg); recognized && state == functionElementMayBeZero {
									a.addZeroAggregate(&result, pointer.Elem())
								}
							}
						}
						if pointer, ok := v.Type().Underlying().(*types.Pointer); ok {
							if array, ok := pointer.Elem().Underlying().(*types.Array); ok && isFunctionType(array.Elem()) {
								elementAddress := address + ".element"
								state := a.zeroFunctionArrayAtAddress(address, ix)
								if state == functionElementUnsupported {
									if a.unknownFunctionElements == nil {
										a.unknownFunctionElements = map[string]bool{}
									}
									a.unknownFunctionElements[elementAddress] = true
								} else {
									cacheSequenceElement(a, elementAddress, state)
									if state == functionElementMayBeZero {
										if a.zeroFunctionArrayElements == nil {
											a.zeroFunctionArrayElements = map[string]bool{}
										}
										a.zeroFunctionArrayElements[elementAddress] = true
									}
								}
							}
						}
					case *ssa.Phi:
						for _, edge := range v.Edges {
							a.merge(&result, a.get(edge))
						}
					case *ssa.ChangeType:
						a.merge(&result, a.get(v.X))
						a.merge(&result, a.retypeZeroAggregate(a.get(v.X), v.X.Type(), v.Type()))
					case *ssa.Convert:
						a.merge(&result, a.get(v.X))
						a.merge(&result, a.retypeZeroAggregate(a.get(v.X), v.X.Type(), v.Type()))
					case *ssa.ChangeInterface:
						a.merge(&result, a.get(v.X))
					case *ssa.MakeInterface:
						a.merge(&result, a.get(v.X))
						a.modelWaitGroupInterface(v.X, &result)
						a.modelAfterFuncInterface(v.X, &result)
					case *ssa.TypeAssert:
						a.merge(&result, a.get(v.X))
						if !v.CommaOk && a.get(v.X).interfaceUnknown && a.seedInterfaceAssertion(v, v, ix) {
							changed = true
						}
					case *ssa.FieldAddr:
						parentType := types.Unalias(v.X.Type())
						if pointer, ok := parentType.Underlying().(*types.Pointer); ok {
							parentType = types.Unalias(pointer.Elem())
						}
						if inlineAggregate(parentType) {
							a.merge(&result, a.zeroAggregateField(a.get(v.X), parentType, v.Field))
						}
						for _, p := range a.recordAddresses(v.X) {
							if inlineAggregate(parentType) {
								a.merge(&result, a.zeroAggregateField(a.get(v.X), parentType, v.Field))
								a.merge(&result, a.zeroAggregateField(a.memory[p], parentType, v.Field))
							}
							address := fmt.Sprintf("%s.field:%d", p, v.Field)
							result.addresses[address] = true
							if pointer, ok := v.Type().Underlying().(*types.Pointer); ok && isFunctionType(pointer.Elem()) && a.zeroFunctionFieldAtAddress(address, ix) {
								if a.zeroFunctionFields == nil {
									a.zeroFunctionFields = map[string]bool{}
								}
								a.zeroFunctionFields[address] = true
							}
							if pointer, ok := v.Type().Underlying().(*types.Pointer); ok && isFunctionType(pointer.Elem()) {
								a.addGlobalFunctionState(&result, address, ix)
							}
						}
					case *ssa.IndexAddr:
						for _, p := range a.recordAddresses(v.X) {
							address := p + ".element"
							result.addresses[address] = true
							if pointer, ok := v.Type().Underlying().(*types.Pointer); ok && inlineAggregate(pointer.Elem()) {
								a.merge(&result, aggregateSelectedElement(a, a.get(v.X), v.X.Type(), p))
							}
							if pointer, ok := v.Type().Underlying().(*types.Pointer); ok && isFunctionType(pointer.Elem()) {
								a.merge(&result, a.functionElementValue(v.X, p, address, ix))
							}
						}
						if pointer, ok := v.Type().Underlying().(*types.Pointer); ok && inlineAggregate(pointer.Elem()) {
							sequence, isSlice := types.Unalias(v.X.Type()).Underlying().(*types.Slice)
							if isSlice {
								marker := a.get(v.X)
								if exactZeroAggregateMatches(marker, sequence.Elem()) {
									a.addZeroAggregate(&result, sequence.Elem())
								}
								if marker.zeroAggregateUnknown {
									a.markZeroAggregateUnknown(&result)
								}
							}
						}
					case *ssa.Index:
						for _, p := range a.recordAddresses(v.X) {
							address := p + ".element"
							a.merge(&result, a.memory[address])
							if isFunctionType(v.Type()) {
								a.merge(&result, a.functionElementValue(v.X, p, address, ix))
							}
							if inlineAggregate(v.Type()) {
								result.addresses[address] = true
								a.merge(&result, aggregateSelectedElement(a, a.get(v.X), v.X.Type(), p))
							}
						}
						if inlineAggregate(v.Type()) {
							if sequence, ok := types.Unalias(v.X.Type()).Underlying().(*types.Slice); ok {
								marker := a.get(v.X)
								if exactZeroAggregateMatches(marker, sequence.Elem()) {
									a.addZeroAggregate(&result, sequence.Elem())
								}
								if marker.zeroAggregateUnknown {
									a.markZeroAggregateUnknown(&result)
								}
							}
						}
					case *ssa.Slice:
						a.merge(&result, a.get(v.X))
					case *ssa.Field:
						parentType := types.Unalias(v.X.Type())
						if pointer, ok := parentType.Underlying().(*types.Pointer); ok {
							parentType = types.Unalias(pointer.Elem())
						}
						if inlineAggregate(parentType) {
							a.merge(&result, a.zeroAggregateField(a.get(v.X), parentType, v.Field))
						}
						for _, p := range a.recordAddresses(v.X) {
							address := fmt.Sprintf("%s.field:%d", p, v.Field)
							if inlineAggregate(parentType) {
								a.merge(&result, a.zeroAggregateField(a.get(v.X), parentType, v.Field))
								a.merge(&result, a.zeroAggregateField(a.memory[p], parentType, v.Field))
							}
							a.merge(&result, a.memory[address])
							if isFunctionType(v.Type()) && a.zeroFunctionFieldAtAddress(address, ix) {
								result.functionNil = true
							}
							if isFunctionType(v.Type()) {
								a.addGlobalFunctionState(&result, address, ix)
							}
							if inlineAggregate(v.Type()) {
								result.addresses[address] = true
							}
						}
					case *ssa.Store:
						stored := a.get(v.Val)
						if isScalarType(v.Val.Type()) {
							a.mergeScalar(&stored, a.scalarControlValue(block))
						}
						for _, p := range sortedKeys(a.get(v.Addr).addresses) {
							if a.store(p, stored) {
								changed = true
							}
						}
					case *ssa.UnOp:
						if v.Op == token.SUB || v.Op == token.NOT || v.Op == token.XOR {
							a.mergeScalar(&result, a.get(v.X))
						}
						if v.Op == token.ARROW {
							result, _, _ = a.receiveWithClosedChannelValue(v.X, a.channelClosures)
						}
						if v.Op == token.MUL {
							for _, p := range sortedKeys(a.get(v.X).addresses) {
								a.merge(&result, a.memory[p])
								if isFunctionType(v.Type()) {
									a.addGlobalFunctionState(&result, p, ix)
									if a.zeroFunctionFields[p] || a.zeroFunctionArrayElements[p] || a.get(v.X).functionNil {
										result.functionNil = true
									}
									if a.unknownFunctionElements[p] || a.get(v.X).functionUnknown {
										result.functionUnknown = true
									}
								}
								if inlineAggregate(v.Type()) {
									result.addresses[p] = true
									value := a.get(v.X)
									if exactZeroAggregateMatches(value, v.Type()) {
										a.addZeroAggregate(&result, v.Type())
									}
									if value.zeroAggregateUnknown {
										a.markZeroAggregateUnknown(&result)
									}
								}
							}
						}
					case *ssa.BinOp:
						a.mergeScalar(&result, a.get(v.X))
						a.mergeScalar(&result, a.get(v.Y))
						if v.Op == token.ADD {
							left, right := a.get(v.X), a.get(v.Y)
							result.stringUnknown = left.stringUnknown || right.stringUnknown
							for _, x := range sortedKeys(left.strings) {
								for _, y := range sortedKeys(right.strings) {
									one := emptyFlow()
									if len(x+y) <= 8192 {
										one.strings[x+y] = true
									} else {
										a.coverage.Widened = true
									}
									a.merge(&result, one)
								}
							}
						}
					case *ssa.MakeClosure:
						target := v.Fn.(*ssa.Function)
						result.functions[target] = true
						if len(v.Bindings) > 0 {
							if method, ok := isSQLHandleMethodTarget(target); ok {
								receiver := a.get(v.Bindings[0])
								projected, _, status := a.projectMethodExpressionReceiver(receiver, v.Bindings[0].Type(), method)
								if status == methodReceiverProjectionUnsupported {
									projected = unresolvedProjectedReceiver()
								}
								captureBoundMethodReceiver(&result, target, projected, true)
							} else if method, ok := target.Object().(*types.Func); ok && (contextAdapterName(method) == "Done" || contextAdapterName(method) == "Err") && contextSelectedMethod(v.Bindings[0].Type(), contextAdapterName(method)) != nil {
								method = contextSelectedMethod(v.Bindings[0].Type(), contextAdapterName(method))
								receiver := a.get(v.Bindings[0])
								projected, _, status := a.projectMethodExpressionReceiver(receiver, v.Bindings[0].Type(), method)
								if status == methodReceiverProjectionUnsupported {
									projected = unresolvedProjectedReceiver()
								}
								captureBoundMethodReceiver(&result, target, projected, true)
							} else if len(config.CallRules) > 0 {
								captureBoundMethodReceiver(&result, target, a.get(v.Bindings[0]), true)
							}
						}
						a.modelWaitGroupBinding(v, &result)
						a.modelLifecycleBinding(v, &result, config)
						for i, binding := range v.Bindings {
							if i < len(target.FreeVars) && a.put(target.FreeVars[i], a.get(binding)) {
								changed = true
							}
						}
					case *ssa.Extract:
						if assertion, ok := v.Tuple.(*ssa.TypeAssert); ok && v.Index == 0 && a.get(assertion.X).interfaceUnknown && a.seedInterfaceAssertion(v, assertion, ix) {
							changed = true
						}
						if call, ok := v.Tuple.(*ssa.Call); ok && a.seedAggregateResult(v, call, ix, false) {
							changed = true
						}
						if call, ok := v.Tuple.(*ssa.Call); ok && isFunctionType(v.Type()) && a.outsideFunctionResult(call, ix) {
							result.functionUnknown = true
						}
						if call, ok := v.Tuple.(*ssa.Call); ok && isSQLHandleType(v.Type()) && a.special[call] == nil && a.sqlExternalResult(call.Common(), ix) {
							result.sqlUnknown = true
						}
						switch tuple := v.Tuple.(type) {
						case *ssa.Next:
							if !tuple.IsString {
								a.merge(&result, a.mapIterationValue(tuple.Iter, v.Index, v.Type()))
							}
						case *ssa.Lookup, *ssa.TypeAssert, *ssa.UnOp:
							if v.Index == 0 {
								a.merge(&result, a.get(v.Tuple))
							}
						case *ssa.Select:
							index := 2
							for _, state := range tuple.States {
								if state.Dir == types.RecvOnly {
									if index == v.Index {
										value, _, _ := a.receiveWithClosedChannelValue(state.Chan, a.channelClosures)
										a.merge(&result, value)
									}
									index++
								}
							}
						}
						if call, ok := v.Tuple.(*ssa.Call); ok {
							if v.Index < len(a.special[call]) {
								a.merge(&result, a.special[call][v.Index])
							}
							for _, target := range sortedFunctions(a.targets(call.Common())) {
								if v.Index < len(a.returns[target]) {
									a.merge(&result, a.returns[target][v.Index])
								}
							}
						}
					case *ssa.Call:
						if a.openMutatedMaps(v) {
							changed = true
						}
						a.scalarBuiltinFlow(v, &result)
						wrapped, _ := a.errorWrapCandidates(v)
						a.merge(&result, wrapped)
						a.modelIterator(v, ix, &result)
						if a.sliceBuiltin(v, ix, &result) {
							changed = true
						}
						if a.callbackEscape(v, ix) {
							changed = true
						}
						a.modelContext(v, ix)
						a.modelSQLHandle(v, ix)
						if a.seedAggregateResult(v, v, ix, false) {
							changed = true
						}
						if isFunctionType(v.Type()) && a.outsideFunctionResult(v, ix) {
							result.functionUnknown = true
						}
						if isSQLHandleType(v.Type()) && a.special[v] == nil && a.sqlExternalResult(v.Common(), ix) {
							result.sqlUnknown = true
						}
						if len(a.special[v]) == 1 {
							a.merge(&result, a.special[v][0])
						}
						if a.call(v.Common(), ix) {
							changed = true
						}
						for _, target := range sortedFunctions(a.targets(v.Common())) {
							if len(a.returns[target]) == 1 {
								a.merge(&result, a.returns[target][0])
							}
						}
					case *ssa.Defer:
						a.modelAfterFunc(v, ix)
						if a.sliceBuiltin(v, ix, &result) {
							changed = true
						}
						if a.callbackEscape(v, ix) {
							changed = true
						}
						if a.call(v.Common(), ix) {
							changed = true
						}
					case *ssa.Go:
						a.modelAfterFunc(v, ix)
						if a.sliceBuiltin(v, ix, &result) {
							changed = true
						}
						if a.callbackEscape(v, ix) {
							changed = true
						}
						if a.call(v.Common(), ix) {
							changed = true
						}
					case *ssa.Return:
						if a.callbackReturnEscape(v, ix) {
							changed = true
						}
						old := a.returns[fn]
						if old == nil {
							old = make([]flowValue, len(v.Results))
						}
						for i, value := range v.Results {
							returned := a.get(value)
							if isScalarType(value.Type()) {
								a.mergeScalar(&returned, a.scalarControlValue(block))
							}
							if a.merge(&old[i], returned) {
								changed = true
							}
						}
						a.returns[fn] = old
					}
					if value, ok := instruction.(ssa.Value); ok {
						a.configuredLifecycleResult(value, &result, ix, config)
						if isScalarType(value.Type()) {
							a.mergeScalar(&result, a.scalarControlValue(block))
							if _, phi := value.(*ssa.Phi); phi {
								for _, pred := range block.Preds {
									a.mergeScalar(&result, a.scalarControlValue(pred))
								}
							}
						}
						if a.put(value, result) {
							changed = true
						}
					}
				}
			}
		}
		if a.afterFuncSchedulerFlow() {
			changed = true
		}
		if a.callbackGlobalEscapes(ix) {
			changed = true
		}
		if a.refreshChannelClosures(a.channelClosures) {
			changed = true
		}
		a.coverage.Iterations = round + 1
		if !changed {
			if !uncalledInputsSeeded {
				uncalledInputsSeeded = true
				newInputs := a.seedSQLInputs(funcs, ix, true)
				newResults := a.seedUnresolvedSQLResults(funcs)
				newFunctionResults := a.seedUnresolvedFunctionResults(funcs)
				newErrorInputs := a.seedErrorInputs(funcs, ix, true)
				newAggregateResults := a.seedUnresolvedAggregateResults(funcs, ix)
				if newInputs || newResults || newFunctionResults || newAggregateResults || newErrorInputs {
					continue
				}
			}
			a.coverage.Converged = true
			break
		}
	}
	// Source expression candidates are captured only after the fixed point.
	a.scalarControlCache = map[*ssa.BasicBlock]flowValue{}
	a.globalRelationships(ix)
	a.iteratorRelationships(ix)
	for _, fn := range funcs {
		for _, block := range fn.Blocks {
			for _, ins := range block.Instrs {
				a.errorUse(ins, ix)
				a.scalarUse(ins, ix)
				if call, ok := ins.(*ssa.Call); ok {
					a.errorWrapUse(call, ix)
				}
				a.globalUse(ins, ix)
				if iterator, ok := ins.(*ssa.Range); ok {
					if _, isMap := iterator.X.Type().Underlying().(*types.Map); isMap {
						ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(fn), Kind: "map_iteration_model", Reason: "map iteration retains separate unordered key and value candidates; key/value pairing, order, membership after delete/clear and runtime invocation are not proved; outside map inputs and unsupported aliases remain open", Evidence: ix.evidence(iterator.Pos())})
						concrete := false
						for address := range a.get(iterator.X).addresses {
							concrete = concrete || !strings.HasPrefix(address, "input:")
						}
						if !concrete {
							ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(fn), Kind: "unresolved_map_iteration", Reason: "map iteration has no concrete modeled storage origin; a synthetic outside input, nil or unsupported alias cannot establish an empty candidate set", Evidence: ix.evidence(iterator.Pos())})
						}
					}
				}
				if returned, ok := ins.(*ssa.Return); ok {
					a.callbackReturnRelationships(returned, ix)
					a.callableSummaryReturnEscapes(returned, ix)
				}
				a.channelUse(ins, ix.owner(fn), ix)
				ix.recoveryUse(ins)
				if d, ok := ins.(*ssa.DebugRef); ok && d.Expr != nil {
					old := a.positions[d.Expr.Pos()]
					value := a.get(d.X)
					if d.IsAddr {
						for _, p := range sortedKeys(value.addresses) {
							a.merge(&value, a.memory[p])
						}
					}
					a.merge(&old, value)
					a.positions[d.Expr.Pos()] = old
				}
				if v, ok := ins.(ssa.Value); ok && v.Pos() != token.NoPos {
					old := a.positions[v.Pos()]
					a.merge(&old, a.get(v))
					a.positions[v.Pos()] = old
				}
				if call, ok := ins.(ssa.CallInstruction); ok {
					if a.callTargets != nil {
						callee := a.get(call.Common().Value)
						oldCallee := a.callTargets[call.Common().Pos()]
						a.merge(&oldCallee, callee)
						a.callTargets[call.Common().Pos()] = oldCallee
					}
					a.callableSummaryCallEscapes(call, ix)
					a.unresolvedFunctionUse(call, ix)
					a.iteratorUse(call, ix)
					a.callbackEscapeRelationships(call, ix)
					from := ix.owner(fn)
					a.configuredLifecycleUse(call, from, ix, config)
					a.contextUse(call, from, ix)
					a.sqlHandleUse(call, ix)
					a.waitGroupUse(call, ix)
					a.cleanupUse(call, ix)
					if call.Common().IsInvoke() && len(a.targets(call.Common())) == 0 {
						ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "unresolved_interface_flow", Reason: "no local implementation body modeled for interface method " + call.Common().Method.Name() + "; dependency bodies or runtime implementations can supply additional values", Evidence: ix.callEvidence(call)})
					}
					for _, target := range sortedFunctions(a.targets(call.Common())) {
						to := ix.owner(target)
						if to == "" {
							continue
						}
						if call.Common().StaticCallee() == nil && !call.Common().IsInvoke() {
							kind := "resolved_callback_call"
							switch call.(type) {
							case *ssa.Go:
								kind = "goroutine_" + kind
							case *ssa.Defer:
								kind = "deferred_" + kind
							}
							ix.callEdge(from, to, kind, "possible", call)
						}
						for i, arg := range flowArguments(call.Common()) {
							value := a.get(arg)
							summaries := flowSummary(value, ix)
							if len(summaries) > 0 {
								ix.edges = append(ix.edges, Relationship{From: from, To: to, Kind: "argument_flow", Certainty: "possible", Evidence: ix.callEvidence(call), Slot: flowArgumentSlot(call.Common(), i), Values: summaries})
							}
						}
						for i, value := range a.returns[target] {
							summaries := flowSummary(value, ix)
							if len(summaries) > 0 {
								ix.edges = append(ix.edges, Relationship{From: to, To: from, Kind: "return_flow", Certainty: "possible", Evidence: ix.callEvidence(call), Slot: fmt.Sprintf("result:%d", i), Values: summaries})
							}
						}
					}
				}
			}
		}
	}
	a.contextBoundaries(ix)
	a.afterFuncRelationships(ix)
	a.doneRelationships(ix)
	for _, key := range sortedKeys(a.sqlHandles) {
		site := a.sqlHandles[key]
		ix.edge(site.owner, key, "sql_handle_create", "fact", site.position)
		if len(site.originals) > 0 {
			linked := false
			for _, candidate := range sortedSSAValues(site.originals) {
				originalValue := a.get(candidate)
				for _, original := range sortedKeys(originalValue.addresses) {
					if strings.HasPrefix(original, "statement:") {
						ix.edge(key, original, "statement_rebind", "possible", site.position)
						linked = true
					}
				}
				if originalValue.sqlUnknown || originalValue.interfaceUnknown {
					site.originalUnknown = true
				}
			}
			if !linked || site.originalUnknown {
				ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "unresolved_statement_rebind", Reason: "transaction statement rebinding includes a candidate without a modeled original statement handle", Evidence: ix.evidence(site.position)})
			}
		}
		if site.hasParent {
			linked := false
			for _, parent := range sortedKeys(site.parent.addresses) {
				parentKind := ""
				for _, kind := range []string{"database", "connection", "transaction"} {
					if strings.HasPrefix(parent, kind+":") {
						parentKind = kind
					}
				}
				if parentKind != "" {
					ix.edge(key, parent, ix.funcs[key].node.Kind+"_"+parentKind, "possible", site.position)
					linked = true
				}
			}
			if !linked || site.parentUnknown || site.parent.sqlUnknown || site.parent.interfaceUnknown {
				ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "unresolved_sql_parent", Reason: "SQL handle creation includes a candidate without a modeled parent receiver origin", Evidence: ix.evidence(site.position)})
			}
		}
	}
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "sql_handle_model", Reason: "Recognized direct, bound, method-expression and concrete-handle-backed interface calls to database/sql Open/OpenDB, Conn, prepare/begin and transaction statement APIs retain allocation-site handle candidates. SQL operation sites connect to receiver and table candidates; statement queries retain preparation origins. Flow is context-insensitive: receiver/query candidates are not correlated by invocation. Allocation sites do not establish physical database identity, successful creation, transaction ordering or cleanup. Unsupported wrappers, interface targets without compatible modeled database/sql handles, outside implementations and external origins remain unresolved."})
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "recovery_model", Reason: "Explicit panic and recover sites are structural facts. Deferred local callees connect to recover calls directly in that SSA body; ordinary nested helper calls are not promoted to direct recovery. Actual panic ownership, registration/execution order, implicit panics, recovery success, repanics and cross-goroutine behavior remain unresolved."})
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "waitgroup_model", Reason: "Static sync.WaitGroup methods, modeled bound closures and interfaces carrying typed WaitGroup receiver summaries retain operation/receiver candidates; Go tasks are function candidates. Direct constant Add deltas are reported, but runtime counts, ordering, reuse, copies, panic behavior and eventual task/join completion are not proved. Untagged external interface inputs, dependency bodies and unsupported wrapper shapes remain boundaries."})
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "cleanup_model", Reason: "Defer sites identify registrations, resolved resources/callees and CFG-reachable explicit return/panic exits. Path feasibility, implicit panics, recovery effects, repeated registration, LIFO ordering, os.Exit/fatal termination and eventual cleanup execution are not proved."})
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "callback_escape_model", Reason: "Function-value candidates, modeled public record fields, slice/array elements and map keys/values returned through exported APIs or passed to known dependencies/unmodeled bodies retain possible escapes. Pointer/container cycles and queued work are bounded; allocation/global types recover modeled containers through interfaces. Elements and map entries are unordered candidates, and deletion does not remove prior values. Append retains possible reused/new backing storage; copy retains possible source elements, including defer/go registrations, without proving lengths or execution order. Embedded records expose public descendant candidates without resolving selector shadowing or ambiguity. Private fields, unmodeled container flow, unresolved targets, external mutations, reflection and dependency bodies can add behavior; retention or invocation is not proved."})
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "value_flow_model", Reason: "Context-insensitive propagation of string/function values through parameters, results, closures, modeled memory, map lookups and channels. Channel payloads are unordered unions; map deletion, map iteration, external inputs, reflection and unmodeled aliasing can add or retain values. Candidates do not establish exhaustive runtime behavior."})
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "interface_flow_model", Reason: "Interface values propagate through local CHA implementation candidates, including receiver and result slots. Candidate implementations are not filtered by runtime receiver type; dependency bodies and implementations outside the loaded type universe can add behavior."})
	a.configuredLifecycleBoundaries(ix, config)
	return a, nil
}
func (a *flowAnalysis) targets(c *ssa.CallCommon) map[*ssa.Function]bool {
	if target := c.StaticCallee(); target != nil {
		return map[*ssa.Function]bool{target: true}
	}
	if c.IsInvoke() {
		return a.invokes[c]
	}
	return a.get(c.Value).functions
}
func (a *flowAnalysis) call(c *ssa.CallCommon, ix *index) bool {
	changed := a.invokeIterator(c, ix)
	targets := a.targets(c)
	external := len(targets) == 0 || a.get(c.Value).functionUnknown || c.IsInvoke() && a.get(c.Value).interfaceUnknown
	for _, target := range sortedFunctions(targets) {
		if ix.owner(target) == "" {
			external = true
			continue
		}
		for i, arg := range flowArguments(c) {
			if i < len(target.Params) && a.put(target.Params[i], a.get(arg)) {
				changed = true
			}
		}
	}
	if external {
		for _, argument := range flowArguments(c) {
			if isMapValue(argument) && a.openMapValue(argument) {
				changed = true
			}
		}
	}
	return changed
}
func flowSummary(v flowValue, ix *index) []string {
	result := []string{}
	if v.scalarUnknown {
		result = append(result, "unresolved:scalar_origins")
	}
	for origin := range v.scalars {
		result = append(result, origin)
	}
	if v.stringUnknown {
		result = append(result, "unresolved:string_value")
	}
	if v.interfaceUnknown {
		result = append(result, "unresolved:interface_value")
	}
	if v.functionUnknown {
		result = append(result, "unresolved:function_target")
	}
	if v.functionNil {
		result = append(result, "unresolved:nil_function_value")
	}
	if v.sqlUnknown {
		result = append(result, "unresolved:sql_handle")
	}
	for s := range v.strings {
		result = append(result, "string:"+s)
	}
	for f := range v.functions {
		if id := ix.owner(f); id != "" {
			result = append(result, "function:"+id)
		}
	}
	for p := range v.addresses {
		if strings.HasPrefix(p, "global:") || strings.HasPrefix(p, "context:") || strings.HasPrefix(p, "channel:") || strings.HasPrefix(p, "map:") {
			result = append(result, p)
		}
	}
	for effect := range v.effects {
		result = append(result, effect)
	}
	for origin := range v.errors {
		result = append(result, origin)
	}
	sort.Strings(result)
	return result
}
