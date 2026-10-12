package contracttrace

import (
	"fmt"
	"go/token"

	"golang.org/x/tools/go/ssa"
)

func (ix *index) lifecycleSite(ins ssa.Instruction, kind, name string) string {
	fn := ins.Parent()
	owner := ix.owner(fn)
	evidence := ix.evidence(ins.Pos())
	if ins.Pos() == token.NoPos {
		evidence = ix.evidence(fn.Pos())
		if fn.Pos() == token.NoPos {
			evidence = ix.funcs[owner].node.Evidence
		}
		evidence.Origin = "synthetic_declaration"
	}
	ordinal := 0
	for i, candidate := range ins.Block().Instrs {
		if candidate == ins {
			ordinal = i
			break
		}
	}
	id := resourceID(kind, "", fmt.Sprintf("%s:%s:%d:%d", owner, fn.String(), ins.Block().Index, ordinal))
	nodeKind := kind
	if kind == "exit" {
		nodeKind = "function_exit"
	}
	ix.funcs[id] = &function{node: Node{ID: id, Name: name, Kind: nodeKind, Evidence: evidence}}
	return id
}

func isRecoverCall(call ssa.CallInstruction) bool {
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	return ok && builtin.Name() == "recover"
}

func (ix *index) recoveryUse(ins ssa.Instruction) {
	owner := ix.owner(ins.Parent())
	kind, site := "", ""
	if _, ok := ins.(*ssa.Panic); ok {
		kind = "panic_site"
		site = ix.lifecycleSite(ins, "exit", "panic exit")
	} else if call, ok := ins.(ssa.CallInstruction); ok && isRecoverCall(call) {
		kind = "recover_observe"
		site = ix.lifecycleSite(ins, "recover", "recover observation")
	}
	if site != "" {
		ix.edges = append(ix.edges, Relationship{From: owner, To: site, Kind: kind, Certainty: "fact", Evidence: ix.funcs[site].node.Evidence})
	}
}

func (ix *index) directRecoverSites(target *ssa.Function) []string {
	sites := []string{}
	for _, block := range target.Blocks {
		for _, ins := range block.Instrs {
			if call, ok := ins.(*ssa.Call); ok && isRecoverCall(call) {
				sites = append(sites, ix.lifecycleSite(call, "recover", "recover observation"))
			}
		}
	}
	return sites
}
