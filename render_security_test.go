package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownKeepsReportValuesInsideTheirContexts(t *testing.T) {
	plain := "<em>label</em>\n# forged heading"
	path := "src/odd`path\n| forged row.md"
	report := Report{
		Invariant: plain,
		Focus:     []string{"focus|name", "two`tick"},
		Coverage: Coverage{
			Build:              map[string]string{"GOOS": "linux\n# build", "GOARCH": "amd64"},
			Tags:               "tag|value",
			SourceSHA256:       "hash`with`ticks",
			LoadedSourceSHA256: "loaded`hash",
			LoadedSources:      []LoadedSource{{Path: path}},
			ExcludedGoFiles:    []string{path},
		},
		Nodes: []Node{{
			ID:       "node|id",
			Name:     "name **outside** <b>text</b>",
			Kind:     "kind|value",
			Evidence: Evidence{File: path, Snippet: "source\ntext"},
			ReachedBy: &Relationship{
				From: "from`id", To: "to|id", Kind: "edge|kind", Certainty: "possible\n# block",
				Evidence: Evidence{File: path},
			},
			Relevance: &Relevance{
				Anchor: "anchor`id", Distance: 1, PathCertainty: "possible\n| row",
				Via: &Relationship{From: "via`from", To: "via|to", Kind: "via|kind", Certainty: "possible", Evidence: Evidence{File: path}},
			},
		}},
		Boundaries: []Boundary{
			{
				Node: "boundary|node", Kind: "bad | forged\n| injected | row |", Reason: "unused text",
				Evidence: Evidence{File: path}, Candidates: []string{"candidate|one"}, Examples: []string{"example`one"},
			},
			{Kind: "plain-reason", Reason: "<em>label</em>\n# forged heading"},
		},
	}

	markdown := Markdown(report)
	if strings.Contains(markdown, plain) || strings.Contains(markdown, "<em>label</em>") {
		t.Error("report text was emitted as unescaped Markdown or active markup")
	}
	if strings.Contains(markdown, "\n# forged heading\n") || strings.Contains(markdown, "\n| injected | row |\n") {
		t.Error("report text created a Markdown block or table row")
	}
	if !hasCommonMarkCodeContent(markdown, "src/odd`path\\n| forged row.md") {
		t.Error("source path was not preserved in a valid code span")
	}
}

func TestMarkdownControlNormalizationHasIndependentExpectedText(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
	}{
		{"line and tab controls", "first\r\nsecond\t\x00", `first\r\nsecond\t\u{0000}`},
		{"bidi control", "left\u202e right", `left\u{202E} right`},
		{"invalid UTF-8 byte", string([]byte{0xff}), `\xFF`},
		{"Unicode text", "café 雪", "café 雪"},
		{"Unicode line separators", "first\u2028second\u2029", `first\u{2028}second\u{2029}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := visibleMarkdownControls(test.input); got != test.want {
				t.Fatalf("visibleMarkdownControls(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestMarkdownFromSavedExplorationKeepsSourceTextLiteral(t *testing.T) {
	root := t.TempDir()
	module := "module example.com/eventmarkdown\n\ngo 1.27.0\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	filename := "event`source.go"
	source := `package app

type Envelope struct { EventType string }

func Emit() { _ = Envelope{EventType: "<em>label</em>"} }
`
	if err := os.WriteFile(filepath.Join(root, filename), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	options := Options{
		Root: root, Seeds: []string{"Emit"}, Invariant: "<em>label</em>", Depth: 2, MaxNodes: 50,
		Config: Config{EventFields: []string{"EventType"}},
	}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	line := 5
	owner := "example.com/eventmarkdown::Emit"
	resource := "event:%3Cem%3Elabel%3C%2Fem%3E"
	if !hasRelationship(fresh, owner, resource, "event_construct", filename, line) {
		t.Fatalf("Trace did not retain the source-backed event candidate: %+v", fresh.Relationships)
	}
	var encoded bytes.Buffer
	if err := WriteAnalysis(&encoded, analysis, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: options.Seeds, Depth: options.Depth, MaxNodes: options.MaxNodes})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		if !hasRelationship(report, owner, resource, "event_construct", filename, line) {
			t.Fatal("saved exploration lost the source-backed event candidate")
		}
		markdown := Markdown(report)
		if strings.Contains(markdown, "<em>label</em>") || !strings.Contains(markdown, `\<em\>label\<\/em\>`) {
			t.Fatalf("Markdown did not present the event value as literal text: %s", markdown)
		}
		if !hasCommonMarkCodeContent(markdown, filename+":5") {
			t.Fatalf("Markdown did not preserve the backtick source filename in a code span: %s", markdown)
		}
	}
}

func TestMarkdownCodeSpanBoundaryCases(t *testing.T) {
	for _, value := range []string{"", "plain", "`", "```", "leading ", " trailing", " both ", "   ", "\r\n", "`edge`", "spoof\u202e\ntext", string([]byte{0xff})} {
		t.Run(value, func(t *testing.T) {
			rendered := markdownCode(value)
			if strings.ContainsAny(rendered, "\r\n") {
				t.Fatalf("rendered code span contains a structural line break: %q", rendered)
			}
			if !hasCommonMarkCodeContent(rendered, expectedCodeText(value)) {
				t.Fatalf("CommonMark code span changed %q: %q", value, rendered)
			}
		})
	}
}

func TestMarkdownPlainTextEscapesCommonMarkPunctuation(t *testing.T) {
	input := "<b>x</b>\r\n| row"
	want := `\<b\>x\<\/b\>\\r\\n\| row`
	if got := markdownText(input); got != want {
		t.Fatalf("markdownText(%q) = %q, want %q", input, got, want)
	}
}

// This fuzz target checks code-span delimiters and whitespace preservation.
// Exact control-character normalization has independent examples above.
func FuzzMarkdownCodeSpanDelimiterRules(f *testing.F) {
	for _, seed := range []string{"", "plain", "`", "`` and `", " leading", "trailing ", "   ", "\r\n|#", "<b>&entity;", "spoof\u202e\ntext", string([]byte{0xff})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		rendered := markdownCode(value)
		if strings.ContainsAny(rendered, "\r\n") {
			t.Fatalf("rendered code span contains a structural line break: %q", rendered)
		}
		if !hasCommonMarkCodeContent(rendered, expectedCodeText(value)) {
			t.Fatalf("CommonMark code span changed %q: %q", value, rendered)
		}
	})
}

func expectedCodeText(value string) string {
	if value == "" {
		return `""`
	}
	return visibleMarkdownControls(value)
}

// This small reader is an independent check of CommonMark code-span delimiters
// and the spec's single-space trimming rule; it does not call the renderer.
func hasCommonMarkCodeContent(document, want string) bool {
	for i := 0; i < len(document); {
		if document[i] != '`' {
			i++
			continue
		}
		start := i
		for i < len(document) && document[i] == '`' {
			i++
		}
		run := i - start
		for search := i; search < len(document); {
			if document[search] != '`' {
				search++
				continue
			}
			end := search
			for end < len(document) && document[end] == '`' {
				end++
			}
			if end-search == run {
				content := document[i:search]
				if len(content) >= 2 && content[0] == ' ' && content[len(content)-1] == ' ' && strings.Trim(content, " ") != "" {
					content = content[1 : len(content)-1]
				}
				if content == want {
					return true
				}
				search = end
				break
			}
			search = end
		}
	}
	return false
}
