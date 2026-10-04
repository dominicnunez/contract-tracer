package contracttrace

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

var flowSnapshotSink flowValue

func TestFlowReadSnapshotIsolationAndAllocationBound(t *testing.T) {
	value := new(ssa.Alloc)
	source := emptyFlow()
	source.errors = map[string]bool{}
	source.scalars = map[string]bool{}
	source.sqlUnknown, source.functionUnknown, source.functionNil, source.interfaceUnknown, source.stringUnknown, source.scalarUnknown = true, true, true, true, true, true
	for i := 0; i < 64; i++ {
		key := fmt.Sprintf("origin-%d", i)
		source.strings[key], source.addresses[key], source.effects[key], source.errors[key], source.scalars[key] = true, true, true, true, true
		source.functions[new(ssa.Function)] = true
	}
	a := &flowAnalysis{values: map[ssa.Value]flowValue{value: source}}
	first := a.get(value)
	if !first.sqlUnknown || !first.functionUnknown || !first.functionNil || !first.interfaceUnknown || !first.stringUnknown || !first.scalarUnknown {
		t.Fatal("snapshot lost unresolved domains")
	}
	for name, candidates := range map[string]map[string]bool{"strings": first.strings, "addresses": first.addresses, "effects": first.effects, "errors": first.errors, "scalars": first.scalars} {
		if len(candidates) != 64 {
			t.Fatalf("%s snapshot lost candidates", name)
		}
		delete(candidates, "origin-0")
		candidates["new-origin"] = true
	}
	for function := range first.functions {
		delete(first.functions, function)
		break
	}
	second := a.get(value)
	for name, candidates := range map[string]map[string]bool{"strings": second.strings, "addresses": second.addresses, "effects": second.effects, "errors": second.errors, "scalars": second.scalars} {
		if len(candidates) != 64 || !candidates["origin-0"] || candidates["new-origin"] {
			t.Errorf("%s snapshot mutation changed stored origins", name)
		}
	}
	if len(second.functions) != 64 {
		t.Error("snapshot mutation changed stored callable origins")
	}
	allocations := testing.AllocsPerRun(100, func() { flowSnapshotSink = a.get(value) })
	t.Logf("64 candidates in all six domains: %.0f allocations per isolated read", allocations)
	if allocations > 24 {
		t.Errorf("bounded snapshot read rebuilds candidates excessively: %.0f allocations, budget 24", allocations)
	}
}

func TestBoundReceiverSnapshotIsolation(t *testing.T) {
	fn := new(ssa.Function)
	source := emptyFlow()
	source.boundReceivers = map[*ssa.Function]boundReceiverCandidates{
		fn: {addresses: map[string]bool{"database:primary": true}},
	}
	first := snapshotFlow(source)
	receivers := first.boundReceivers[fn]
	delete(receivers.addresses, "database:primary")
	receivers.addresses["database:mutated"] = true
	first.boundReceivers[fn] = receivers
	if !source.boundReceivers[fn].addresses["database:primary"] || source.boundReceivers[fn].addresses["database:mutated"] {
		t.Fatal("nested receiver snapshot mutation changed stored candidates")
	}

	empty := emptyFlow()
	empty.boundReceivers = map[*ssa.Function]boundReceiverCandidates{}
	emptySnapshot := snapshotFlow(empty)
	emptySnapshot.boundReceivers[fn] = boundReceiverCandidates{addresses: map[string]bool{"new": true}}
	if len(empty.boundReceivers) != 0 {
		t.Fatal("empty bound receiver snapshot map shared with storage")
	}
	allocations := testing.AllocsPerRun(100, func() { flowSnapshotSink = snapshotFlow(source) })
	t.Logf("bound receiver snapshot: %.0f allocations for one target and one address", allocations)
}

func TestZeroAggregateSnapshotIsolationAndEmptyAllocation(t *testing.T) {
	typ := types.NewStruct([]*types.Var{types.NewVar(token.NoPos, nil, "callback", types.NewSignatureType(nil, nil, nil, nil, nil, false))}, nil)
	source := emptyFlow()
	source.zeroAggregates = map[types.Type]bool{typ: true}
	snapshot := snapshotFlow(source)
	delete(snapshot.zeroAggregates, typ)
	snapshot.zeroAggregates[types.NewStruct(nil, nil)] = true
	if !source.zeroAggregates[typ] || len(source.zeroAggregates) != 1 {
		t.Fatal("zero aggregate snapshot mutation changed stored candidates")
	}
	if empty := snapshotFlow(emptyFlow()); empty.zeroAggregates != nil {
		t.Fatal("empty snapshot should not allocate an empty zero aggregate map")
	}
}

