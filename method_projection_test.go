package contracttrace

import (
	"go/importer"
	"go/token"
	"go/types"
	"testing"
)

func TestPromotedMethodProjectionFollowsTypedInitialPointerStorageAlias(t *testing.T) {
	contextPackage, err := importer.Default().Import("context")
	if err != nil {
		t.Fatal(err)
	}
	contextType := contextPackage.Scope().Lookup("Context").Type()
	contextInterface := contextType.Underlying().(*types.Interface)
	contextInterface.Complete()
	var done *types.Func
	for i := 0; i < contextInterface.NumMethods(); i++ {
		if contextInterface.Method(i).Name() == "Done" {
			done = contextInterface.Method(i)
		}
	}
	if done == nil {
		t.Fatal("context.Context.Done not found")
	}

	pkg := types.NewPackage("example.com/contextprojection", "contextprojection")
	field := types.NewField(token.NoPos, contextPackage, "Context", contextType, true)
	wrapper := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Wrapper", nil), types.NewStruct([]*types.Var{field}, nil), nil)
	wrapperPointer := types.NewPointer(wrapper)
	root, copyAddress := "alloc:pointer-cell", "alloc:wrapper-copy"
	contextOrigin := "context:known-context"
	a := &flowAnalysis{
		memory: map[string]flowValue{
			root:                         {addresses: map[string]bool{copyAddress: true}},
			fieldAddress(copyAddress, 0): {addresses: map[string]bool{contextOrigin: true}},
		},
		addressTypes: map[string]types.Type{copyAddress: wrapperPointer},
	}
	outer := emptyFlow()
	outer.addresses[root] = true
	outer.strings["outer-wrapper-payload"] = true

	projected, signature, status := a.projectMethodExpressionReceiver(outer, wrapperPointer, done)
	if status != methodReceiverProjectionPromoted || signature == nil {
		t.Fatalf("projection status/signature = %v/%v, want promoted Context.Done", status, signature)
	}
	if !projected.addresses[contextOrigin] || projected.addresses[root] || projected.addresses[copyAddress] || projected.strings["outer-wrapper-payload"] {
		t.Fatalf("projection did not follow the typed initial pointer alias: %+v", projected)
	}
	if projected.interfaceUnknown {
		t.Fatalf("complete typed alias path was marked uncertain: %+v", projected)
	}
}

func TestPromotedMethodProjectionKeepsRootFieldAndTypedAliasAlternatives(t *testing.T) {
	pkg := types.NewPackage("example.com/contextprojectionbranches", "contextprojectionbranches")
	lease, closeMethod := projectionLeaseType(pkg)
	wrapper := projectionStructType(pkg, "Wrapper", types.NewPointer(lease), true)
	wrapperPointer := types.NewPointer(wrapper)
	root, copyAddress := "alloc:pointer-cell", "alloc:wrapper-copy"
	rootOrigin, copyOrigin := "known:root-lease", "known:copy-lease"
	a := &flowAnalysis{
		memory: map[string]flowValue{
			root:                         {addresses: map[string]bool{copyAddress: true}},
			fieldAddress(root, 0):        {addresses: map[string]bool{rootOrigin: true}},
			fieldAddress(copyAddress, 0): {addresses: map[string]bool{copyOrigin: true}},
		},
		addressTypes: map[string]types.Type{copyAddress: wrapperPointer},
	}
	outer := emptyFlow()
	outer.addresses[root] = true

	projected, _, status := a.projectMethodExpressionReceiver(outer, wrapperPointer, closeMethod)
	if status != methodReceiverProjectionPromoted || !projected.addresses[rootOrigin] || !projected.addresses[copyOrigin] {
		t.Fatalf("projection dropped a root/alias receiver branch: status=%v flow=%+v", status, projected)
	}
}

