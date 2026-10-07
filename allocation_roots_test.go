package contracttrace

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// legacyAllocationRoot is an independent reference for the former full scan.
func legacyAllocationRoot[V any](address string, roots map[string]V, allowEmpty bool) (string, bool) {
	selected := ""
	found := false
	for root := range roots {
		matches := address == root || strings.HasPrefix(address, root+".field:")
		if !matches || root == "" && !allowEmpty {
			continue
		}
		if !found || len(root) > len(selected) {
			selected, found = root, true
		}
	}
	return selected, found
}

func TestLongestAllocationRootMatchesLegacyMaximumPrefix(t *testing.T) {
	cases := []struct {
		name       string
		address    string
		roots      map[string]*int
		allowEmpty bool
		want       string
		found      bool
	}{
		{name: "exact", address: "alloc:one", roots: map[string]*int{"alloc:one": nil}, want: "alloc:one", found: true},
		{name: "deepest field", address: "alloc:one.field:2.field:0", roots: map[string]*int{"alloc:one": nil, "alloc:one.field:2": nil}, want: "alloc:one.field:2", found: true},
		{name: "delimiter in allocation id", address: "alloc.field:embedded.field:1", roots: map[string]*int{"alloc.field:embedded": nil, "alloc": nil}, want: "alloc.field:embedded", found: true},
		{name: "malformed field suffix still selects allocation", address: "alloc.field:nope", roots: map[string]*int{"alloc": nil}, want: "alloc", found: true},
		{name: "miss", address: "other.field:0", roots: map[string]*int{"alloc": nil}, found: false},
		{name: "empty root excluded by field and array state lookup", address: ".field:0", roots: map[string]*int{"": nil}, found: false},
		{name: "empty root included by historical any-array predicate", address: ".field:0", roots: map[string]*int{"": nil}, allowEmpty: true, want: "", found: true},
		{name: "empty exact root included", address: "", roots: map[string]*int{"": nil}, allowEmpty: true, want: "", found: true},
		{name: "empty map excluded", address: "", roots: nil, found: false},
		{name: "plain textual prefix is not a field prefix", address: "allocfoo.field:1", roots: map[string]*int{"alloc": nil}, found: false},
	}
	shorter := 7
	deepest := 11
	cases[1].roots["alloc:one"] = &shorter
	cases[1].roots["alloc:one.field:2"] = &deepest
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := cloneRootTestMap(tc.roots)
			got, found := longestAllocationRoot(tc.address, tc.roots, tc.allowEmpty)
			if got != tc.want || found != tc.found {
				t.Fatalf("longestAllocationRoot(%q) = (%q,%v), want (%q,%v)", tc.address, got, found, tc.want, tc.found)
			}
			legacy, legacyFound := legacyAllocationRoot(tc.address, tc.roots, tc.allowEmpty)
			if got != legacy || found != legacyFound {
				t.Fatalf("reference mismatch: optimized=(%q,%v), legacy=(%q,%v)", got, found, legacy, legacyFound)
			}
			if !equalRootTestMaps(tc.roots, before) {
				t.Fatalf("lookup mutated allocation map: before=%v after=%v", before, tc.roots)
			}
		})
	}
}