func TestBoundReceiverCaptureRequiresConfiguredCallRules(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "bound.go", "package bound\ntype DB struct{}\nfunc (*DB) Exec(string) {}\nfunc use(db *DB) { db.Exec(\"query\") }\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	typed, err := new(types.Config).Check("example.com/bound", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	program := ssa.NewProgram(fset, 0)
	ssaPackage := program.CreatePackage(typed, []*ast.File{file}, info, true)
	ssaPackage.Build()
	var methodValue *types.Selection
	ast.Inspect(file, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "Exec" {
			methodValue = info.Selections[selector]
		}
		return true
	})
	if methodValue == nil {
		t.Fatal("fixture did not produce a bound method selection")
	}
	target := program.MethodValue(methodValue)
	if target == nil {
		t.Fatal("SSA did not create the bound method wrapper")
	}
	receiver := emptyFlow()
	receiver.addresses["database:primary"] = true
	receiver.interfaceUnknown = true
	result := emptyFlow()
	captureBoundMethodReceiver(&result, target, receiver, false)
	if len(result.boundReceivers) != 0 {
		t.Fatal("default configuration captured CallRules-only receiver metadata")
	}
	captureBoundMethodReceiver(&result, target, receiver, true)
	if !result.boundReceivers[target].addresses["database:primary"] || !result.boundReceivers[target].unknown {
		t.Fatal("configured CallRules omitted known receiver or interface uncertainty")
	}
}

func TestFlowReadSnapshotKeepsIntrinsicAndOversizedCandidates(t *testing.T) {
	value := new(ssa.Alloc)
	oversized := emptyFlow()
	oversized.scalars = map[string]bool{}
	for i := 0; i < 65; i++ {
		key := fmt.Sprintf("origin-%02d", i)
		oversized.strings[key] = true
		oversized.scalars[key] = true
	}
	a := &flowAnalysis{values: map[ssa.Value]flowValue{value: oversized}}
	read := a.get(value)
	if len(read.strings) != 64 || len(read.scalars) != 64 || !read.stringUnknown || !read.scalarUnknown || !a.coverage.Widened {
		t.Fatal("oversized source bypassed bounded merging or uncertainty")
	}
	if !read.strings["origin-00"] || read.strings["origin-64"] || !read.scalars["origin-00"] || read.scalars["origin-64"] {
		t.Fatal("oversized source lost deterministic candidate selection")
	}
	literal := ssa.NewConst(constant.MakeString("literal"), types.Typ[types.String])
	stored := emptyFlow()
	stored.strings["stored"] = true
	a.values[literal] = stored
	read = a.get(literal)
	if !read.strings["literal"] || !read.strings["stored"] {
		t.Fatal("intrinsic literal or stored candidates lost")
	}
	function := new(ssa.Function)
	if !a.get(function).functions[function] {
		t.Fatal("intrinsic callable origin lost")
	}
	empty := emptyFlow()
	empty.errors = map[string]bool{}
	empty.scalars = map[string]bool{}
	empty.boundReceivers = map[*ssa.Function]boundReceiverCandidates{}
	a.values[value] = empty
	read = a.get(value)
	read.strings["new"] = true
	read.addresses["new"] = true
	read.effects["new"] = true
	read.functions[function] = true
	read.boundReceivers[function] = boundReceiverCandidates{addresses: map[string]bool{"new": true}}
	if len(a.values[value].strings) != 0 || len(a.values[value].addresses) != 0 || len(a.values[value].effects) != 0 || len(a.values[value].functions) != 0 {
		t.Fatal("empty snapshot maps shared with storage")
	}
	if len(a.values[value].boundReceivers) != 0 {
		t.Fatal("empty bound receiver snapshot map shared with storage")
	}
	if read.errors != nil || read.scalars != nil {
		t.Fatal("empty lazy domains unexpectedly share maps")
	}
}

func TestBoundReceiverUnknownCanMergeBeforeKnownOrigin(t *testing.T) {
	fn := new(ssa.Function)
	a := &flowAnalysis{}
	merged := emptyFlow()
	unknownFirst := emptyFlow()
	unknownFirst.boundReceivers = map[*ssa.Function]boundReceiverCandidates{fn: {unknown: true}}
	a.merge(&merged, unknownFirst)
	knownLater := emptyFlow()
	knownLater.boundReceivers = map[*ssa.Function]boundReceiverCandidates{fn: {addresses: map[string]bool{"database:primary": true}}}
	a.merge(&merged, knownLater)
	got := merged.boundReceivers[fn]
	if !got.unknown || !got.addresses["database:primary"] {
		t.Fatalf("unknown-first merge lost known receiver or uncertainty: %+v", got)
	}
}

