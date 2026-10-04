package contracttrace

import (
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/ssa"
)

// channelClosureIndex contains typed builtin-close sites and the channel
// allocation candidates that have reached those sites in the flow fixed
// point. Sites are collected once; refreshChannelClosures is intended to run
// once per flow round, rather than scanning all functions for every receive.
type channelClosureIndex struct {
	calls   []ssa.CallInstruction
	closed  map[string]map[token.Pos]bool
	widened bool
}

func newChannelClosureIndex(funcs []*ssa.Function) *channelClosureIndex {
	index := &channelClosureIndex{closed: map[string]map[token.Pos]bool{}}
	for _, function := range funcs {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				common := call.Common()
				builtin, ok := common.Value.(*ssa.Builtin)
				if ok && builtin.Name() == "close" && len(common.Args) == 1 {
					index.calls = append(index.calls, call)
				}
			}
		}
	}
	return index
}

// refreshChannelClosures monotonically adds allocation/source pairs whose
// values now reach a typed close operand. Call, defer and go instructions all
// implement ssa.CallInstruction, so they share this source-backed handling.
func (a *flowAnalysis) refreshChannelClosures(index *channelClosureIndex) bool {
	if index == nil {
		return false
	}
	changed := false
	for _, call := range index.calls {
		common := call.Common()
		if len(common.Args) != 1 {
			continue
		}
		for _, address := range sortedKeys(a.get(common.Args[0]).addresses) {
			sources := index.closed[address]
			if sources[common.Pos()] {
				continue
			}
			if len(sources) >= maxFlowValues {
				index.widened = true
				a.coverage.Widened = true
				continue
			}
			if sources == nil {
				sources = map[token.Pos]bool{}
				index.closed[address] = sources
			}
			sources[common.Pos()] = true
			changed = true
		}
	}
	return changed
}

// receiveWithClosedChannelValue retains the existing unordered send model and
// adds a possible typed zero value when a modeled channel allocation has a
// source-backed close site. A closed channel can eventually yield the element
// zero value, but this deliberately does not model queue occupancy or
// scheduling: for a channel with modeled sends, the candidate is paired with
// an explicit receive-order boundary by the caller.
func (a *flowAnalysis) receiveWithClosedChannelValue(channel ssa.Value, index *channelClosureIndex) (flowValue, []token.Pos, bool) {
	result := a.receive(channel)
	orderUncertain := len(result.functions) > 0 || len(result.addresses) > 0 || result.functionUnknown || result.functionNil || result.zeroAggregateUnknown || len(result.zeroAggregates) > 0
	if channel == nil || index == nil {
		return result, nil, false
	}
	channelType, ok := types.Unalias(channel.Type()).Underlying().(*types.Chan)
	if !ok || !isFunctionType(channelType.Elem()) && !inlineAggregate(channelType.Elem()) {
		return result, nil, false
	}

	positions := map[token.Pos]bool{}
	for _, address := range sortedKeys(a.get(channel).addresses) {
		for pos := range index.closed[address] {
			positions[pos] = true
		}
	}
	closeSites := make([]token.Pos, 0, len(positions))
	for pos := range positions {
		closeSites = append(closeSites, pos)
	}
	sort.Slice(closeSites, func(i, j int) bool { return closeSites[i] < closeSites[j] })
	if len(closeSites) == 0 {
		return result, nil, false
	}

	if isFunctionType(channelType.Elem()) {
		result.functionNil = true
	} else {
		a.addZeroAggregate(&result, channelType.Elem())
	}
	return result, closeSites, orderUncertain
}