func TestPromotedMethodProjectionFollowsBoundedInitialAliasChainAndCycle(t *testing.T) {
	pkg := types.NewPackage("example.com/contextprojectionchain", "contextprojectionchain")
	lease, closeMethod := projectionLeaseType(pkg)
	wrapper := projectionStructType(pkg, "Wrapper", types.NewPointer(lease), true)
	wrapperPointer := types.NewPointer(wrapper)
	root, first, second := "alloc:pointer-cell", "alloc:wrapper-copy-one", "alloc:wrapper-copy-two"
	origin := "known:terminal-lease"
	a := &flowAnalysis{
		memory: map[string]flowValue{
			root:                    {addresses: map[string]bool{first: true}},
			first:                   {addresses: map[string]bool{second: true}},
			fieldAddress(second, 0): {addresses: map[string]bool{origin: true}},
			"alloc:cycle-a":         {addresses: map[string]bool{"alloc:cycle-b": true}},
			"alloc:cycle-b":         {addresses: map[string]bool{"alloc:cycle-a": true}},
		},
		addressTypes: map[string]types.Type{first: wrapperPointer, second: wrapperPointer, "alloc:cycle-a": wrapperPointer, "alloc:cycle-b": wrapperPointer},
	}
	outer := emptyFlow()
	outer.addresses[root] = true
	projected, _, status := a.projectMethodExpressionReceiver(outer, wrapperPointer, closeMethod)
	if status != methodReceiverProjectionPromoted || !projected.addresses[origin] || projected.interfaceUnknown {
		t.Fatalf("typed two-hop alias chain did not resolve exactly: status=%v flow=%+v", status, projected)
	}

	cycle := emptyFlow()
	cycle.addresses["alloc:cycle-a"] = true
	cyclic, _, _ := a.projectMethodExpressionReceiver(cycle, wrapperPointer, closeMethod)
	if !cyclic.interfaceUnknown {
		t.Fatalf("cyclic initial storage aliases were not retained as unresolved: %+v", cyclic)
	}
}

func TestPromotedMethodProjectionDoesNotFollowUntypedInitialAlias(t *testing.T) {
	pkg := types.NewPackage("example.com/contextprojectionuntyped", "contextprojectionuntyped")
	lease, closeMethod := projectionLeaseType(pkg)
	wrapper := projectionStructType(pkg, "Wrapper", types.NewPointer(lease), true)
	root, unrelated := "alloc:pointer-cell", "alloc:unrelated"
	outer := emptyFlow()
	outer.addresses[root] = true
	a := &flowAnalysis{memory: map[string]flowValue{
		root:                       {addresses: map[string]bool{unrelated: true}},
		fieldAddress(unrelated, 0): {addresses: map[string]bool{"wrong:lease": true}},
	}}
	projected, _, status := a.projectMethodExpressionReceiver(outer, types.NewPointer(wrapper), closeMethod)
	if status != methodReceiverProjectionPromoted || projected.addresses["wrong:lease"] || !projected.interfaceUnknown {
		t.Fatalf("untyped storage alias was followed as an API receiver: status=%v flow=%+v", status, projected)
	}
}

func TestPromotedMethodProjectionRetainsKnownAndMissingBranches(t *testing.T) {
	pkg := types.NewPackage("example.com/projection", "projection")
	lease, closeMethod := projectionLeaseType(pkg)
	wrapper := projectionStructType(pkg, "Wrapper", types.NewPointer(lease), true)
	wrapperPointer := types.NewPointer(wrapper)
	root := "alloc:outer-wrapper"
	missingRoot := "alloc:wrapper-with-missing-embedded-field"
	leaseOrigin := "configured:Acquire:Scenario:result:0"
	a := &flowAnalysis{memory: map[string]flowValue{}}
	a.memory[fieldAddress(root, 0)] = flowValue{addresses: map[string]bool{leaseOrigin: true}}
	outer := emptyFlow()
	outer.addresses[root] = true
	outer.addresses[missingRoot] = true
	outer.strings["outer-wrapper-payload"] = true

	projected, signature, status := a.projectMethodExpressionReceiver(outer, wrapperPointer, closeMethod)
	if status != methodReceiverProjectionPromoted {
		t.Fatalf("status = %v, want promoted", status)
	}
	if signature == nil || !types.Identical(signature.Recv().Type(), types.NewPointer(lease)) {
		t.Fatalf("selected receiver signature = %v, want *Lease", signature)
	}
	if !projected.addresses[leaseOrigin] {
		t.Fatalf("supported embedding branch lost Lease origin: %+v", projected)
	}
	if projected.addresses[root] || projected.addresses[missingRoot] || projected.strings["outer-wrapper-payload"] {
		t.Fatalf("outer wrapper identity or payload leaked into API receiver candidates: %+v", projected)
	}
	if !projected.interfaceUnknown {
		t.Fatalf("missing embedding branch was hidden by the supported branch: %+v", projected)
	}
}

