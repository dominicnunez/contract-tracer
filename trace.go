package contracttrace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type function struct {
	node Node
	decl *ast.FuncDecl
	pkg  *packages.Package
}
type index struct {
	root               string
	fset               *token.FileSet
	funcs              map[string]*function
	objects            map[*types.Func]string
	edges              []Relationship
	boundaries         []Boundary
	lines              map[string][]string
	flow               *flowAnalysis
	ssaOwners          map[*ssa.Function]string
	ownerCache         map[*ssa.Function]string
	ownerKnown         map[*ssa.Function]bool
	sqlCache           map[string]sqlAnalysis
	storage            StorageCoverage
	embedded           map[*types.Var]string
	assets             []string
	assetHash          string
	ranges             map[string]SourceRange
	declarationRanges  map[string]SourceRange
	globalRanges       map[token.Pos]SourceRange
	storageAssignments map[string]bool
}

func Trace(ctx context.Context, o Options) (report Report, err error) {
	defer func() {
		if p := recover(); p != nil {
			report = Report{}
			err = fmt.Errorf("analysis failed: %v\n%s", p, debug.Stack())
		}
	}()
	if len(o.Seeds) == 0 && len(o.Locations) == 0 {
		return report, fmt.Errorf("at least one symbol or source-location seed is required")
	}
	if o.Depth < 1 || o.MaxNodes < 1 {
		return report, fmt.Errorf("depth and max-nodes must be positive")
	}
	o.Config = normalizeConfig(o.Config)
	if err := validateConfig(o.Config); err != nil {
		return Report{}, err
	}
	root, err := canonicalRoot(o.Root)
	if err != nil {
		return report, err
	}
	before, allFiles, err := fingerprint(root)
	if err != nil {
		return report, err
	}
	env, err := buildEnvironment(ctx, root)
	if err != nil {
		return report, err
	}
	resolution, err := captureResolutionInputs(env)
	if err != nil {
		return report, err
	}
	fset := token.NewFileSet()
	sources := &loadedSourceCapture{}
	cfg := &packages.Config{Context: ctx, Dir: root, Fset: fset, Tests: o.Tests, Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule | packages.NeedEmbedFiles | packages.NeedEmbedPatterns}
	if o.Tags != "" {
		cfg.BuildFlags = []string{"-tags=" + o.Tags}
	}
	metadataConfig := *cfg
	metadataConfig.Mode = packages.NeedName | packages.NeedImports | packages.NeedDeps | packages.NeedModule
	metadata, err := packages.Load(&metadataConfig, "./...")
	if err != nil {
		return report, err
	}
	workspace := resolution
	resolution, err = captureModuleInputs(metadata, workspace)
	if err != nil {
		return report, err
	}
	cfg.ParseFile = sources.parseFile
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return report, err
	}
	var diagnostics []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			diagnostics = append(diagnostics, e.Error())
		}
	})
	if len(diagnostics) > 0 {
		sort.Strings(diagnostics)
		return report, fmt.Errorf("package loading incomplete: %s", strings.Join(diagnostics, "\n"))
	}
	if len(pkgs) == 0 {
		return report, fmt.Errorf("no Go packages loaded")
	}
	loadedResolution, err := captureModuleInputs(pkgs, workspace)
	if err != nil {
		return report, err
	}
	if loadedSourceIdentity(loadedResolution) != loadedSourceIdentity(resolution) {
		return report, fmt.Errorf("module inputs changed during package loading; rerun analysis")
	}
	ix := &index{root: root, fset: fset, funcs: map[string]*function{}, objects: map[*types.Func]string{}, lines: map[string][]string{}}
	ix.declarationRanges = map[string]SourceRange{}
	ix.globalRanges = map[token.Pos]SourceRange{}
	coverage := Coverage{Build: env, Tests: o.Tests, Tags: o.Tags, Depth: o.Depth, MaxNodes: o.MaxNodes, ExpandCallbacks: o.ExpandCallbacks, Packages: []string{}, Files: []string{}, ExcludedGoFiles: []string{}}
	loaded := map[string]bool{}
	for _, p := range pkgs {
		coverage.Packages = append(coverage.Packages, p.ID)
		for _, file := range p.CompiledGoFiles {
			if rel, inside := relative(root, file); inside {
				loaded[rel] = true
			}
		}
		for _, file := range p.Syntax {
			if _, inside := relative(root, fset.Position(file.Pos()).Filename); !inside {
				continue
			}
			for _, d := range file.Decls {
				if declaration, ok := d.(*ast.GenDecl); ok && declaration.Tok == token.VAR {
					for _, spec := range declaration.Specs {
						value := spec.(*ast.ValueSpec)
						for _, name := range value.Names {
							ix.globalRanges[name.Pos()] = SourceRange{fset.Position(name.Pos()).Line, fset.Position(value.End()).Line}
						}
					}
				}
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				obj, ok := p.TypesInfo.Defs[fd.Name].(*types.Func)
				if !ok {
					continue
				}
				e := ix.evidence(fd.Pos())
				name := functionName(obj)
				id := p.PkgPath + "::" + name
				if name == "init" {
					id += fmt.Sprintf("@%s:%d", e.File, e.Line)
				}
				ix.objects[obj.Origin()] = id
				ix.funcs[id] = &function{node: Node{ID: id, Name: name, Kind: "function", Evidence: e}, decl: fd, pkg: p}
			}
		}
	}
	for f := range loaded {
		coverage.Files = append(coverage.Files, f)
	}
	for _, f := range allFiles {
		if strings.HasSuffix(f, ".go") && !loaded[f] {
			coverage.ExcludedGoFiles = append(coverage.ExcludedGoFiles, f)
		}
	}
	for id, f := range ix.funcs {
		ast.Inspect(f.decl.Body, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			obj, ok := f.pkg.TypesInfo.Uses[ident].(*types.Func)
			if !ok {
				return true
			}
			if target := ix.objects[obj.Origin()]; target != "" {
				ix.edge(id, target, "function_reference", "fact", ident.Pos())
			}
			return true
		})
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := ix.loadEmbedded(pkgs); err != nil {
		return Report{}, err
	}
	coverage.EmbeddedFiles = append([]string{}, ix.assets...)
	prog, _ := ssautil.Packages(pkgs, ssa.InstantiateGenerics|ssa.GlobalDebug)
	prog.Build()
	ix.registerInitializers(prog, pkgs)
	ix.recordFields(pkgs, o.Config)
	cg := cha.CallGraph(prog)
	ix.flow, err = ix.analyzeFlow(ctx, prog, cg, o.Config)
	if err != nil {
		return Report{}, err
	}
	for fn, n := range cg.Nodes {
		from := ix.owner(fn)
		if from == "" {
			continue
		}
		for _, edge := range n.Out {
			if edge.Site == nil {
				continue
			}
			target := ix.owner(edge.Callee.Func)
			kind, certainty := "possible_callback_call", "possible"
			if edge.Site.Common().IsInvoke() {
				kind = "possible_interface_call"
			}
			if edge.Site.Common().StaticCallee() != nil {
				kind, certainty = "call", "fact"
			}
			switch edge.Site.(type) {
			case *ssa.Go:
				kind = "goroutine_" + kind
			case *ssa.Defer:
				kind = "deferred_" + kind
			}
			if target != "" {
				ix.callEdge(from, target, kind, certainty, edge.Site)
			} else {
				ix.boundaries = append(ix.boundaries, Boundary{Node: from, Kind: "external_call", Reason: "callee bodies outside indexed repository; examples are not an exhaustive target list", Evidence: ix.callEvidence(edge.Site), Examples: []string{edge.Callee.Func.String()}})
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := ix.semantic(o.Config); err != nil {
		return Report{}, err
	}
	if err := ix.sqlFiles(ctx, o); err != nil {
		return Report{}, err
	}
	coverage.Storage = ix.storage
	coverage.ValueFlow = ix.flow.coverage
	if !ix.flow.coverage.Converged || ix.flow.coverage.Widened {
		ix.boundaries = append(ix.boundaries, Boundary{Kind: "value_flow_limit", Reason: "value-flow fixed-point or candidate budget reached; candidates are incomplete"})
	}
	report, err = ix.expand(o, coverage)
	if err != nil {
		return Report{}, err
	}
	after, _, err := fingerprint(root)
	if err != nil {
		return Report{}, err
	}
	if before != after {
		return Report{}, fmt.Errorf("source changed during analysis; rerun on a stable revision")
	}
	assetHash, err := hashFiles(ix.root, ix.assets)
	if err != nil {
		return Report{}, err
	}
	if assetHash != ix.assetHash {
		return Report{}, fmt.Errorf("embedded assets changed during analysis")
	}
	report.Coverage.LoadedSources = sources.sources()
	if err := verifyLoadedSources(ctx, report.Coverage.LoadedSources); err != nil {
		return Report{}, err
	}
	report.Coverage.LoadedSourceSHA256 = loadedSourceIdentity(report.Coverage.LoadedSources)
	if err := verifyLoadedSources(ctx, resolution); err != nil {
		return Report{}, fmt.Errorf("resolution input check: %w", err)
	}
	if err := verifyBuildEnvironment(ctx, root, env); err != nil {
		return Report{}, err
	}
	report.Coverage.ResolutionInputs = resolution
	report.Coverage.ResolutionSHA256 = loadedSourceIdentity(resolution)
	report.Coverage.ResolutionScope = "loaded_modules.v1"
	identity, _ := json.Marshal(struct {
		Build  map[string]string
		Config Config
		Tests  bool
		Tags   string
	}{env, o.Config, o.Tests, o.Tags})
	report.Coverage.SourceSHA256 = hashText(before + string(identity) + ix.assetHash + report.Coverage.LoadedSourceSHA256 + report.Coverage.ResolutionSHA256)
	report.Schema = "contract-tracer.v0.1"
	report.Root = root
	report.Invariant = o.Invariant
	report.Config = o.Config
	report.Boundaries = append(report.Boundaries,
		Boundary{Kind: "semantic_scope", Reason: "The supplied invariant is a hypothesis. This report does not prove it or discover every behavioral relationship."},
		Boundary{Kind: "analysis_model", Reason: "CHA targets are conservative candidates. References prove use, not invocation. Reflection, unsafe/cgo, runtime dispatch, dependency bodies and unselected build configurations are not fully modeled."},
		Boundary{Kind: "loaded_source_identity", Reason: "identity includes exact Go bytes presented to the package parser, active workspace and its use-module manifests, effective loaded-module manifests checked around package loading, and recorded Go build/selection environment, checked again before completion; unloaded module directives, checksum files, unrecorded settings, non-Go dependency assets and analyzer implementation remain outside this attestation, and source identity does not imply dependency body analysis"},
		Boundary{Kind: "lifecycle", Reason: "Go/defer and configured name hints identify investigation sites; temporal ownership, cancellation propagation and eventual cleanup are not proven."})
	sortReport(&report)
	groupBoundaries(&report)
	if o.capture != nil {
		*o.capture = ix.snapshot(report)
	}
	return report, nil
}

func canonicalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func functionName(obj *types.Func) string {
	sig, _ := obj.Type().(*types.Signature)
	if sig != nil && sig.Recv() != nil {
		t := sig.Recv().Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if n, ok := t.(*types.Named); ok {
			return n.Obj().Name() + "." + obj.Name()
		}
	}
	return obj.Name()
}
func (ix *index) owner(fn *ssa.Function) (result string) {
	if fn == nil {
		return ""
	}
	if ix.ownerKnown[fn] {
		return ix.ownerCache[fn]
	}
	if ix.ownerKnown == nil {
		ix.ownerKnown = map[*ssa.Function]bool{}
		ix.ownerCache = map[*ssa.Function]string{}
	}
	defer func() { ix.ownerKnown[fn] = true; ix.ownerCache[fn] = result }()
	if id := ix.ssaOwners[fn]; id != "" {
		return id
	}
	if obj, ok := fn.Object().(*types.Func); ok && obj != nil {
		if id := ix.objects[obj.Origin()]; id != "" {
			return id
		}
	}
	if origin := fn.Origin(); origin != nil && origin != fn {
		if obj, ok := origin.Object().(*types.Func); ok && obj != nil {
			if id := ix.objects[obj.Origin()]; id != "" {
				return id
			}
		}
	}
	if parent := fn.Parent(); parent != nil {
		return ix.owner(parent)
	}
	for id, f := range ix.funcs {
		if f.decl != nil && fn.Pos() >= f.decl.Pos() && fn.Pos() <= f.decl.End() {
			return id
		}
	}
	return ""
}
func (ix *index) evidence(pos token.Pos) Evidence {
	p := ix.fset.Position(pos)
	rel, inside := relative(ix.root, p.Filename)
	if !inside {
		rel = filepath.ToSlash(p.Filename)
	}
	lines, ok := ix.lines[p.Filename]
	if !ok {
		b, _ := os.ReadFile(p.Filename)
		lines = strings.Split(string(b), "\n")
		ix.lines[p.Filename] = lines
	}
	snippet := ""
	if p.Line > 0 && p.Line <= len(lines) {
		snippet = strings.TrimSpace(lines[p.Line-1])
	}
	return Evidence{File: rel, Line: p.Line, Column: p.Column, Snippet: snippet}
}
func (ix *index) edge(from, to, kind, certainty string, pos token.Pos) {
	ix.edges = append(ix.edges, Relationship{From: from, To: to, Kind: kind, Certainty: certainty, Evidence: ix.evidence(pos)})
}
func relative(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	return filepath.ToSlash(rel), err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func hashText(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func fingerprint(root string) (string, []string, error) {
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return "", nil, err
	}
	defer rootFS.Close()
	files := []string{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", ".gograph":
				return filepath.SkipDir
			}
			return nil
		}
		isSQL := strings.HasSuffix(strings.ToLower(path), ".sql")
		if isSQL && d.Type()&os.ModeSymlink != 0 {
			rel, _ := relative(root, path)
			return fmt.Errorf("SQL file symlinks are not supported: %s", rel)
		}
		if strings.HasSuffix(path, ".go") || isSQL || d.Name() == "go.mod" || d.Name() == "go.sum" {
			rel, _ := relative(root, path)
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		data, err := rootFS.ReadFile(filepath.FromSlash(f))
		if err != nil {
			return "", nil, err
		}
		fmt.Fprintf(h, "%d:%s:%d:", len(f), f, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), files, nil
}
func buildEnvironment(ctx context.Context, root string) (map[string]string, error) {
	keys := buildEnvironmentKeys()
	args := append([]string{"env", "-json"}, keys...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	var values map[string]string
	if err := json.Unmarshal(b, &values); err != nil {
		return nil, fmt.Errorf("unexpected go env output: %w", err)
	}
	for _, key := range keys {
		if _, present := values[key]; !present {
			return nil, fmt.Errorf("go env output missing %s", key)
		}
	}
	return values, nil
}
