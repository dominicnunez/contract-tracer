package contracttrace

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func declarationKind(kind string) bool {
	return kind == "global" || kind == "record_field" || kind == "event_discriminator_field"
}

func (ix *index) locationSeeds(locations []string) ([]string, error) {
	symbols := []string{}
	for _, location := range locations {
		colon := strings.LastIndex(location, ":")
		if colon < 0 {
			return nil, fmt.Errorf("location must be file:line: %q", location)
		}
		line, err := strconv.Atoi(location[colon+1:])
		if err != nil || line < 1 {
			return nil, fmt.Errorf("invalid location line: %q", location)
		}
		path := filepath.FromSlash(location[:colon])
		if !filepath.IsAbs(path) {
			path = filepath.Join(ix.root, path)
		}
		file, inside := relative(ix.root, filepath.Clean(path))
		if !inside {
			return nil, fmt.Errorf("location is outside target root: %q", location)
		}
		matches := []string{}
		for id, f := range ix.funcs {
			if f.node.Evidence.File != file {
				continue
			}
			first, last := 0, 0
			if f.decl != nil {
				first, last = ix.fset.Position(f.decl.Pos()).Line, ix.fset.Position(f.decl.End()).Line
			} else if span, ok := ix.ranges[id]; ok {
				first, last = span.First, span.Last
			} else if span, ok := ix.declarationRanges[id]; ok {
				first, last = span.First, span.Last
			} else if declarationKind(f.node.Kind) {
				first, last = f.node.Evidence.Line, f.node.Evidence.Line
			} else {
				continue
			}
			if line >= first && line <= last {
				matches = append(matches, id)
			}
		}
		sort.Strings(matches)
		if len(matches) == 0 {
			return nil, fmt.Errorf("no indexed function, global or record-field declaration contains location %q; unindexed package-level initializers are not supported", location)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("ambiguous location %q: %s", location, strings.Join(matches, ", "))
		}
		symbols = append(symbols, matches[0])
	}
	return symbols, nil
}
