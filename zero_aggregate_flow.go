package contracttrace

import (
	"go/types"
)

func carriesTypedAddressAlternative(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch types.Unalias(typ).Underlying().(type) {
	case *types.Pointer, *types.Map, *types.Interface, *types.Chan, *types.Slice:
		return true
	default:
		return false
	}
}

func (a *flowAnalysis) addTypedAddressAlternative(value *flowValue, address string, typ types.Type, zero, unknown bool) bool {
	if value == nil || address == "" {
		return false
	}
	if typ == nil {
		zero = false
		unknown = true
	}
	if !zero && !unknown {
		return false
	}
	current, exists := value.addressAlternatives[address]
	if !exists {
		if len(value.addressAlternatives) >= maxFlowValues {
			changed := !value.addressAltUnknown
			value.addressAltUnknown = true
			if a != nil {
				a.coverage.Widened = true
			}
			return changed
		}
		if value.addressAlternatives == nil {
			value.addressAlternatives = map[string]typedAddressAlternative{}
		}
		value.addressAlternatives[address] = typedAddressAlternative{typ: typ, zero: zero, unknown: unknown}
		return true
	}
	if current.typ == nil {
		if current.zero || !current.unknown {
			current.zero = false
			current.unknown = true
			value.addressAlternatives[address] = current
			return true
		}
		return false
	}
	changed := false
	if typ == nil {
		current.typ = nil
		current.zero = false
		current.unknown = true
		changed = true
	} else if current.typ == nil {
		current.typ = typ
		changed = true
	} else if !types.Identical(current.typ, typ) {
		current.typ = nil
		current.zero = false
		current.unknown = true
		changed = true
	}
	if current.typ == nil {
		if changed {
			value.addressAlternatives[address] = current
		}
		return changed
	}
	if zero && !current.zero {
		current.zero = true
		changed = true
	}
	if unknown && !current.unknown {
		current.unknown = true
		changed = true
	}
	if changed {
		value.addressAlternatives[address] = current
	}
	return changed
}

func (a *flowAnalysis) projectTypedAddressAlternative(result *flowValue, source flowValue, address string, typ types.Type) {
	if result == nil || typ == nil {
		return
	}
	if alternative, ok := source.addressAlternatives[address]; ok {
		if alternative.typ != nil && types.Identical(alternative.typ, typ) {
			addTypedAlternative(a, result, typ, alternative.zero, alternative.unknown)
		} else if alternative.typ == nil {
			addTypedAlternative(a, result, typ, false, true)
		}
	}
	if source.addressAltUnknown {
		addTypedAlternative(a, result, typ, false, true)
	}
}

func retypeAddressAlternatives(value *flowValue, from, to types.Type) {
	if value == nil || from == nil || to == nil || !types.ConvertibleTo(from, to) {
		return
	}
	fromPointer, fromOK := types.Unalias(from).Underlying().(*types.Pointer)
	toPointer, toOK := types.Unalias(to).Underlying().(*types.Pointer)
	if !fromOK || !toOK {
		return
	}
	for address, alternative := range value.addressAlternatives {
		if alternative.typ != nil && types.Identical(types.Unalias(alternative.typ), types.Unalias(fromPointer.Elem())) {
			alternative.typ = toPointer.Elem()
		} else if alternative.typ != nil {
			alternative.typ = toPointer.Elem()
			alternative.zero = false
			alternative.unknown = true
		}
		value.addressAlternatives[address] = alternative
	}
}

// addZeroAggregate records a source-proven zero-value alternative for an
// inline aggregate. Later field or array reads project its modeled zero child.
func (a *flowAnalysis) addZeroAggregate(value *flowValue, typ types.Type) bool {
	if value == nil || typ == nil || !inlineAggregate(typ) {
		return false
	}
	typ = types.Unalias(typ)
	if value.zeroAggregates[typ] {
		return false
	}
	if len(value.zeroAggregates) >= maxFlowValues {
		if value.zeroAggregateUnknown {
			return false
		}
		value.zeroAggregateUnknown = true
		if a != nil {
			a.coverage.Widened = true
		}
		return true
	}
	if value.zeroAggregates == nil {
		value.zeroAggregates = map[types.Type]bool{}
	}
	value.zeroAggregates[typ] = true
	return true
}

