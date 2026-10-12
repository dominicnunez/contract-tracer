package contracttrace

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/packages"
)

func inspectParents(root ast.Node, visit func(ast.Node, []ast.Node)) {
	parents := []ast.Node{}
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			parents = parents[:len(parents)-1]
			return true
		}
		visit(node, parents)
		parents = append(parents, node)
		return true
	})
}

func (ix *index) recordFields(pkgs []*packages.Package, config Config) {
	fields := map[token.Pos]string{}
	if ix.declarationRanges == nil {
		ix.declarationRanges = map[string]SourceRange{}
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			if _, inside := relative(ix.root, ix.fset.Position(file.Pos()).Filename); !inside {
				continue
			}
			inspectParents(file, func(node ast.Node, parents []ast.Node) {
				literal, ok := node.(*ast.StructType)
				if !ok {
					return
				}
				typ := pkg.TypesInfo.TypeOf(literal)
				if typ == nil {
					return
				}
				shape, ok := typ.Underlying().(*types.Struct)
				if !ok {
					return
				}
				e := ix.evidence(literal.Pos())
				label := fmt.Sprintf("struct@%s:%d:%d", e.File, e.Line, e.Column)
				if len(parents) > 0 {
					if spec, ok := parents[len(parents)-1].(*ast.TypeSpec); ok && spec.Type == literal {
						label = spec.Name.Name
						for _, parent := range parents {
							if fn, ok := parent.(*ast.FuncDecl); ok {
								label = fmt.Sprintf("%s.%s@%s:%d:%d", fn.Name.Name, label, e.File, e.Line, e.Column)
							}
						}
					}
				}
				for i := 0; i < shape.NumFields(); i++ {
					field := shape.Field(i)
					if field.Name() == "_" {
						continue
					}
					id := "field:" + pkg.PkgPath + "::" + label + "." + field.Name()
					fields[field.Pos()] = id
					if ix.funcs[id] == nil {
						kind := "record_field"
						if basic, ok := field.Type().Underlying().(*types.Basic); ok && basic.Kind() == types.String && contains(config.EventFields, field.Name()) {
							kind = "event_discriminator_field"
						}
						ix.funcs[id] = &function{node: Node{ID: id, Name: label + "." + field.Name(), Kind: kind, Evidence: ix.evidence(field.Pos())}}
					}
					// Each named field starts at its own declaration evidence. A
					// grouped declaration shares the type's end, so overlapping
					// line seeds remain explicit ambiguities rather than guesses.
					for _, group := range literal.Fields.List {
						if field.Pos() >= group.Pos() && field.Pos() < group.End() {
							ix.declarationRanges[id] = SourceRange{First: ix.fset.Position(field.Pos()).Line, Last: ix.fset.Position(group.End()).Line}
							break
						}
					}
				}
			})
		}
	}
	link := func(owner string, field *types.Var, kind string, pos token.Pos) {
		if target := fields[field.Pos()]; target != "" {
			ix.edge(owner, target, kind, "fact", pos)
		} else {
			ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "unindexed_record_field", Reason: "Typed field " + field.String() + " has no indexed declaration in the target; dependency or omitted type storage relationships remain unresolved.", Evidence: ix.evidence(pos)})
		}
	}
	// A Go value copy includes nested records and nonempty fixed arrays, but
	// copying a reference/descriptor does not copy its referenced contents.
	// Cache declaration inventories rather than enumerating array elements or
	// repeating a shared nested type for every occurrence in a larger record.
	inventories := map[types.Type][]*types.Var{}
	valueFields := func(typ types.Type) []*types.Var {
		if cached, ok := inventories[typ]; ok {
			return cached
		}
		seen := map[types.Type]bool{}
		var found []*types.Var
		var visit func(types.Type)
		visit = func(current types.Type) {
			if current == nil || seen[current] {
				return
			}
			seen[current] = true
			switch shape := current.Underlying().(type) {
			case *types.Struct:
				for i := 0; i < shape.NumFields(); i++ {
					field := shape.Field(i)
					if field.Name() != "_" {
						found = append(found, field)
						visit(field.Type())
					}
				}
			case *types.Array:
				if shape.Len() > 0 {
					visit(shape.Elem())
				}
			case *types.Tuple:
				for i := 0; i < shape.Len(); i++ {
					visit(shape.At(i).Type())
				}
			}
		}
		visit(typ)
		inventories[typ] = found
		return found
	}
	scan := func(owner string, root ast.Node, pkg *packages.Package, results, receiver *ast.FieldList) {
		record := func(typ types.Type, kind string, pos token.Pos) {
			for _, field := range valueFields(typ) {
				link(owner, field, kind, pos)
			}
		}
		namedResults := func(list *ast.FieldList, kind string, pos token.Pos) {
			if list == nil {
				return
			}
			for _, result := range list.List {
				for _, name := range result.Names {
					if variable, ok := pkg.TypesInfo.Defs[name].(*types.Var); ok {
						evidence := pos
						if !evidence.IsValid() {
							evidence = name.Pos()
						}
						record(variable.Type(), kind, evidence)
					}
				}
			}
		}
		namedResults(results, "field_zero_initialize", token.NoPos)
		if receiver != nil {
			for _, field := range receiver.List {
				record(pkg.TypesInfo.TypeOf(field.Type), "field_receiver_parameter", field.Type.Pos())
			}
		}
		embeddingAccess := func(selection *types.Selection, pos token.Pos) {
			typ := selection.Recv()
			for _, index := range selection.Index()[:len(selection.Index())-1] {
				if pointer, ok := typ.Underlying().(*types.Pointer); ok {
					typ = pointer.Elem()
				}
				shape, ok := typ.Underlying().(*types.Struct)
				if !ok || index >= shape.NumFields() {
					break
				}
				embedded := shape.Field(index)
				link(owner, embedded, "field_embedding_access", pos)
				typ = embedded.Type()
			}
		}
		copyReceiver := func(selection *types.Selection, pos token.Pos) {
			if signature, ok := selection.Obj().Type().(*types.Signature); ok && signature.Recv() != nil {
				typ := signature.Recv().Type()
				if _, ok := typ.Underlying().(*types.Interface); ok {
					ix.boundaries = append(ix.boundaries, Boundary{Node: owner, Kind: "unresolved_record_receiver", Reason: "Interface method receiver implementation may use a value or pointer receiver; record copies and runtime dispatch identity are unresolved.", Evidence: ix.evidence(pos)})
				} else {
					record(typ, "field_receiver_copy", pos)
				}
			}
		}
		whole := func(expression ast.Expr, kind string) {
			if expression == nil {
				return
			}
			if name, ok := expression.(*ast.Ident); ok && name.Name == "_" {
				return
			}
			if tv, ok := pkg.TypesInfo.Types[expression]; ok && tv.IsType() {
				return
			}
			record(pkg.TypesInfo.TypeOf(expression), kind, expression.Pos())
		}
		inspectParents(root, func(node ast.Node, parents []ast.Node) {
			switch expression := node.(type) {
			case *ast.ReturnStmt:
				if len(expression.Results) == 0 {
					list := results
					for i := len(parents) - 1; i >= 0; i-- {
						if literal, ok := parents[i].(*ast.FuncLit); ok {
							list = literal.Type.Results
							break
						}
					}
					namedResults(list, "field_record_read", expression.Pos())
				}
				for _, value := range expression.Results {
					whole(value, "field_record_read")
				}
			case *ast.AssignStmt:
				for _, value := range expression.Lhs {
					whole(value, "field_record_write")
				}
				for _, value := range expression.Rhs {
					whole(value, "field_record_read")
				}
			case *ast.ValueSpec:
				for _, name := range expression.Names {
					if name.Name != "_" {
						if variable, ok := pkg.TypesInfo.Defs[name].(*types.Var); ok {
							kind := "field_record_write"
							if len(expression.Values) == 0 {
								kind = "field_zero_initialize"
							}
							record(variable.Type(), kind, name.Pos())
						}
					}
				}
				for _, value := range expression.Values {
					whole(value, "field_record_read")
				}
			case *ast.SendStmt:
				whole(expression.Value, "field_record_read")
			case *ast.BinaryExpr:
				whole(expression.X, "field_record_read")
				whole(expression.Y, "field_record_read")
			case *ast.CallExpr:
				callee := expression.Fun
				for {
					parenthesized, ok := callee.(*ast.ParenExpr)
					if !ok {
						break
					}
					callee = parenthesized.X
				}
				if selector, ok := callee.(*ast.SelectorExpr); ok {
					if selection := pkg.TypesInfo.Selections[selector]; selection != nil && selection.Kind() == types.MethodExpr {
						copyReceiver(selection, expression.Pos())
						embeddingAccess(selection, expression.Pos())
					}
				}
				for _, argument := range expression.Args {
					whole(argument, "field_record_read")
				}
				if callee, ok := expression.Fun.(*ast.Ident); ok {
					if builtin, ok := pkg.TypesInfo.Uses[callee].(*types.Builtin); ok && builtin.Name() == "new" {
						if typ := pkg.TypesInfo.TypeOf(expression); typ != nil {
							if pointer, ok := typ.Underlying().(*types.Pointer); ok {
								record(pointer.Elem(), "field_zero_initialize", expression.Pos())
							}
						}
					}
				}
			case *ast.FuncLit:
				namedResults(expression.Type.Results, "field_zero_initialize", token.NoPos)
			case *ast.SelectorExpr:
				selection := pkg.TypesInfo.Selections[expression]
				if selection == nil {
					return
				}
				if selection.Kind() == types.MethodVal {
					copyReceiver(selection, expression.Sel.Pos())
					embeddingAccess(selection, expression.Sel.Pos())
					return
				}
				if selection.Kind() != types.FieldVal {
					return
				}
				field, ok := selection.Obj().(*types.Var)
				if !ok {
					return
				}
				for _, kind := range fieldAccessRoles(expression, parents) {
					link(owner, field, kind, expression.Sel.Pos())
				}
				embeddingAccess(selection, expression.Sel.Pos())
			case *ast.CompositeLit:
				typ := pkg.TypesInfo.TypeOf(expression)
				if typ == nil {
					return
				}
				if array, ok := typ.Underlying().(*types.Array); ok {
					written := map[int64]bool{}
					next := int64(0)
					for _, element := range expression.Elts {
						value := element
						if pair, ok := element.(*ast.KeyValueExpr); ok {
							value = pair.Value
							index, exact := constant.Int64Val(pkg.TypesInfo.Types[pair.Key].Value)
							if !exact {
								continue
							}
							next = index
						}
						written[next] = true
						next++
						record(array.Elem(), "field_write", element.Pos())
						whole(value, "field_record_read")
					}
					if int64(len(written)) < array.Len() {
						record(array.Elem(), "field_zero_initialize", expression.Pos())
					}
					return
				}
				shape, ok := typ.Underlying().(*types.Struct)
				if !ok {
					return
				}
				written := map[int]bool{}
				for i, element := range expression.Elts {
					index := i
					if pair, ok := element.(*ast.KeyValueExpr); ok {
						key, ok := pair.Key.(*ast.Ident)
						if !ok {
							continue
						}
						index = -1
						for j := 0; j < shape.NumFields(); j++ {
							if shape.Field(j).Name() == key.Name {
								index = j
								break
							}
						}
					}
					if index >= 0 && index < shape.NumFields() {
						written[index] = true
						if shape.Field(index).Name() != "_" {
							link(owner, shape.Field(index), "field_write", element.Pos())
							record(shape.Field(index).Type(), "field_write", element.Pos())
						}
					}
				}
				for i := 0; i < shape.NumFields(); i++ {
					if !written[i] && shape.Field(i).Name() != "_" {
						link(owner, shape.Field(i), "field_zero_initialize", expression.Pos())
						record(shape.Field(i).Type(), "field_zero_initialize", expression.Pos())
					}
				}
			}
		})
	}
	ids := []string{}
	for id, f := range ix.funcs {
		if f.decl != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		f := ix.funcs[id]
		scan(id, f.decl.Body, f.pkg, f.decl.Type.Results, f.decl.Recv)
	}
	for _, pkg := range pkgs {
		owner := pkg.PkgPath + "::(package init)"
		if ix.funcs[owner] == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			if _, inside := relative(ix.root, ix.fset.Position(file.Pos()).Filename); !inside {
				continue
			}
			for _, decl := range file.Decls {
				if group, ok := decl.(*ast.GenDecl); ok && group.Tok == token.VAR {
					for _, spec := range group.Specs {
						scan(owner, spec, pkg, nil, nil)
					}
				}
			}
		}
	}
	ix.boundaries = append(ix.boundaries, Boundary{Kind: "record_field_model", Reason: "Typed source accesses, composite/new/variable/named-result initialization, record bindings, explicit and bare returns, tuple result values, arguments, sends, comparisons, formal value receivers and direct/bound receiver copies connect to indexed field declarations, including private fields, promoted selections and generic instances. Shared declaration identity connects sibling investigation paths, not proof of a shared runtime object. Conditional execution, pointer/container identity, field address escapes, reflection, dependency declarations and temporal ownership remain unresolved. Aliased method-expression invocation copies, dynamic interface receiver copies, pointer and descriptor contents are not implied by value copies; nested value records and nonempty fixed arrays are enumerated without runtime element correlation; field relationships do not correlate tuple slots or runtime instances."})
}

