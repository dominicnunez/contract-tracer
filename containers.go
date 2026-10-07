package contracttrace

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func inlineAggregate(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Struct, *types.Array:
		return true
	}
	return false
}

func (a *flowAnalysis) sequenceElements(value ssa.Value) flowValue {
	result := emptyFlow()
	sequence, ok := value.Type().Underlying().(*types.Slice)
	if !ok {
		return result
	}
	for _, root := range sortedKeys(a.get(value).addresses) {
		address := root + ".element"
		a.merge(&result, a.memory[address])
		if sequenceMayHaveElement(value) {
			result.functionNil = result.functionNil || a.zeroFunctionElements[address]
			result.functionUnknown = result.functionUnknown || a.unknownFunctionElements[address]
		}
		if inlineAggregate(sequence.Elem()) {
			result.addresses[address] = true
			marker := a.get(value)
			if sequenceMayHaveElement(value) && exactZeroAggregateMatches(marker, sequence.Elem()) {
				a.addZeroAggregate(&result, sequence.Elem())
			}
			if sequenceMayHaveElement(value) && marker.zeroAggregateUnknown {
				a.markZeroAggregateUnknown(&result)
			}
		}
	}
	return result
}

// Element candidates are unordered and monotone. Append may reuse its backing
// array or allocate a new one; copy may move any prefix up to the shorter length.
// Preserve both append storage candidates and all modeled source elements.
func (a *flowAnalysis) sliceBuiltin(call ssa.CallInstruction, ix *index, result *flowValue) bool {
	common := call.Common()
	builtin, ok := common.Value.(*ssa.Builtin)
	if !ok || len(common.Args) != 2 || (builtin.Name() != "append" && builtin.Name() != "copy") {
		return false
	}
	destinations := a.get(common.Args[0])
	elements := a.sequenceElements(common.Args[1])
	if builtin.Name() == "append" {
		value, ok := call.(*ssa.Call)
		if !ok {
			return false
		}
		a.merge(&elements, a.sequenceElements(common.Args[0]))
		root := ix.allocationID("append", call.Parent(), value)
		candidate := emptyFlow()
		candidate.addresses[root] = true
		a.merge(&destinations, candidate)
		a.addressTypes[root] = value.Type()
		a.merge(result, destinations)
	}
	changed := false
	for _, root := range sortedKeys(destinations.addresses) {
		if a.store(root+".element", elements) {
			changed = true
		}
	}
	return changed
}

func (a *flowAnalysis) store(key string, value flowValue) bool {
	old := a.memory[key]
	aliases := len(old.addresses)
	changed := a.merge(&old, value)
	a.memory[key] = old
	if changed {
		if a.memoryVersions == nil {
			a.memoryVersions = map[string]uint64{}
		}
		a.memoryVersions[key]++
	}
	if len(old.addresses) != aliases {
		if a.aliasVersions == nil {
			a.aliasVersions = map[string]uint64{}
		}
		a.aliasVersions[key]++
	}
	return changed
}

func (a *flowAnalysis) storeMap(root, key string, value flowValue) bool {
	if a.mapEntries == nil {
		a.mapEntries = map[string]map[string]bool{}
	}
	if a.mapEntries[root] == nil {
		a.mapEntries[root] = map[string]bool{}
	}
	address := root + ".key:" + key
	a.mapEntries[root][address] = true
	return a.store(address, value)
}

func isMapValue(value ssa.Value) bool {
	if value == nil || value.Type() == nil {
		return false
	}
	_, ok := value.Type().Underlying().(*types.Map)
	return ok
}

func (a *flowAnalysis) openMap(root string) bool {
	if a.mapOpen == nil {
		a.mapOpen = map[string]bool{}
	}
	if a.mapOpen[root] {
		return false
	}
	a.mapOpen[root] = true
	return true
}

func (a *flowAnalysis) openMapValue(value ssa.Value) bool {
	changed := false
	for _, root := range sortedKeys(a.get(value).addresses) {
		if a.openMap(root) {
			changed = true
		}
	}
	return changed
}

// Literal MapUpdates are the only writes for which the analyzer currently
// retains a closed key set. Later updates and wildcard keys invalidate absence
// claims rather than making a historical entry definite.
func (a *flowAnalysis) mapUpdateOpens(function *ssa.Function, update *ssa.MapUpdate, ix *index) bool {
	if update == nil || !isMapValue(update.Map) {
		return false
	}
	mapValue, ok := update.Map.(*ssa.MakeMap)
	if !ok || !a.mapUpdateInLiteral(function, update, mapValue, ix) {
		return true
	}
	keys := a.mapKeys(update.Key)
	return len(keys) != 1 || keys[0] == "*"
}

