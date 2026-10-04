package contracttrace

import "testing"

func TestScopePriorityKeepsNearbyModeledStorage(t *testing.T) {
	ix := &index{funcs: map[string]*function{}, edges: []Relationship{
		{From: "seed", To: "a", Kind: "call", Certainty: "fact"},
		{From: "a", To: "b", Kind: "call", Certainty: "fact"},
		{From: "b", To: "c", Kind: "call", Certainty: "fact"},
		{From: "seed", To: "table", Kind: "sql_read", Certainty: "possible"},
	}}
	for _, id := range []string{"seed", "a", "b", "c", "table"} {
		ix.funcs[id] = &function{node: Node{ID: id, Name: id, Kind: "function"}}
	}
	r, err := ix.expand(Options{Seeds: []string{"seed"}, Depth: 3, MaxNodes: 4}, Coverage{})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, node := range r.Nodes {
		found[node.ID] = true
	}
	if !found["table"] || found["c"] || !r.Coverage.Truncated {
		t.Fatalf("nearby modeled storage displaced by distant calls: %v", found)
	}
}

func TestScopePriorityRevisitsShorterPathsWithinDepth(t *testing.T) {
	ix := &index{funcs: map[string]*function{}, edges: []Relationship{
		{From: "seed", To: "a", Kind: "call", Certainty: "fact"},
		{From: "a", To: "b", Kind: "call", Certainty: "fact"},
		{From: "b", To: "c", Kind: "call", Certainty: "fact"},
		{From: "c", To: "d", Kind: "call", Certainty: "fact"},
		{From: "seed", To: "b", Kind: "resolved_callback_call", Certainty: "possible"},
	}}
	for _, id := range []string{"seed", "a", "b", "c", "d"} {
		ix.funcs[id] = &function{node: Node{ID: id, Name: id, Kind: "function"}}
	}
	r, err := ix.expand(Options{Seeds: []string{"seed"}, Depth: 3, MaxNodes: 5}, Coverage{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"seed": 0, "a": 1, "b": 1, "c": 2, "d": 3}
	if len(r.Nodes) != len(want) || r.Coverage.Truncated {
		t.Fatalf("shorter path scope: %+v", r)
	}
	for _, node := range r.Nodes {
		if node.Distance != want[node.ID] {
			t.Errorf("%s distance=%d want=%d", node.ID, node.Distance, want[node.ID])
		}
		if node.ReachedBy != nil {
			parent := node.ReachedBy.From
			if parent == node.ID {
				parent = node.ReachedBy.To
			}
			if want[parent]+1 != node.Distance {
				t.Errorf("%s has inconsistent parent %s", node.ID, parent)
			}
		}
	}
}

func TestScopeBudgetKeepsEstablishedPathsBeforePossibleExpansion(t *testing.T) {
	ix := &index{funcs: map[string]*function{}, edges: []Relationship{
		{From: "seed", To: "caller", Kind: "call", Certainty: "fact"},
		{From: "caller", To: "entry", Kind: "call", Certainty: "fact"},
		{From: "entry", To: "outer", Kind: "call", Certainty: "fact"},
		{From: "seed", To: "outside", Kind: "external_input", Certainty: "possible"},
		{From: "outside", To: "speculative", Kind: "possible_callback_call", Certainty: "possible"},
	}}
	for _, id := range []string{"seed", "caller", "entry", "outer", "outside", "speculative"} {
		ix.funcs[id] = &function{node: Node{ID: id, Name: id, Kind: "function"}}
	}
	r, err := ix.expand(Options{Seeds: []string{"seed"}, Depth: 3, MaxNodes: 4, ExpandCallbacks: true}, Coverage{})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, node := range r.Nodes {
		found[node.ID] = true
	}
	for _, id := range []string{"seed", "caller", "entry", "outer"} {
		if !found[id] {
			t.Errorf("bounded scope lost established path node %s: %v", id, found)
		}
	}
	if !r.Coverage.Truncated {
		t.Fatal("possible expansion omission was not disclosed")
	}
}
