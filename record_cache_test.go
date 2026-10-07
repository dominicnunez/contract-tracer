package contracttrace

import (
	"fmt"
	"go/types"
	"reflect"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestRepeatedRecordAddressExpansionHasBoundedAllocations(t *testing.T) {
	measure := func(width int) float64 {
		a := &flowAnalysis{values: map[ssa.Value]flowValue{}, memory: map[string]flowValue{}}
		value := ssa.NewConst(nil, types.NewPointer(types.NewStruct(nil, nil)))
		root := emptyFlow()
		root.addresses["root"] = true
		a.put(value, root)
		aliases := emptyFlow()
		for i := 0; i < width; i++ {
			aliases.addresses[fmt.Sprintf("alias:%02d", i)] = true
		}
		a.store("root", aliases)
		for alias := range aliases.addresses {
			a.store(alias, aliases)
		}
		return testing.AllocsPerRun(10, func() {
			if got := len(a.recordAddresses(value)); got != width+1 {
				t.Fatalf("reachable addresses=%d, want %d", got, width+1)
			}
		})
	}
	small, large := measure(3), measure(63)
	t.Logf("allocations per unchanged expansion: small=%.0f large=%.0f", small, large)
	if large > small*4+10 {
		t.Fatalf("unchanged alias expansion allocates with closure width: small=%.0f large=%.0f", small, large)
	}
}

func TestRecordAddressExpansionObservesLaterMemoryAndRootAliases(t *testing.T) {
	a := &flowAnalysis{values: map[ssa.Value]flowValue{}, memory: map[string]flowValue{}}
	value := ssa.NewConst(nil, types.NewPointer(types.NewStruct(nil, nil)))
	putRoot := func(name string) { flow := emptyFlow(); flow.addresses[name] = true; a.put(value, flow) }
	storeAlias := func(from, to string) { flow := emptyFlow(); flow.addresses[to] = true; a.store(from, flow) }
	check := func(expected []string) {
		t.Helper()
		if got := a.recordAddresses(value); !reflect.DeepEqual(got, expected) {
			t.Fatalf("addresses=%v, want %v", got, expected)
		}
	}
	putRoot("A")
	storeAlias("A", "B")
	check([]string{"A", "B"})
	storeAlias("B", "C") // B previously had no memory; that missing read matters.
	check([]string{"A", "B", "C"})
	storeAlias("C", "A") // A pointer cycle must still terminate.
	check([]string{"A", "B", "C"})
	putRoot("D")
	storeAlias("D", "E")
	check([]string{"A", "B", "C", "D", "E"})
}
