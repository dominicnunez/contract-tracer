package contracttrace

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"net/url"
	"sort"
	"strings"
)

func normalizeConfig(c Config) Config {
	d := DefaultConfig()
	if c.EventFields == nil {
		c.EventFields = d.EventFields
	}
	if c.LifecycleNames == nil {
		c.LifecycleNames = d.LifecycleNames
	}
	if c.SQLMethods == nil {
		c.SQLMethods = d.SQLMethods
	}
	if c.SQLDialect == "" {
		c.SQLDialect = d.SQLDialect
	}
	if c.SQLFiles == nil {
		c.SQLFiles = d.SQLFiles
	}
	return c
}
func contains(list []string, value string) bool {
	for _, s := range list {
		if s == value {
			return true
		}
	}
	return false
}
func field(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.Ident:
		return e.Name
	}
	return ""
}

type assignments struct {
	values map[types.Object]ast.Expr
	counts map[types.Object]int
}

func semanticRoots(ix *index, f *function) []ast.Node {
	if f.decl != nil && f.decl.Body != nil {
		return []ast.Node{f.decl.Body}
	}
	if f.node.Kind != "initializer" || f.pkg == nil {
		return nil
	}
	roots := []ast.Node{}
	for _, file := range f.pkg.Syntax {
		if _, inside := relative(ix.root, ix.fset.Position(file.Pos()).Filename); !inside {
			continue
		}
		for _, declaration := range file.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.VAR {
				continue
			}
			for _, specification := range group.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if ok && len(value.Values) > 0 {
					roots = append(roots, value)
				}
			}
		}
	}
	return roots
}

func initializerFuncLiteral(expression ast.Expr) *ast.FuncLit {
	switch value := expression.(type) {
	case *ast.FuncLit:
		return value
	case *ast.ParenExpr:
		return initializerFuncLiteral(value.X)
	default:
		return nil
	}
}

func inspectSemanticRoot(root ast.Node, f *function, visit func(ast.Node) bool) {
	if f.node.Kind != "initializer" {
		ast.Inspect(root, visit)
		return
	}
	executed := map[*ast.FuncLit]bool{}
	ast.Inspect(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok {
			if literal := initializerFuncLiteral(call.Fun); literal != nil {
				executed[literal] = true
			}
		}
		return true
	})
	ast.Inspect(root, func(node ast.Node) bool {
		if literal, ok := node.(*ast.FuncLit); ok && !executed[literal] {
			return false
		}
		return visit(node)
	})
}

func localAssignments(f *function, roots []ast.Node) assignments {
	a := assignments{values: map[types.Object]ast.Expr{}, counts: map[types.Object]int{}}
	set := func(lhs ast.Expr, rhs ast.Expr) {
		id, ok := lhs.(*ast.Ident)
		if !ok {
			return
		}
		obj := f.pkg.TypesInfo.ObjectOf(id)
		if obj != nil {
			if f.node.Kind == "initializer" && obj.Parent() == f.pkg.Types.Scope() {
				return
			}
			a.counts[obj]++
			a.values[obj] = rhs
		}
	}
	for _, root := range roots {
		inspectSemanticRoot(root, f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.AssignStmt:
				if v.Tok == token.ASSIGN || v.Tok == token.DEFINE {
					if len(v.Lhs) > len(v.Rhs) {
						for _, lhs := range v.Lhs {
							set(lhs, nil)
						}
					} else {
						for i, lhs := range v.Lhs {
							if i < len(v.Rhs) {
								set(lhs, v.Rhs[i])
							}
						}
					}
				} else {
					for _, lhs := range v.Lhs {
						if id, ok := lhs.(*ast.Ident); ok {
							if obj := f.pkg.TypesInfo.ObjectOf(id); obj != nil {
								a.counts[obj]++
							}
						}
					}
				}
			case *ast.ValueSpec:
				if len(v.Values) > 0 && len(v.Names) > len(v.Values) {
					for _, id := range v.Names {
						set(id, nil)
					}
				} else {
					for i, id := range v.Names {
						if i < len(v.Values) {
							set(id, v.Values[i])
						}
					}
				}
			case *ast.IncDecStmt:
				if id, ok := v.X.(*ast.Ident); ok {
					a.counts[f.pkg.TypesInfo.ObjectOf(id)]++
				}
			case *ast.RangeStmt:
				for _, lhs := range []ast.Expr{v.Key, v.Value} {
					if id, ok := lhs.(*ast.Ident); ok {
						a.counts[f.pkg.TypesInfo.ObjectOf(id)]++
					}
				}
			}
			return true
		})
	}
	return a
}

