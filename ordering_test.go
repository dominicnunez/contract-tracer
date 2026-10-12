package contracttrace

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// This ceiling distinguishes one formatted key per relationship from the
// legacy comparator's formatted-key allocation on every sort comparison.
func TestCanonicalEdgesAvoidsPerComparisonFormatting(t *testing.T) {
	input := orderingFixture(256)
	allocations := testing.AllocsPerRun(3, func() { _ = canonicalEdges(input) })
	limit := float64(len(input)*12 + 256)
	t.Logf("canonicalEdges allocations: %.0f for %d relationships (limit %.0f)", allocations, len(input), limit)
	if allocations > limit {
		t.Fatalf("canonical edge sorting allocated %.0f objects for %d relationships; want <= %.0f (one key per relationship, not one per comparator call)", allocations, len(input), limit)
	}
}

func orderingFixture(count int) []Relationship {
	input := make([]Relationship, count)
	for i := range input {
		input[i] = Relationship{
			From:      "source-" + string(rune('A'+i%26)) + "-" + itoaForOrderingTest(i),
			To:        "target-" + itoaForOrderingTest(count-i),
			Kind:      "call",
			Certainty: "fact",
			Evidence:  Evidence{File: "fixture.go", Line: i + 1, Column: i%17 + 1},
		}
	}
	return input
}

func TestPrioritySortAvoidsPerComparisonFormatting(t *testing.T) {
	input := orderingFixture(256)
	allocations := testing.AllocsPerRun(3, func() {
		edges := append([]Relationship(nil), input...)
		legacySortByPriorityAndKeyForTest(edges)
	})
	optimized := testing.AllocsPerRun(3, func() {
		edges := append([]Relationship(nil), input...)
		sortEdgesByPriorityAndKey(edges)
	})
	t.Logf("priority sort allocations: legacy %.0f, precomputed %.0f for %d relationships", allocations, optimized, len(input))
	if optimized > allocations*0.4 {
		t.Fatalf("precomputed priority sort allocated %.0f objects; want at most 40%% of legacy comparator's %.0f", optimized, allocations)
	}
}

func itoaForOrderingTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func legacyCanonicalEdgesForTest(input []Relationship) []Relationship {
	edges := map[string]Relationship{}
	values := map[string]map[string]bool{}
	for _, edge := range input {
		key := edgeKey(edge)
		if _, ok := edges[key]; !ok {
			edges[key] = edge
			values[key] = map[string]bool{}
		}
		for _, value := range edge.Values {
			values[key][value] = true
		}
	}
	output := make([]Relationship, 0, len(edges))
	for key, edge := range edges {
		if len(values[key]) > 0 {
			edge.Values = sortedKeys(values[key])
		}
		output = append(output, edge)
	}
	sort.Slice(output, func(i, j int) bool { return edgeKey(output[i]) < edgeKey(output[j]) })
	return output
}

func legacySortByKeyForTest(edges []Relationship) {
	sort.Slice(edges, func(i, j int) bool { return edgeKey(edges[i]) < edgeKey(edges[j]) })
}

func legacySortByPriorityAndKeyForTest(edges []Relationship) {
	sort.Slice(edges, func(i, j int) bool {
		if edgePriority(edges[i]) != edgePriority(edges[j]) {
			return edgePriority(edges[i]) < edgePriority(edges[j])
		}
		return edgeKey(edges[i]) < edgeKey(edges[j])
	})
}

func cloneRelationshipsForTest(input []Relationship) []Relationship {
	copy := append([]Relationship(nil), input...)
	for i := range copy {
		if input[i].Values != nil {
			copy[i].Values = append([]string{}, input[i].Values...)
		}
	}
	return copy
}

