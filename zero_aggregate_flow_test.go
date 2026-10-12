package contracttrace

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

func zeroAggregateTestTypes(t *testing.T, extra int) *types.Package {
	t.Helper()
	source := `package zeroagg
type inner struct { task func() }
type outer struct { child inner; pointer *inner; callbacks [2]func(); items [2]inner }
type converted outer
type incompatible struct { text string }
type empty [0]inner
`
	for index := 0; index < extra; index++ {
		source += fmt.Sprintf("type aggregate%d struct { child inner }\n", index)
	}
	if extra > 0 {
		source += "type convertedAggregate0 aggregate0\n"
	}
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "zero.go", source, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := new(types.Config).Check("example.com/zeroagg", fileset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestZeroAggregateProjectionIsTypedAndStopsAtPointers(t *testing.T) {
	pkg := zeroAggregateTestTypes(t, 0)
	analysis := &flowAnalysis{}
	inner := pkg.Scope().Lookup("inner").Type()
	outer := pkg.Scope().Lookup("outer").Type()
	value := emptyFlow()
	if !analysis.addZeroAggregate(&value, outer) {
		t.Fatal("failed to add typed zero outer aggregate")
	}
	child := analysis.zeroAggregateField(value, outer, 0)
	if !child.zeroAggregates[types.Unalias(inner)] || child.functionNil || child.functionUnknown {
		t.Fatalf("inline child did not retain its typed zero candidate: %+v", child)
	}
	task := analysis.zeroAggregateField(child, inner, 0)
	if !task.functionNil || task.functionUnknown {
		t.Fatalf("function field of a typed zero record was not projected to nil: %+v", task)
	}
	pointer := analysis.zeroAggregateField(value, outer, 1)
	if pointer.functionNil || pointer.functionUnknown || len(pointer.zeroAggregates) != 0 || pointer.zeroAggregateUnknown {
		t.Fatalf("zero pointer child was treated as a zero function-bearing record: %+v", pointer)
	}
	array := analysis.zeroAggregateField(value, outer, 2)
	functionElement := analysis.zeroAggregateIndex(array, fieldType(outer, 2))
	if !functionElement.functionNil || functionElement.functionUnknown {
		t.Fatalf("zero array element function was not projected to nil: %+v", functionElement)
	}
	items := analysis.zeroAggregateField(value, outer, 3)
	item := analysis.zeroAggregateIndex(items, fieldType(outer, 3))
	if !item.zeroAggregates[types.Unalias(inner)] {
		t.Fatalf("zero array element did not retain its inline record type: %+v", item)
	}
	zeroEmpty := emptyFlow()
	analysis.addZeroAggregate(&zeroEmpty, pkg.Scope().Lookup("empty").Type())
	if projected := analysis.zeroAggregateIndex(zeroEmpty, pkg.Scope().Lookup("empty").Type()); projected.functionNil || len(projected.zeroAggregates) != 0 {
		t.Fatalf("zero-length array manufactured an element alternative: %+v", projected)
	}
}

func TestZeroAggregateProjectionRetypesConversionsAndWidensConservatively(t *testing.T) {
	pkg := zeroAggregateTestTypes(t, maxFlowValues+1)
	analysis := &flowAnalysis{}
	outer := pkg.Scope().Lookup("outer").Type()
	convertedType := pkg.Scope().Lookup("converted").Type()
	value := emptyFlow()
	analysis.addZeroAggregate(&value, outer)
	converted := analysis.retypeZeroAggregate(value, outer, convertedType)
	if !converted.zeroAggregates[types.Unalias(convertedType)] {
		t.Fatalf("compatible named aggregate conversion dropped the zero marker: %+v", converted)
	}
	convertedTask := analysis.zeroAggregateField(converted, convertedType, 0)
	if !convertedTask.zeroAggregates[types.Unalias(fieldType(convertedType, 0))] {
		t.Fatalf("converted aggregate lost its nested typed zero marker: %+v", convertedTask)
	}
	convertedTask = analysis.zeroAggregateField(convertedTask, fieldType(convertedType, 0), 0)
	if !convertedTask.functionNil {
		t.Fatalf("converted aggregate lost its function-nil alternative: %+v", convertedTask)
	}
	incompatible := analysis.retypeZeroAggregate(value, outer, pkg.Scope().Lookup("incompatible").Type())
	if len(incompatible.zeroAggregates) != 0 || incompatible.zeroAggregateUnknown {
		t.Fatalf("incompatible conversion retained a zero marker: %+v", incompatible)
	}
	wide := emptyFlow()
	for index := 0; index < maxFlowValues+1; index++ {
		if !analysis.addZeroAggregate(&wide, pkg.Scope().Lookup(fmt.Sprintf("aggregate%d", index)).Type()) && index == maxFlowValues {
			t.Fatal("zero aggregate cap failed to record widening")
		}
	}
	if len(wide.zeroAggregates) > maxFlowValues || !wide.zeroAggregateUnknown || !analysis.coverage.Widened {
		t.Fatalf("zero aggregate cap did not widen conservatively: count=%d unknown=%t coverage=%+v", len(wide.zeroAggregates), wide.zeroAggregateUnknown, analysis.coverage)
	}
	knownChild := analysis.zeroAggregateField(wide, pkg.Scope().Lookup("aggregate0").Type(), 0)
	knownTask := analysis.zeroAggregateField(knownChild, fieldType(pkg.Scope().Lookup("aggregate0").Type(), 0), 0)
	if !knownTask.functionUnknown || !knownTask.functionNil {
		t.Fatalf("widening erased an established zero alternative or its uncertainty: %+v", knownTask)
	}
	droppedChild := analysis.zeroAggregateField(wide, pkg.Scope().Lookup(fmt.Sprintf("aggregate%d", maxFlowValues)).Type(), 0)
	droppedTask := analysis.zeroAggregateField(droppedChild, fieldType(pkg.Scope().Lookup(fmt.Sprintf("aggregate%d", maxFlowValues)).Type(), 0), 0)
	if !droppedTask.functionUnknown || droppedTask.functionNil {
		t.Fatalf("widened-only alternative was misclassified as a known nil: %+v", droppedTask)
	}
	convertedWide := analysis.retypeZeroAggregate(wide, pkg.Scope().Lookup("aggregate0").Type(), pkg.Scope().Lookup("convertedAggregate0").Type())
	if !convertedWide.zeroAggregates[types.Unalias(pkg.Scope().Lookup("convertedAggregate0").Type())] || !convertedWide.zeroAggregateUnknown {
		t.Fatalf("conversion erased a known candidate or its widened alternative: %+v", convertedWide)
	}
	convertedWideTask := analysis.zeroAggregateField(convertedWide, pkg.Scope().Lookup("convertedAggregate0").Type(), 0)
	convertedWideTask = analysis.zeroAggregateField(convertedWideTask, fieldType(pkg.Scope().Lookup("convertedAggregate0").Type(), 0), 0)
	if !convertedWideTask.functionNil || !convertedWideTask.functionUnknown {
		t.Fatalf("converted known+unknown aggregate lost either alternative: %+v", convertedWideTask)
	}
	merged := emptyFlow()
	analysis.merge(&merged, wide)
	snapshot := snapshotFlow(merged)
	if !snapshot.zeroAggregateUnknown || !snapshot.zeroAggregates[types.Unalias(pkg.Scope().Lookup("aggregate0").Type())] {
		t.Fatalf("merge/snapshot lost known or widened aggregate metadata: merged=%+v snapshot=%+v", merged, snapshot)
	}
}

func fieldType(typ types.Type, index int) types.Type {
	return types.Unalias(typ).Underlying().(*types.Struct).Field(index).Type()
}
