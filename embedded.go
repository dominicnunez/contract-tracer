package contracttrace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

func embedPatterns(text string) ([]string, error) {
	result := []string{}
	for text = strings.TrimSpace(text); text != ""; text = strings.TrimSpace(text) {
		if text[0] == '"' || text[0] == '`' {
			quote := text[0]
			end := 1
			for end < len(text) {
				if quote == '"' && text[end] == '\\' {
					end += 2
					continue
				}
				if text[end] == quote {
					break
				}
				end++
			}
			if end >= len(text) {
				return nil, fmt.Errorf("unterminated embed pattern")
			}
			word, err := strconv.Unquote(text[:end+1])
			if err != nil {
				return nil, err
			}
			result = append(result, word)
			text = text[end+1:]
		} else {
			end := strings.IndexFunc(text, unicode.IsSpace)
			if end < 0 {
				end = len(text)
			}
			result = append(result, text[:end])
			text = text[end:]
		}
	}
	return result, nil
}
func patternContains(pattern, file string) bool {
	pattern = strings.TrimPrefix(pattern, "all:")
	for candidate := file; candidate != "."; candidate = filepath.ToSlash(filepath.Dir(candidate)) {
		if globMatch(pattern, candidate) {
			return true
		}
	}
	return false
}
func (ix *index) loadEmbedded(pkgs []*packages.Package) error {
	ix.embedded = map[*types.Var]string{}
	ix.assets = selectedEmbeddedAssetPaths(ix.root, pkgs)
	bodies := map[string][]byte{}
	h := sha256.New()
	for _, file := range ix.assets {
		body, err := os.ReadFile(filepath.Join(ix.root, filepath.FromSlash(file)))
		if err != nil {
			return err
		}
		bodies[file] = body
		fmt.Fprintf(h, "%d:%s:%d:", len(file), file, len(body))
		h.Write(body)
	}
	ix.assetHash = hex.EncodeToString(h.Sum(nil))
	for _, p := range pkgs {
		for _, file := range p.Syntax {
			directory := filepath.Dir(ix.fset.Position(file.Pos()).Filename)
			for _, decl := range file.Decls {
				group, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range group.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok || len(value.Names) != 1 {
						continue
					}
					patterns := []string{}
					for _, comments := range []*ast.CommentGroup{group.Doc, value.Doc} {
						if comments != nil {
							for _, comment := range comments.List {
								if strings.HasPrefix(comment.Text, "//go:embed ") {
									parts, err := embedPatterns(strings.TrimPrefix(comment.Text, "//go:embed "))
									if err != nil {
										return err
									}
									patterns = append(patterns, parts...)
								}
							}
						}
					}
					if len(patterns) == 0 {
						continue
					}
					variable, ok := p.TypesInfo.Defs[value.Names[0]].(*types.Var)
					if !ok {
						continue
					}
					candidates := []string{}
					for _, file := range p.EmbedFiles {
						rel, inside := relative(directory, file)
						if inside {
							for _, pattern := range patterns {
								if patternContains(pattern, rel) {
									candidates = append(candidates, file)
									break
								}
							}
						}
					}
					isString := false
					switch t := variable.Type().Underlying().(type) {
					case *types.Basic:
						isString = t.Kind() == types.String
					case *types.Slice:
						if elem, ok := t.Elem().Underlying().(*types.Basic); ok {
							isString = elem.Kind() == types.Byte
						}
					}
					if isString && len(candidates) == 1 {
						rel, inside := relative(ix.root, candidates[0])
						body, present := bodies[rel]
						if !inside || !present {
							return fmt.Errorf("embedded asset outside target snapshot: %s", candidates[0])
						}
						ix.embedded[variable] = string(body)
					} else {
						ix.boundaries = append(ix.boundaries, Boundary{Kind: "embedded_filesystem", Reason: "embedded FS or ambiguous asset value is inventoried but not flattened into string flow", Evidence: ix.evidence(value.Pos())})
					}
				}
			}
		}
	}
	return nil
}

func selectedEmbeddedAssetPaths(root string, pkgs []*packages.Package) []string {
	files := map[string]bool{}
	for _, p := range pkgs {
		for _, file := range p.EmbedFiles {
			if rel, inside := relative(root, file); inside {
				files[rel] = true
			}
		}
	}
	assets := make([]string, 0, len(files))
	for file := range files {
		assets = append(assets, file)
	}
	sort.Strings(assets)
	return assets
}

func hashFiles(root string, files []string) (string, error) {
	h := sha256.New()
	for _, file := range files {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:%s:%d:", len(file), file, len(body))
		h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
