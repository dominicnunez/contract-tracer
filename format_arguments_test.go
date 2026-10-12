package contracttrace

import (
	"errors"
	"fmt"
	"testing"
)

// The runtime's Unwrap behavior is the oracle, independent of our parser.
func TestWrappingArgumentsMatchRuntime(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	cases := []struct {
		format string
		args   []any
	}{
		{"%v %w", []any{first, second}},
		{"%[2]w %[1]v", []any{first, second}},
		{"%w %w", []any{first, second}},
		{"%[2]w %[2]w", []any{first, second}},
		{"%%w %v", []any{first}},
		{"%*w %v", []any{8, first, second}},
		{"%.*w %v", []any{3, first, second}},
		{"%[3]*.[2]*[1]w %[4]v", []any{first, 3, 8, second}},
		{"%[2]v %w", []any{first, second, first}},
		{"%+10.3w %v", []any{first, second}},
		{"%*% %w", []any{8, first}},
		{"%.[2]*[1]w", []any{first, 3}},
		{"%v", []any{first}},
		{"literal", nil},
	}
	for _, test := range cases {
		t.Run(test.format, func(t *testing.T) {
			positions, unresolved := wrappingArguments(test.format, len(test.args))
			if unresolved {
				t.Fatal("valid format was unresolved")
			}
			wrapped := fmt.Errorf(test.format, test.args...)
			for _, candidate := range []error{first, second} {
				selected := false
				for _, position := range positions {
					selected = selected || test.args[position] == candidate
				}
				if selected != errors.Is(wrapped, candidate) {
					t.Errorf("%v: selected=%v, runtime=%v", candidate, selected, errors.Is(wrapped, candidate))
				}
			}
		})
	}
}
