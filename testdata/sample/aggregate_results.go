package sample

import "database/sql"

func OpenAggregateResult(factory func() APIInputRecord) {
	record := factory()
	record.Run(record.Database)
}
func OpenPointerAggregateResult(factory func() *APIInputRecord) {
	record := factory()
	record.Run(record.Database)
}
func OpenTupleAggregateResult(factory func() (APIInputRecord, error)) {
	record, _ := factory()
	record.Run(record.Database)
}
func OpenSliceAggregateResult(factory func() []APIInputRecord) {
	records := factory()
	records[0].Run(records[0].Database)
}
func OpenMapAggregateResult(factory func() map[string]APIInputRecord) {
	record := factory()["local"]
	record.Run(record.Database)
}
func closedAggregateResult(factory func() APIInputRecord) {
	record := factory()
	record.Run(record.Database)
}
func AggregateResultCaller() {
	OpenArrayAggregateResult(localArrayResult)
	OpenAggregateResult(localRecordResult)
	OpenPointerAggregateResult(localPointerResult)
	OpenTupleAggregateResult(localTupleResult)
	OpenSliceAggregateResult(localSliceResult)
	OpenMapAggregateResult(localMapResult)
	closedAggregateResult(localClosedRecordResult)
}
func localRecordResult() APIInputRecord {
	return APIInputRecord{Run: recordResultQuery, Database: knownAggregateDatabase()}
}
func localPointerResult() *APIInputRecord {
	return &APIInputRecord{Run: pointerResultQuery, Database: knownAggregateDatabase()}
}
func localTupleResult() (APIInputRecord, error) {
	return APIInputRecord{Run: tupleResultQuery, Database: knownAggregateDatabase()}, nil
}
func localSliceResult() []APIInputRecord {
	return []APIInputRecord{{Run: sliceResultQuery, Database: knownAggregateDatabase()}}
}
func localMapResult() map[string]APIInputRecord {
	return map[string]APIInputRecord{"local": {Run: mapResultQuery, Database: knownAggregateDatabase()}}
}
func localClosedRecordResult() APIInputRecord {
	return APIInputRecord{Run: closedResultQuery, Database: knownAggregateDatabase()}
}
func recordResultQuery(db *sql.DB)  { db.Query("SELECT body FROM record_result_records") }
func pointerResultQuery(db *sql.DB) { db.Query("SELECT body FROM pointer_result_records") }
func tupleResultQuery(db *sql.DB)   { db.Query("SELECT body FROM tuple_result_records") }
func sliceResultQuery(db *sql.DB)   { db.Query("SELECT body FROM slice_result_records") }
func mapResultQuery(db *sql.DB)     { db.Query("SELECT body FROM map_result_records") }
func closedResultQuery(db *sql.DB)  { db.Query("SELECT body FROM closed_result_records") }

func OpenArrayAggregateResult(factory func() [1]APIInputRecord) {
	records := factory()
	records[0].Run(records[0].Database)
}
func unresolvedRecordResult(factory func() APIInputRecord) {
	record := factory()
	record.Run(record.Database)
	record.Database.Query("SELECT body FROM unresolved_result_records")
}
func localArrayResult() [1]APIInputRecord {
	return [1]APIInputRecord{{Run: arrayResultQuery, Database: knownAggregateDatabase()}}
}
func arrayResultQuery(db *sql.DB) { db.Query("SELECT body FROM array_result_records") }