func TestPromotedMethodProjectionCarriesEmbeddedStringValue(t *testing.T) {
	pkg := types.NewPackage("example.com/keyprojection", "keyprojection")
	key := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "ResourceKey", nil), types.Typ[types.String], nil)
	methodType := types.NewSignatureType(types.NewVar(token.NoPos, pkg, "", key), nil, nil,
		types.NewTuple(), types.NewTuple(), false)
	method := types.NewFunc(token.NoPos, pkg, "ReleaseKey", methodType)
	key.AddMethod(method)
	wrapper := projectionStructType(pkg, "Envelope", key, true)
	root := "alloc:envelope"
	outer := emptyFlow()
	outer.addresses[root] = true
	outer.strings["not-the-resource-key"] = true
	a := &flowAnalysis{memory: map[string]flowValue{fieldAddress(root, 0): {strings: map[string]bool{"tenant-key": true}}}}

	projected, signature, status := a.projectMethodExpressionReceiver(outer, types.NewPointer(wrapper), method)
	if status != methodReceiverProjectionPromoted || signature == nil || !types.Identical(signature.Recv().Type(), key) {
		t.Fatalf("projection status/signature = %v/%v, want promoted ResourceKey receiver", status, signature)
	}
	if !projected.strings["tenant-key"] || projected.strings["not-the-resource-key"] {
		t.Fatalf("projection did not isolate the embedded string payload: %+v", projected)
	}
}

func TestPromotedPointerReceiverUsesEmbeddedScalarFieldCell(t *testing.T) {
	pkg := types.NewPackage("example.com/keypointerprojection", "keypointerprojection")
	key := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "ResourceKey", nil), types.Typ[types.String], nil)
	methodType := types.NewSignatureType(types.NewVar(token.NoPos, pkg, "", types.NewPointer(key)), nil, nil,
		types.NewTuple(), types.NewTuple(), false)
	method := types.NewFunc(token.NoPos, pkg, "ReleaseKey", methodType)
	key.AddMethod(method)
	wrapper := projectionStructType(pkg, "Envelope", key, true)
	root := "alloc:envelope"
	cell := fieldAddress(root, 0)
	outer := emptyFlow()
	outer.addresses[root] = true
	outer.strings["outer-payload"] = true
	a := &flowAnalysis{memory: map[string]flowValue{cell: {strings: map[string]bool{"tenant-key": true}}}}

	projected, signature, status := a.projectMethodExpressionReceiver(outer, types.NewPointer(wrapper), method)
	if status != methodReceiverProjectionPromoted || signature == nil || !types.Identical(signature.Recv().Type(), types.NewPointer(key)) {
		t.Fatalf("projection status/signature = %v/%v, want promoted *ResourceKey receiver", status, signature)
	}
	if !projected.addresses[cell] {
		t.Fatalf("pointer receiver on embedded scalar lost the addressable field cell: %+v", projected)
	}
	if !projected.strings["tenant-key"] || projected.strings["outer-payload"] {
		t.Fatalf("projection did not preserve only the embedded field payload: %+v", projected)
	}
}

