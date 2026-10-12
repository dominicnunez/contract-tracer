package contracttrace

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

type mergeBudgetDomain uint8

const (
	mergeBudgetScalar mergeBudgetDomain = iota
	mergeBudgetError
	mergeBudgetString
	mergeBudgetAddress
	mergeBudgetEffect
)

type mergeBudgetState struct {
	values  map[string]bool
	unknown bool
	widened bool
	changed bool
}

// referenceMergeBudget models the current caller transition directly: gather
// and sort missing keys only when crossing the bound, then apply each
// domain's existing full-cap behavior.
func referenceMergeBudget(existing, incoming map[string]bool, unknown, incomingUnknown bool, domain mergeBudgetDomain) mergeBudgetState {
	state := mergeBudgetState{values: cloneBoolMap(existing), unknown: unknown}
	if incomingUnknown && (domain == mergeBudgetScalar || domain == mergeBudgetString) && !state.unknown {
		state.unknown, state.changed = true, true
	}
	missing := make([]string, 0, len(incoming))
	for key := range incoming {
		if !state.values[key] {
			missing = append(missing, key)
		}
	}
	if len(state.values)+len(missing) > maxFlowValues {
		sort.Strings(missing)
	}
	for _, key := range missing {
		if state.values[key] {
			continue
		}
		if len(state.values) >= maxFlowValues {
			state.widened = true
			if (domain == mergeBudgetScalar || domain == mergeBudgetString) && !state.unknown {
				state.unknown, state.changed = true, true
			}
			continue
		}
		state.values[key] = true
		state.changed = true
	}
	return state
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}
	copy := make(map[string]bool, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func TestMergeAtCapacityMatchesReferenceTransition(t *testing.T) {
	rng := rand.New(rand.NewSource(21764))
	for _, domain := range []mergeBudgetDomain{mergeBudgetScalar, mergeBudgetError, mergeBudgetString, mergeBudgetAddress, mergeBudgetEffect} {
		for _, size := range []int{0, 1, maxFlowValues - 1, maxFlowValues, maxFlowValues + 1} {
			for _, incomingCount := range []int{0, 1, maxFlowValues, maxFlowValues + 1} {
				for _, overlap := range []bool{false, true} {
					existing := trueCandidates("e", size)
					incoming := trueCandidates("k", incomingCount)
					if overlap {
						incoming = trueCandidates("e", incomingCount)
					}
					assertMergeTransition(t, domain, existing, incoming, false, false)
				}
			}
		}
		// Nil maps and false-valued keys exercise map-presence versus boolean
		// membership semantics explicitly.
		assertMergeTransition(t, domain, nil, nil, false, false)
		assertMergeTransition(t, domain, trueCandidates("e", maxFlowValues), nil, false, false)
		falseKeys := trueCandidates("e", maxFlowValues)
		for key := range falseKeys {
			falseKeys[key] = false
		}
		assertMergeTransition(t, domain, falseKeys, map[string]bool{"e000": true, "incoming": true}, false, false)
		for caseIndex := 0; caseIndex < 500; caseIndex++ {
			existing := map[string]bool{}
			incoming := map[string]bool{}
			population := rng.Intn(maxFlowValues + 3) // below, at, and above the bound
			for i := 0; i < population; i++ {
				existing[fmt.Sprintf("e%03d", i)] = rng.Intn(5) != 0 // false-valued keys are intentional
			}
			incomingCount := rng.Intn(maxFlowValues*2 + 1)
			for i := 0; i < incomingCount; i++ {
				key := fmt.Sprintf("k%03d", rng.Intn(maxFlowValues*3+1))
				incoming[key] = rng.Intn(4) != 0
			}
			if caseIndex%4 == 0 && len(existing) >= maxFlowValues {
				for key := range existing {
					incoming[key] = true
				}
			}
			unknown, incomingUnknown := rng.Intn(2) == 0, rng.Intn(2) == 0
			if domain != mergeBudgetScalar && domain != mergeBudgetString {
				unknown, incomingUnknown = false, false
			}
			if caseIndex%7 == 0 {
				incoming = nil
			}
			assertMergeTransition(t, domain, existing, incoming, unknown, incomingUnknown)
		}
		shared := map[string]bool{"alias-true": true, "alias-false": false}
		destination, source := emptyFlow(), emptyFlow()
		setBudgetValues(&destination, domain, shared)
		setBudgetValues(&source, domain, shared)
		if domain == mergeBudgetScalar {
			destination.scalarUnknown = false
		}
		if domain == mergeBudgetString {
			destination.stringUnknown = false
		}
		a := &flowAnalysis{}
		expected := referenceMergeBudget(shared, shared, false, false, domain)
		changed := a.merge(&destination, source)
		actual, unknown := budgetValues(destination, domain)
		if !equalBoolMaps(actual, expected.values) || unknown != expected.unknown || changed != expected.changed || a.coverage.Widened != expected.widened {
			t.Fatalf("domain=%d aliased source/destination differs from reference transition", domain)
		}
	}
}

func equalBoolMaps(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || other != value {
			return false
		}
	}
	return true // nil and empty maps represent the same candidate set.
}

func trueCandidates(prefix string, count int) map[string]bool {
	result := make(map[string]bool, count)
	for i := 0; i < count; i++ {
		result[fmt.Sprintf("%s%03d", prefix, i)] = true
	}
	return result
}

