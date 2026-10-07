package contracttrace

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type sqlHandle struct {
	owner, symbol   string
	position        token.Pos
	certainty       string
	discarded       bool
	discardEvidence Evidence
	typ             types.Type
	parent          flowValue
	hasParent       bool
	parentUnknown   bool
	queryUnknown    bool
	originalUnknown bool
	queries         map[sqlQueryBinding]bool
	originals       map[ssa.Value]bool
	symbols         map[string]bool
}

type sqlQueryBinding struct {
	query  ssa.Value
	symbol string
}

type sqlQueryOrigin struct {
	handle string
	query  ssa.Value
	symbol string
}

func isSQLHandleType(t types.Type) bool {
	switch sqlReceiver(t) {
	case "DB", "Conn", "Tx", "Stmt":
		return true
	}
	return false
}

func (a *flowAnalysis) sqlExternalResult(common *ssa.CallCommon, ix *index) bool {
	if a.get(common.Value).functionUnknown || common.IsInvoke() && a.get(common.Value).interfaceUnknown {
		return true
	}
	targets := a.targets(common)
	if len(targets) == 0 {
		return false
	}
	for target := range targets {
		if ix.owner(target) == "" || len(target.Blocks) == 0 {
			return true
		}
	}
	return false
}

func (a *flowAnalysis) seedUnresolvedSQLResults(funcs []*ssa.Function) bool {
	changed := false
	for _, fn := range funcs {
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				var call *ssa.Call
				var value ssa.Value
				switch result := instruction.(type) {
				case *ssa.Call:
					call, value = result, result
				case *ssa.Extract:
					call, _ = result.Tuple.(*ssa.Call)
					value = result
				}
				if call == nil || !isSQLHandleType(value.Type()) || a.special[call] != nil || len(a.targets(call.Common())) != 0 {
					continue
				}
				unknown := emptyFlow()
				unknown.sqlUnknown = true
				if a.put(value, unknown) {
					changed = true
				}
			}
		}
	}
	return changed
}

// Exported functions remain open to callers outside the selected build even
// when local calls are known. Uncalled local parameters also lack an origin.
func (a *flowAnalysis) seedSQLInputs(funcs []*ssa.Function, ix *index, uncalled bool) bool {
	changed := false
	called := map[*ssa.Function]bool{}
	for _, fn := range funcs {
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if call, ok := instruction.(ssa.CallInstruction); ok {
					for target := range a.targets(call.Common()) {
						if ix.owner(target) != "" {
							called[target] = true
						}
					}
				}
			}
		}
	}
	for _, fn := range funcs {
		exported := fn.Object() != nil && fn.Object().Exported()
		if uncalled == exported {
			continue
		}
		if called[fn] && !exported {
			continue
		}
		for _, parameter := range fn.Params {
			if isSQLHandleType(parameter.Type()) {
				value := emptyFlow()
				value.sqlUnknown = true
				if a.put(parameter, value) {
					changed = true
				}
				evidence := ix.evidence(parameter.Pos())
				if parameter.Pos() == token.NoPos {
					evidence = ix.evidence(fn.Pos())
					if fn.Pos() == token.NoPos && ix.funcs[ix.owner(fn)] != nil {
						evidence = ix.funcs[ix.owner(fn)].node.Evidence
					}
					evidence.Origin = "synthetic_declaration"
				}
				reason := "SQL-handle parameter has no modeled local caller and retains an unknown origin"
				if exported {
					reason = "exported SQL-handle parameter can receive values from outside the selected package/build; local callers do not close its origin set"
				}
				ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(fn), Kind: "external_sql_input", Reason: reason + ": " + parameter.Name(), Evidence: evidence})
			}
		}
	}
	return changed
}

