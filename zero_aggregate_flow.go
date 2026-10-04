package contracttrace

import (
	"go/types"
)

// addZeroAggregate records a source-proven zero-value alternative for an
// inline aggregate. The marker is projected only when a later field or array
// index reads a function-bearing child.
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

// zeroAggregateField projects a typed zero aggregate through one inline
// struct field. Pointer fields deliberately stop: a zero pointer can panic on
// selection, but is not itself evidence that a function field is nil.
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
	child := array.Elem()
	switch {
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
	}
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