func (a *flowAnalysis) mapUpdateInLiteral(function *ssa.Function, update *ssa.MapUpdate, allocation *ssa.MakeMap, ix *index) bool {
	if function == nil || function.Syntax() == nil || update == nil || allocation == nil || ix == nil || ix.fset == nil {
		return false
	}
	owner := ix.funcs[ix.owner(function)]
	if owner == nil || owner.pkg == nil || owner.pkg.TypesInfo == nil {
		return false
	}
	updatePos, allocationPos := update.Pos(), allocation.Pos()
	if updatePos == token.NoPos || allocationPos == token.NoPos {
		return false
	}
	matched := false
	ast.Inspect(function.Syntax(), func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || literal.Pos() > updatePos || literal.End() < updatePos || literal.Pos() > allocationPos || literal.End() < allocationPos {
			return true
		}
		typ := owner.pkg.TypesInfo.TypeOf(literal)
		if typ == nil {
			return true
		}
		_, mapLiteral := typ.Underlying().(*types.Map)
		if mapLiteral && types.Identical(types.Unalias(typ), types.Unalias(allocation.Type())) {
			matched = true
			return false
		}
		return true
	})
	return matched
}

func (a *flowAnalysis) openMutatedMaps(call *ssa.Call) bool {
	if call == nil || len(call.Common().Args) == 0 {
		return false
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || (builtin.Name() != "delete" && builtin.Name() != "clear") {
		return false
	}
	return a.openMapValue(call.Common().Args[0])
}

// Quoted strings and typed constants avoid collisions with the unknown-key marker.
func (a *flowAnalysis) mapKeys(value ssa.Value) []string {
	if c, ok := value.(*ssa.Const); ok && c.Value != nil && c.Value.Kind() != constant.String {
		return []string{c.Type().String() + ":" + c.Value.ExactString()}
	}
	keys := []string{}
	candidates := a.get(value)
	for _, s := range sortedKeys(candidates.strings) {
		keys = append(keys, strconv.Quote(s))
	}
	if len(keys) == 0 {
		return []string{"*"}
	}
	if candidates.stringUnknown {
		keys = append(keys, "*")
	}
	return keys
}
func (a *flowAnalysis) mapLookup(value, key ssa.Value) flowValue {
	result := emptyFlow()
	for _, p := range sortedKeys(a.get(value).addresses) {
		prefix := p + ".key:"
		keys := a.mapKeys(key)
		for _, k := range keys {
			if k == "*" {
				for _, address := range sortedKeys(a.mapEntries[p]) {
					a.merge(&result, a.memory[address])
				}
			} else {
				a.merge(&result, a.memory[prefix+k])
			}
			a.merge(&result, a.memory[prefix+"*"])
			mapType, isMap := value.Type().Underlying().(*types.Map)
			if isMap && isFunctionType(mapType.Elem()) {
				if !a.mapClosed[p] {
					result.functionUnknown = true
				} else if a.mapOpen[p] {
					result.functionUnknown = true
					result.functionNil = true
				} else if k == "*" && !a.mapEntries[p][prefix+"*"] {
					result.functionNil = true
				} else if k != "*" && !a.mapEntries[p][prefix+k] && !a.mapEntries[p][prefix+"*"] {
					result.functionNil = true
				}
			} else if isMap && inlineAggregate(mapType.Elem()) {
				missing := k == "*" && !a.mapEntries[p][prefix+"*"] || k != "*" && !a.mapEntries[p][prefix+k] && !a.mapEntries[p][prefix+"*"]
				if a.mapClosed[p] && (missing || a.mapOpen[p]) {
					a.addZeroAggregate(&result, mapType.Elem())
				}
				if a.mapOpen[p] || !a.mapClosed[p] {
					a.markZeroAggregateUnknown(&result)
				}
			}
		}
	}
	return result
}

// SSA Next slots are (ok, key, value). Each iteration retains an unordered
// union, with keys and values separate; pairing and iteration order are lost.
func (a *flowAnalysis) mapIterationValue(iterator ssa.Value, slot int, typ types.Type) flowValue {
	result := emptyFlow()
	if slot != 1 && slot != 2 {
		return result
	}
	for _, root := range sortedKeys(a.get(iterator).addresses) {
		if slot == 1 {
			a.merge(&result, a.memory[root+".keys"])
			continue
		}
		addresses := map[string]bool{root + ".key:*": true}
		for address := range a.mapEntries[root] {
			addresses[address] = true
		}
		for _, address := range sortedKeys(addresses) {
			a.merge(&result, a.memory[address])
			if inlineAggregate(typ) {
				result.addresses[address] = true
			}
		}
	}
	return result
}

func (a *flowAnalysis) send(channel, value ssa.Value, block *ssa.BasicBlock) bool {
	changed := false
	sent := a.get(value)
	if isScalarType(value.Type()) {
		a.mergeScalar(&sent, a.scalarControlValue(block))
	}
	for _, p := range sortedKeys(a.get(channel).addresses) {
		if a.store(p+".sent", sent) {
			changed = true
		}
	}
	return changed
}
func (a *flowAnalysis) receive(channel ssa.Value) flowValue {
	result := emptyFlow()
	for _, p := range sortedKeys(a.get(channel).addresses) {
		a.merge(&result, a.memory[p+".sent"])
	}
	return result
}
func (ix *index) channelID(pos token.Pos) string {
	e := ix.evidence(pos)
	return resourceID("channel", "", fmt.Sprintf("%s:%d:%d", e.File, e.Line, e.Column))
}
func (a *flowAnalysis) channelUse(ins ssa.Instruction, from string, ix *index) {
	link := func(value ssa.Value, kind string, pos token.Pos) {
		found := false
		flow := a.get(value)
		if kind == "channel_receive" || kind == "channel_select_receive" {
			_, closeSites, orderUncertain := a.receiveWithClosedChannelValue(value, a.channelClosures)
			if len(closeSites) > 0 {
				examples := make([]string, 0, len(closeSites))
				for _, closePos := range closeSites {
					evidence := ix.evidence(closePos)
					examples = append(examples, fmt.Sprintf("close at %s:%d:%d", evidence.File, evidence.Line, evidence.Column))
				}
				reason := "a modeled close permits the element zero value, which can carry a nil callback through an inline aggregate, after queued values drain; send/close ordering and queue occupancy are not proved"
				if orderUncertain {
					reason = "a modeled close and send candidates coexist; receive ordering, queue occupancy and whether a queued element or element zero value (which can carry a nil callback through an inline aggregate) is returned are unresolved"
				}
				ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "closed_function_channel_zero", Reason: reason, Evidence: ix.evidence(pos), Examples: examples})
			}
		}
		for _, effect := range sortedKeys(flow.effects) {
			if strings.HasPrefix(effect, "nil_done:") {
				found = true
				if kind == "channel_receive" {
					ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "nil_context_done_wait", Reason: "receive has a modeled nil Done channel candidate; it cannot be released by cancellation of that context", Evidence: ix.evidence(pos)})
				} else if kind == "channel_select_receive" {
					ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "nil_context_done_select", Reason: "select has a modeled nil Done channel candidate; that arm is disabled for this candidate", Evidence: ix.evidence(pos)})
				}
			}
		}
		for key := range a.get(value).addresses {
			if strings.HasPrefix(key, "channel:") {
				found = true
				ix.edge(from, key, kind, "possible", pos)
			}
		}
		if !found || flow.interfaceUnknown || flow.sqlUnknown || flow.boundReceiverUnknown {
			reason := "channel operation has no modeled creation site; input or alias may be external"
			if found {
				reason = "channel operation has a modeled creation site and an unresolved receiver or alias candidate"
			}
			ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "unresolved_channel", Reason: reason, Evidence: ix.evidence(pos)})
		}
	}
	switch v := ins.(type) {
	case *ssa.MakeChan:
		key := ix.channelID(v.Pos())
		if ix.funcs[key] == nil {
			ix.funcs[key] = &function{node: Node{ID: key, Name: key, Kind: "channel", Evidence: ix.evidence(v.Pos())}}
		}
		ix.edge(from, key, "channel_create", "fact", v.Pos())
	case *ssa.Send:
		link(v.Chan, "channel_send", v.Pos())
	case *ssa.UnOp:
		if v.Op == token.ARROW {
			link(v.X, "channel_receive", v.Pos())
		}
	case *ssa.Select:
		for _, state := range v.States {
			kind := "channel_select_receive"
			if state.Dir == types.SendOnly {
				kind = "channel_select_send"
			}
			link(state.Chan, kind, state.Pos)
		}
	case ssa.CallInstruction:
		if builtin, ok := v.Common().Value.(*ssa.Builtin); ok && builtin.Name() == "close" && len(v.Common().Args) == 1 {
			link(v.Common().Args[0], "channel_close", v.Pos())
		}
	}
}
