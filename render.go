package contracttrace

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func visibleMarkdownControls(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		switch value[i] {
		case '\r':
			if i+1 < len(value) && value[i+1] == '\n' {
				b.WriteString(`\r\n`)
				i += 2
			} else {
				b.WriteString(`\r`)
				i++
			}
			continue
		case '\n':
			b.WriteString(`\n`)
			i++
			continue
		case '\t':
			b.WriteString(`\t`)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&b, `\x%02X`, value[i])
			i++
			continue
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) || r == '\u2028' || r == '\u2029' {
			fmt.Fprintf(&b, `\u{%04X}`, r)
		} else {
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func markdownText(value string) string {
	value = visibleMarkdownControls(value)
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if r >= 0x21 && r <= 0x7e && !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func markdownCode(value string) string {
	value = visibleMarkdownControls(value)
	if value == "" {
		// Markdown has no empty code span; show the empty string explicitly.
		value = `""`
	}
	longest, run := 0, 0
	for _, r := range value {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	delimiter := strings.Repeat("`", longest+1)
	content := value
	if len(content) > 0 && (content[0] == ' ' || content[len(content)-1] == ' ' || content[0] == '`' || content[len(content)-1] == '`') && strings.Trim(content, " ") != "" {
		content = " " + content + " "
	}
	return delimiter + content + delimiter
}

// Markdown summarizes scope. JSON retains the complete reported relationships.
func Markdown(r Report) string {
	var b strings.Builder
	fmt.Fprint(&b, "# Contract investigation scope\n\n")
	fmt.Fprintf(&b, "Invariant hypothesis: %s\n\n", markdownText(r.Invariant))
	if len(r.Focus) > 0 {
		focus := make([]string, len(r.Focus))
		for i, value := range r.Focus {
			focus[i] = markdownText(value)
		}
		fmt.Fprintf(&b, "Explicit contract focus: %s. Ordering uses graph distance to these anchors; it does not prove semantic relevance.\n\n", strings.Join(focus, ", "))
	}
	fmt.Fprintf(&b, "Contract completeness is **not established**. %d nodes, %d relationships, %d grouped unresolved boundaries. Scope truncated: **%t**.\n\n", len(r.Nodes), len(r.Relationships), len(r.Boundaries), r.Coverage.Truncated)
	fmt.Fprintf(&b, "Source/configuration fingerprint: %s. Build: %s/%s, %s; tests=%t, tags=%s.\n\n", markdownCode(r.Coverage.SourceSHA256), markdownText(r.Coverage.Build["GOOS"]), markdownText(r.Coverage.Build["GOARCH"]), markdownText(r.Coverage.Build["GOVERSION"]), r.Coverage.Tests, markdownCode(fmt.Sprintf("%q", r.Coverage.Tags)))
	if len(r.Coverage.LoadedSources) > 0 {
		fmt.Fprintf(&b, "Loaded Go source identity: %s across %d captured inputs, including dependency and generated sources. The JSON inventory retains their paths and hashes.\n\n", markdownCode(r.Coverage.LoadedSourceSHA256), len(r.Coverage.LoadedSources))
	}
	fmt.Fprint(&b, "## Included scope\n\n")
	nodes := append([]Node(nil), r.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		if len(r.Focus) > 0 {
			a, b := nodes[i].Relevance, nodes[j].Relevance
			if (a != nil) != (b != nil) {
				return a != nil
			}
			if a != nil && b != nil && a.Distance != b.Distance {
				return a.Distance < b.Distance
			}
		}
		if nodes[i].Distance != nodes[j].Distance {
			return nodes[i].Distance < nodes[j].Distance
		}
		return nodes[i].ID < nodes[j].ID
	})
	for _, n := range nodes {
		fmt.Fprintf(&b, "- **%s** (%s, %d hops): %s\n", markdownText(n.Name), markdownText(n.Kind), n.Distance, markdownCode(fmt.Sprintf("%s:%d", n.Evidence.File, n.Evidence.Line)))
		if relevance := n.Relevance; relevance != nil {
			fmt.Fprintf(&b, "  Contract anchor %s: %d graph hops.\n", markdownCode(relevance.Anchor), relevance.Distance)
			certainty := relevance.PathCertainty
			if certainty == "" {
				certainty = "unknown (annotation has no cumulative certainty)"
			}
			fmt.Fprintf(&b, "  Whole focus path: %s. This describes structural edges, not semantic relevance or an invariant proof.\n", markdownText(certainty))
			if e := relevance.Via; e != nil {
				next := e.From
				if next == n.ID {
					next = e.To
				}
				fmt.Fprintf(&b, "  Next focus-path node %s via %s (%s), at %s.\n", markdownCode(next), markdownCode(e.Kind), markdownText(e.Certainty), markdownCode(fmt.Sprintf("%s:%d", e.Evidence.File, e.Evidence.Line)))
			}
		}
		if e := n.ReachedBy; e != nil {
			other := e.From
			if other == n.ID {
				other = e.To
			}
			attribution := "evidenced"
			if e.Evidence.Origin == "synthetic_declaration" {
				attribution = "attributed to the generating declaration"
			}
			fmt.Fprintf(&b, "  Connected to %s by %s (%s), %s at %s.\n", markdownCode(other), markdownCode(e.Kind), markdownText(e.Certainty), attribution, markdownCode(fmt.Sprintf("%s:%d", e.Evidence.File, e.Evidence.Line)))
		}
	}
	counts := map[string]int{}
	targets := map[string]int{}
	missingInventories := 0
	for _, v := range r.Boundaries {
		counts[v.Kind]++
		targets[v.Kind] += v.CandidateCount
		if v.CandidateCount > 0 && len(v.Candidates) != v.CandidateCount {
			missingInventories++
		}
	}
	kinds := []string{}
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	fmt.Fprintln(&b, "\n## Unresolved boundary summary\n\n| Kind | Entries | Distinct targets counted per entry |\n| --- | ---: | ---: |")
	for _, k := range kinds {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", markdownText(k), counts[k], targets[k])
	}
	fmt.Fprintln(&b, "\nCounts are not globally distinct targets. Fresh JSON reports retain each grouped entry's discovered targets in `candidates`, alongside source evidence and up to five preview `examples`. These sets cover discovered candidates only; omitted bodies, unsupported analysis and widening remain boundaries. Use the JSON report to inspect targets; this summary does not discharge them.")
	if missingInventories > 0 {
		fmt.Fprintf(&b, "\n%d entries lack a full retained candidate inventory, as can occur with older snapshots. Their counts and examples cannot reconstruct the missing targets; rerun analysis to obtain current inventories.\n", missingInventories)
	}
	for _, v := range r.Boundaries {
		if v.Node == "" {
			fmt.Fprintf(&b, "\n- %s: %s\n", markdownText(v.Kind), markdownText(v.Reason))
		}
	}
	if len(r.Coverage.ExcludedGoFiles) > 0 {
		fmt.Fprint(&b, "\n## Unselected Go files\n\n")
		for _, f := range r.Coverage.ExcludedGoFiles {
			fmt.Fprintf(&b, "- %s\n", markdownCode(f))
		}
	}
	return b.String()
}
