package contracttrace

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

const analysisSchema = "contract-tracer.analysis.v0.1"

// Analysis retains the discovered graph, including scope-excluded candidates.
// It is a source-bound snapshot, not a completeness certificate.
type Analysis struct {
	Schema            string                 `json:"schema"`
	Root              string                 `json:"root"`
	Invariant         string                 `json:"invariant"`
	Coverage          Coverage               `json:"coverage"`
	Config            Config                 `json:"config"`
	Nodes             []Node                 `json:"nodes"`
	Relationships     []Relationship         `json:"relationships"`
	Boundaries        []Boundary             `json:"unresolved_boundaries"`
	Ranges            map[string]SourceRange `json:"function_ranges"`
	DeclarationRanges map[string]SourceRange `json:"declaration_ranges,omitempty"`
}
type SourceRange struct {
	First int `json:"first_line"`
	Last  int `json:"last_line"`
}
type ExploreOptions struct {
	Seeds, Locations []string
	Invariant        string
	Focus            []string
	Depth, MaxNodes  int
	ExpandCallbacks  bool
}

func TraceWithAnalysis(ctx context.Context, o Options) (Report, Analysis, error) {
	var analysis Analysis
	o.capture = &analysis
	r, err := Trace(ctx, o)
	return r, analysis, err
}
func ReadAnalysis(reader io.Reader) (Analysis, error) {
	var a Analysis
	buffered := bufio.NewReader(reader)
	if magic, _ := buffered.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		compressed, err := gzip.NewReader(buffered)
		if err != nil {
			return a, fmt.Errorf("compressed saved analysis: %w", err)
		}
		defer compressed.Close()
		reader = compressed
	} else {
		reader = buffered
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&a); err != nil {
		return a, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Analysis{}, fmt.Errorf("saved analysis must contain exactly one JSON object")
	}
	if err := a.validate(); err != nil {
		return Analysis{}, err
	}
	return a, nil
}

// WriteAnalysis streams the complete snapshot as JSON, optionally in gzip.
// The caller owns the writer. Compression changes representation, not scope.
func WriteAnalysis(writer io.Writer, a Analysis, gzipCompressed bool) error {
	if err := a.validate(); err != nil {
		return err
	}
	if !gzipCompressed {
		return json.NewEncoder(writer).Encode(a)
	}
	compressed, err := gzip.NewWriterLevel(writer, gzip.BestSpeed)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(compressed).Encode(a); err != nil {
		compressed.Close()
		return err
	}
	return compressed.Close()
}
func Explore(ctx context.Context, a Analysis, o ExploreOptions) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if err := a.validate(); err != nil {
		return Report{}, err
	}
	if len(o.Seeds) == 0 && len(o.Locations) == 0 {
		return Report{}, fmt.Errorf("at least one symbol or source-location seed is required")
	}
	if o.Depth < 1 || o.MaxNodes < 1 {
		return Report{}, fmt.Errorf("depth and max-nodes must be positive")
	}
	before, _, err := fingerprint(a.Root)
	if err != nil {
		return Report{}, err
	}
	assets, err := hashFiles(a.Root, a.Coverage.EmbeddedFiles)
	if err != nil {
		return Report{}, err
	}
	identity, _ := json.Marshal(struct {
		Build  map[string]string
		Config Config
		Tests  bool
		Tags   string
	}{a.Coverage.Build, a.Config, a.Coverage.Tests, a.Coverage.Tags})
	if err := verifyLoadedSources(ctx, a.Coverage.LoadedSources); err != nil {
		return Report{}, err
	}
	if hashText(before+string(identity)+assets+a.Coverage.LoadedSourceSHA256+a.Coverage.ResolutionSHA256) != a.Coverage.SourceSHA256 {
		return Report{}, fmt.Errorf("source changed since saved analysis; rerun analysis before exploring")
	}
	if err := verifyLoadedSources(ctx, a.Coverage.ResolutionInputs); err != nil {
		return Report{}, fmt.Errorf("resolution input check: %w", err)
	}
	if err := verifyBuildEnvironment(ctx, a.Root, a.Coverage.Build); err != nil {
		return Report{}, err
	}
	if a.Coverage.ResolutionScope == "workspace_modules.v1" {
		inputs, err := captureResolutionInputs(a.Coverage.Build)
		if err != nil {
			return Report{}, err
		}
		if loadedSourceIdentity(inputs) != a.Coverage.ResolutionSHA256 {
			return Report{}, fmt.Errorf("workspace module inventory changed; rerun analysis")
		}
	}
	ix := &index{root: a.Root, funcs: map[string]*function{}, edges: a.Relationships, boundaries: a.Boundaries, ranges: a.Ranges, declarationRanges: a.DeclarationRanges}
	for _, n := range a.Nodes {
		n.Distance = 0
		n.ReachedBy = nil
		n.Relevance = nil
		ix.funcs[n.ID] = &function{node: n}
	}
	coverage := a.Coverage
	coverage.Depth = o.Depth
	coverage.MaxNodes = o.MaxNodes
	coverage.Truncated = false
	coverage.ExpandCallbacks = o.ExpandCallbacks
	report, err := ix.expand(Options{Seeds: o.Seeds, Locations: o.Locations, Focus: o.Focus, Depth: o.Depth, MaxNodes: o.MaxNodes, ExpandCallbacks: o.ExpandCallbacks}, coverage)
	if err != nil {
		return Report{}, err
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	report.Schema = "contract-tracer.v0.1"
	report.Root = a.Root
	report.Config = a.Config
	report.Invariant = o.Invariant
	if report.Invariant == "" {
		report.Invariant = a.Invariant
	}
	report.Boundaries = append(report.Boundaries, Boundary{Kind: "saved_analysis", Reason: "Explored a saved graph after checking target Go/SQL/module, selected embedded bytes and any captured loaded Go sources. Dependency analysis and unmodeled relationships were not refreshed; original analysis boundaries remain."})
	if len(a.Coverage.LoadedSources) == 0 {
		report.Boundaries = append(report.Boundaries, Boundary{Kind: "legacy_loaded_source_identity", Reason: "this snapshot predates loaded-source capture; dependency and generated Go source edits are not checked, so rerun analysis for their source identity"})
	}
	if _, captured := a.Coverage.Build["GOWORK"]; !captured {
		report.Boundaries = append(report.Boundaries, Boundary{Kind: "legacy_resolution_identity", Reason: "this snapshot predates workspace and GOFLAGS capture; workspace selection and those settings are not checked, so rerun analysis for their identity"})
	}
	if a.Coverage.ResolutionScope == "" {
		report.Boundaries = append(report.Boundaries, Boundary{Kind: "legacy_workspace_module_identity", Reason: "this snapshot predates workspace module manifest capture; sibling module dependency changes are not checked, so rerun analysis for their identity"})
	}
	if a.Coverage.ResolutionScope != "loaded_modules.v1" {
		report.Boundaries = append(report.Boundaries, Boundary{Kind: "legacy_dependency_module_identity", Reason: "this snapshot predates effective dependency module manifest capture; external dependency module directives are not checked, so rerun analysis for their identity"})
	}
	groupBoundaries(&report)
	return report, nil
}

