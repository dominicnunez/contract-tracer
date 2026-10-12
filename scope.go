package contracttrace

import (
	"fmt"
	"sort"
	"strings"
)

func (ix *index) expand(o Options, c Coverage) (Report, error) {
	r := Report{Nodes: []Node{}, Relationships: []Relationship{}, Boundaries: []Boundary{}, Coverage: c, Seeds: []string{}}
	distances := map[string]int{}
	reached := map[string]Relationship{}
	queue := []string{}
	locationSymbols, err := ix.locationSeeds(o.Locations)
	if err != nil {
		return r, err
	}
	o.Seeds = append(append([]string(nil), o.Seeds...), locationSymbols...)
	for _, seed := range o.Seeds {
		matches := []string{}
		for id, f := range ix.funcs {
			if id == seed || f.node.Name == seed {
				matches = append(matches, id)
			}
		}
		sort.Strings(matches)
		if len(matches) == 0 {
			return r, fmt.Errorf("seed %q not found", seed)
		}
		if len(matches) > 1 {
			return r, fmt.Errorf("seed %q is ambiguous: %s", seed, strings.Join(matches, ", "))
		}
		id := matches[0]
		r.Seeds = append(r.Seeds, id)
		if _, ok := distances[id]; !ok {
			distances[id] = 0
			queue = append(queue, id)
		}
	}
	if len(queue) > o.MaxNodes {
		return r, fmt.Errorf("max-nodes is smaller than the seed count")
	}
	sort.Strings(queue)
	adj := map[string][]Relationship{}
	edges := canonicalEdges(ix.edges)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e)
		adj[e.To] = append(adj[e.To], e)
	}
	for id := range adj {
		sortEdgesByPriorityAndKey(adj[id])
	}
	seeds := append([]string(nil), queue...)
	seeded := map[string]bool{}
	for _, id := range seeds {
		seeded[id] = true
	}
	// Configured event discriminators have value-specific resources. Crossing
	// their declaration hub by default merges otherwise distinct event families.
	// Keep all field edges in the inventory and expose the excluded users; an
	// explicit field seed requests the broader declaration-based investigation.
	blockedFieldHub := func(id string) bool {
		return !seeded[id] && ix.funcs[id] != nil && ix.funcs[id].node.Kind == "event_discriminator_field"
	}
	// Apply priority to whole paths, not only each node's adjacency list.
	// Revisit selected nodes at each tier so a shorter possible path can still
	// enable expansion within Depth. Every admitted node has a selected parent.
	for priority := 0; priority <= 1; priority++ {
		queue = append([]string(nil), seeds...)
		passDistances := map[string]int{}
		for _, id := range seeds {
			passDistances[id] = 0
		}
		for head := 0; head < len(queue); head++ {
			id := queue[head]
			if blockedFieldHub(id) {
				continue
			}
			for _, e := range adj[id] {
				if scopePathPriority(e) > priority || strings.Contains(e.Kind, "possible_callback_call") && !o.ExpandCallbacks {
					continue
				}
				next := e.To
				if next == id {
					next = e.From
				}
				if _, visited := passDistances[next]; visited || passDistances[id] >= o.Depth {
					continue
				}
				oldDistance, selected := distances[next]
				if !selected && len(distances) >= o.MaxNodes {
					continue
				}
				distance := passDistances[id] + 1
				passDistances[next] = distance
				if !selected || distance < oldDistance {
					distances[next] = distance
					reached[next] = e
				}
				queue = append(queue, next)
			}
		}
	}
	frontier := map[string]bool{}
	for _, id := range sortedKeys(distances) {
		for _, e := range adj[id] {
			next := e.To
			if next == id {
				next = e.From
			}
			if strings.Contains(e.Kind, "possible_callback_call") && !o.ExpandCallbacks {
				r.Boundaries = append(r.Boundaries, Boundary{Node: id, Kind: "callback_candidates", Reason: "same-signature CHA candidates were not used to expand scope; enable -expand-callbacks to investigate them", Evidence: e.Evidence, Examples: []string{next}})
				continue
			}
			if _, visited := distances[next]; visited {
				continue
			}
			if blockedFieldHub(id) {
				r.Boundaries = append(r.Boundaries, Boundary{Node: id, Kind: "field_scope_exclusion", Reason: "Configured string event discriminator users were not expanded through shared declaration identity; event value relationships retain their scope. Seed this field resource explicitly to inspect all users, including unresolved event values. This exclusion does not prove irrelevance.", Evidence: e.Evidence, Examples: []string{next}})
				continue
			}
			r.Coverage.Truncated = true
			key := id + "|" + edgeKey(e)
			if !frontier[key] {
				frontier[key] = true
				r.Boundaries = append(r.Boundaries, Boundary{Node: id, Kind: "scope_frontier", Reason: "unexpanded relationships at depth or node budget; examples are not an exhaustive target list", Evidence: e.Evidence, Examples: []string{next}})
			}
		}
	}
	for id := range distances {
		if f := ix.funcs[id]; f != nil {
			n := f.node
			n.Distance = distances[id]
			if e, ok := reached[id]; ok {
				copy := e
				n.ReachedBy = &copy
			}
			r.Nodes = append(r.Nodes, n)
		}
	}
	for _, e := range edges {
		_, a := distances[e.From]
		_, b := distances[e.To]
		if a && b {
			r.Relationships = append(r.Relationships, e)
		}
	}
	for _, b := range ix.boundaries {
		if b.Node == "" {
			r.Boundaries = append(r.Boundaries, b)
			continue
		}
		if _, ok := distances[b.Node]; ok {
			r.Boundaries = append(r.Boundaries, b)
		}
	}
	if err := ix.applyFocus(&r, o.Focus); err != nil {
		return r, err
	}
	return r, nil
}