func fieldAccessRoles(expression ast.Expr, parents []ast.Node) []string {
	current := ast.Node(expression)
	contents := false
	for i := len(parents) - 1; i >= 0; i-- {
		switch parent := parents[i].(type) {
		case *ast.ParenExpr:
			if parent.X != current {
				return []string{"field_read"}
			}
			current = parent
		case *ast.IndexExpr:
			if parent.X != current {
				return []string{"field_read"}
			}
			contents = true
			current = parent
		case *ast.StarExpr:
			if parent.X != current {
				return []string{"field_read"}
			}
			contents = true
			current = parent
		case *ast.SelectorExpr:
			if parent.X != current {
				return []string{"field_read"}
			}
			contents = true
			current = parent
		case *ast.AssignStmt:
			for _, left := range parent.Lhs {
				if left == current {
					if contents {
						return []string{"field_read", "field_content_write"}
					}
					if parent.Tok != token.ASSIGN && parent.Tok != token.DEFINE {
						return []string{"field_read", "field_write"}
					}
					return []string{"field_write"}
				}
			}
			return []string{"field_read"}
		case *ast.IncDecStmt:
			if parent.X == current {
				if contents {
					return []string{"field_read", "field_content_write"}
				}
				return []string{"field_read", "field_write"}
			}
			return []string{"field_read"}
		case *ast.UnaryExpr:
			if parent.Op == token.AND && parent.X == current {
				return []string{"field_address"}
			}
			return []string{"field_read"}
		default:
			return []string{"field_read"}
		}
	}
	return []string{"field_read"}
}