func TestBoundReceiverMetadataWideningIsBoundedAndDeterministic(t *testing.T) {
	fset := token.NewFileSet()
	var source strings.Builder
	source.WriteString("package bounded\n")
	for i := 0; i < maxFlowValues+1; i++ {
		fmt.Fprintf(&source, "func Target%02d() {}\n", i)
	}
	file, err := parser.ParseFile(fset, "bounded.go", source.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}}
	typed, err := new(types.Config).Check("example.com/bounded", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	program := ssa.NewProgram(fset, 0)
	ssaPackage := program.CreatePackage(typed, []*ast.File{file}, info, true)
	ssaPackage.Build()

	functions := map[string]*ssa.Function{}
	for i := 0; i < maxFlowValues+1; i++ {
		name := fmt.Sprintf("Target%02d", i)
		functions[name] = ssaPackage.Func(name)
	}
	mergeTargets := func(reverse bool) (*flowAnalysis, flowValue) {
		analysis := &flowAnalysis{}
		source := emptyFlow()
		source.boundReceivers = map[*ssa.Function]boundReceiverCandidates{}
		for i := 0; i < maxFlowValues+1; i++ {
			index := i
			if reverse {
				index = maxFlowValues - i
			}
			name := fmt.Sprintf("Target%02d", index)
			fn := functions[name]
			source.boundReceivers[fn] = boundReceiverCandidates{addresses: map[string]bool{"database:" + name: true}}
		}
		value := emptyFlow()
		analysis.merge(&value, source)
		return analysis, value
	}
	firstAnalysis, first := mergeTargets(false)
	secondAnalysis, second := mergeTargets(true)
	if len(first.boundReceivers) != maxFlowValues || !first.boundReceiverUnknown || !firstAnalysis.coverage.Widened {
		t.Fatalf("target metadata was not bounded with an explicit unknown: targets=%d unknown=%t widened=%t", len(first.boundReceivers), first.boundReceiverUnknown, firstAnalysis.coverage.Widened)
	}
	if len(second.boundReceivers) != maxFlowValues || !second.boundReceiverUnknown || !secondAnalysis.coverage.Widened {
		t.Fatalf("reverse target order bypassed bound: targets=%d unknown=%t widened=%t", len(second.boundReceivers), second.boundReceiverUnknown, secondAnalysis.coverage.Widened)
	}
	for i := 0; i < maxFlowValues; i++ {
		name := fmt.Sprintf("Target%02d", i)
		fn := functions[name]
		if !first.boundReceivers[fn].addresses["database:"+name] || !second.boundReceivers[fn].addresses["database:"+name] {
			t.Fatalf("bounded target selection depends on insertion order at %s", name)
		}
	}

	fn := functions["Target00"]
	mergeOrigins := func(reverse bool) (*flowAnalysis, boundReceiverCandidates) {
		origins := emptyFlow()
		one := boundReceiverCandidates{addresses: map[string]bool{}}
		for i := 0; i < maxFlowValues+1; i++ {
			index := i
			if reverse {
				index = maxFlowValues - i
			}
			one.addresses[fmt.Sprintf("database:origin-%02d", index)] = true
		}
		origins.boundReceivers = map[*ssa.Function]boundReceiverCandidates{fn: one}
		analysis := &flowAnalysis{}
		value := emptyFlow()
		analysis.merge(&value, origins)
		return analysis, value.boundReceivers[fn]
	}
	firstOriginsAnalysis, firstOrigins := mergeOrigins(false)
	secondOriginsAnalysis, secondOrigins := mergeOrigins(true)
	for name, result := range map[string]struct {
		analysis *flowAnalysis
		value    boundReceiverCandidates
	}{"forward": {firstOriginsAnalysis, firstOrigins}, "reverse": {secondOriginsAnalysis, secondOrigins}} {
		if len(result.value.addresses) != maxFlowValues || !result.value.unknown || !result.analysis.coverage.Widened {
			t.Fatalf("%s receiver origins were not bounded with explicit uncertainty: origins=%d unknown=%t widened=%t", name, len(result.value.addresses), result.value.unknown, result.analysis.coverage.Widened)
		}
	}
	for i := 0; i < maxFlowValues; i++ {
		key := fmt.Sprintf("database:origin-%02d", i)
		if !firstOrigins.addresses[key] || !secondOrigins.addresses[key] {
			t.Fatalf("bounded receiver-origin selection depends on insertion order at %s", key)
		}
	}
}
