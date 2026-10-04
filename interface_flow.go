package contracttrace

import (
	"fmt"
	"go/token"

	"golang.org/x/tools/go/ssa"
)

// SSA stores an interface receiver in Value, whereas a direct method receiver
// is argument zero. Both must align with the concrete callee's Params.
func flowArguments(call *ssa.CallCommon) []ssa.Value {
	if !call.IsInvoke() {
		return call.Args
	}
	args := make([]ssa.Value, 0, len(call.Args)+1)
	args = append(args, call.Value)
	return append(args, call.Args...)
}

// Generated wrappers have no source call expression. Attribute their structural
// relationship to the declaration and preserve that distinction in evidence.
func (ix *index) callEvidence(call ssa.CallInstruction) Evidence {
	if call.Pos() != token.NoPos {
		return ix.evidence(call.Pos())
	}
	parent := call.Parent()
	var evidence Evidence
	if parent.Pos() != token.NoPos {
		evidence = ix.evidence(parent.Pos())
	} else if owner := ix.funcs[ix.owner(parent)]; owner != nil {
		evidence = owner.node.Evidence
	}
	evidence.Origin = "synthetic_declaration"
	return evidence
}
func (ix *index) callEdge(from, to, kind, certainty string, call ssa.CallInstruction) {
	ix.edges = append(ix.edges, Relationship{From: from, To: to, Kind: kind, Certainty: certainty, Evidence: ix.callEvidence(call)})
}
func flowArgumentSlot(call *ssa.CallCommon, index int) string {
	receiver := call.IsInvoke()
	if target := call.StaticCallee(); target != nil && target.Signature.Recv() != nil {
		receiver = true
	}
	if receiver {
		if index == 0 {
			return "receiver"
		}
		index--
	}
	return fmt.Sprintf("argument:%d", index)
}

// A struct copy can be stored at a new allocation before its fields are read.
// Retain source-record candidates through such copies. This is a conservative
// field summary: later writes may remain visible through copied candidates.
type recordAddressCache struct {
	roots     []string
	reads     map[string]uint64
	addresses []string
}

func (a *flowAnalysis) recordAddresses(value ssa.Value) []string {
	result := a.get(value)
	roots := sortedKeys(result.addresses)
	if cached, ok := a.recordCache[value]; ok && sameAddressRoots(roots, cached.roots) {
		valid := true
		for address, version := range cached.reads {
			if a.aliasVersions[address] != version {
				valid = false
				break
			}
		}
		if valid {
			return cached.addresses
		}
	}
	queue := append([]string(nil), roots...)
	reads := map[string]uint64{}
	for head := 0; head < len(queue); head++ {
		reads[queue[head]] = a.aliasVersions[queue[head]]
		for _, alias := range sortedKeys(a.memory[queue[head]].addresses) {
			if result.addresses[alias] {
				continue
			}
			candidate := emptyFlow()
			candidate.addresses[alias] = true
			if a.merge(&result, candidate) {
				queue = append(queue, alias)
			}
		}
	}
	addresses := sortedKeys(result.addresses)
	if a.recordCache == nil {
		a.recordCache = map[ssa.Value]recordAddressCache{}
	}
	a.recordCache[value] = recordAddressCache{roots, reads, addresses}
	return addresses
}

func sameAddressRoots(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i, address := range left {
		if address != right[i] {
			return false
		}
	}
	return true
}
