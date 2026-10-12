package contracttrace

import (
	"fmt"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
)

// Wildcard lookup should scale with the selected map, rather than all modeled
// memory in the repository. Setup stays outside the measured operation.
func BenchmarkWildcardMapLookup(b *testing.B) {
	for _, unrelated := range []int{100, 10000} {
		b.Run(fmt.Sprintf("unrelated_%d", unrelated), func(b *testing.B) {
			a := &flowAnalysis{memory: map[string]flowValue{}, values: map[ssa.Value]flowValue{}}
			for i := 0; i < unrelated; i++ {
				a.memory[fmt.Sprintf("unrelated:%d", i)] = emptyFlow()
			}
			value := emptyFlow()
			value.strings["EVENT"] = true
			a.storeMap("map:selected", `"topic"`, value)
			mapValue := ssa.NewConst(nil, types.NewMap(types.Typ[types.String], types.Typ[types.String]))
			address := emptyFlow()
			address.addresses["map:selected"] = true
			a.values[mapValue] = address
			unknownKey := &ssa.Parameter{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result := a.mapLookup(mapValue, unknownKey)
				if !result.strings["EVENT"] {
					b.Fatal("map payload lost")
				}
			}
		})
	}
}