func (ix *index) snapshot(r Report) Analysis {
	full := Report{Nodes: []Node{}, Relationships: canonicalEdges(ix.edges), Boundaries: append([]Boundary(nil), ix.boundaries...)}
	ranges := map[string]SourceRange{}
	for id, f := range ix.funcs {
		node := f.node
		node.Distance = 0
		node.ReachedBy = nil
		node.Relevance = nil
		full.Nodes = append(full.Nodes, node)
		if f.decl != nil {
			ranges[id] = SourceRange{ix.fset.Position(f.decl.Pos()).Line, ix.fset.Position(f.decl.End()).Line}
		}
	}
	for _, b := range r.Boundaries {
		if b.Kind == "semantic_scope" || b.Kind == "analysis_model" || b.Kind == "lifecycle" || b.Kind == "loaded_source_identity" {
			full.Boundaries = append(full.Boundaries, b)
		}
	}
	groupBoundaries(&full)
	coverage := r.Coverage
	coverage.Depth = 0
	coverage.MaxNodes = 0
	coverage.Truncated = false
	coverage.ExpandCallbacks = false
	declarations := map[string]SourceRange{}
	for id, span := range ix.declarationRanges {
		declarations[id] = span
	}
	return Analysis{analysisSchema, r.Root, r.Invariant, coverage, r.Config, full.Nodes, full.Relationships, full.Boundaries, ranges, declarations}
}

