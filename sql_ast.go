package contracttrace

import (
	"fmt"
	"io"
	"strings"

	sql "github.com/rqlite/sql"
)

type sqlVisitor struct {
	accesses   []sqlAccess
	positions  []sql.Pos
	statements int
	scopes     []map[string]bool
	scopeNodes map[sql.Node]bool
	writes     map[*sql.QualifiedTableName]bool
}

func (v *sqlVisitor) add(schema, name *sql.Ident, role string) {
	if name == nil {
		return
	}
	table := strings.ToLower(name.Name)
	if schema != nil {
		table = strings.ToLower(schema.Name) + "." + table
	} else if role == "read" {
		for i := len(v.scopes) - 1; i >= 0; i-- {
			if v.scopes[i][table] {
				return
			}
		}
	}
	v.accesses = append(v.accesses, sqlAccess{table, role})
	v.positions = append(v.positions, name.NamePos)
}
func (v *sqlVisitor) Visit(node sql.Node) (sql.Visitor, sql.Node, error) {
	var with *sql.WithClause
	switch n := node.(type) {
	case sql.SelectExpr:
		// The upstream visitor also treats scalar SELECT wrappers as leaves.
		if _, err := sql.Walk(v, n.SelectStatement); err != nil {
			return nil, node, err
		}
	case *sql.SelectExpr:
		if _, err := sql.Walk(v, n.SelectStatement); err != nil {
			return nil, node, err
		}
	case *sql.SelectStatement:
		with = n.WithClause
	case *sql.InsertStatement:
		with = n.WithClause
	case *sql.UpdateStatement:
		with = n.WithClause
	case *sql.DeleteStatement:
		with = n.WithClause
	}
	if with != nil {
		scope := map[string]bool{}
		for _, cte := range with.CTEs {
			scope[strings.ToLower(cte.TableName.Name)] = true
		}
		v.scopes = append(v.scopes, scope)
		v.scopeNodes[node] = true
	}
	switch n := node.(type) {
	case *sql.WithClause:
		// The pinned upstream walker visits WithClause but not its CTE bodies.
		for _, cte := range n.CTEs {
			if cte.Select != nil {
				if _, err := sql.Walk(v, cte.Select); err != nil {
					return nil, node, err
				}
			}
		}
	case *sql.QualifiedTableName:
		if !v.writes[n] {
			v.add(n.Schema, n.Name, "read")
		}
	case *sql.InsertStatement:
		v.add(n.Schema, n.Table, "write")
	case *sql.UpdateStatement:
		if n.Table != nil {
			v.writes[n.Table] = true
			v.add(n.Table.Schema, n.Table.Name, "write")
		}
	case *sql.DeleteStatement:
		if n.Table != nil {
			v.writes[n.Table] = true
			v.add(n.Table.Schema, n.Table.Name, "write")
		}
	case *sql.CreateTableStatement:
		v.add(n.Schema, n.Name, "schema")
	case *sql.AlterTableStatement:
		v.add(n.Schema, n.Name, "schema")
		if n.NewName != nil {
			v.add(n.Schema, n.NewName, "schema")
		}
	case *sql.DropTableStatement:
		v.add(n.Schema, n.Name, "schema")
	case *sql.CreateVirtualTableStatement:
		v.add(n.Schema, n.Name, "schema")
	case *sql.CreateViewStatement:
		v.add(nil, n.Name, "schema")
	case *sql.DropViewStatement:
		v.add(nil, n.Name, "schema")
	case *sql.CreateIndexStatement:
		v.add(n.Schema, n.Table, "schema")
	case *sql.CreateTriggerStatement:
		v.add(nil, n.Table, "schema")
	case *sql.ForeignKeyConstraint:
		v.add(nil, n.ForeignTable, "schema_ref")
	}
	return v, node, nil
}
func (v *sqlVisitor) VisitEnd(node sql.Node) (sql.Node, error) {
	if v.scopeNodes[node] {
		v.scopes = v.scopes[:len(v.scopes)-1]
	}
	return node, nil
}

func parseSQL(q string) sqlAnalysis {
	parser := sql.NewParser(strings.NewReader(q))
	v := &sqlVisitor{scopeNodes: map[sql.Node]bool{}, writes: map[*sql.QualifiedTableName]bool{}}
	for {
		stmt, err := parser.ParseStatement()
		if err == io.EOF {
			return makeSQLAnalysis(v, nil)
		}
		if err != nil {
			return makeSQLAnalysis(v, fmt.Errorf("SQLite parsing: %w", err))
		}
		if _, err := sql.Walk(v, stmt); err != nil {
			return makeSQLAnalysis(v, err)
		}
		v.statements++
	}
}
func parseSQLTables(q string) ([]sqlAccess, error) {
	r := parseSQL(q)
	result := []sqlAccess{}
	for _, f := range r.found {
		result = append(result, f.access)
	}
	return result, r.err
}
func sqlTables(q string) []sqlAccess {
	accesses, err := parseSQLTables(q)
	if err != nil {
		return inventorySQLTables(q)
	}
	if accesses == nil {
		return []sqlAccess{}
	}
	return accesses
}
