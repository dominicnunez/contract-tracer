package contracttrace

import (
	"errors"
	"fmt"
	"go/types"
)

var errUnsupportedMethodReceiverProjection = errors.New("method-expression receiver projection is unsupported")

type methodReceiverProjection uint8

const (
	methodReceiverProjectionUnsupported methodReceiverProjection = iota
	methodReceiverProjectionDirect
	methodReceiverProjectionPromoted
)

// projectMethodExpressionReceiver follows only the field path selected by the
// Go method set for this invocation's actual receiver type. It evaluates that
// path against the receiver value and modeled field memory; it never merges a
// generated thunk's parameters or interprets arbitrary method bodies.
func (a *flowAnalysis) projectMethodExpressionReceiver(value flowValue, receiverType types.Type, apiMethod *types.Func) (flowValue, *types.Signature, methodReceiverProjection) {
	selectedMethod, selectedSignature, index := lifecycleMethodSetSelection(receiverType, apiMethod)
	if selectedMethod == nil || selectedSignature == nil {
		return unresolvedProjectedReceiver(), nil, methodReceiverProjectionUnsupported
	}
	fields := index[:len(index)-1]
	if len(fields) == 0 {
		return value, selectedSignature, methodReceiverProjectionDirect
	}
	apiReceiverType := selectedSignature.Recv().Type()

	// Keep uncertainty from the receiver, but never treat outer wrapper payloads
	// or addresses as API resource candidates.
	projected := emptyFlow()
	projected.sqlUnknown = value.sqlUnknown
	projected.functionUnknown = value.functionUnknown
	projected.functionNil = value.functionNil
	projected.interfaceUnknown = value.interfaceUnknown
	projected.stringUnknown = value.stringUnknown
	projected.scalarUnknown = value.scalarUnknown
	projected.boundReceiverUnknown = value.boundReceiverUnknown

	for _, root := range sortedKeys(value.addresses) {
		initial, initialUncertain := a.methodProjectionInitialAddresses(root, receiverType, fields[0])
		if initialUncertain {
			projected.interfaceUnknown = true
		}
		addresses := map[string]bool{}
		for _, address := range initial {
			addresses[address] = true
		}
		currentType := receiverType
		rootResolved := false
		for pathIndex, fieldIndex := range fields {
			structType := currentType
			if pointer, ok := structType.Underlying().(*types.Pointer); ok {
				structType = pointer.Elem()
			}
			record, ok := structType.Underlying().(*types.Struct)
			if !ok || fieldIndex < 0 || fieldIndex >= record.NumFields() {
				addresses = nil
				break
			}
			fieldType := record.Field(fieldIndex).Type()
			next := map[string]bool{}
			for _, address := range sortedKeys(addresses) {
				cell := fieldAddress(address, fieldIndex)
				stored := a.memory[cell]
				last := pathIndex == len(fields)-1
				if last {
					_, fieldIsPointer := fieldType.Underlying().(*types.Pointer)
					_, receiverIsPointer := apiReceiverType.Underlying().(*types.Pointer)
					terminal := stored
					branchResolved := false
					if fieldIsPointer && !receiverIsPointer {
						// A promoted value-receiver method through an embedded
						// pointer implicitly dereferences that pointer. The API value
						// is the pointee's modeled contents, not the pointer identity.
						terminal = emptyFlow()
						mergeProjectedUncertainty(&projected, stored)
						targets := sortedKeys(stored.addresses)
						if len(targets) == 0 {
							projected.interfaceUnknown = true
						}
						for _, target := range targets {
							loaded := a.memory[target]
							a.merge(&terminal, loaded)
							if !projectedReceiverHasCandidate(loaded, apiReceiverType) {
								projected.interfaceUnknown = true
							} else {
								branchResolved = true
							}
						}
						a.merge(&projected, terminal)
					} else {
						a.merge(&projected, stored)
					}
					// A promoted pointer-receiver method on an embedded scalar
					// takes the address of the field cell itself. Value aggregates
					// also keep their cell as an identity candidate. An embedded
					// pointer instead requires the modeled pointee address below.
					fieldCellIsReceiver := !fieldIsPointer && (receiverIsPointer || inlineAggregate(fieldType))
					if fieldCellIsReceiver {
						candidate := emptyFlow()
						candidate.addresses[cell] = true
						a.merge(&projected, candidate)
						branchResolved = true
					}
					if !fieldIsPointer || receiverIsPointer {
						branchResolved = branchResolved || projectedReceiverHasCandidate(stored, apiReceiverType)
					}
					rootResolved = rootResolved || branchResolved
					if !branchResolved {
						projected.interfaceUnknown = true
					}
					continue
				}
				if _, ok := fieldType.Underlying().(*types.Pointer); ok {
					knownTargets := sortedKeys(stored.addresses)
					// An inline value-copy alias may hold this field's modeled
					// contents at the aliased record address. Defer incompleteness
					// to those paths instead of treating the empty copy cell as an
					// independent missing pointer.
					if len(knownTargets) == 0 && len(a.memory[address].addresses) == 0 {
						projected.interfaceUnknown = true
					}
					for _, target := range knownTargets {
						if !next[target] && len(next) >= maxFlowValues {
							a.coverage.Widened = true
							projected.interfaceUnknown = true
							break
						}
						next[target] = true
					}
					// Unknown or non-address candidates at an intermediate pointer
					// make the projected receiver incomplete, while known paths remain.
					mergeProjectedUncertainty(&projected, stored)
				} else {
					fieldAddresses := []string{cell}
					if inlineAggregate(fieldType) {
						var complete bool
						fieldAddresses, complete = a.methodProjectionRecordAddresses(cell)
						mergeProjectedUncertainty(&projected, stored)
						if !complete {
							projected.interfaceUnknown = true
						}
					}
					for _, fieldAddress := range fieldAddresses {
						if !next[fieldAddress] && len(next) >= maxFlowValues {
							a.coverage.Widened = true
							projected.interfaceUnknown = true
							continue
						}
						next[fieldAddress] = true
					}
				}
			}
			addresses = next
			currentType = fieldType
			if len(addresses) == 0 {
				break
			}
			if pathIndex == len(fields)-1 {
				break
			}
		}
		if !rootResolved {
			projected.interfaceUnknown = true
		}
	}
	if len(projected.addresses) == 0 && len(projected.strings) == 0 && len(projected.errors) == 0 &&
		len(projected.functions) == 0 && len(projected.scalars) == 0 && !projected.sqlUnknown && !projected.interfaceUnknown &&
		!projected.stringUnknown && !projected.scalarUnknown {
		projected.interfaceUnknown = true
	}
	return projected, selectedSignature, methodReceiverProjectionPromoted
}

