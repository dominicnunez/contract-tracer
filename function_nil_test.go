package contracttrace

import (
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestTypedFunctionNilFlowIsDistinctAndSurvivesMergeAndSnapshot(t *testing.T) {
	functionType := types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(), false)
	nilFunction := ssa.NewConst(nil, functionType)
	analysis := &flowAnalysis{}
	read := analysis.get(nilFunction)
	if !read.functionNil || read.functionUnknown || len(read.functions) != 0 {
		t.Fatalf("typed nil was not kept as a distinct function alternative: %+v", read)
	}

	known := new(ssa.Function)
	combined := emptyFlow()
	analysis.merge(&combined, read)
	knownFlow := emptyFlow()
	knownFlow.functions[known] = true
	analysis.merge(&combined, knownFlow)
	if !combined.functionNil || combined.functionUnknown || !combined.functions[known] {
		t.Fatalf("known callback and nil alternatives were not preserved separately: %+v", combined)
	}

	// A nil strings map triggers lazy initialization in merge. That path must
	// preserve the preexisting nil metadata instead of silently clearing it.
	preseeded := flowValue{functionNil: true}
	analysis.merge(&preseeded, emptyFlow())
	if !preseeded.functionNil {
		t.Fatal("lazy flow initialization erased preexisting typed nil metadata")
	}

	snapshot := snapshotFlow(combined)
	if !snapshot.functionNil || snapshot.functionUnknown || !snapshot.functions[known] {
		t.Fatalf("flow snapshot lost distinct known/nil alternatives: %+v", snapshot)
	}
	summaries := flowSummary(combined, &index{})
	foundNil := false
	for _, summary := range summaries {
		foundNil = foundNil || summary == "unresolved:nil_function_value"
	}
	if !foundNil {
		t.Fatalf("flow summary hid explicit nil function uncertainty: %v", summaries)
	}
	delete(snapshot.functions, known)
	if !combined.functions[known] {
		t.Fatal("mutating snapshot changed stored known callback candidates")
	}
}