type stringPart struct {
	text string
	hole bool
}

type stringExpression struct {
	parts    []stringPart
	complete bool
}

type stringCandidate struct {
	text  string
	holes []string
}

func knownString(text string) stringExpression {
	return stringExpression{parts: []stringPart{{text: text}}, complete: true}
}

func unknownString() stringExpression {
	return stringExpression{parts: []stringPart{{hole: true}}}
}

func (value stringExpression) candidate() stringCandidate {
	knownText := strings.Builder{}
	for _, part := range value.parts {
		if !part.hole {
			knownText.WriteString(part.text)
		}
	}
	known := knownText.String()
	lowerKnown := strings.ToLower(known)
	used := map[string]bool{}
	text := strings.Builder{}
	holes := []string{}
	for _, part := range value.parts {
		if !part.hole {
			text.WriteString(part.text)
			continue
		}
		for index := 0; ; index++ {
			token := fmt.Sprintf("__ct_unknown_hole_%d__", index)
			if strings.Contains(lowerKnown, token) || used[token] {
				continue
			}
			used[token] = true
			holes = append(holes, token)
			text.WriteString(token)
			break
		}
	}
	return stringCandidate{text: text.String(), holes: holes}
}

func stringValue(expr ast.Expr, f *function, a assignments, seen map[types.Object]bool) stringExpression {
	if v, ok := f.pkg.TypesInfo.Types[expr]; ok && v.Value != nil && v.Value.Kind() == constant.String {
		return knownString(constant.StringVal(v.Value))
	}
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return stringValue(e.X, f, a, seen)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			x := stringValue(e.X, f, a, seen)
			y := stringValue(e.Y, f, a, seen)
			parts := append([]stringPart{}, x.parts...)
			parts = append(parts, y.parts...)
			return stringExpression{parts: parts, complete: x.complete && y.complete}
		}
	case *ast.Ident:
		obj := f.pkg.TypesInfo.ObjectOf(e)
		if c, ok := obj.(*types.Const); ok && c.Val().Kind() == constant.String {
			return knownString(constant.StringVal(c.Val()))
		}
		if obj != nil && !seen[obj] && a.counts[obj] == 1 {
			seen[obj] = true
			value := stringValue(a.values[obj], f, a, seen)
			delete(seen, obj)
			return value
		}
	}
	return unknownString()
}
func (ix *index) resource(from, key, name, kind, relationship string, pos token.Pos) {
	evidence := ix.evidence(pos)
	if existing := ix.funcs[key]; existing == nil {
		ix.funcs[key] = &function{node: Node{ID: key, Name: name, Kind: kind, Evidence: evidence}}
	}
	ix.edge(from, key, relationship, "possible", pos)
}

func (ix *index) stringCandidates(expr ast.Expr, f *function, a assignments) ([]stringCandidate, bool) {
	value := stringValue(expr, f, a, map[types.Object]bool{})
	if value.complete {
		return []stringCandidate{value.candidate()}, true
	}
	if ix.flow != nil {
		value := ix.flow.positions[expr.Pos()]
		if call, ok := expr.(*ast.CallExpr); ok {
			ix.flow.merge(&value, ix.flow.positions[call.Lparen])
		}
		values := []stringCandidate{}
		for s := range value.strings {
			values = append(values, stringCandidate{text: s})
		}
		sort.Slice(values, func(i, j int) bool { return values[i].text < values[j].text })
		if len(values) > 0 {
			return values, false
		}
	}
	return []stringCandidate{value.candidate()}, false
}