func (a *flowAnalysis) markZeroAggregateUnknown(value *flowValue) bool {
	if value == nil || value.zeroAggregateUnknown {
		return false
	}
	value.zeroAggregateUnknown = true
	return true
}

func exactZeroAggregateMatches(value flowValue, typ types.Type) bool {
	if typ == nil || !inlineAggregate(typ) {
		return false
	}
	typ = types.Unalias(typ)
	for candidate := range value.zeroAggregates {
		if types.Identical(types.Unalias(candidate), typ) {
			return true
		}
	}
	return false
}

// propagateIndirectAlternative carries a typed child candidate through a
// pointer load. Generic pointer uncertainty is not projected into the pointee:
// a nil parent may panic before any child value is observed.
func propagateIndirectAlternative(result *flowValue, source flowValue, typ types.Type) {
	if result == nil || typ == nil {
		return
	}
	if pointer, ok := types.Unalias(typ).Underlying().(*types.Pointer); ok && isSQLHandleType(pointer.Elem()) {
		result.sqlUnknown = result.sqlUnknown || source.sqlUnknown
		return
	}
	if isFunctionType(typ) {
		result.functionNil = result.functionNil || source.functionNil
		result.functionUnknown = result.functionUnknown || source.functionUnknown
		return
	}
	if isMapType(typ) {
		result.mapNil = result.mapNil || source.mapNil
	}
}

// zeroAggregateField projects a typed zero aggregate through one inline
// struct field. Pointer fields retain their own zero alternative, but the
// marker never implies that the pointer's pointee is zero.
func (a *flowAnalysis) zeroAggregateField(value flowValue, parentType types.Type, field int) flowValue {
	result := emptyFlow()
	knownZero := exactZeroAggregateMatches(value, parentType)
	if !knownZero && !value.zeroAggregateUnknown {
		return result
	}
	structure, ok := types.Unalias(parentType).Underlying().(*types.Struct)
	if !ok || field < 0 || field >= structure.NumFields() {
		return result
	}
	child := structure.Field(field).Type()
	switch {
	case isSQLHandleType(child):
		result.sqlUnknown = true
	case isMapType(child):
		result.mapNil = knownZero
		result.interfaceUnknown = value.zeroAggregateUnknown
	case isFunctionType(child):
		if knownZero {
			result.functionNil = true
		}
		if value.zeroAggregateUnknown {
			result.functionUnknown = true
		}
	case inlineAggregate(child):
		if knownZero {
			a.addZeroAggregate(&result, child)
		}
		if value.zeroAggregateUnknown {
			a.markZeroAggregateUnknown(&result)
		}
	default:
		addTypedAlternative(a, &result, child, knownZero, value.zeroAggregateUnknown)
	}
	return result
}

// zeroAggregateIndex projects a typed zero array through an element. Empty
// arrays and pointer elements do not produce a function-nil alternative.
func (a *flowAnalysis) zeroAggregateIndex(value flowValue, parentType types.Type) flowValue {
	result := emptyFlow()
	knownZero := exactZeroAggregateMatches(value, parentType)
	if (!knownZero && !value.zeroAggregateUnknown) || parentType == nil {
		return result
	}
	array, ok := types.Unalias(parentType).Underlying().(*types.Array)
	if !ok || array.Len() == 0 {
		return result
	}
	addTypedAlternative(a, &result, array.Elem(), knownZero, value.zeroAggregateUnknown)
	return result
}

// retypeZeroAggregate carries default-value evidence across SSA conversions
// only when the converted inline aggregate remains a compatible Go value.
func (a *flowAnalysis) retypeZeroAggregate(value flowValue, from, to types.Type) flowValue {
	result := emptyFlow()
	if from == nil || to == nil || !inlineAggregate(from) || !inlineAggregate(to) || !types.ConvertibleTo(from, to) {
		return result
	}
	from = types.Unalias(from)
	for candidate := range value.zeroAggregates {
		if types.Identical(types.Unalias(candidate), from) {
			a.addZeroAggregate(&result, to)
		}
	}
	if value.zeroAggregateUnknown {
		a.markZeroAggregateUnknown(&result)
	}
	return result
}
