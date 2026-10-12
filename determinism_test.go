package contracttrace

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestBoundedFlowMergeIsDeterministic(t *testing.T) {
	source := emptyFlow()
	for i := 0; i < maxFlowValues+20; i++ {
		source.strings[fmt.Sprintf("value-%03d", i)] = true
	}
	expected := map[string]bool{}
	for i := 0; i < maxFlowValues; i++ {
		expected[fmt.Sprintf("value-%03d", i)] = true
	}
	for run := 0; run < 20; run++ {
		a := &flowAnalysis{}
		dst := emptyFlow()
		a.merge(&dst, source)
		if !reflect.DeepEqual(dst.strings, expected) || !a.coverage.Widened {
			t.Fatal("bounded merge chose arbitrary map iteration candidates or hid widening")
		}
	}
}

func TestRepeatedTraceHasIdenticalEvidence(t *testing.T) {
	options := Options{Root: "testdata/sample", Seeds: []string{"MapCallback", "ChannelEvent", "WrappedWrite"}, Depth: 4, MaxNodes: 250}
	first, err := Trace(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Trace(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(second)
	if string(left) != string(right) {
		t.Fatal("unchanged source/build/options produced different reports")
	}
}
