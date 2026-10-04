package sample

import "database/sql"

type PointerInputRecord struct {
	Record   **APIInputRecord
	Callback **func(*sql.DB)
	Database ***sql.DB
}

func PublicPointerChain(record **APIInputRecord) {
	(*record).Run((*record).Database)
}
func PublicPointerFields(record PointerInputRecord) {
	(*record.Record).Run((*record.Record).Database)
	(**record.Callback)(**record.Database)
}
func OpenPointerChainResult(factory func() **APIInputRecord) {
	record := factory()
	(*record).Run((*record).Database)
}
func closedPointerChain(record **APIInputRecord) {
	(*record).Run((*record).Database)
}
func localPointerChainResult() **APIInputRecord {
	record := &APIInputRecord{Run: pointerChainResultQuery, Database: knownAggregateDatabase()}
	return &record
}
func PointerChainCaller() {
	db := knownAggregateDatabase()
	dbPointer := &db
	callback := pointerFieldQuery
	callbackPointer := &callback
	record := &APIInputRecord{Run: pointerChainInputQuery, Database: db}
	PublicPointerChain(&record)
	PublicPointerFields(PointerInputRecord{Record: &record, Callback: &callbackPointer, Database: &dbPointer})
	OpenPointerChainResult(localPointerChainResult)
	closed := &APIInputRecord{Run: closedPointerChainQuery, Database: db}
	closedPointerChain(&closed)
}
func pointerChainInputQuery(db *sql.DB)  { db.Query("SELECT body FROM pointer_chain_input_records") }
func pointerFieldQuery(db *sql.DB)       { db.Query("SELECT body FROM pointer_field_records") }
func pointerChainResultQuery(db *sql.DB) { db.Query("SELECT body FROM pointer_chain_result_records") }
func closedPointerChainQuery(db *sql.DB) { db.Query("SELECT body FROM closed_pointer_chain_records") }
