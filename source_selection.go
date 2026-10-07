package contracttrace

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func packageLoaderConfig(ctx context.Context, root string, build map[string]string, tests bool, tags string) *packages.Config {
	cfg := &packages.Config{Context: ctx, Dir: root, Env: packageLoaderEnvironment(build), Tests: tests}
	if tags != "" {
		cfg.BuildFlags = []string{"-tags=" + tags}
	}
	return cfg
}

func selectedPackageSourcePaths(pkgs []*packages.Package) ([]string, error) {
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no Go packages loaded")
	}
	var diagnostics []string
	paths := map[string]bool{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, err := range p.Errors {
			diagnostics = append(diagnostics, err.Error())
		}
		for _, filename := range p.CompiledGoFiles {
			absolute, err := filepath.Abs(filename)
			if err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("source path %q: %v", filename, err))
				continue
			}
			paths[filepath.ToSlash(absolute)] = true
		}
	})
	if len(diagnostics) != 0 {
		sort.Strings(diagnostics)
		return nil, fmt.Errorf("package source selection incomplete: %s", strings.Join(diagnostics, "\n"))
	}
	selected := make([]string, 0, len(paths))
	for path := range paths {
		selected = append(selected, path)
	}
	sort.Strings(selected)
	return selected, nil
}

func loadedSourcePaths(sources []LoadedSource) []string {
	paths := make([]string, 0, len(sources))
	for _, source := range sources {
		paths = append(paths, source.Path)
	}
	return paths
}

func verifySelectedPackageSources(ctx context.Context, root string, build map[string]string, tests bool, tags string, loaded []LoadedSource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := packageLoaderConfig(ctx, root, build, tests, tags)
	cfg.Mode = packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedDeps | packages.NeedModule
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("recheck selected Go source set: %w", err)
	}
	selected, err := selectedPackageSourcePaths(pkgs)
	if err != nil {
		return fmt.Errorf("recheck selected Go source set: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	previous := loadedSourcePaths(loaded)
	if len(selected) != len(previous) {
		return fmt.Errorf("selected Go source set changed; rerun analysis")
	}
	for i := range selected {
		if selected[i] != previous[i] {
			return fmt.Errorf("selected Go source set changed; rerun analysis")
		}
	}
	return ctx.Err()
}

func parserSourcePaths(sources []LoadedSource) []string {
	paths := loadedSourcePaths(sources)
	sort.Strings(paths)
	return paths
}

func verifyParserMatchesSelection(pkgs []*packages.Package, sources []LoadedSource) error {
	selected, err := selectedPackageSourcePaths(pkgs)
	if err != nil {
		return err
	}
	parsed := parserSourcePaths(sources)
	if len(parsed) == 0 {
		return fmt.Errorf("package loader produced no selected Go source inputs")
	}
	if len(selected) != len(parsed) {
		return fmt.Errorf("package source selection did not match parser inputs")
	}
	for i := range selected {
		if selected[i] != parsed[i] {
			return fmt.Errorf("package source selection did not match parser inputs")
		}
	}
	return nil
}
