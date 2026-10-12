package sample

import (
	"database/sql"
	"iter"
	"maps"
	"slices"
)

func localCallbackSequence() iter.Seq[func(*sql.DB)] {
	return func(yield func(func(*sql.DB)) bool) { yield(functionIterationQuery) }
}

func FunctionIterationCaller() {
	db := OpenOrdersDatabase()
	for callback := range localCallbackSequence() {
		callback(db)
	}
}

func SliceFunctionIterationCaller() {
	db := OpenOrdersDatabase()
	for callback := range slices.Values([]func(*sql.DB){sliceFunctionIterationQuery}) {
		callback(db)
	}
}

func functionIterationQuery(db *sql.DB) { db.Query("SELECT body FROM function_iteration_records") }
func sliceFunctionIterationQuery(db *sql.DB) {
	db.Query("SELECT body FROM slice_function_iteration_records")
}

func PublicSliceIterator() iter.Seq[func(*sql.DB)] {
	return slices.Values([]func(*sql.DB){publicIteratorQuery})
}

func PublicIteratorCaller() {
	for callback := range PublicSliceIterator() {
		callback(OpenOrdersDatabase())
	}
}

func publicIteratorQuery(db *sql.DB) { db.Query("SELECT body FROM public_iterator_records") }

func SliceAllIterationCaller() {
	for _, callback := range slices.All([]func(*sql.DB){sliceAllIterationQuery}) {
		callback(OpenOrdersDatabase())
	}
}
func SliceBackwardIterationCaller() {
	for _, callback := range slices.Backward([]func(*sql.DB){sliceBackwardIterationQuery}) {
		callback(OpenOrdersDatabase())
	}
}
func MapValuesIterationCaller() {
	for callback := range maps.Values(map[string]func(*sql.DB){"callback": mapValuesIterationQuery}) {
		callback(OpenOrdersDatabase())
	}
}
func MapKeysIterationCaller() {
	for record := range maps.Keys(map[*iterationRecord]string{&iterationRecord{Callback: mapKeysIterationQuery}: "payload"}) {
		record.Callback(OpenOrdersDatabase())
	}
}
func MapAllIterationCaller() {
	for _, callback := range maps.All(map[string]func(*sql.DB){"callback": mapAllIterationQuery}) {
		callback(OpenOrdersDatabase())
	}
}
func sliceAllIterationQuery(db *sql.DB) { db.Query("SELECT body FROM slice_all_iteration_records") }
func sliceBackwardIterationQuery(db *sql.DB) {
	db.Query("SELECT body FROM slice_backward_iteration_records")
}
func mapValuesIterationQuery(db *sql.DB) { db.Query("SELECT body FROM map_values_iteration_records") }
func mapKeysIterationQuery(db *sql.DB)   { db.Query("SELECT body FROM map_keys_iteration_records") }
func mapAllIterationQuery(db *sql.DB)    { db.Query("SELECT body FROM map_all_iteration_records") }

func PublicIteratorRecord() struct{ Sequence iter.Seq[func(*sql.DB)] } {
	return struct{ Sequence iter.Seq[func(*sql.DB)] }{Sequence: slices.Values([]func(*sql.DB){recordIteratorQuery})}
}
func RecordIteratorCaller() {
	for callback := range PublicIteratorRecord().Sequence {
		callback(OpenOrdersDatabase())
	}
}
func recordIteratorQuery(db *sql.DB) { db.Query("SELECT body FROM record_iterator_records") }
