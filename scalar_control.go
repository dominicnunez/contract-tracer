package contracttrace

import (
	"golang.org/x/tools/go/ssa"
)

// A synthetic exit joins all modeled terminal blocks. Nonterminating paths are
// excluded from this postdominator model and disclosed, not assumed to finish.
// Bound quadratic storage and graph work independently of scalar candidate caps.
const maxControlBlocks = 256

func (a *flowAnalysis) seedScalarControl(funcs []*ssa.Function, ix *index) {
	a.scalarControls = map[*ssa.BasicBlock][]*ssa.If{}
	for _, fn := range funcs {
		n := len(fn.Blocks)
		if n == 0 {
			continue
		}
		if n > maxControlBlocks {
			ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(fn), Kind: "scalar_control_bound", Reason: "Function exceeds the 256-block control analysis bound; implicit scalar influence remains unresolved.", Evidence: ix.funcs[ix.owner(fn)].node.Evidence})
			continue
		}
		canExit := make([]bool, n)
		queue := []*ssa.BasicBlock{}
		for _, block := range fn.Blocks {
			if len(block.Succs) == 0 {
				canExit[block.Index] = true
				queue = append(queue, block)
			}
		}
		for head := 0; head < len(queue); head++ {
			for _, pred := range queue[head].Preds {
				if !canExit[pred.Index] {
					canExit[pred.Index] = true
					queue = append(queue, pred)
				}
			}
		}
		incomplete := false
		post := make([][]bool, n+1)
		for i := 0; i <= n; i++ {
			post[i] = make([]bool, n+1)
			if i == n {
				post[i][n] = true
				continue
			}
			if !canExit[i] {
				incomplete = true
				continue
			}
			for j := 0; j < n; j++ {
				post[i][j] = canExit[j]
			}
			post[i][n] = true
		}
		for changed := true; changed; {
			changed = false
			for i := n - 1; i >= 0; i-- {
				if !canExit[i] {
					continue
				}
				block := fn.Blocks[i]
				for j := 0; j <= n; j++ {
					keep := true
					if len(block.Succs) == 0 {
						keep = post[n][j]
					} else {
						for _, succ := range block.Succs {
							if !canExit[succ.Index] {
								incomplete = true
								continue
							}
							keep = keep && post[succ.Index][j]
						}
					}
					keep = keep || i == j
					if post[i][j] != keep {
						post[i][j] = keep
						changed = true
					}
				}
			}
		}
		if incomplete {
			ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(fn), Kind: "scalar_control_nonterminating", Reason: "Some control paths have no modeled terminal block. Control candidates use exit-reaching paths only; divergence, panic/recovery and termination-sensitive influence are unresolved.", Evidence: ix.funcs[ix.owner(fn)].node.Evidence})
		}
		for _, block := range fn.Blocks {
			if !canExit[block.Index] || len(block.Instrs) == 0 {
				continue
			}
			branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
			if !ok {
				continue
			}
			stop := n
			for candidate := 0; candidate <= n; candidate++ {
				if candidate == block.Index || !post[block.Index][candidate] {
					continue
				}
				nearest := true
				for other := 0; other <= n; other++ {
					if other != block.Index && other != candidate && post[block.Index][other] && !post[candidate][other] {
						nearest = false
						break
					}
				}
				if nearest {
					stop = candidate
					break
				}
			}
			seen := make([]bool, n)
			work := append([]*ssa.BasicBlock{}, block.Succs...)
			for head := 0; head < len(work); head++ {
				target := work[head]
				if target.Index == stop || seen[target.Index] || !canExit[target.Index] {
					continue
				}
				seen[target.Index] = true
				a.scalarControls[target] = append(a.scalarControls[target], branch)
				work = append(work, target.Succs...)
			}
		}
	}
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "scalar_control_model", Reason: "Bounded exit-reaching CFG control dependencies retain candidate branch influence on scalar computations, selected returns, phi alternatives and conditional operations. Conditions are not proved feasible; equal branch outcomes may still retain candidates. Termination, panic/recovery, asynchronous ordering and numeric correctness are not established."})
}

func (a *flowAnalysis) scalarControlValue(block *ssa.BasicBlock) flowValue {
	if len(a.scalarControls[block]) == 0 {
		return flowValue{}
	}
	if result, ok := a.scalarControlCache[block]; ok {
		return result
	}
	result := flowValue{}
	for _, branch := range a.scalarControls[block] {
		// Constants have no scalar origins. Other predicate origins already
		// reside in values; copying the other flow domains is unnecessary.
		a.mergeScalar(&result, a.values[branch.Cond])
	}
	if a.scalarControlCache != nil {
		a.scalarControlCache[block] = result
	}
	return result
}
