package contracttrace

import (
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Recognize wrapping verbs without treating escaped percent signs or a later
// literal 'w' as directives. Argument pairing is deliberately not inferred.
func wrappingFormat(format string) (wrap, unresolved bool) {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i == len(format) {
			return wrap, true
		}
		if format[i] == '%' {
			continue
		}
		for i < len(format) {
			c := format[i]
			if strings.ContainsRune("+-# 0.*", rune(c)) || c >= '1' && c <= '9' {
				i++
				continue
			}
			if c == '[' {
				i++
				start := i
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					i++
				}
				if i == start || i == len(format) || format[i] != ']' {
					return wrap, true
				}
				i++
				continue
			}
			break
		}
		if i == len(format) {
			return wrap, true
		}
		wrap = wrap || format[i] == 'w'
	}
	return wrap, false
}

func errorWrapperName(target *ssa.Function) string {
	if target == nil || target.Object() == nil || target.Object().Pkg() == nil || target.Signature.Recv() != nil {
		return ""
	}
	name := target.Object().Pkg().Path() + "." + target.Object().Name()
	if name == "errors.Join" || name == "fmt.Errorf" {
		return name
	}
	return ""
}

func (a *flowAnalysis) errorWrapCandidates(call *ssa.Call) (flowValue, bool) {
	var result flowValue
	unresolved := false
	common := call.Common()
	if common.IsInvoke() {
		return result, false
	}
	if target := common.StaticCallee(); target != nil && errorWrapperName(target) == "" {
		return result, false
	}
	for _, target := range sortedFunctions(a.targets(common)) {
		name := errorWrapperName(target)
		var elements flowValue
		switch name {
		case "errors.Join":
			if len(common.Args) != 1 {
				continue
			}
			elements = a.sequenceElements(common.Args[0])
		case "fmt.Errorf":
			if len(common.Args) != 2 {
				continue
			}
			formatValue := a.get(common.Args[0])
			formats := formatValue.strings
			arguments, paired := literalFormatArguments(call)
			if paired && len(formats) != 0 && !formatValue.stringUnknown {
				for _, format := range sortedKeys(formats) {
					positions, unknown := wrappingArguments(format, len(arguments))
					if unknown {
						unresolved = true
						a.merge(&elements, a.sequenceElements(common.Args[1]))
						continue
					}
					for _, position := range positions {
						a.merge(&elements, a.get(arguments[position]))
					}
				}
				break
			}
			mayWrap := len(formats) == 0 || formatValue.stringUnknown
			unresolved = unresolved || len(formats) == 0 || formatValue.stringUnknown
			for _, format := range sortedKeys(formats) {
				wrap, unknown := wrappingFormat(format)
				mayWrap = mayWrap || wrap || unknown
				unresolved = unresolved || unknown
			}
			if !mayWrap {
				continue
			}
			unresolved = true
			elements = a.sequenceElements(common.Args[1])
		default:
			continue
		}
		origins := emptyFlow()
		origins.errors = elements.errors
		a.merge(&result, origins)
	}
	return result, unresolved
}

func (a *flowAnalysis) errorWrapUse(call *ssa.Call, ix *index) {
	value, unresolved := a.errorWrapCandidates(call)
	for _, origin := range sortedKeys(value.errors) {
		ix.edges = append(ix.edges, Relationship{From: errorResultID(call, 0, ix), To: origin, Kind: "error_wrap_cause", Certainty: "possible", Evidence: ix.callEvidence(call)})
	}
	if unresolved {
		ix.boundaries = append(ix.boundaries, Boundary{Node: ix.owner(call.Parent()), Kind: "error_wrap_format", Reason: "fmt.Errorf format or argument positions are unavailable or not fully recognized; candidate error arguments may be wrapped, and formatting-only behavior remains possible", Evidence: ix.callEvidence(call)})
	}
}
