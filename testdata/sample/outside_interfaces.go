package sample

import "database/sql"

type AnyRecord struct{ Value any }

func PublicAnyInput(value any) { record := value.(APIInputRecord); record.Run(record.Database) }
func PublicAnyField(input AnyRecord) {
	record, _ := input.Value.(APIInputRecord)
	record.Run(record.Database)
}
func PublicAnyFactory(factory func() any) {
	record := factory().(APIInputRecord)
	record.Run(record.Database)
}
func PublicAnyCallback() func(any) { return escapedAnyCallback }
func escapedAnyCallback(value any) { record := value.(APIInputRecord); record.Run(record.Database) }

var AnyGlobal any = APIInputRecord{Run: globalAnyQuery, Database: knownAggregateDatabase()}

func useAnyGlobal()            { record := AnyGlobal.(APIInputRecord); record.Run(record.Database) }
func closedAnyInput(value any) { record := value.(APIInputRecord); record.Run(record.Database) }
func localAnyFactory() any {
	return APIInputRecord{Run: factoryAnyQuery, Database: knownAggregateDatabase()}
}

type AnyRecordProvider interface{ Record() APIInputRecord }
type localAnyProvider struct{}

func (localAnyProvider) Record() APIInputRecord {
	return APIInputRecord{Run: providerAnyQuery, Database: knownAggregateDatabase()}
}
func PublicAnyProvider(provider AnyRecordProvider) {
	record := provider.Record()
	record.Run(record.Database)
}
func PublicAnyFunction(value any) { callback := value.(func()); callback() }
func anyFunctionTarget()          {}
func OutsideInterfaceCaller() {
	db := knownAggregateDatabase()
	PublicAnyInput(APIInputRecord{Run: inputAnyQuery, Database: db})
	PublicAnyField(AnyRecord{Value: APIInputRecord{Run: fieldAnyQuery, Database: db}})
	PublicAnyFactory(localAnyFactory)
	PublicAnyCallback()(APIInputRecord{Run: callbackAnyQuery, Database: db})
	useAnyGlobal()
	closedAnyInput(APIInputRecord{Run: closedAnyQuery, Database: db})
	PublicAnyProvider(localAnyProvider{})
	PublicAnyFunction(anyFunctionTarget)
}
func inputAnyQuery(db *sql.DB)    { db.Query("SELECT body FROM input_any_records") }
func fieldAnyQuery(db *sql.DB)    { db.Query("SELECT body FROM field_any_records") }
func factoryAnyQuery(db *sql.DB)  { db.Query("SELECT body FROM factory_any_records") }
func callbackAnyQuery(db *sql.DB) { db.Query("SELECT body FROM callback_any_records") }
func globalAnyQuery(db *sql.DB)   { db.Query("SELECT body FROM global_any_records") }
func closedAnyQuery(db *sql.DB)   { db.Query("SELECT body FROM closed_any_records") }
func providerAnyQuery(db *sql.DB) { db.Query("SELECT body FROM provider_any_records") }
