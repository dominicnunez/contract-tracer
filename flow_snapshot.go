package contracttrace

import "golang.org/x/tools/go/ssa"

// Stored candidates have already passed bounded merging. A read still owns
// independent maps because callers may extend them before storing elsewhere.
// Avoid temporary key slices and repeated growth when making that snapshot.
func snapshotCandidates[K comparable](source map[K]bool) map[K]bool {
	result := make(map[K]bool, len(source))
	for key := range source {
		result[key] = true
	}
	return result
}

func snapshotFlow(source flowValue) flowValue {
	result := source
	result.strings = snapshotCandidates(source.strings)
	result.functions = snapshotCandidates(source.functions)
	result.boundReceiverUnknown = source.boundReceiverUnknown
	result.boundReceivers = nil
	if source.boundReceivers != nil {
		result.boundReceivers = make(map[*ssa.Function]boundReceiverCandidates, len(source.boundReceivers))
		for fn, receivers := range source.boundReceivers {
			result.boundReceivers[fn] = boundReceiverCandidates{addresses: snapshotCandidates(receivers.addresses), typ: receivers.typ, unknown: receivers.unknown}
		}
	}
	result.addresses = snapshotCandidates(source.addresses)
	if len(source.zeroAggregates) != 0 {
		result.zeroAggregates = snapshotCandidates(source.zeroAggregates)
	} else {
		result.zeroAggregates = nil
	}
	result.effects = snapshotCandidates(source.effects)
	if len(source.errors) != 0 {
		result.errors = snapshotCandidates(source.errors)
	} else {
		result.errors = nil
	}
	if len(source.scalars) != 0 {
		result.scalars = snapshotCandidates(source.scalars)
	} else {
		result.scalars = nil
	}
	return result
}
