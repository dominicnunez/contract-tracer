package sample

import "database/sql"

type APIInputRecord struct {
	Run      func(*sql.DB)
	Database *sql.DB
	Next     *APIInputRecord
}

func PublicValueRecord(record APIInputRecord)         { consumeValueRecord(record) }
func consumeValueRecord(record APIInputRecord)        { record.Run(record.Database) }
func PublicPointerRecordInput(record *APIInputRecord) { record.Next.Run(record.Next.Database) }
func PublicSliceInput(records []APIInputRecord)       { records[0].Run(records[0].Database) }
func PublicMapInput(records map[string]APIInputRecord) {
	record := records["local"]
	record.Run(record.Database)
}
func closedRecordInput(record APIInputRecord) { record.Run(record.Database) }

func AggregateInputCaller() {
	db := knownAggregateDatabase()
	record := APIInputRecord{Run: aggregateInputQuery, Database: db}
	record.Next = &record
	PublicValueRecord(record)
	PublicPointerRecordInput(&record)
	PublicSliceInput([]APIInputRecord{record})
	PublicMapInput(map[string]APIInputRecord{"local": record})
	closedRecordInput(APIInputRecord{Run: closedAggregateQuery, Database: db})
}

func knownAggregateDatabase() *sql.DB { db, _ := sql.Open("sqlite", "aggregate.db"); return db }
func aggregateInputQuery(db *sql.DB)  { db.Query("SELECT body FROM aggregate_input_records") }
func closedAggregateQuery(db *sql.DB) { db.Query("SELECT body FROM closed_aggregate_input_records") }

type APIRecordPair struct {
	First, Second   APIInputRecord
	FunctionPointer *func(*sql.DB)
}

func PublicRecordPair(pair APIRecordPair) {
	pair.First.Run(pair.First.Database)
	pair.Second.Run(pair.Second.Database)
	(*pair.FunctionPointer)(pair.Second.Database)
}
func AggregatePairCaller() {
	callback := aggregatePairQuery
	record := APIInputRecord{Run: callback, Database: knownAggregateDatabase()}
	PublicRecordPair(APIRecordPair{First: record, Second: record, FunctionPointer: &callback})
}
func aggregatePairQuery(db *sql.DB) { db.Query("SELECT body FROM aggregate_pair_records") }

type RecursiveAPIRecord struct {
	Run      func()
	Children []RecursiveAPIRecord
}

func PublicRecursiveContainer(record RecursiveAPIRecord) { record.Children[0].Run() }
func RecursiveContainerCaller() {
	PublicRecursiveContainer(RecursiveAPIRecord{Children: []RecursiveAPIRecord{{Run: recursiveInputCallback}}})
}
func recursiveInputCallback() {}