func cloneRootTestMap(input map[string]*int) map[string]*int {
	if input == nil {
		return nil
	}
	result := make(map[string]*int, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func equalRootTestMaps(left, right map[string]*int) bool {
	if len(left) != len(right) || (left == nil) != (right == nil) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || other != value {
			return false
		}
	}
	return true
}

func TestLongestAllocationRootGeneratedDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(204871))
	for iteration := 0; iteration < 1500; iteration++ {
		roots := map[string]*int{}
		base := fmt.Sprintf("alloc:%d", rng.Intn(12))
		address := base
		depth := rng.Intn(6)
		for field := 0; field < depth; field++ {
			if rng.Intn(2) == 0 {
				roots[address] = nil
			}
			address += fmt.Sprintf(".field:%d", rng.Intn(5))
		}
		if rng.Intn(2) == 0 {
			roots[address] = nil
		}
		// Add unrelated and delimiter-bearing candidate IDs to exercise exact
		// dictionary membership independently of the generated path.
		roots[fmt.Sprintf("unrelated:%d", iteration)] = nil
		if iteration%3 == 0 {
			roots[base+".field:embedded"] = nil
		}
		allowEmpty := iteration%2 == 0
		if iteration%7 == 0 {
			roots[""] = nil
		}
		got, gotFound := longestAllocationRoot(address, roots, allowEmpty)
		want, wantFound := legacyAllocationRoot(address, roots, allowEmpty)
		if got != want || gotFound != wantFound {
			t.Fatalf("iteration %d address %q: optimized=(%q,%v), legacy=(%q,%v)", iteration, address, got, gotFound, want, wantFound)
		}
		if gotFound {
			if _, keyExists := roots[got]; !keyExists || got == "" && !allowEmpty || address != got && !strings.HasPrefix(address, got+".field:") {
				t.Fatalf("iteration %d selected invalid prefix %q for %q", iteration, got, address)
			}
			for candidate := range roots {
				if candidate == "" && !allowEmpty {
					continue
				}
				if (address == candidate || strings.HasPrefix(address, candidate+".field:")) && len(candidate) > len(got) {
					t.Fatalf("iteration %d selected %q instead of longer matching root %q", iteration, got, candidate)
				}
			}
		} else {
			for candidate := range roots {
				if candidate != "" || allowEmpty {
					if address == candidate || strings.HasPrefix(address, candidate+".field:") {
						t.Fatalf("iteration %d missed matching root %q for %q", iteration, candidate, address)
					}
				}
			}
		}
	}
}

func TestLongestAllocationRootDoesNotCacheMissesOrMutateMap(t *testing.T) {
	short := 7
	roots := map[string]*int{"short": &short}
	before := len(roots)
	if got, found := longestAllocationRoot("later.field:0", roots, false); found || got != "" {
		t.Fatalf("initial miss = (%q,%v), want empty miss", got, found)
	}
	// Allocations are discovered incrementally during flow rounds. A previous
	// rootless miss must not hide a later inserted allocation.
	roots["later"] = &short
	if got, found := longestAllocationRoot("later.field:0", roots, false); !found || got != "later" {
		t.Fatalf("lookup after insertion = (%q,%v), want later root", got, found)
	}
	// A nil-valued map entry is still a present, deeper allocation key and must
	// shadow a shorter valid root exactly as the old full scan did.
	roots["later.field:0"] = nil
	if got, found := longestAllocationRoot("later.field:0.field:1", roots, false); !found || got != "later.field:0" {
		t.Fatalf("nil-valued deepest root = (%q,%v), want deepest key", got, found)
	}
	if len(roots) != before+2 || roots["short"] != &short {
		t.Fatalf("lookup mutated allocation roots unexpectedly: %#v", roots)
	}
}

func BenchmarkLongestAllocationRootUnrelatedAllocations(b *testing.B) {
	for _, count := range []int{100, 10000} {
		b.Run(fmt.Sprintf("unrelated_%d", count), func(b *testing.B) {
			roots := make(map[string]*int, count)
			for i := 0; i < count; i++ {
				roots[fmt.Sprintf("allocation:%d", i)] = nil
			}
			const address = "rootless.field:3.field:1"
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				allocationRootBenchmarkSink, allocationRootBenchmarkFound = longestAllocationRoot(address, roots, false)
			}
		})
		b.Run(fmt.Sprintf("legacy_scan_%d", count), func(b *testing.B) {
			roots := make(map[string]*int, count)
			for i := 0; i < count; i++ {
				roots[fmt.Sprintf("allocation:%d", i)] = nil
			}
			const address = "rootless.field:3.field:1"
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				allocationRootBenchmarkSink, allocationRootBenchmarkFound = legacyAllocationRoot(address, roots, false)
			}
		})
	}
}

var allocationRootBenchmarkSink string
var allocationRootBenchmarkFound bool
