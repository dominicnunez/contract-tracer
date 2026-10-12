package contracttrace

import "testing"

func TestPossibleErrorOriginsDoNotCrowdCoreCallerPaths(t *testing.T) {
	ix := &index{funcs: map[string]*function{
		"seed":   {node: Node{ID: "seed", Name: "seed", Kind: "function"}},
		"caller": {node: Node{ID: "caller", Name: "caller", Kind: "function"}},
		"entry":  {node: Node{ID: "entry", Name: "entry", Kind: "function"}},
		"error":  {node: Node{ID: "error", Name: "error", Kind: "error_result"}},
	}, edges: []Relationship{
		{From: "caller", To: "seed", Kind: "call", Certainty: "fact"},
		{From: "entry", To: "caller", Kind: "call", Certainty: "fact"},
		{From: "seed", To: "error", Kind: "error_return", Certainty: "possible"},
	}}
	r, err := ix.expand(Options{Seeds: []string{"seed"}, Depth: 2, MaxNodes: 3}, Coverage{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "entry") {
		t.Fatal("possible error origin displaced source-backed entry caller")
	}
	if !r.Coverage.Truncated {
		t.Fatal("omitted error candidate was hidden")
	}
	r, err = ix.expand(Options{Seeds: []string{"seed"}, Depth: 2, MaxNodes: 4}, Coverage{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(r, "error") {
		t.Fatal("error candidate was removed instead of deferred")
	}
}