func TestPromotedValueReceiverDereferencesEmbeddedPointer(t *testing.T) {
	pkg := types.NewPackage("example.com/keypointervalueprojection", "keypointervalueprojection")
	key := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "ResourceKey", nil), types.Typ[types.String], nil)
	methodType := types.NewSignatureType(types.NewVar(token.NoPos, pkg, "", key), nil, nil,
		types.NewTuple(), types.NewTuple(), false)
	method := types.NewFunc(token.NoPos, pkg, "ReleaseKey", methodType)
	key.AddMethod(method)
	wrapper := projectionStructType(pkg, "Envelope", types.NewPointer(key), true)
	root := "alloc:envelope"
	keyAddress := "alloc:key-value"
	outer := emptyFlow()
	outer.addresses[root] = true
	a := &flowAnalysis{memory: map[string]flowValue{
		fieldAddress(root, 0): {addresses: map[string]bool{keyAddress: true}},
		keyAddress:            {strings: map[string]bool{"pointer-key-value": true}},
	}}

	projected, signature, status := a.projectMethodExpressionReceiver(outer, types.NewPointer(wrapper), method)
	if status != methodReceiverProjectionPromoted || signature == nil || !types.Identical(signature.Recv().Type(), key) {
		t.Fatalf("projection status/signature = %v/%v, want promoted ResourceKey value receiver", status, signature)
	}
	if !projected.strings["pointer-key-value"] || len(projected.addresses) != 0 {
		t.Fatalf("value receiver through embedded pointer did not load only the pointee string: %+v", projected)
	}
}

func TestPromotedReceiverEffectsDoNotResolveMissingResourceBranch(t *testing.T) {
	pkg := types.NewPackage("example.com/effectprojection", "effectprojection")
	lease, closeMethod := projectionLeaseType(pkg)
	wrapper := projectionStructType(pkg, "Wrapper", types.NewPointer(lease), true)
	root := "alloc:wrapper-with-resource"
	missingRoot := "alloc:wrapper-with-effect-only-field"
	leaseOrigin := "configured:Acquire:Scenario:result:0"
	outer := emptyFlow()
	outer.addresses[root] = true
	outer.addresses[missingRoot] = true
	a := &flowAnalysis{memory: map[string]flowValue{
		fieldAddress(root, 0):        {addresses: map[string]bool{leaseOrigin: true}},
		fieldAddress(missingRoot, 0): {effects: map[string]bool{"nil_done:context:background": true}},
	}}

	projected, _, status := a.projectMethodExpressionReceiver(outer, types.NewPointer(wrapper), closeMethod)
	if status != methodReceiverProjectionPromoted {
		t.Fatalf("projection status = %v, want promoted", status)
	}
	if !projected.addresses[leaseOrigin] || !projected.interfaceUnknown {
		t.Fatalf("effect-only missing branch was hidden by the supported branch: %+v", projected)
	}
}

func TestPromotedMethodProjectionWalksDeepPointerEmbedding(t *testing.T) {
	pkg := types.NewPackage("example.com/deepprojection", "deepprojection")
	lease, closeMethod := projectionLeaseType(pkg)
	middle := projectionStructType(pkg, "Middle", types.NewPointer(lease), true)
	outerType := projectionStructType(pkg, "Outer", types.NewPointer(middle), true)
	root := "alloc:outer"
	middleAddress := "alloc:middle"
	leaseOrigin := "configured:Acquire:DeepScenario:result:0"
	a := &flowAnalysis{memory: map[string]flowValue{
		fieldAddress(root, 0):          {addresses: map[string]bool{middleAddress: true}},
		fieldAddress(middleAddress, 0): {addresses: map[string]bool{leaseOrigin: true}},
	}}
	outer := emptyFlow()
	outer.addresses[root] = true

	projected, signature, status := a.projectMethodExpressionReceiver(outer, types.NewPointer(outerType), closeMethod)
	if status != methodReceiverProjectionPromoted || signature == nil || !types.Identical(signature.Recv().Type(), types.NewPointer(lease)) {
		t.Fatalf("deep projection status/signature = %v/%v, want promoted *Lease receiver", status, signature)
	}
	if !projected.addresses[leaseOrigin] || projected.addresses[root] || projected.addresses[middleAddress] {
		t.Fatalf("deep projection did not select only the embedded Lease origin: %+v", projected)
	}
}