// methodProjectionInitialAddresses accounts for SSA's separate storage
// allocation for addressable value receivers. An expression such as
// (*Envelope).Done(&value) can pass a pointer cell whose memory contains the
// address of the aggregate copy; embedded fields then live under that copy.
// Preserve root-backed field storage when both representations are present.
func (a *flowAnalysis) methodProjectionInitialAddresses(root string, receiverType types.Type, firstField int) ([]string, bool) {
	pointer, ok := receiverType.Underlying().(*types.Pointer)
	if !ok {
		return []string{root}, false
	}
	contents := a.memory[root]
	uncertain := flowProjectionUncertain(contents)
	if len(contents.addresses) == 0 {
		return []string{root}, uncertain
	}
	result := map[string]bool{}
	visited := map[string]bool{root: true}
	queue := sortedKeys(contents.addresses)
	if flowHasProjectionPayload(a.memory[fieldAddress(root, firstField)]) {
		result[root] = true
	}
	for head := 0; head < len(queue); head++ {
		if len(visited) >= maxFlowValues && !visited[queue[head]] {
			a.coverage.Widened = true
			uncertain = true
			break
		}
		alias := queue[head]
		if visited[alias] {
			uncertain = true
			continue
		}
		visited[alias] = true
		aliasType := a.addressTypes[alias]
		if aliasType == nil {
			uncertain = true
			continue
		}
		if !types.Identical(aliasType, receiverType) && !types.Identical(aliasType, pointer.Elem()) {
			uncertain = true
			continue
		}
		aliasContents := a.memory[alias]
		uncertain = uncertain || flowProjectionUncertain(aliasContents)
		if flowHasProjectionPayload(a.memory[fieldAddress(alias, firstField)]) {
			result[alias] = true
		}
		children := sortedKeys(aliasContents.addresses)
		if len(children) == 0 {
			result[alias] = true
			continue
		}
		for _, child := range children {
			if !visited[child] {
				if len(queue) >= maxFlowValues {
					a.coverage.Widened = true
					uncertain = true
					continue
				}
				queue = append(queue, child)
			} else {
				uncertain = true
			}
		}
	}
	if len(result) == 0 {
		// Keep the root path as an unresolved candidate when no typed alias
		// establishes the aggregate storage location.
		result[root] = true
		uncertain = true
	}
	return sortedKeys(result), uncertain
}

