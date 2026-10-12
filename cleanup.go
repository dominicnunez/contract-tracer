package contracttrace

import (
	"fmt"
	"go/token"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Registrations and CFG exit candidates describe ownership and local paths.
// They do not establish registration frequency, execution or termination.
func (a *flowAnalysis) cleanupUse(call ssa.CallInstruction, ix *index) {
	registration, ok := call.(*ssa.Defer)
	if !ok {
		return
	}
	owner := ix.owner(call.Parent())
	evidence := ix.callEvidence(call)
	id := resourceID("defer", "", fmt.Sprintf("%s:%d:%d:%s", evidence.File, evidence.Line, evidence.Column, call.Parent().String()))
	ix.funcs[id] = &function{node: Node{ID: id, Name: "defer registration", Kind: "defer", Evidence: evidence}}
	link := func(to, kind, certainty string) {
		ix.edges = append(ix.edges, Relationship{From: id, To: to, Kind: kind, Certainty: certainty, Evidence: evidence})
	}
	ix.edges = append(ix.edges, Relationship{From: owner, To: id, Kind: "defer_registration", Certainty: "fact", Evidence: evidence})
	resolved := false
	sqlInvocations, unknownSQLTarget := a.sqlInvocations(call.Common())
	for _, invocation := range sqlInvocations {
		receiver, name := invocation.receiverKind, invocation.name
		kind := ""
		if (receiver == "Stmt" || receiver == "DB" || receiver == "Conn") && name == "Close" {
			kind = "cleanup_statement_close"
			if receiver == "DB" {
				kind = "cleanup_database_close"
			} else if receiver == "Conn" {
				kind = "cleanup_connection_close"
			}
		} else if receiver == "Tx" && (name == "Commit" || name == "Rollback") {
			kind = "cleanup_transaction_" + strings.ToLower(name)
		}
		if kind == "" {
			continue
		}
		resolved = true
		resources := a.sqlResources(invocation.receiver, receiver)
		for _, resource := range resources {
			link(resource, kind, "possible")
		}
		if len(resources) == 0 || invocation.unknownReceiver || unknownSQLTarget {
			ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "unresolved_cleanup_resource", Reason: "deferred SQL cleanup retains a receiver without a modeled origin or an outside target; known handles remain possible", Evidence: evidence})
		}
	}
	for _, use := range a.waitGroupResources(call, ix) {
		for _, resource := range use.resources {
			link(resource, "cleanup_waitgroup_"+strings.ToLower(use.name), "possible")
			resolved = true
		}
	}
	for _, effect := range sortedKeys(a.get(call.Common().Value).effects) {
		if strings.HasPrefix(effect, "cancel:") {
			link(strings.TrimPrefix(effect, "cancel:"), "cleanup_cancel", "possible")
			resolved = true
		}
	}
	if builtin, ok := call.Common().Value.(*ssa.Builtin); ok && builtin.Name() == "close" && len(call.Common().Args) == 1 {
		for _, channel := range sortedKeys(a.get(call.Common().Args[0]).addresses) {
			if strings.HasPrefix(channel, "channel:") {
				link(channel, "cleanup_close", "possible")
				resolved = true
			}
		}
	}
	for _, target := range sortedFunctions(a.targets(call.Common())) {
		if to := ix.owner(target); to != "" {
			certainty := "possible"
			if call.Common().StaticCallee() == target {
				certainty = "fact"
			}
			link(to, "cleanup_call", certainty)
			for _, site := range ix.directRecoverSites(target) {
				link(site, "cleanup_recover_candidate", "possible")
			}
			resolved = true
		}
	}
	if !resolved {
		ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "unresolved_cleanup_target", Reason: "deferred target/resource has no modeled local body or resource identity; dependency code, unknown values or unsupported builtins require investigation", Evidence: evidence})
	}
	queue := []*ssa.BasicBlock{registration.Block()}
	visited := map[*ssa.BasicBlock]bool{}
	exitCount := 0
	for head := 0; head < len(queue); head++ {
		block := queue[head]
		if visited[block] {
			continue
		}
		visited[block] = true
		for ordinal, ins := range block.Instrs {
			name := ""
			switch ins.(type) {
			case *ssa.Return:
				name = "return exit"
			case *ssa.Panic:
				name = "panic exit"
			}
			if name == "" {
				continue
			}
			exitEvidence := ix.evidence(ins.Pos())
			if ins.Pos() == token.NoPos {
				exitEvidence = ix.evidence(call.Parent().Pos())
				if call.Parent().Pos() == token.NoPos {
					exitEvidence = ix.funcs[owner].node.Evidence
				}
				exitEvidence.Origin = "synthetic_declaration"
			}
			exit := resourceID("exit", "", fmt.Sprintf("%s:%s:%d:%d", owner, call.Parent().String(), block.Index, ordinal))
			ix.funcs[exit] = &function{node: Node{ID: exit, Name: name, Kind: "function_exit", Evidence: exitEvidence}}
			link(exit, "cleanup_exit_candidate", "possible")
			exitCount++
		}
		queue = append(queue, block.Succs...)
	}
	if exitCount == 0 {
		ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "cleanup_exit_unresolved", Reason: "no explicit return/panic reachable from registration in local SSA control flow; nontermination, implicit panic or external termination remains unresolved", Evidence: evidence})
	}
}
