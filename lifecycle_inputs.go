package contracttrace

import (
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// Opaque lifecycle handles can have no public fields. Their type still needs
// outside-input summaries when it crosses a public or escaping boundary.
func (a *flowAnalysis) collectLifecycleTypes(fn *ssa.Function, config Config) {
	if len(config.LifecycleRules) == 0 {
		return
	}
	symbol := lifecycleSymbol(fn)
	if symbol == "" {
		return
	}
	signature := lifecycleDeclaredSignature(fn)
	if signature == nil {
		return
	}
	for _, rule := range config.LifecycleRules {
		if rule.Symbol != symbol || rule.Identity != "origin" {
			continue
		}
		var typ types.Type
		switch {
		case rule.Result != nil && *rule.Result < signature.Results().Len():
			typ = signature.Results().At(*rule.Result).Type()
		case rule.Argument != nil && *rule.Argument < signature.Params().Len():
			typ = signature.Params().At(*rule.Argument).Type()
		case rule.Receiver && signature.Recv() != nil:
			typ = signature.Recv().Type()
		}
		if typ == nil || !lifecycleOriginType(typ) {
			continue
		}
		found := false
		for _, previous := range a.lifecycleTypes {
			found = found || types.Identical(previous, typ)
		}
		if !found {
			a.lifecycleTypes = append(a.lifecycleTypes, typ)
		}
	}
}

func (a *flowAnalysis) lifecycleSensitiveType(typ types.Type, seen map[types.Type]bool) bool {
	if len(a.lifecycleTypes) == 0 || seen[typ] {
		return false
	}
	seen[typ] = true
	for _, handle := range a.lifecycleTypes {
		if types.Identical(handle, typ) {
			return true
		}
	}
	switch shape := typ.Underlying().(type) {
	case *types.Pointer:
		return a.lifecycleSensitiveType(shape.Elem(), seen)
	case *types.Array:
		return shape.Len() > 0 && a.lifecycleSensitiveType(shape.Elem(), seen)
	case *types.Slice:
		return a.lifecycleSensitiveType(shape.Elem(), seen)
	case *types.Map:
		return a.lifecycleSensitiveType(shape.Key(), seen) || a.lifecycleSensitiveType(shape.Elem(), seen)
	case *types.Struct:
		for i := 0; i < shape.NumFields(); i++ {
			field := shape.Field(i)
			if (field.Exported() || embeddedRecord(field)) && a.lifecycleSensitiveType(field.Type(), seen) {
				return true
			}
		}
	}
	return false
}
