package contracttrace

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type sqlFound struct {
	access       sqlAccess
	line, column int
}
type sqlAnalysis struct {
	found      []sqlFound
	statements int
	err        error
}

func (ix *index) parsedSQL(texts []string) bool {
	for _, text := range texts {
		if r, ok := ix.sqlCache[text]; !ok || r.err != nil {
			return false
		}
	}
	return len(texts) > 0
}

func makeSQLAnalysis(v *sqlVisitor, err error) sqlAnalysis {
	result := sqlAnalysis{statements: v.statements, err: err}
	for i, a := range v.accesses {
		p := v.positions[i]
		result.found = append(result.found, sqlFound{a, p.Line, p.Column})
	}
	return result
}
func (ix *index) sqlAccesses(owner, query string, evidence Evidence) []sqlFound {
	if ix.sqlCache == nil {
		ix.sqlCache = map[string]sqlAnalysis{}
	}
	r, exists := ix.sqlCache[query]
	if !exists {
		ix.storage.Queries++
		if ix.storage.Dialect == "inventory" {
			r.err = fmt.Errorf("inventory mode has no dialect parser")
		} else {
			r = parseSQL(query)
		}
		if r.err != nil {
			ix.storage.ParseFailures++
			for _, access := range inventorySQLTables(query) {
				r.found = append(r.found, sqlFound{access, 1, 1})
			}
		}
		ix.storage.ParsedStatements += r.statements
		ix.sqlCache[query] = r
	}
	if r.err != nil {
		ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "sql_parse_failure", Reason: r.err.Error() + "; inventory fallback is provisional and may omit relationships", Evidence: evidence})
	}
	return r.found
}
func (ix *index) sqlFiles(ctx context.Context, o Options) error {
	rootFS, err := os.OpenRoot(ix.root)
	if err != nil {
		return err
	}
	defer rootFS.Close()
	ix.storage.Files = []string{}
	ix.storage.ExcludedFiles = []string{}
	err = filepath.WalkDir(ix.root, func(file string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" || d.Name() == ".gograph" {
				return filepath.SkipDir
			}
			// A selected Root follows one Go module tree; SQL selectors do not
			// extend the inventory into a nested module.
			if filepath.Clean(file) != filepath.Clean(ix.root) {
				manifest := filepath.Join(file, "go.mod")
				if info, err := os.Stat(manifest); err == nil && !info.IsDir() {
					return filepath.SkipDir
				} else if err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(file), ".sql") {
			return nil
		}
		rel, _ := relative(ix.root, file)
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("SQL file symlinks are not supported: %s", rel)
		}
		selected := false
		for _, pattern := range o.Config.SQLFiles {
			if globMatch(pattern, rel) {
				selected = true
				break
			}
		}
		if !o.Tests {
			for _, segment := range strings.Split(rel, "/") {
				if segment == "testdata" {
					selected = false
				}
			}
		}
		if !selected {
			ix.storage.ExcludedFiles = append(ix.storage.ExcludedFiles, rel)
			return nil
		}
		body, err := rootFS.ReadFile(filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		lines := strings.Split(string(body), "\n")
		namespace, err := scopedStorageFile(rel, true, o.Config)
		if err != nil {
			return err
		}
		origin := "sql_file_rule"
		if namespace == "" {
			origin = "unscoped"
		}
		ix.assignStorage(rel, namespace, origin)
		key := resourceID("sql_file", namespace, rel)
		evidence := Evidence{File: rel, Line: 1, Column: 1, Snippet: strings.TrimSpace(lines[0])}
		ix.funcs[key] = &function{node: Node{ID: key, Name: rel, Kind: "sql_file", Evidence: evidence}}
		if namespace == "" && len(o.Config.StorageScopes) > 0 {
			ix.boundaries = append(ix.boundaries, Boundary{Node: key, Kind: "unresolved_storage_namespace", Reason: "SQL file has no configured database namespace; unscoped inventory may alias configured databases", Evidence: evidence})
		}
		ix.storage.Files = append(ix.storage.Files, rel)
		for _, found := range ix.sqlAccesses(key, string(body), evidence) {
			ev := Evidence{File: rel, Line: found.line, Column: found.column}
			if ev.Line >= 1 && ev.Line <= len(lines) {
				ev.Snippet = strings.TrimSpace(lines[ev.Line-1])
			}
			table := resourceID("table", namespace, found.access.table)
			if ix.funcs[table] == nil {
				ix.funcs[table] = &function{node: Node{ID: table, Name: found.access.table, Kind: "table", Evidence: ev}}
			}
			ix.edges = append(ix.edges, Relationship{From: key, To: table, Kind: "sql_" + found.access.role, Certainty: "possible", Evidence: ev})
		}
		return nil
	})
	sort.Strings(ix.storage.Files)
	sort.Strings(ix.storage.ExcludedFiles)
	sortAssignments(&ix.storage)
	return err
}
func globMatch(pattern, name string) bool {
	p, n := strings.Split(filepath.ToSlash(pattern), "/"), strings.Split(name, "/")
	var match func(int, int) bool
	match = func(i, j int) bool {
		if i == len(p) {
			return j == len(n)
		}
		if p[i] == "**" {
			return match(i+1, j) || j < len(n) && match(i, j+1)
		}
		if j >= len(n) {
			return false
		}
		ok, err := path.Match(p[i], n[j])
		return err == nil && ok && match(i+1, j+1)
	}
	return match(0, 0)
}