func flowProjectionUncertain(value flowValue) bool {
	return value.interfaceUnknown || value.sqlUnknown || value.boundReceiverUnknown || value.functionUnknown || value.functionNil || value.stringUnknown || value.scalarUnknown
}

func flowHasProjectionPayload(value flowValue) bool {
	return len(value.addresses) > 0 || len(value.strings) > 0 || len(value.effects) > 0 || len(value.errors) > 0 || len(value.scalars) > 0 || len(value.functions) > 0 || len(value.boundReceivers) > 0 || value.interfaceUnknown || value.sqlUnknown || value.functionUnknown || value.functionNil || value.stringUnknown || value.scalarUnknown
}

func lifecycleMethodSetSelection(receiverType types.Type, apiMethod *types.Func) (*types.Func, *types.Signature, []int) {
	if receiverType == nil || apiMethod == nil {
		return nil, nil, nil
	}
	selection := types.NewMethodSet(receiverType).Lookup(apiMethod.Pkg(), apiMethod.Name())
	if selection == nil {
		return nil, nil, nil
	}
	selectedMethod, ok := selection.Obj().(*types.Func)
	if !ok || selectedMethod.Origin() != apiMethod.Origin() {
		return nil, nil, nil
	}
	selectedSignature, ok := selectedMethod.Type().(*types.Signature)
	if !ok || selectedSignature.Recv() == nil {
		return nil, nil, nil
	}
	return selectedMethod, selectedSignature, selection.Index()
}

func projectedReceiverHasCandidate(value flowValue, receiverType types.Type) bool {
	if receiverType == nil {
		return false
	}
	if _, pointer := receiverType.Underlying().(*types.Pointer); pointer {
		return len(value.addresses) > 0
	}
	if basic, ok := receiverType.Underlying().(*types.Basic); ok && basic.Kind() == types.String {
		return len(value.strings) > 0
	}
	// Other resource receivers are represented by aggregate or interface
	// addresses. Effects and uncertainty flags describe incomplete behavior,
	// but do not establish a concrete receiver candidate.
	return len(value.addresses) > 0
}

// methodProjectionRecordAddresses mirrors recordAddresses for one typed inline
// aggregate field. Memory may hold aliases created by value copies, so follow
// those aliases before selecting the next embedded field. Keep the root cell,
// cap fanout, and stop cycles without mutating modeled memory.
func (a *flowAnalysis) methodProjectionRecordAddresses(root string) ([]string, bool) {
	addresses := map[string]bool{root: true}
	queue := []string{root}
	complete := true
	for head := 0; head < len(queue); head++ {
		for _, alias := range sortedKeys(a.memory[queue[head]].addresses) {
			if addresses[alias] {
				continue
			}
			if len(addresses) >= maxFlowValues {
				a.coverage.Widened = true
				complete = false
				continue
			}
			addresses[alias] = true
			queue = append(queue, alias)
		}
	}
	return sortedKeys(addresses), complete
}

func fieldAddress(address string, index int) string {
	return fmt.Sprintf("%s.field:%d", address, index)
}

func unresolvedProjectedReceiver() flowValue {
	value := emptyFlow()
	value.interfaceUnknown = true
	return value
}

func mergeProjectedUncertainty(destination *flowValue, source flowValue) {
	destination.sqlUnknown = destination.sqlUnknown || source.sqlUnknown
	destination.functionUnknown = destination.functionUnknown || source.functionUnknown
	destination.functionNil = destination.functionNil || source.functionNil
	destination.interfaceUnknown = destination.interfaceUnknown || source.interfaceUnknown
	destination.stringUnknown = destination.stringUnknown || source.stringUnknown
	destination.scalarUnknown = destination.scalarUnknown || source.scalarUnknown
	destination.boundReceiverUnknown = destination.boundReceiverUnknown || source.boundReceiverUnknown
}