func TestPromotedMethodProjectionFollowsInlineRecordAliases(t *testing.T) {
	pkg := types.NewPackage("example.com/aliasprojection", "aliasprojection")
	waitGroup := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "WaitGroup", nil), types.NewStruct(nil, nil), nil)
	methodType := types.NewSignatureType(types.NewVar(token.NoPos, pkg, "", types.NewPointer(waitGroup)), nil, nil,
		types.NewTuple(), types.NewTuple(), false)
	done := types.NewFunc(token.NoPos, pkg, "Done", methodType)
	waitGroup.AddMethod(done)
	valueGroup := projectionStructType(pkg, "ValueGroup", waitGroup, true)
	pointerGroup := projectionStructType(pkg, "PointerGroup", types.NewPointer(valueGroup), true)
	nestedGroup := projectionStructType(pkg, "NestedGroup", pointerGroup, true)

	root := "alloc:nested-group"
	valueGroupAddress := "alloc:value-group-copy"
	waitGroupAddress := "alloc:wait-group"
	waitGroupOrigin := "known:wait-group-origin"
	a := &flowAnalysis{memory: map[string]flowValue{
		fieldAddress(root, 0):              {addresses: map[string]bool{valueGroupAddress: true}},
		fieldAddress(valueGroupAddress, 0): {addresses: map[string]bool{waitGroupAddress: true}},
		fieldAddress(waitGroupAddress, 0):  {addresses: map[string]bool{waitGroupOrigin: true}},
	}}
	outer := emptyFlow()
	outer.addresses[root] = true

	projected, signature, status := a.projectMethodExpressionReceiver(outer, types.NewPointer(nestedGroup), done)
	if status != methodReceiverProjectionPromoted || signature == nil || !types.Identical(signature.Recv().Type(), types.NewPointer(waitGroup)) {
		t.Fatalf("nested alias projection status/signature = %v/%v, want promoted *WaitGroup receiver", status, signature)
	}
	if !projected.addresses[waitGroupAddress+".field:0"] {
		t.Fatalf("nested inline-record alias did not retain the embedded WaitGroup cell: %+v", projected)
	}
	if projected.interfaceUnknown {
		t.Fatalf("empty copied field cell incorrectly made a resolved alias path incomplete: %+v", projected)
	}
}

func projectionLeaseType(pkg *types.Package) (*types.Named, *types.Func) {
	lease := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Lease", nil), types.NewStruct(nil, nil), nil)
	signature := types.NewSignatureType(types.NewVar(token.NoPos, pkg, "", types.NewPointer(lease)), nil, nil,
		types.NewTuple(), types.NewTuple(), false)
	closeMethod := types.NewFunc(token.NoPos, pkg, "Close", signature)
	lease.AddMethod(closeMethod)
	return lease, closeMethod
}

func projectionStructType(pkg *types.Package, name string, fieldType types.Type, embedded bool) *types.Named {
	fieldName := name
	if embedded {
		switch typ := fieldType.(type) {
		case *types.Pointer:
			if named, ok := typ.Elem().(*types.Named); ok {
				fieldName = named.Obj().Name()
			}
		case *types.Named:
			fieldName = typ.Obj().Name()
		}
	}
	field := types.NewField(token.NoPos, pkg, fieldName, fieldType, embedded)
	typ := types.NewNamed(types.NewTypeName(token.NoPos, pkg, name, nil), types.NewStruct([]*types.Var{field}, []string{""}), nil)
	return typ
}
