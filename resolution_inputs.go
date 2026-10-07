package contracttrace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

var baseBuildEnvironmentKeys = []string{"GOOS", "GOARCH", "GOVERSION", "CGO_ENABLED", "GOFLAGS", "GOWORK"}

var featureBuildEnvironmentKeys = []string{
	"GO386", "GOAMD64", "GOARM", "GOARM64", "GOMIPS", "GOMIPS64",
	"GOPPC64", "GORISCV64", "GOWASM", "GOEXPERIMENT", "GOFIPS140",
}

func buildEnvironmentKeys() []string {
	keys := append([]string(nil), baseBuildEnvironmentKeys...)
	return append(keys, featureBuildEnvironmentKeys...)
}

// packageLoaderEnvironment keeps the workspace directory spelling in the
// same Windows path namespace as the canonical analysis root. The workfile
// itself is never resolved through a file symlink, so its relative module
// paths retain their original base.
func packageLoaderEnvironment(build map[string]string) []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	work := build["GOWORK"]
	if work == "" || work == "off" {
		return nil
	}
	absolute, err := filepath.Abs(work)
	if err != nil {
		return nil
	}
	if _, err := os.Lstat(absolute); err != nil || hasPathReparsePoint(filepath.Dir(absolute)) {
		return nil
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil
	}
	candidate := filepath.Join(parent, filepath.Base(absolute))
	originalInfo, originalErr := os.Stat(absolute)
	candidateInfo, candidateErr := os.Stat(candidate)
	if originalErr != nil || candidateErr != nil || !os.SameFile(originalInfo, candidateInfo) {
		return nil
	}

	env := os.Environ()
	for i, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, "GOWORK") {
			env[i] = "GOWORK=" + candidate
			return env
		}
	}
	return append(env, "GOWORK="+candidate)
}

func captureResolutionInputs(build map[string]string) ([]LoadedSource, error) {
	work := build["GOWORK"]
	if work == "" || work == "off" {
		return nil, nil
	}
	abs, err := filepath.Abs(work)
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace input unavailable: %w", err)
	}
	parsed, err := modfile.ParseWork(abs, contents, nil)
	if err != nil {
		return nil, fmt.Errorf("workspace input invalid: %w", err)
	}
	inputs := map[string]string{abs: hashText(string(contents))}
	for _, use := range parsed.Use {
		directory := use.Path
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(filepath.Dir(abs), directory)
		}
		manifest := filepath.Join(directory, "go.mod")
		data, err := os.ReadFile(manifest)
		if err != nil {
			return nil, fmt.Errorf("workspace module input unavailable: %w", err)
		}
		inputs[filepath.Clean(manifest)] = hashText(string(data))
	}
	var sources []LoadedSource
	for path, digest := range inputs {
		sources = append(sources, LoadedSource{Path: filepath.ToSlash(path), SHA256: digest})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	return sources, nil
}

// Read effective manifests from the same loader metadata used by the syntax
// pass. Replacements may live outside both the root and the workspace.
func captureModuleInputs(pkgs []*packages.Package, workspace []LoadedSource) ([]LoadedSource, error) {
	paths := map[string]bool{}
	var diagnostics []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, err := range p.Errors {
			diagnostics = append(diagnostics, err.Error())
		}
		module := p.Module
		for module != nil && module.Replace != nil {
			module = module.Replace
		}
		if module != nil && module.GoMod != "" {
			paths[module.GoMod] = true
		}
	})
	if len(diagnostics) != 0 {
		sort.Strings(diagnostics)
		return nil, fmt.Errorf("module discovery incomplete: %s", strings.Join(diagnostics, "\n"))
	}
	inputs := map[string]string{}
	for _, input := range workspace {
		inputs[input.Path] = input.SHA256
	}
	for path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		contents, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("module input unavailable: %w", err)
		}
		inputs[filepath.ToSlash(abs)] = hashText(string(contents))
	}
	var sources []LoadedSource
	for path, digest := range inputs {
		sources = append(sources, LoadedSource{Path: path, SHA256: digest})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	return sources, nil
}

func verifyBuildEnvironment(ctx context.Context, root string, recorded map[string]string) error {
	current, err := buildEnvironment(ctx, root)
	if err != nil {
		return err
	}
	return compareBuildEnvironment(current, recorded)
}

func compareBuildEnvironment(current, recorded map[string]string) error {
	for _, key := range baseBuildEnvironmentKeys {
		if previous, captured := recorded[key]; captured && current[key] != previous {
			return fmt.Errorf("build/resolution environment changed: %s; rerun analysis", key)
		}
	}
	for _, key := range featureBuildEnvironmentKeys {
		previous, captured := recorded[key]
		if !captured {
			return fmt.Errorf("saved analysis is missing required build feature setting %s; rerun analysis", key)
		}
		if current[key] != previous {
			return fmt.Errorf("build/resolution environment changed: %s; rerun analysis", key)
		}
	}
	return nil
}
