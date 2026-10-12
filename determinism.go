package contracttrace

import (
	"fmt"
	"sort"

	"golang.org/x/tools/go/ssa"
)

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Ordering affects the result only when a candidate budget can be reached.
func mergeKeys(values, existing map[string]bool) []string {
	if len(existing) >= maxFlowValues {
		for key := range values {
			if !existing[key] {
				// Every caller discards the candidate at capacity, while setting
				// the same widening and uncertainty flags. One witness suffices.
				return []string{key}
			}
		}
		return nil
	}
	var result []string
	for key := range values {
		if !existing[key] {
			result = append(result, key)
		}
	}
	if len(existing)+len(result) > maxFlowValues {
		sort.Strings(result)
	}
	return result
}
func mergeFunctions(values, existing map[*ssa.Function]bool) []*ssa.Function {
	var result []*ssa.Function
	for fn := range values {
		if !existing[fn] {
			result = append(result, fn)
		}
	}
	if len(existing)+len(result) > maxFlowValues {
		sort.Slice(result, func(i, j int) bool {
			if result[i].String() != result[j].String() {
				return result[i].String() < result[j].String()
			}
			return result[i].Pos() < result[j].Pos()
		})
	}
	return result
}
func sortedFunctions(values map[*ssa.Function]bool) []*ssa.Function {
	result := make([]*ssa.Function, 0, len(values))
	for function := range values {
		result = append(result, function)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].String() != result[j].String() {
			return result[i].String() < result[j].String()
		}
		return result[i].Pos() < result[j].Pos()
	})
	return result
}
func (ix *index) allocationID(kind string, fn *ssa.Function, value ssa.Value) string {
	evidence := ix.evidence(value.Pos())
	return fmt.Sprintf("%s:%s:%s:%d:%d:%s", kind, fn.String(), evidence.File, evidence.Line, evidence.Column, value.Name())
}