func TestCanonicalEdgesPreservesLegacyFormattedKeySemantics(t *testing.T) {
	input := []Relationship{
		{From: "left|mid", To: "right", Kind: "call", Certainty: "fact", Slot: "slot", Evidence: Evidence{File: "f|g.go", Line: 1, Column: 2, Snippet: "first collision representative"}, Values: []string{"z", "a", "a"}},
		{From: "left", To: "mid|right", Kind: "call", Certainty: "fact", Slot: "slot", Evidence: Evidence{File: "f|g.go", Line: 1, Column: 2, Snippet: "different fields, same formatted key"}, Values: []string{"b"}},
		{From: "src|with|pipes", To: "dst", Kind: "field_read", Certainty: "possible", Slot: "a:b", Evidence: Evidence{File: "z.go", Line: 1234567890, Column: 12345678901, Snippet: "wide numeric fields"}},
		{From: "src", To: "dst", Kind: "event_publish", Certainty: "possible", Slot: "", Evidence: Evidence{File: "a.go", Line: -3, Column: 0, Snippet: "negative line formatting"}, Values: make([]string, 0)},
		{From: "src", To: "dst", Kind: "event_publish", Certainty: "possible", Slot: "", Evidence: Evidence{File: "a.go", Line: -3, Column: 0, Snippet: "later duplicate"}, Values: nil},
		{From: "src", To: "dst", Kind: "sql_read", Certainty: "fact", Slot: "", Evidence: Evidence{File: "b.go", Line: 8, Column: 1}, Values: []string{"same"}},
		{From: "src", To: "dst", Kind: "sql_read", Certainty: "fact", Slot: "", Evidence: Evidence{File: "b.go", Line: 8, Column: 1}, Values: []string{"same"}},
	}
	original := cloneRelationshipsForTest(input)
	want := legacyCanonicalEdgesForTest(input)
	got := canonicalEdges(input)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical edge output differs from legacy formatted-key behavior\ngot:  %#v\nwant: %#v", got, want)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatal("canonicalization mutated caller-owned relationships")
	}
	if got[0].From != "left|mid" || got[0].Evidence.Snippet != "first collision representative" {
		t.Fatalf("equal formatted keys did not keep the first representative: %#v", got[0])
	}
	if !reflect.DeepEqual(got[0].Values, []string{"a", "b", "z"}) {
		t.Fatalf("duplicate value union is not sorted/deduplicated: %#v", got[0].Values)
	}
	got[0].Values[0] = "changed"
	if input[0].Values[0] != "z" {
		t.Fatal("merged output Values aliases caller-owned input storage")
	}
}

func TestPrecomputedOrderingMatchesLegacyComparators(t *testing.T) {
	input := []Relationship{
		{From: "b|c", To: "d", Kind: "call", Certainty: "fact", Evidence: Evidence{File: "z.go", Line: 10, Column: 1, Snippet: "collision later"}},
		{From: "b", To: "c|d", Kind: "call", Certainty: "fact", Evidence: Evidence{File: "z.go", Line: 10, Column: 1, Snippet: "collision first"}},
		{From: "same", To: "node", Kind: "scalar_read", Certainty: "possible", Evidence: Evidence{File: "a.go", Line: 1234567890, Column: 12345678901}},
		{From: "same", To: "node", Kind: "call", Certainty: "possible", Evidence: Evidence{File: "a.go", Line: 2, Column: 1}},
		{From: "same", To: "node", Kind: "sql_read", Certainty: "fact", Evidence: Evidence{File: "z.go", Line: 3, Column: 1}},
		{From: "same", To: "node", Kind: "event_publish", Certainty: "fact", Evidence: Evidence{File: "a.go", Line: 3, Column: 1}},
		{From: "same", To: "node", Kind: "call", Certainty: "fact", Evidence: Evidence{File: "b.go", Line: 2, Column: 1}, Values: []string{"x"}},
	}

	wantKey := cloneRelationshipsForTest(input)
	legacySortByKeyForTest(wantKey)
	gotKey := cloneRelationshipsForTest(input)
	sortEdgesByKey(gotKey)
	if !reflect.DeepEqual(gotKey, wantKey) {
		t.Fatalf("precomputed report ordering differs from legacy edgeKey order\ngot:  %#v\nwant: %#v", gotKey, wantKey)
	}

	wantPriority := cloneRelationshipsForTest(input)
	legacySortByPriorityAndKeyForTest(wantPriority)
	gotPriority := cloneRelationshipsForTest(input)
	sortEdgesByPriorityAndKey(gotPriority)
	if !reflect.DeepEqual(gotPriority, wantPriority) {
		t.Fatalf("precomputed priority ordering differs from legacy priority/key order\ngot:  %#v\nwant: %#v", gotPriority, wantPriority)
	}
}