func sqlReceiver(t types.Type) string {
	if pointer, ok := types.Unalias(t).(*types.Pointer); ok {
		t = pointer.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "database/sql" {
		return ""
	}
	return named.Obj().Name()
}

func (a *flowAnalysis) modelSQLHandle(call ssa.CallInstruction, ix *index) {
	common := call.Common()
	invocations, _ := a.sqlInvocations(common)
	for _, invocation := range invocations {
		kind := ""
		queryArgument := -1
		originalArgument := -1
		switch {
		case invocation.apiFunction && (invocation.name == "Open" || invocation.name == "OpenDB"):
			kind = "database"
		case invocation.receiverKind == "DB" && invocation.name == "Conn":
			kind = "connection"
		case strings.HasPrefix(invocation.name, "Prepare"):
			kind = "statement"
			queryArgument = 0
			if invocation.name == "PrepareContext" {
				queryArgument = 1
			}
		case invocation.name == "Begin" || invocation.name == "BeginTx":
			kind = "transaction"
		case invocation.receiverKind == "Tx" && (invocation.name == "Stmt" || invocation.name == "StmtContext"):
			kind = "statement"
			originalArgument = 0
			if invocation.name == "StmtContext" {
				originalArgument = 1
			}
		}
		if kind == "" {
			continue
		}
		id := ix.sqlHandleAllocationID(kind, call)
		value := emptyFlow()
		value.addresses[id] = true
		direct, hasResult := call.(*ssa.Call)
		if hasResult {
			if a.special == nil {
				a.special = map[*ssa.Call][]flowValue{}
			}
			resultCount := 1
			if tuple, ok := direct.Type().(*types.Tuple); ok {
				resultCount = tuple.Len()
			}
			if resultCount < 1 {
				resultCount = 1
			}
			results := a.special[direct]
			for len(results) < resultCount {
				results = append(results, emptyFlow())
			}
			a.merge(&results[0], value)
			a.special[direct] = results
		}
		certainty := "fact"
		if !hasResult {
			certainty = "possible"
		}
		if ix.funcs[id] == nil {
			ix.funcs[id] = &function{node: Node{ID: id, Name: kind + " creation", Kind: kind, Evidence: ix.callEvidence(call)}}
		}
		if a.sqlHandles == nil {
			a.sqlHandles = map[string]sqlHandle{}
		}
		site, exists := a.sqlHandles[id]
		if !exists {
			var handleType types.Type
			if signature, ok := invocation.method.Type().(*types.Signature); ok && signature.Results().Len() > 0 {
				handleType = signature.Results().At(0).Type()
			}
			site = sqlHandle{owner: ix.owner(call.Parent()), position: common.Pos(), certainty: certainty, typ: handleType, parent: emptyFlow(), queries: map[sqlQueryBinding]bool{}, originals: map[ssa.Value]bool{}, symbols: map[string]bool{}}
		}
		if !hasResult {
			site.discarded = true
			site.discardEvidence = ix.callEvidence(call)
		}
		site.symbol = sqlInvocationSymbol(invocation)
		site.symbols[site.symbol] = true
		if invocation.receiverKind != "" {
			site.hasParent = true
			a.merge(&site.parent, invocation.receiver)
			site.parentUnknown = site.parentUnknown || invocation.unknownReceiver
		}
		if queryArgument >= 0 {
			if queryArgument < len(invocation.arguments) {
				site.queries[sqlQueryBinding{query: invocation.arguments[queryArgument], symbol: site.symbol}] = true
			} else {
				site.queryUnknown = true
			}
		}
		if originalArgument >= 0 {
			if originalArgument < len(invocation.arguments) {
				site.originals[invocation.arguments[originalArgument]] = true
			} else {
				site.originalUnknown = true
			}
		}
		a.sqlHandles[id] = site
	}
}

func (ix *index) sqlHandleAllocationID(kind string, call ssa.CallInstruction) string {
	if value, ok := call.(ssa.Value); ok {
		return ix.allocationID(kind, call.Parent(), value)
	}
	evidence := ix.callEvidence(call)
	return fmt.Sprintf("%s:%s:%s:%d:%d:call", kind, call.Parent().String(), evidence.File, evidence.Line, evidence.Column)
}

func (a *flowAnalysis) sqlHandleUse(call ssa.CallInstruction, ix *index) {
	invocations, unknown := a.sqlInvocations(call.Common())
	for _, invocation := range invocations {
		receiver, name := invocation.receiverKind, invocation.name
		value := invocation.receiver
		position := call.Common().Pos()
		query := strings.HasPrefix(name, "Exec") || strings.HasPrefix(name, "Query") || strings.HasPrefix(name, "Prepare")
		if query {
			a.recordSQLSemanticCall(position, invocation)
			if a.sqlReceivers == nil {
				a.sqlReceivers = map[token.Pos]flowValue{}
			}
			oldReceiver := a.sqlReceivers[position]
			a.merge(&oldReceiver, value)
			oldReceiver.sqlUnknown = oldReceiver.sqlUnknown || invocation.unknownReceiver || unknown
			a.sqlReceivers[position] = oldReceiver
			operation := ix.lifecycleSite(call, "sql_operation", receiver+"."+name+" SQL operation")
			ix.callEdge(ix.owner(call.Parent()), operation, "sql_operation", "fact", call)
			if a.sqlOperations == nil {
				a.sqlOperations = map[token.Pos]map[string]bool{}
			}
			if a.sqlOperations[position] == nil {
				a.sqlOperations[position] = map[string]bool{}
			}
			a.sqlOperations[position][operation] = true
			resources := a.sqlResources(value, receiver)
			for _, resource := range resources {
				ix.callEdge(operation, resource, "sql_receiver", "possible", call)
			}
			if len(resources) == 0 || invocation.unknownReceiver || unknown {
				ix.boundaries = append(ix.boundaries, Boundary{Node: operation, Kind: "unresolved_sql_handle", Reason: "SQL operation includes a candidate without a modeled receiver handle origin or an outside target", Evidence: ix.callEvidence(call)})
			}
		}
		if receiver == "Stmt" && (strings.HasPrefix(name, "Exec") || strings.HasPrefix(name, "Query")) {
			if a.statementExecutions == nil {
				a.statementExecutions = map[token.Pos]map[string]flowValue{}
			}
			if a.statementExecutions[position] == nil {
				a.statementExecutions[position] = map[string]flowValue{}
			}
			old := a.statementExecutions[position][name]
			a.merge(&old, value)
			a.statementExecutions[position][name] = old
		}
		kind := ""
		if receiver == "Stmt" && name == "Close" {
			kind = "statement_close"
		}
		if receiver == "DB" && name == "Close" {
			kind = "database_close"
		}
		if receiver == "Conn" && name == "Close" {
			kind = "connection_close"
		}
		if receiver == "Stmt" && (strings.HasPrefix(name, "Exec") || strings.HasPrefix(name, "Query")) {
			kind = "statement_execute"
		}
		if receiver == "Tx" && (name == "Commit" || name == "Rollback") {
			kind = "transaction_" + strings.ToLower(name)
		}
		if kind == "" {
			continue
		}
		resources := a.sqlResources(value, receiver)
		for _, resource := range resources {
			ix.callEdge(ix.owner(call.Parent()), resource, kind, "possible", call)
		}
		if len(resources) == 0 || invocation.unknownReceiver || unknown {
			ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(call.Parent()), Kind: "unresolved_sql_handle", Reason: "database/sql operation includes a candidate without a modeled receiver handle origin or an outside target", Evidence: ix.callEvidence(call)})
		}
	}
}

