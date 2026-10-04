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

func localAssignments(f *function) assignments {
	a := assignments{values: map[types.Object]ast.Expr{}, counts: map[types.Object]int{}}
	set := func(lhs ast.Expr, rhs ast.Expr) {
		id, ok := lhs.(*ast.Ident)
		if !ok {
			return
		}
		obj := f.pkg.TypesInfo.ObjectOf(id)
		if obj != nil {
			a.counts[obj]++
			a.values[obj] = rhs
		}
	}
	ast.Inspect(f.decl.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range v.Lhs {
				if i < len(v.Rhs) {
					set(lhs, v.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, id := range v.Names {
				if i < len(v.Values) {
					set(id, v.Values[i])
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
	return a
}

const unknown = "__contract_unknown__"

func stringValue(expr ast.Expr, f *function, a assignments, seen map[types.Object]bool) (string, bool) {
	if v, ok := f.pkg.TypesInfo.Types[expr]; ok && v.Value != nil && v.Value.Kind() == constant.String {
		return constant.StringVal(v.Value), true
	}
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return stringValue(e.X, f, a, seen)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			x, xok := stringValue(e.X, f, a, seen)
			y, yok := stringValue(e.Y, f, a, seen)
			return x + y, xok && yok
		}
	case *ast.Ident:
		obj := f.pkg.TypesInfo.ObjectOf(e)
		if c, ok := obj.(*types.Const); ok && c.Val().Kind() == constant.String {
			return constant.StringVal(c.Val()), true
		}
		if obj != nil && !seen[obj] && a.counts[obj] == 1 {
			seen[obj] = true
			value, ok := stringValue(a.values[obj], f, a, seen)
			delete(seen, obj)
			return value, ok
		}
	}
	return unknown, false
}
func (ix *index) resource(from, key, name, kind, relationship string, pos token.Pos) {
	evidence := ix.evidence(pos)
	if existing := ix.funcs[key]; existing == nil {
		ix.funcs[key] = &function{node: Node{ID: key, Name: name, Kind: kind, Evidence: evidence}}
	}
	ix.edge(from, key, relationship, "possible", pos)
}

func (ix *index) stringCandidates(expr ast.Expr, f *function, a assignments) ([]string, bool) {
	text, complete := stringValue(expr, f, a, map[types.Object]bool{})
	if complete {
		return []string{text}, true
	}
	if ix.flow != nil {
		value := ix.flow.positions[expr.Pos()]
		if call, ok := expr.(*ast.CallExpr); ok {
			ix.flow.merge(&value, ix.flow.positions[call.Lparen])
		}
		values := []string{}
		for s := range value.strings {
			values = append(values, s)
		}
		sort.Strings(values)
		if len(values) > 0 {
			return values, false
		}
	}
	return []string{text}, false
}
func (ix *index) semantic(c Config) error {
	ix.databaseOriginBoundaries(c)
	ix.storage.Dialect = c.SQLDialect
	matchedRules := map[string]bool{}
	ids := []string{}
	for id, f := range ix.funcs {
		if f.decl != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		f := ix.funcs[id]
		a := localAssignments(f)
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
				if value != "" && !strings.Contains(value, unknown) {
					ix.resource(id, resourceID("event", "", value), value, "event", role, expr.Pos())
				}
			}
		}
		var semanticErr error
		ast.Inspect(f.decl.Body, func(n ast.Node) bool {
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
				prepared, err := ix.preparedSQL(id, f, v, c)
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
				for _, text := range texts {
					for _, found := range ix.sqlAccesses(id, text, ix.evidence(v.Pos())) {
						tables = append(tables, found.access)
					}
				}
				if complete && len(tables) == 0 && !ix.parsedSQL(texts) {
					ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "unparsed_sql", Reason: "configured SQL method has no recognized table access; statement or wrapper may be unsupported", Evidence: ix.evidence(v.Pos())})
				}
				for _, access := range tables {
					if !strings.Contains(access.table, unknown) {
						for _, namespace := range namespaces {
							ix.sqlTableAccess(id, resourceID("table", namespace, access.table), access.table, access.role, v)
						}
					}
				}
			case *ast.KeyValueExpr:
				if contains(c.EventFields, field(v.Key)) {
					event(v.Value, "event_construct")
				}
			case *ast.AssignStmt:
				for i, lhs := range v.Lhs {
					if contains(c.EventFields, field(lhs)) && i < len(v.Rhs) {
						event(v.Rhs[i], "event_assign")
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
