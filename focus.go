package contracttrace

import (
	"fmt"
	"sort"
	"strings"
)

// Focus explains paths in the selected graph; it never removes structural scope.
func (ix *index) applyFocus(r *Report, focus []string) error {
	if len(focus) == 0 {
		return nil
	}
	anchors := map[string]bool{}
	for _, requested := range focus {
		matches := []string{}
		for id, f := range ix.funcs {
			if id == requested || f.node.Name == requested {
				matches = append(matches, id)
			}
		}
		sort.Strings(matches)
		if len(matches) == 0 {
			return fmt.Errorf("contract focus %q not found", requested)
		}
		if len(matches) > 1 {
			return fmt.Errorf("contract focus %q is ambiguous: %s", requested, strings.Join(matches, ", "))
		}
		anchors[matches[0]] = true
	}
	r.Focus = sortedKeys(anchors)
	nodes := map[string]int{}
	for i := range r.Nodes {
		nodes[r.Nodes[i].ID] = i
		r.Nodes[i].Relevance = nil
	}
	adj := map[string][]Relationship{}
	for _, e := range r.Relationships {
		adj[e.From] = append(adj[e.From], e)
		adj[e.To] = append(adj[e.To], e)
	}
	for id := range adj {
		sortEdgesByPriorityAndKey(adj[id])
	}
	queue := []string{}
	for _, anchor := range r.Focus {
		if index, ok := nodes[anchor]; ok {
			r.Nodes[index].Relevance = &Relevance{Anchor: anchor, Distance: 0, PathCertainty: "fact"}
			queue = append(queue, anchor)
		} else {
			r.Boundaries = append(r.Boundaries, Boundary{Kind: "focus_outside_scope", Reason: "contract focus " + anchor + " is discovered but outside selected scope; expand seeds/depth/budget to inspect paths", Evidence: ix.funcs[anchor].node.Evidence})
		}
	}
	for head := 0; head < len(queue); head++ {
		id := queue[head]
		current := r.Nodes[nodes[id]].Relevance
		for _, edge := range adj[id] {
			next := edge.To
			if next == id {
				next = edge.From
			}
			index, exists := nodes[next]
			if !exists || r.Nodes[index].Relevance != nil {
				continue
			}
			copy := edge
			certainty := current.PathCertainty
			if edge.Certainty != "fact" {
				certainty = "possible"
			}
			r.Nodes[index].Relevance = &Relevance{Anchor: current.Anchor, Distance: current.Distance + 1, Via: &copy, PathCertainty: certainty}
			queue = append(queue, next)
		}
	}
	r.Boundaries = append(r.Boundaries, Boundary{Kind: "contract_focus_model", Reason: "Explicit focus anchors explain shortest undirected paths in the selected graph. Possible relationships remain possible; graph proximity does not establish semantic relevance or an invariant. Missing paths can reflect scope limits or unsupported analysis, and do not establish irrelevance."})
	return nil
}
