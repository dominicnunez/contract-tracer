package sample

import "database/sql"

func UnknownMapIteration(callbacks map[string]func()) {
	for _, callback := range callbacks {
		callback()
	}
}

var PublicIterationDatabases = map[string]*sql.DB{"local": OpenOrdersDatabase()}

func PublicMapIterationCaller() {
	for _, db := range PublicIterationDatabases {
		publicIterationDatabaseQuery(db)
	}
}

func publicIterationDatabaseQuery(db *sql.DB) {
	db.Query("SELECT body FROM iteration_public_database_records")
}

type iterationRecord struct {
	Callback func(*sql.DB)
	Database *sql.DB
}

func MapIterationCaller() {
	db := OpenOrdersDatabase()
	callbacks := map[string]func(*sql.DB){"callback": iterationValueQuery}
	for _, callback := range callbacks {
		callback(db)
	}
	keys := map[*iterationRecord]string{&iterationRecord{Callback: iterationKeyQuery}: "payload"}
	for record := range keys {
		record.Callback(db)
	}
	records := map[string]iterationRecord{"record": {Callback: iterationRecordQuery, Database: db}}
	for _, record := range records {
		record.Callback(record.Database)
	}
	queries := map[string]string{"SELECT body FROM iteration_key_records": "SELECT body FROM iteration_value_records"}
	for key, value := range queries {
		consumeIterationKey(db, key)
		consumeIterationValue(db, value)
	}
}

func iterationValueQuery(db *sql.DB)                 { db.Query("SELECT body FROM iteration_callback_records") }
func iterationKeyQuery(db *sql.DB)                   { db.Query("SELECT body FROM iteration_key_callback_records") }
func iterationRecordQuery(db *sql.DB)                { db.Query("SELECT body FROM iteration_record_callback_records") }
func consumeIterationKey(db *sql.DB, query string)   { db.Query(query) }
func consumeIterationValue(db *sql.DB, query string) { db.Query(query) }