func (a *flowAnalysis) sqlResources(receiverValue flowValue, receiver string) []string {
	prefix := "statement:"
	if receiver == "Tx" {
		prefix = "transaction:"
	} else if receiver == "DB" {
		prefix = "database:"
	} else if receiver == "Conn" {
		prefix = "connection:"
	}
	var resources []string
	for _, alias := range sortedKeys(receiverValue.addresses) {
		if strings.HasPrefix(alias, prefix) {
			resources = append(resources, alias)
		}
	}
	return resources
}

func (ix *index) sqlTableAccess(owner, key, table, role string, call *ast.CallExpr) {
	ix.resource(owner, key, table, "table", "sql_"+role, call.Pos())
	if ix.flow != nil {
		for _, operation := range sortedKeys(ix.flow.sqlOperations[call.Lparen]) {
			ix.resource(operation, key, table, "table", "sql_"+role, call.Pos())
		}
	}
}

// Follow recorded rebinding aliases after value-flow convergence. A visited set
// bounds cyclic source paths without dropping other reachable preparation sites.
func (a *flowAnalysis) sqlQueryOrigins(key string) ([]sqlQueryOrigin, bool) {
	queue := []string{key}
	visited := map[string]bool{}
	origins := map[sqlQueryOrigin]bool{}
	unresolved := false
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		if visited[current] {
			continue
		}
		visited[current] = true
		site, exists := a.sqlHandles[current]
		if !exists {
			unresolved = true
			continue
		}
		for binding := range site.queries {
			origins[sqlQueryOrigin{handle: current, query: binding.query, symbol: binding.symbol}] = true
		}
		unresolved = unresolved || site.queryUnknown || site.originalUnknown
		if len(site.originals) == 0 {
			if len(site.queries) == 0 {
				unresolved = true
			}
			continue
		}
		for _, candidate := range sortedSSAValues(site.originals) {
			original := a.get(candidate)
			unresolved = unresolved || original.sqlUnknown || original.interfaceUnknown
			aliases := sortedKeys(original.addresses)
			if len(aliases) == 0 {
				unresolved = true
			}
			queue = append(queue, aliases...)
		}
	}
	result := make([]sqlQueryOrigin, 0, len(origins))
	for origin := range origins {
		result = append(result, origin)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].handle != result[j].handle {
			return result[i].handle < result[j].handle
		}
		if result[i].symbol != result[j].symbol {
			return result[i].symbol < result[j].symbol
		}
		return result[i].query.String() < result[j].query.String()
	})
	return result, unresolved || len(result) == 0
}