func scopePathPriority(e Relationship) int {
	if strings.HasPrefix(e.Kind, "field_") || strings.HasPrefix(e.Kind, "scalar_") || e.Kind == "external_input" || e.Kind == "external_result" || strings.Contains(e.Kind, "possible_callback_call") || strings.HasPrefix(e.Kind, "error_") && e.Certainty == "possible" {
		return 1
	}
	return 0
}

func edgePriority(e Relationship) int {
	if e.Certainty == "fact" {
		return 0
	}
	if strings.HasPrefix(e.Kind, "sql_") || strings.HasPrefix(e.Kind, "event_") {
		return 1
	}
	return 2
}

// Group repeated candidate targets by source site, retaining a distinct target
// count, the full discovered target inventory and five deterministic examples.
func groupBoundaries(r *Report) {
	grouped := map[string]*Boundary{}
	targets := map[string]map[string]bool{}
	result := []Boundary{}
	seenFacts := map[string]bool{}
	for _, b := range r.Boundaries {
		// Older snapshots retained only examples. Preserve their count without
		// inventing the targets that were discarded by the older analyzer.
		if b.CandidateCount > 0 && len(b.Candidates) == 0 {
			result = append(result, b)
			continue
		}
		if len(b.Examples) == 0 && len(b.Candidates) == 0 {
			key := fmt.Sprintf("%s|%s|%s:%d:%d|%s", b.Node, b.Kind, b.Evidence.File, b.Evidence.Line, b.Evidence.Column, b.Reason)
			if !seenFacts[key] {
				result = append(result, b)
				seenFacts[key] = true
			}
			continue
		}
		key := fmt.Sprintf("%s|%s|%s:%d:%d|%s", b.Node, b.Kind, b.Evidence.File, b.Evidence.Line, b.Evidence.Column, b.Reason)
		if grouped[key] == nil {
			copy := b
			copy.Examples = nil
			copy.Candidates = nil
			grouped[key] = &copy
			targets[key] = map[string]bool{}
		}
		for _, target := range b.Examples {
			targets[key][target] = true
		}
		for _, target := range b.Candidates {
			targets[key][target] = true
		}
	}
	for key, b := range grouped {
		examples := []string{}
		for target := range targets[key] {
			examples = append(examples, target)
		}
		sort.Strings(examples)
		b.CandidateCount = len(examples)
		b.Candidates = examples
		if len(examples) > 5 {
			examples = examples[:5]
		}
		b.Examples = examples
		result = append(result, *b)
	}
	r.Boundaries = result
	sortReport(r)
}
func edgeKey(e Relationship) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s:%09d:%09d", e.From, e.To, e.Kind, e.Certainty, e.Slot, e.Evidence.File, e.Evidence.Line, e.Evidence.Column)
}

type keyedRelationship struct {
	// Metadata stays parallel to edges so sorting does not allocate a second
	// copy of each Relationship and its evidence/value slice headers.
	edges      []Relationship
	keys       []string
	priorities []int
}

func (s keyedRelationship) Len() int { return len(s.edges) }
func (s keyedRelationship) Less(i, j int) bool {
	if s.priorities != nil && s.priorities[i] != s.priorities[j] {
		return s.priorities[i] < s.priorities[j]
	}
	return s.keys[i] < s.keys[j]
}
func (s keyedRelationship) Swap(i, j int) {
	s.edges[i], s.edges[j] = s.edges[j], s.edges[i]
	s.keys[i], s.keys[j] = s.keys[j], s.keys[i]
	if s.priorities != nil {
		s.priorities[i], s.priorities[j] = s.priorities[j], s.priorities[i]
	}
}

// sortEdgesByPriorityAndKey decorates each edge once so comparator calls do
// not repeatedly format the same source identity. It keeps the existing
// priority tiers and exact edgeKey lexical order, including its delimiter and
// numeric-width behavior.
func sortEdgesByPriorityAndKey(edges []Relationship) {
	if len(edges) < 2 {
		return
	}
	keys := make([]string, len(edges))
	priorities := make([]int, len(edges))
	for i, edge := range edges {
		keys[i] = edgeKey(edge)
		priorities[i] = edgePriority(edge)
	}
	sort.Sort(keyedRelationship{edges: edges, keys: keys, priorities: priorities})
}

func sortEdgesByKey(edges []Relationship) {
	if len(edges) < 2 {
		return
	}
	keys := make([]string, len(edges))
	for i, edge := range edges {
		keys[i] = edgeKey(edge)
	}
	sort.Sort(keyedRelationship{edges: edges, keys: keys})
}

func sortReport(r *Report) {
	sort.Slice(r.Nodes, func(i, j int) bool { return r.Nodes[i].ID < r.Nodes[j].ID })
	sortEdgesByKey(r.Relationships)
	sort.Strings(r.Seeds)
	sort.Strings(r.Coverage.Packages)
	sort.Strings(r.Coverage.Files)
	sort.Strings(r.Coverage.ExcludedGoFiles)
	sort.Slice(r.Boundaries, func(i, j int) bool {
		a, b := r.Boundaries[i], r.Boundaries[j]
		return fmt.Sprint(a.Node, a.Kind, a.Reason, a.Evidence) < fmt.Sprint(b.Node, b.Kind, b.Reason, b.Evidence)
	})
}
