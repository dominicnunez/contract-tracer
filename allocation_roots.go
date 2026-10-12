package contracttrace

import "strings"

// longestAllocationRoot finds the deepest allocation ID that is either the
// complete address or a field-path prefix. Looking up each peeled prefix keeps
// lookup work proportional to path depth rather than the number of allocations.
// When allowEmpty is false, the empty map key is deliberately ignored; callers
// that historically accepted an empty allocation ID pass true.
func longestAllocationRoot[V any](address string, roots map[string]V, allowEmpty bool) (string, bool) {
	if _, exists := roots[address]; exists && (allowEmpty || address != "") {
		return address, true
	}
	for end := len(address); end > 0; {
		marker := strings.LastIndex(address[:end], ".field:")
		if marker < 0 {
			break
		}
		candidate := address[:marker]
		if candidate != "" || allowEmpty {
			if _, exists := roots[candidate]; exists {
				return candidate, true
			}
		}
		end = marker
	}
	return "", false
}
