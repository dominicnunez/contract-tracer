package sample

import "database/sql"

func PublicAggregateCallback() func(APIInputRecord)   { return returnedAggregateCallback }
func returnedAggregateCallback(record APIInputRecord) { record.Run(record.Database) }

var AggregateCallback = globalAggregateCallback

func globalAggregateCallback(record APIInputRecord) { record.Run(record.Database) }
func DispatchAggregateCallback(dispatch func(func(APIInputRecord))) {
	dispatch(dependencyAggregateCallback)
}
func localAggregateDispatch(callback func(APIInputRecord)) {
	callback(APIInputRecord{Run: dependencyAggregateQuery, Database: knownAggregateDatabase()})
}
func dependencyAggregateCallback(record APIInputRecord) { record.Run(record.Database) }
func privateAggregateCallback() func(APIInputRecord)    { return closedAggregateCallback }
func closedAggregateCallback(record APIInputRecord)     { record.Run(record.Database) }

func EscapedAggregateInputCaller() {
	db := knownAggregateDatabase()
	PublicAggregateCallback()(APIInputRecord{Run: returnedAggregateQuery, Database: db})
	AggregateCallback(APIInputRecord{Run: globalAggregateQuery, Database: db})
	DispatchAggregateCallback(localAggregateDispatch)
	privateAggregateCallback()(APIInputRecord{Run: privateAggregateQuery, Database: db})
}
func returnedAggregateQuery(db *sql.DB)   { db.Query("SELECT body FROM returned_aggregate_records") }
func globalAggregateQuery(db *sql.DB)     { db.Query("SELECT body FROM global_aggregate_records") }
func dependencyAggregateQuery(db *sql.DB) { db.Query("SELECT body FROM dependency_aggregate_records") }
func privateAggregateQuery(db *sql.DB)    { db.Query("SELECT body FROM private_aggregate_records") }