func assertMergeTransition(t *testing.T, domain mergeBudgetDomain, existing, incoming map[string]bool, unknown, incomingUnknown bool) {
	t.Helper()
	if domain != mergeBudgetScalar && domain != mergeBudgetString {
		unknown, incomingUnknown = false, false
	}
	expected := referenceMergeBudget(existing, incoming, unknown, incomingUnknown, domain)
	sourceBefore := cloneBoolMap(incoming)
	a := &flowAnalysis{}
	destination, source := emptyFlow(), emptyFlow()
	setBudgetValues(&destination, domain, cloneBoolMap(existing))
	setBudgetValues(&source, domain, incoming)
	if domain == mergeBudgetScalar {
		destination.scalarUnknown, source.scalarUnknown = unknown, incomingUnknown
	}
	if domain == mergeBudgetString {
		destination.stringUnknown, source.stringUnknown = unknown, incomingUnknown
	}
	changed := a.merge(&destination, source)
	actualValues, actualUnknown := budgetValues(destination, domain)
	if !equalBoolMaps(actualValues, expected.values) || actualUnknown != expected.unknown || a.coverage.Widened != expected.widened || changed != expected.changed {
		t.Fatalf("domain=%d differs from legacy caller transition: got values=%v unknown=%v widened=%v changed=%v; want %+v", domain, actualValues, actualUnknown, a.coverage.Widened, changed, expected)
	}
	if !equalBoolMaps(incoming, sourceBefore) {
		t.Fatal("merge mutated its incoming candidate map")
	}
	if onlyTrue(existing) && onlyTrue(incoming) {
		assertMergeSetInvariants(t, domain, existing, incoming, unknown, incomingUnknown, destination, changed, a.coverage.Widened)
	}
}

func onlyTrue(values map[string]bool) bool {
	for _, value := range values {
		if !value {
			return false
		}
	}
	return true
}

func assertMergeSetInvariants(t *testing.T, domain mergeBudgetDomain, existing, incoming map[string]bool, unknown, incomingUnknown bool, destination flowValue, changed, widened bool) {
	t.Helper()
	actual, actualUnknown := budgetValues(destination, domain)
	missing := make([]string, 0, len(incoming))
	for key := range incoming {
		if !existing[key] {
			missing = append(missing, key)
		}
	}
	capacity := maxFlowValues - len(existing)
	if capacity < 0 {
		capacity = 0
	}
	if len(existing) >= maxFlowValues {
		if !equalBoolMaps(actual, existing) {
			t.Fatal("full/overfull merge inserted a candidate")
		}
		wantUnknown := unknown
		if domain == mergeBudgetScalar || domain == mergeBudgetString {
			wantUnknown = wantUnknown || incomingUnknown || len(missing) > 0
		}
		if actualUnknown != wantUnknown || widened != (len(missing) > 0) {
			t.Fatalf("full-cap flags differ: unknown=%v/%v widened=%v missing=%d", actualUnknown, wantUnknown, widened, len(missing))
		}
		if changed != ((domain == mergeBudgetScalar || domain == mergeBudgetString) && !unknown && wantUnknown) {
			t.Fatalf("full-cap changed=%v does not match newly-set uncertainty", changed)
		}
		return
	}
	if len(missing) <= capacity {
		for _, key := range missing {
			if !actual[key] {
				t.Fatal("candidate fitting below cap was lost")
			}
		}
		if widened {
			t.Fatal("merge widened while all candidates fit")
		}
	}
}

func setBudgetValues(value *flowValue, domain mergeBudgetDomain, candidates map[string]bool) {
	switch domain {
	case mergeBudgetScalar:
		value.scalars = candidates
	case mergeBudgetError:
		value.errors = candidates
	case mergeBudgetString:
		value.strings = candidates
	case mergeBudgetAddress:
		value.addresses = candidates
	case mergeBudgetEffect:
		value.effects = candidates
	}
}

func budgetValues(value flowValue, domain mergeBudgetDomain) (map[string]bool, bool) {
	switch domain {
	case mergeBudgetScalar:
		return value.scalars, value.scalarUnknown
	case mergeBudgetError:
		return value.errors, false
	case mergeBudgetString:
		return value.strings, value.stringUnknown
	case mergeBudgetAddress:
		return value.addresses, false
	default:
		return value.effects, false
	}
}

func legacyMergeKeys(values, existing map[string]bool) []string {
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

var mergeKeysSink []string

func BenchmarkMergeKeysAtCapacity(b *testing.B) {
	existing := map[string]bool{}
	for i := 0; i < maxFlowValues; i++ {
		existing[fmt.Sprintf("existing-%03d", i)] = true
	}
	contained := cloneBoolMap(existing)
	for _, count := range []int{maxFlowValues, 2048} {
		disjoint := map[string]bool{}
		for i := 0; i < count; i++ {
			disjoint[fmt.Sprintf("incoming-%04d", i)] = true
		}
		b.Run(fmt.Sprintf("legacy/full-disjoint-%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				mergeKeysSink = legacyMergeKeys(disjoint, existing)
			}
		})
		b.Run(fmt.Sprintf("current/full-disjoint-%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				mergeKeysSink = mergeKeys(disjoint, existing)
			}
		})
	}
	b.Run("legacy/full-contained", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			mergeKeysSink = legacyMergeKeys(contained, existing)
		}
	})
	b.Run("current/full-contained", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			mergeKeysSink = mergeKeys(contained, existing)
		}
	})
}
