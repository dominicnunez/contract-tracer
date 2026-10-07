package contracttrace

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type LoadedSource struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func validateSourceInventory(sources []LoadedSource, identity string) error {
	if len(sources) == 0 && identity == "" {
		return nil
	}
	if loadedSourceIdentity(sources) != identity {
		return fmt.Errorf("saved loaded-source identity does not match its inventory")
	}
	previous := ""
	for _, source := range sources {
		absolute := strings.HasPrefix(source.Path, "/") || len(source.Path) >= 3 && source.Path[1] == ':' && source.Path[2] == '/'
		if !absolute || strings.ContainsRune(source.Path, 0) || source.Path <= previous || len(source.SHA256) != 64 {
			return fmt.Errorf("invalid saved loaded-source entry")
		}
		if _, err := hex.DecodeString(source.SHA256); err != nil {
			return fmt.Errorf("invalid saved loaded-source digest")
		}
		previous = source.Path
	}
	return nil
}

// packages.Load can parse files concurrently. Capture the exact bytes supplied
// to the parser, including dependency, replacement and generated Go sources.
type loadedSourceCapture struct {
	mu    sync.Mutex
	files map[string]string
}

func (c *loadedSourceCapture) parseFile(fset *token.FileSet, filename string, contents []byte) (*ast.File, error) {
	if contents == nil {
		var err error
		contents, err = os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	path := filepath.ToSlash(abs)
	digest := hashText(string(contents))
	c.mu.Lock()
	if c.files == nil {
		c.files = map[string]string{}
	}
	previous, found := c.files[path]
	c.files[path] = digest
	c.mu.Unlock()
	if found && previous != digest {
		return nil, fmt.Errorf("loaded source changed during package loading: %s", path)
	}
	return parser.ParseFile(fset, filename, contents, parser.AllErrors|parser.ParseComments|parser.SkipObjectResolution)
}

func (c *loadedSourceCapture) sources() []LoadedSource {
	c.mu.Lock()
	defer c.mu.Unlock()
	paths := make([]string, 0, len(c.files))
	for path := range c.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]LoadedSource, 0, len(paths))
	for _, path := range paths {
		result = append(result, LoadedSource{path, c.files[path]})
	}
	return result
}

func loadedSourceIdentity(sources []LoadedSource) string {
	if len(sources) == 0 {
		return ""
	}
	contents, _ := json.Marshal(sources)
	return hashText(string(contents))
}

func verifyLoadedSources(ctx context.Context, sources []LoadedSource) error {
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		contents, err := os.ReadFile(filepath.FromSlash(source.Path))
		if err != nil {
			return fmt.Errorf("loaded source unavailable %s: %w", source.Path, err)
		}
		if hashText(string(contents)) != source.SHA256 {
			return fmt.Errorf("loaded source changed: %s; rerun analysis on stable sources", source.Path)
		}
	}
	return ctx.Err()
}