func TestPrecomputedOrderingMatchesGeneratedLegacyCases(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5eed))
	pool := make([]Relationship, 180)
	kinds := []string{"call", "sql_read", "event_publish", "scalar_read", "field_write"}
	for i := range pool {
		from := "source-" + itoaForOrderingTest(rng.Intn(19))
		to := "target-" + itoaForOrderingTest(rng.Intn(23))
		if i%17 == 0 {
			from += "|segment"
		}
		pool[i] = Relationship{
			From: from, To: to, Kind: kinds[rng.Intn(len(kinds))],
			Certainty: []string{"fact", "possible"}[rng.Intn(2)],
			Slot:      []string{"", "arg:0", "result:1", "x|y"}[rng.Intn(4)],
			Evidence: Evidence{File: []string{"a.go", "b.go", "dir/c.go"}[rng.Intn(3)],
				Line: rng.Intn(1_500_000_000), Column: rng.Intn(1_500_000_000), Snippet: "origin-" + itoaForOrderingTest(i)},
			Values: []string{"v-" + itoaForOrderingTest(rng.Intn(12)), "common"},
		}
	}
	input := make([]Relationship, 0, 280)
	for i := 0; i < 220; i++ {
		input = append(input, pool[rng.Intn(len(pool))])
	}
	// Add equal formatted-key records with distinct snippets and value inputs.
	for i := 0; i < 60; i++ {
		base := pool[i]
		base.Evidence.Snippet = "duplicate-origin-" + itoaForOrderingTest(i)
		base.Values = []string{"extra-" + itoaForOrderingTest(i%9)}
		input = append(input, base)
	}
	rng.Shuffle(len(input), func(i, j int) { input[i], input[j] = input[j], input[i] })

	canonicalWant := legacyCanonicalEdgesForTest(input)
	canonicalGot := canonicalEdges(input)
	if !reflect.DeepEqual(canonicalGot, canonicalWant) {
		t.Fatal("generated canonical output differs from the legacy formatted-key reference")
	}
	wantKey := cloneRelationshipsForTest(input)
	legacySortByKeyForTest(wantKey)
	gotKey := cloneRelationshipsForTest(input)
	sortEdgesByKey(gotKey)
	if !reflect.DeepEqual(gotKey, wantKey) {
		t.Fatal("generated report ordering differs from the legacy formatted-key reference")
	}
	wantPriority := cloneRelationshipsForTest(input)
	legacySortByPriorityAndKeyForTest(wantPriority)
	gotPriority := cloneRelationshipsForTest(input)
	sortEdgesByPriorityAndKey(gotPriority)
	if !reflect.DeepEqual(gotPriority, wantPriority) {
		t.Fatal("generated priority ordering differs from the legacy priority/key reference")
	}
	for i := 1; i < len(gotPriority); i++ {
		previous, current := gotPriority[i-1], gotPriority[i]
		if edgePriority(previous) > edgePriority(current) || edgePriority(previous) == edgePriority(current) && edgeKey(previous) > edgeKey(current) {
			t.Fatalf("priority tiers or exact formatted-key order violated at positions %d/%d", i-1, i)
		}
	}
}

func TestPrecomputedOrderingSkipsTrivialSlices(t *testing.T) {
	edge := Relationship{From: "only", To: "edge", Kind: "call", Certainty: "fact"}
	one := []Relationship{edge}
	keyAllocs := testing.AllocsPerRun(20, func() { sortEdgesByKey(nil) })
	priorityAllocs := testing.AllocsPerRun(20, func() { sortEdgesByPriorityAndKey(nil) })
	oneKeyAllocs := testing.AllocsPerRun(20, func() { sortEdgesByKey(one) })
	onePriorityAllocs := testing.AllocsPerRun(20, func() { sortEdgesByPriorityAndKey(one) })
	if keyAllocs != 0 || priorityAllocs != 0 || oneKeyAllocs != 0 || onePriorityAllocs != 0 {
		t.Fatalf("trivial edge sorting decorated without comparisons: nil key %.0f, nil priority %.0f, one key %.0f, one priority %.0f allocations", keyAllocs, priorityAllocs, oneKeyAllocs, onePriorityAllocs)
	}
}

func TestSortReportUsesPrecomputedFormattedKeys(t *testing.T) {
	input := []Relationship{
		{From: "a|b", To: "c", Kind: "call", Certainty: "fact", Evidence: Evidence{File: "same.go", Line: 1, Column: 1, Snippet: "first"}},
		{From: "a", To: "b|c", Kind: "call", Certainty: "fact", Evidence: Evidence{File: "same.go", Line: 1, Column: 1, Snippet: "second"}},
		{From: "z", To: "x", Kind: "event_dispatch", Certainty: "possible", Evidence: Evidence{File: "other.go", Line: 2, Column: 10}},
	}
	want := cloneRelationshipsForTest(input)
	legacySortByKeyForTest(want)
	report := Report{Relationships: cloneRelationshipsForTest(input)}
	sortReport(&report)
	if !reflect.DeepEqual(report.Relationships, want) {
		t.Fatalf("sortReport changed legacy edge ordering: got %#v want %#v", report.Relationships, want)
	}
}