func tableHasHole(table string, candidate stringCandidate) bool {
	for _, hole := range candidate.holes {
		if strings.Contains(table, hole) {
			return true
		}
	}
	return false
}
func (ix *index) semantic(c Config) error {
	ix.databaseOriginBoundaries(c)
	ix.storage.Dialect = c.SQLDialect
	matchedRules := map[string]bool{}
	ids := []string{}
	rootsByID := map[string][]ast.Node{}
	for id, f := range ix.funcs {
		if roots := semanticRoots(ix, f); len(roots) > 0 {
			ids = append(ids, id)
			rootsByID[id] = roots
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		f := ix.funcs[id]
		roots := rootsByID[id]
		a := localAssignments(f, roots)
		for _, hint := range c.LifecycleNames {
			if strings.Contains(strings.ToLower(f.node.Name), strings.ToLower(hint)) {
				ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "lifecycle_hint", Reason: "configured name hint " + hint + "; inspect lifecycle ownership, not an established invariant", Evidence: f.node.Evidence})
				break
			}
		}
		event := func(expr ast.Expr, role string) {
			values, complete := ix.stringCandidates(expr, f, a)
			if !complete {
				ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "dynamic_event", Reason: "configured event field uses a value that is not statically resolved", Evidence: ix.evidence(expr.Pos())})
			}
			for _, value := range values {
				if value.text != "" && len(value.holes) == 0 {
					ix.resource(id, resourceID("event", "", value.text), value.text, "event", role, expr.Pos())
				}
			}
		}
		var semanticErr error
		for _, root := range roots {
			inspectSemanticRoot(root, f, func(n ast.Node) bool {
				if semanticErr != nil {
					return false
				}
				switch v := n.(type) {
				case *ast.CallExpr:
					handled, err := ix.applyCallRules(id, f, v, a, c, matchedRules)
					if err != nil {
						semanticErr = err
						return false
					}
					if handled {
						break
					}
					prepared, err := ix.preparedSQL(id, v, c)
					if err != nil {
						semanticErr = err
						return false
					}
					if prepared {
						break
					}
					sel, ok := v.Fun.(*ast.SelectorExpr)
					if !ok || !contains(c.SQLMethods, sel.Sel.Name) {
						break
					}
					arg := 0
					if strings.HasSuffix(sel.Sel.Name, "Context") {
						arg = 1
					}
					if arg >= len(v.Args) {
						break
					}
					namespaces, err := ix.callNamespaces(id, v, "", c)
					if err != nil {
						semanticErr = err
						return false
					}
					texts, complete := ix.stringCandidates(v.Args[arg], f, a)
					if !complete {
						ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "dynamic_sql", Reason: "query contains unresolved or multiply assigned values; only resolvable table fragments are inventoried", Evidence: ix.evidence(v.Args[arg].Pos())})
					}
					tables := []sqlAccess{}
					queries := make([]string, 0, len(texts))
					for _, text := range texts {
						queries = append(queries, text.text)
						for _, found := range ix.sqlAccesses(id, text.text, ix.evidence(v.Pos())) {
							if !tableHasHole(found.access.table, text) {
								tables = append(tables, found.access)
							}
						}
					}
					if complete && len(tables) == 0 && !ix.parsedSQL(queries) {
						ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "unparsed_sql", Reason: "configured SQL method has no recognized table access; statement or wrapper may be unsupported", Evidence: ix.evidence(v.Pos())})
					}
					for _, access := range tables {
						for _, namespace := range namespaces {
							ix.sqlTableAccess(id, resourceID("table", namespace, access.table), access.table, access.role, v)
						}
					}
				case *ast.CompositeLit:
					typ := f.pkg.TypesInfo.TypeOf(v)
					if typ == nil {
						break
					}
					resolvedType := types.Unalias(typ)
					if pointer, ok := resolvedType.Underlying().(*types.Pointer); ok {
						resolvedType = types.Unalias(pointer.Elem())
					}
					structType, ok := resolvedType.Underlying().(*types.Struct)
					if !ok {
						break
					}
					for i, element := range v.Elts {
						if keyed, ok := element.(*ast.KeyValueExpr); ok {
							if contains(c.EventFields, field(keyed.Key)) {
								event(keyed.Value, "event_construct")
							}
							continue
						}
						if i < structType.NumFields() && contains(c.EventFields, structType.Field(i).Name()) {
							event(element, "event_construct")
						}
					}
				case *ast.AssignStmt:
					if (v.Tok == token.ASSIGN || v.Tok == token.DEFINE) && len(v.Lhs) > len(v.Rhs) {
						for _, lhs := range v.Lhs {
							if contains(c.EventFields, field(lhs)) {
								ix.boundaries = append(ix.boundaries, Boundary{
									Node: id, Kind: "dynamic_event",
									Reason:   "configured event field receives a selected result from a multi-result expression; its value is unresolved",
									Evidence: ix.evidence(v.Pos()),
								})
							}
						}
						break
					}
					for i, lhs := range v.Lhs {
						if !contains(c.EventFields, field(lhs)) {
							continue
						}
						if v.Tok == token.ASSIGN || v.Tok == token.DEFINE {
							if i < len(v.Rhs) {
								event(v.Rhs[i], "event_assign")
							}
						} else {
							ix.boundaries = append(ix.boundaries, Boundary{
								Node: id, Kind: "dynamic_event",
								Reason:   "compound assignment combines an event value with another expression; the resulting value is unresolved",
								Evidence: ix.evidence(v.Pos()),
							})
						}
					}
				case *ast.RangeStmt:
					for _, lhs := range []ast.Expr{v.Key, v.Value} {
						if lhs != nil && contains(c.EventFields, field(lhs)) {
							ix.boundaries = append(ix.boundaries, Boundary{
								Node: id, Kind: "dynamic_event",
								Reason:   "configured event field receives a range iteration key or value; the collection candidate is unresolved",
								Evidence: ix.evidence(v.Pos()),
							})
						}
					}
				case *ast.BinaryExpr:
					if v.Op == token.EQL || v.Op == token.NEQ {
						if contains(c.EventFields, field(v.X)) {
							event(v.Y, "event_compare")
						}
						if contains(c.EventFields, field(v.Y)) {
							event(v.X, "event_compare")
						}
					}
				case *ast.SwitchStmt:
					if contains(c.EventFields, field(v.Tag)) {
						for _, item := range v.Body.List {
							if clause, ok := item.(*ast.CaseClause); ok {
								for _, expr := range clause.List {
									event(expr, "event_case")
								}
							}
						}
					}
				case *ast.GoStmt:
					ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "concurrency", Reason: "goroutine ownership and termination need investigation", Evidence: ix.evidence(v.Pos())})
				case *ast.DeferStmt:
					ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "cleanup", Reason: "deferred action is a cleanup candidate; adequacy and ordering are not established", Evidence: ix.evidence(v.Pos())})
				}
				return true
			})
		}
		if semanticErr != nil {
			return semanticErr
		}
	}
	for _, rule := range c.CallRules {
		if !matchedRules[rule.Symbol] {
			ix.boundaries = append(ix.boundaries, Boundary{Kind: "unmatched_rule", Reason: "configured API symbol not encountered: " + rule.Symbol})
		}
	}
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "storage_model", Reason: fmt.Sprintf("Table matches are candidates across potentially different databases. SQLite AST dependencies include schema and query sources; other dialects, unresolved construction, parser failures, trigger runtime conditions and ORM behavior require investigation. Recognized methods: %s", strings.Join(c.SQLMethods, ", "))})
	return nil
}

func resourceID(kind, namespace, value string) string {
	if namespace == "" {
		return kind + ":" + url.QueryEscape(value)
	}
	return kind + ":" + url.QueryEscape(namespace) + ":" + url.QueryEscape(value)
}