func (a Analysis) validate() error {
	if a.Schema != analysisSchema {
		return fmt.Errorf("unsupported saved analysis schema %q", a.Schema)
	}
	if a.Root == "" || len(a.Coverage.SourceSHA256) != 64 {
		return fmt.Errorf("saved analysis has no source identity")
	}
	if _, err := hex.DecodeString(a.Coverage.SourceSHA256); err != nil {
		return fmt.Errorf("invalid saved source fingerprint")
	}
	if err := validateSourceInventory(a.Coverage.LoadedSources, a.Coverage.LoadedSourceSHA256); err != nil {
		return err
	}
	if err := validateSourceInventory(a.Coverage.ResolutionInputs, a.Coverage.ResolutionSHA256); err != nil {
		return fmt.Errorf("resolution inventory: %w", err)
	}
	if a.Coverage.ResolutionScope != "" && a.Coverage.ResolutionScope != "workspace_modules.v1" && a.Coverage.ResolutionScope != "loaded_modules.v1" {
		return fmt.Errorf("unsupported saved resolution scope")
	}
	if work, captured := a.Coverage.Build["GOWORK"]; captured && work != "" && work != "off" {
		found := false
		for _, input := range a.Coverage.ResolutionInputs {
			found = found || input.Path == strings.ReplaceAll(work, "\\", "/")
		}
		if !found {
			return fmt.Errorf("saved workspace has no matching resolution input")
		}
	}
	for _, file := range a.Coverage.EmbeddedFiles {
		if file == "" || file == ".." || strings.HasPrefix(file, "../") || strings.HasPrefix(file, "/") || strings.ContainsAny(file, "\\:") || path.Clean(file) != file {
			return fmt.Errorf("saved embedded asset is not a relative target file: %q", file)
		}
	}
	if err := validateConfig(a.Config); err != nil {
		return err
	}
	nodes := map[string]Node{}
	for _, n := range a.Nodes {
		if n.Relevance != nil {
			return fmt.Errorf("saved analysis contains query-specific contract relevance")
		}
		if n.ID == "" {
			return fmt.Errorf("saved analysis contains an empty node ID")
		}
		if _, present := nodes[n.ID]; present {
			return fmt.Errorf("saved analysis contains duplicate node %q", n.ID)
		}
		nodes[n.ID] = n
	}
	for _, e := range a.Relationships {
		if _, ok := nodes[e.From]; !ok {
			return fmt.Errorf("saved analysis edge has missing source %q", e.From)
		}
		if _, ok := nodes[e.To]; !ok {
			return fmt.Errorf("saved analysis edge has missing target %q", e.To)
		}
	}
	for id, span := range a.Ranges {
		node, ok := nodes[id]
		if !ok || node.Kind != "function" || span.First < 1 || span.Last < span.First || node.Evidence.Line != span.First {
			return fmt.Errorf("invalid saved function range for %q", id)
		}
	}
	for id, span := range a.DeclarationRanges {
		node, ok := nodes[id]
		if !ok || !declarationKind(node.Kind) || span.First < 1 || span.Last < span.First || node.Evidence.Line != span.First {
			return fmt.Errorf("invalid saved declaration range for %q", id)
		}
	}
	for _, b := range a.Boundaries {
		if b.Node != "" {
			if _, present := nodes[b.Node]; !present {
				return fmt.Errorf("saved boundary has missing node %q", b.Node)
			}
		}
		if b.CandidateCount < 0 || b.CandidateCount > 0 && b.CandidateCount < len(b.Examples) {
			return fmt.Errorf("invalid saved boundary candidate count")
		}
		if len(b.Candidates) > 0 {
			if len(b.Candidates) != b.CandidateCount {
				return fmt.Errorf("saved boundary candidate inventory does not match count")
			}
			for i, target := range b.Candidates {
				if target == "" || i > 0 && b.Candidates[i-1] >= target {
					return fmt.Errorf("saved boundary candidates must be nonempty, distinct and sorted")
				}
			}
			for _, example := range b.Examples {
				at := sort.SearchStrings(b.Candidates, example)
				if at == len(b.Candidates) || b.Candidates[at] != example {
					return fmt.Errorf("saved boundary example is absent from candidate inventory")
				}
			}
		}
	}
	return nil
}

// Several SSA instances can project onto one named source function. Retain
// their summary union instead of whichever duplicate relationship arrived first.
func canonicalEdges(input []Relationship) []Relationship {
	edges := map[string]Relationship{}
	values := map[string]map[string]bool{}
	for _, e := range input {
		key := edgeKey(e)
		if _, ok := edges[key]; !ok {
			edges[key] = e
		}
		if len(e.Values) > 0 {
			if values[key] == nil {
				values[key] = map[string]bool{}
			}
			for _, v := range e.Values {
				values[key][v] = true
			}
		}
	}
	keys := make([]string, 0, len(edges))
	for key := range edges {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	output := make([]Relationship, 0, len(edges))
	for _, key := range keys {
		e := edges[key]
		if len(values[key]) > 0 {
			e.Values = sortedKeys(values[key])
		}
		output = append(output, e)
	}
	return output
}
