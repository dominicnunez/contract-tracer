package contracttrace

import (
	"go/constant"
	"go/types"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/ssa"
)

// Follow fmt's argument counter, including index resets and star operands.
// Malformed/missing operands fall back to conservative candidates.
func wrappingArguments(format string, count int) ([]int, bool) {
	next := 0
	var selected []int
	for i := 0; i < len(format); {
		if format[i] != '%' {
			i++
			continue
		}
		i++
		for i < len(format) && strings.ContainsRune("#0+- ", rune(format[i])) {
			i++
		}
		index := func() (bool, bool) {
			if i == len(format) || format[i] != '[' {
				return false, true
			}
			i++
			start := i
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
			if start == i || i == len(format) || format[i] != ']' {
				return true, false
			}
			number, err := strconv.Atoi(format[start:i])
			i++
			if err != nil || number < 1 || number > count {
				return true, false
			}
			next = number - 1
			return true, true
		}
		afterIndex, ok := index()
		if !ok {
			return nil, true
		}
		if i < len(format) && format[i] == '*' {
			if next >= count {
				return nil, true
			}
			next++
			i++
			afterIndex = false
		} else {
			start := i
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
			if afterIndex && i > start {
				return nil, true
			}
		}
		if i < len(format) && format[i] == '.' {
			if afterIndex {
				return nil, true
			}
			i++
			afterIndex, ok = index()
			if !ok {
				return nil, true
			}
			if i < len(format) && format[i] == '*' {
				if next >= count {
					return nil, true
				}
				next++
				i++
				afterIndex = false
			} else {
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					i++
				}
			}
		}
		if !afterIndex {
			_, ok = index()
			if !ok {
				return nil, true
			}
		}
		if i == len(format) {
			return nil, true
		}
		verb, size := utf8.DecodeRuneInString(format[i:])
		i += size
		if verb == '%' {
			continue
		}
		if next >= count {
			return nil, true
		}
		if verb == 'w' {
			selected = append(selected, next)
		}
		next++
	}
	return selected, false
}

// Only recover positions for a closed literal backing array with one slice
// passed to this call, constant indices and at most one write per element.
// Other aliases/writes keep the unordered slice model instead of guessed slots.
func literalFormatArguments(call *ssa.Call) ([]ssa.Value, bool) {
	value := call.Common().Args[1]
	if nilValue, ok := value.(*ssa.Const); ok && nilValue.IsNil() {
		return nil, true
	}
	slice, ok := value.(*ssa.Slice)
	if !ok || slice.Low != nil || slice.High != nil || slice.Max != nil {
		return nil, false
	}
	array, ok := slice.X.(*ssa.Alloc)
	if !ok {
		return nil, false
	}
	pointer, ok := array.Type().Underlying().(*types.Pointer)
	if !ok {
		return nil, false
	}
	typ, ok := pointer.Elem().Underlying().(*types.Array)
	if !ok || typ.Len() > 4096 {
		return nil, false
	}
	values := make([]ssa.Value, typ.Len())
	written := make([]bool, typ.Len())
	// Scan the call block once rather than once for every literal operand.
	beforeCall := make(map[ssa.Instruction]bool)
	for _, instruction := range call.Block().Instrs {
		if instruction == call {
			break
		}
		beforeCall[instruction] = true
	}
	if refs := slice.Referrers(); refs != nil {
		for _, ref := range *refs {
			if _, debug := ref.(*ssa.DebugRef); !debug && ref != call {
				return nil, false
			}
		}
	}
	if refs := array.Referrers(); refs != nil {
		for _, ref := range *refs {
			if _, debug := ref.(*ssa.DebugRef); debug || ref == slice {
				continue
			}
			address, ok := ref.(*ssa.IndexAddr)
			if !ok {
				return nil, false
			}
			position, ok := address.Index.(*ssa.Const)
			if !ok || position.Value == nil || position.Value.Kind() != constant.Int {
				return nil, false
			}
			n, ok := constant.Int64Val(position.Value)
			if !ok || n < 0 || n >= typ.Len() {
				return nil, false
			}
			if uses := address.Referrers(); uses != nil {
				for _, use := range *uses {
					if _, debug := use.(*ssa.DebugRef); debug {
						continue
					}
					store, ok := use.(*ssa.Store)
					if !ok || store.Addr != address || written[n] {
						return nil, false
					}
					if store.Block() != call.Block() {
						if !store.Block().Dominates(call.Block()) {
							return nil, false
						}
					} else if !beforeCall[store] {
						return nil, false
					}
					written[n] = true
					values[n] = store.Val
				}
			}
		}
	}
	return values, true
}
