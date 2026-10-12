package contracttrace

import (
	"go/types"
	"maps"

	"golang.org/x/tools/go/ssa"
)

// Only callback candidate walks are cached. Storage-cell enumeration and
// iterator/effect walks remain live; their dependencies exceed these reads.
type callbackStorageCache struct {
	roots     flowValue
	reads     map[string]uint64
	types     map[string]types.Type
	mapSizes  map[string]int
	functions map[*ssa.Function]bool
	volatile  bool
}

func (c *callbackStorageCache) valid(a *flowAnalysis, roots flowValue) bool {
	if !maps.Equal(c.roots.functions, roots.functions) || !maps.Equal(c.roots.addresses, roots.addresses) || !maps.Equal(c.roots.effects, roots.effects) {
		return false
	}
	for address, version := range c.reads {
		if a.memoryVersions[address] != version {
			return false
		}
	}
	for address, typ := range c.types {
		if a.addressTypes[address] != typ {
			return false
		}
	}
	for address, size := range c.mapSizes {
		if len(a.mapEntries[address]) != size {
			return false
		}
	}
	return true
}
