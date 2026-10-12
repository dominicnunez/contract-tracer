package contracttrace

import (
	"fmt"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCallbackCacheTracksMapMembershipAndDynamicTypes(t *testing.T) {
	signature := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	first, second := &ssa.Function{Signature: signature}, &ssa.Function{Signature: signature}
	a := &flowAnalysis{values: map[ssa.Value]flowValue{}, memory: map[string]flowValue{}, addressTypes: map[string]types.Type{}}
	value := ssa.NewConst(nil, types.NewMap(types.Typ[types.String], signature))
	root := emptyFlow()
	root.addresses["map"] = true
	a.put(value, root)
	callback := emptyFlow()
	callback.functions[first] = true
	a.storeMap("map", "a", callback)
	if !a.accessibleCallbacks(value)[first] {
		t.Fatal("initial map callback missing")
	}
	callback = emptyFlow()
	callback.functions[second] = true
	a.storeMap("map", "b", callback)
	if !a.accessibleCallbacks(value)[second] {
		t.Fatal("new map entry hidden by cached walk")
	}
	iface := ssa.NewConst(nil, types.NewInterfaceType(nil, nil).Complete())
	root = emptyFlow()
	root.addresses["object"] = true
	a.put(iface, root)
	a.addressTypes["object"] = types.NewStruct(nil, nil)
	if len(a.accessibleCallbacks(iface)) != 0 {
		t.Fatal("empty object invented callback")
	}
	a.addressTypes["object"] = types.NewStruct([]*types.Var{types.NewVar(token.NoPos, nil, "Run", signature)}, nil)
	a.store("object.field:0", callback)
	if !a.accessibleCallbacks(iface)[second] {
		t.Fatal("new dynamic object shape hidden by cached walk")
	}
}

func TestCallbackStorageWarmWalkRetainsMutation(t *testing.T) {
	signature := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	fields := []*types.Var{}
	for i := 0; i < 32; i++ {
		fields = append(fields, types.NewVar(token.NoPos, nil, fmt.Sprintf("Callback%d", i), signature))
	}
	value := ssa.NewConst(nil, types.NewPointer(types.NewStruct(fields, nil)))
	a := &flowAnalysis{values: map[ssa.Value]flowValue{}, memory: map[string]flowValue{}}
	root := emptyFlow()
	root.addresses["record"] = true
	a.put(value, root)
	first, second := &ssa.Function{Signature: signature}, &ssa.Function{Signature: signature}
	callback := emptyFlow()
	callback.functions[first] = true
	for i := 0; i < 32; i++ {
		a.store(fmt.Sprintf("record.field:%d", i), callback)
	}
	if !a.accessibleCallbacks(value)[first] {
		t.Fatal("cold walk lost callback")
	}
	allocations := testing.AllocsPerRun(5, func() {
		if !a.accessibleCallbacks(value)[first] {
			t.Fatal("warm walk lost callback")
		}
	})
	if allocations > 40 {
		t.Errorf("unchanged 32-field storage walk allocates %.0f objects; want <=40", allocations)
	}
	callback = emptyFlow()
	callback.functions[second] = true
	a.store("record.field:31", callback)
	found := a.accessibleCallbacks(value)
	if !found[first] || !found[second] {
		t.Fatal("function-only mutation was hidden")
	}
}