func (ix *index) preparedSQL(id string, call *ast.CallExpr, config Config) (bool, error) {
	if ix.flow == nil {
		return false, nil
	}
	executions := ix.flow.statementExecutions[call.Lparen]
	var execution flowValue
	configured := false
	for _, name := range sortedKeys(executions) {
		if !contains(config.SQLMethods, name) {
			continue
		}
		configured = true
		ix.flow.merge(&execution, executions[name])
	}
	if !configured {
		return false, nil
	}
	found := false
	unresolved := false
	keys := sortedKeys(execution.addresses)
	unresolved = len(keys) == 0 || execution.sqlUnknown || execution.interfaceUnknown
	for _, key := range keys {
		origins, missing := ix.flow.sqlQueryOrigins(key)
		unresolved = unresolved || missing
		for _, origin := range origins {
			site := ix.flow.sqlHandles[origin.handle]
			namespaceOverride := ""
			for _, rule := range config.CallRules {
				if rule.Symbol == origin.symbol && rule.Kind == "sql_query" {
					namespaceOverride = rule.Namespace
				}
			}
			namespaces, err := ix.receiverNamespaces(site.owner, ix.evidence(site.position), []string{origin.handle}, execution.sqlUnknown || execution.interfaceUnknown, namespaceOverride, config)
			if err != nil {
				return true, err
			}
			queryValue := ix.flow.get(origin.query)
			queries := sortedKeys(queryValue.strings)
			unresolved = unresolved || queryValue.stringUnknown
			if len(queries) == 0 {
				unresolved = true
			}
			for _, query := range queries {
				found = true
				for _, access := range ix.sqlAccesses(key, query, ix.evidence(site.position)) {
					for _, namespace := range namespaces {
						ix.resource(origin.handle, resourceID("table", namespace, access.access.table), access.access.table, "table", "prepared_sql_"+access.access.role, site.position)
						ix.sqlTableAccess(id, resourceID("table", namespace, access.access.table), access.access.table, access.access.role, call)
					}
				}
			}
		}
	}
	if !found || unresolved {
		ix.boundaries = append(ix.boundaries, Boundary{Node: id, Kind: "unresolved_prepared_sql", Reason: "prepared execution includes an unresolved query origin or additional unknown query strings alongside known candidates; bound parameters are not SQL source", Evidence: ix.evidence(call.Pos())})
	}
	return true, nil
}
