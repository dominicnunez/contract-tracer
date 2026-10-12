package sample

import (
	"context"
	"database/sql"
	"reflect"
)

func DatabasePaths() {
	first, _ := sql.Open("sqlite", "first.db")
	second, _ := sql.Open("sqlite", "second.db")
	defer first.Close()
	defer second.Close()
	SharedDatabaseQuery(first)
	SharedDatabaseQuery(second)
	connection, _ := first.Conn(context.Background())
	defer connection.Close()
	transaction, _ := connection.BeginTx(context.Background(), nil)
	defer transaction.Rollback()
	statement, _ := transaction.Prepare("SELECT body FROM connection_records")
	statement.Query()
}

func SharedDatabaseQuery(db *sql.DB) {
	db.Exec("INSERT INTO shared_records (body) VALUES ('value')")
}

func UnknownDatabaseQuery(db *sql.DB) {
	db.Query("SELECT body FROM external_records")
}

func ConnectorDatabase() {
	db := sql.OpenDB(nil)
	db.Query("SELECT body FROM connector_records")
}

func OpenOrdersDatabase() *sql.DB {
	db, _ := sql.Open("sqlite", "orders.db")
	return db
}

func OpenAuditDatabase() *sql.DB {
	db, _ := sql.Open("sqlite", "audit.db")
	return db
}

func NamespacedDatabasePaths() {
	orders := OpenOrdersDatabase()
	audit := OpenAuditDatabase()
	SharedDatabaseQuery(orders)
	SharedDatabaseQuery(audit)
	transaction, _ := orders.Begin()
	statement, _ := transaction.Prepare("SELECT body FROM scoped_records")
	statement.Query()
}

func MixedDatabaseInput(external *sql.DB) {
	known := OpenOrdersDatabase()
	mixedDatabaseQuery(known)
	mixedDatabaseQuery(external)
}

func mixedDatabaseQuery(db *sql.DB) {
	defer db.Close()
	db.Query("SELECT body FROM mixed_receiver_records")
	statement, _ := db.Prepare("SELECT body FROM mixed_statement_records")
	statement.Query()
}

func MixedStatementInput(external *sql.Stmt) {
	known, _ := OpenOrdersDatabase().Prepare("SELECT body FROM mixed_rebind_records")
	transaction, _ := OpenOrdersDatabase().Begin()
	mixedStatementRebind(transaction, known)
	mixedStatementRebind(transaction, external)
}

func mixedStatementRebind(transaction *sql.Tx, statement *sql.Stmt) {
	rebound := transaction.Stmt(statement)
	defer rebound.Close()
	rebound.Query()
}

func OpaqueStatementInput() {
	db := OpenOrdersDatabase()
	known, _ := db.Prepare("SELECT body FROM unknown_candidate_records")
	prepare := db.Prepare // Bound database methods remain an unsupported origin.
	opaque, _ := prepare("SELECT body FROM opaque_records")
	opaqueStatementQuery(known)
	opaqueStatementQuery(opaque)
}

func opaqueStatementQuery(statement *sql.Stmt) { statement.Query() }

func UnknownStatementInput() {
	known, _ := OpenOrdersDatabase().Prepare("SELECT body FROM unknown_candidate_records")
	unknownStatementQuery(known)
	unknownStatementQuery(nil)
}

func unknownStatementQuery(statement *sql.Stmt) { statement.Query() }

func NilStatementInput(chooseNil bool) {
	statement, _ := OpenOrdersDatabase().Prepare("SELECT body FROM unknown_candidate_records")
	if chooseNil {
		statement = nil
	}
	statement.Query()
}

func PublicDatabaseCaller() { PublicDatabaseQuery(OpenOrdersDatabase()) }

func PublicDatabaseQuery(db *sql.DB) {
	db.Query("SELECT body FROM public_input_records")
}

func PrivateDatabaseCaller() { privateDatabaseQuery(OpenOrdersDatabase()) }

func privateDatabaseQuery(db *sql.DB) {
	db.Query("SELECT body FROM private_input_records")
}

func EscapingSQLCaller(external *sql.DB) {
	escapingSQLQuery(OpenOrdersDatabase())
	callback := passSQLCallback(escapingSQLQuery)
	reflect.ValueOf(callback).Call([]reflect.Value{reflect.ValueOf(external)})
}

func passSQLCallback(callback func(*sql.DB)) func(*sql.DB) { return callback }

func escapingSQLQuery(db *sql.DB) {
	db.Query("SELECT body FROM escaping_input_records")
}

func GenericCallbackEscape() {
	privateTextConsumer("local")
	reflect.ValueOf(privateTextConsumer)
}

func privateTextConsumer(text string) { _ = text }

func ReturnedCallbackCaller() {
	privateReturnedQuery(OpenOrdersDatabase())
	callback := PublicCallbackFactory()
	callback(OpenOrdersDatabase())
}

func PublicCallbackFactory() func(*sql.DB) { return privateCallbackFactory() }

func privateCallbackFactory() func(*sql.DB) { return privateReturnedQuery }

func privateReturnedQuery(db *sql.DB) {
	db.Query("SELECT body FROM returned_callback_records")
}

func ClosedCallbackCaller() { closedCallbackFactory()(OpenOrdersDatabase()) }

func closedCallbackFactory() func(*sql.DB) { return closedCallbackQuery }

func closedCallbackQuery(db *sql.DB) {
	db.Query("SELECT body FROM closed_callback_records")
}

func ClosedDatabaseFactoryCaller() { closedDatabaseConsumer(closedDatabaseFactory()()) }

func closedDatabaseFactory() func() *sql.DB { return OpenOrdersDatabase }

func closedDatabaseConsumer(db *sql.DB) {
	db.Query("SELECT body FROM closed_factory_records")
}

type CallbackRecord struct {
	Nested *CallbackInner
	hidden func(*sql.DB)
}

type CallbackInner struct {
	Query func(*sql.DB)
	Next  *CallbackInner
}

func PublicRecordFactory() CallbackRecord { return privateRecordFactory() }

func privateRecordFactory() CallbackRecord {
	inner := &CallbackInner{Query: recordCallbackQuery}
	inner.Next = inner
	return CallbackRecord{Nested: inner, hidden: hiddenRecordQuery}
}

func RecordCallbackCaller() {
	record := PublicRecordFactory()
	record.Nested.Query(OpenOrdersDatabase())
	record.hidden(OpenOrdersDatabase())
}

func recordCallbackQuery(db *sql.DB) { db.Query("SELECT body FROM record_callback_records") }
func hiddenRecordQuery(db *sql.DB)   { db.Query("SELECT body FROM hidden_record_records") }

func RecordArgumentCaller() {
	record := &CallbackInner{Query: argumentRecordQuery}
	record.Query(OpenOrdersDatabase())
	privateRecordArgument(record)
}

func privateRecordArgument(record any) {
	_ = reflect.ValueOf(record)
}

func argumentRecordQuery(db *sql.DB) { db.Query("SELECT body FROM argument_record_records") }

type hiddenEmbeddedCallback struct {
	Query func(*sql.DB)
}

type PromotedCallbackRecord struct{ hiddenEmbeddedCallback }

func PublicPromotedRecordFactory() PromotedCallbackRecord {
	return PromotedCallbackRecord{hiddenEmbeddedCallback{Query: promotedRecordQuery}}
}

func PromotedRecordCaller() { PublicPromotedRecordFactory().Query(OpenOrdersDatabase()) }

func promotedRecordQuery(db *sql.DB) { db.Query("SELECT body FROM promoted_record_records") }

func PublicSliceCallbacks() []func(*sql.DB)  { return []func(*sql.DB){sliceCallbackQuery} }
func PublicArrayCallbacks() [1]func(*sql.DB) { return [1]func(*sql.DB){arrayCallbackQuery} }
func PublicMapCallbacks() map[string]func(*sql.DB) {
	return map[string]func(*sql.DB){"query": mapCallbackQuery}
}

func ContainerCallbackCaller() {
	db := OpenOrdersDatabase()
	sliceCallbackQuery(db)
	arrayCallbackQuery(db)
	mapCallbackQuery(db)
	PublicSliceCallbacks()[0](db)
	PublicArrayCallbacks()[0](db)
	PublicMapCallbacks()["query"](db)
}

func sliceCallbackQuery(db *sql.DB) { db.Query("SELECT body FROM slice_callback_records") }
func arrayCallbackQuery(db *sql.DB) { db.Query("SELECT body FROM array_callback_records") }
func mapCallbackQuery(db *sql.DB)   { db.Query("SELECT body FROM map_callback_records") }

func ContainerArgumentCaller() {
	slice := make([]func(*sql.DB), 1)
	slice[0] = madeSliceQuery
	slice[0](OpenOrdersDatabase())
	privateRecordArgument(slice)
	keys := map[*CallbackInner]bool{&CallbackInner{Query: mapKeyQuery}: true}
	mapKeyQuery(OpenOrdersDatabase())
	privateRecordArgument(keys)
}

func madeSliceQuery(db *sql.DB) { db.Query("SELECT body FROM made_slice_records") }
func mapKeyQuery(db *sql.DB)    { db.Query("SELECT body FROM map_key_records") }

func ClosedArrayCaller()                   { closedArrayFactory()[0](OpenOrdersDatabase()) }
func closedArrayFactory() [1]func(*sql.DB) { return [1]func(*sql.DB){closedArrayQuery} }
func closedArrayQuery(db *sql.DB)          { db.Query("SELECT body FROM closed_array_records") }

func PublicAppendedCallbacks() []func(*sql.DB) {
	var callbacks []func(*sql.DB)
	return append(callbacks, appendedQuery)
}

func PublicCopiedCallbacks() []func(*sql.DB) {
	source := []func(*sql.DB){copiedQuery}
	destination := make([]func(*sql.DB), len(source))
	copy(destination, source)
	return destination
}

func SliceBuiltinCaller() {
	db := OpenOrdersDatabase()
	appendedQuery(db)
	copiedQuery(db)
	PublicAppendedCallbacks()[0](db)
	PublicCopiedCallbacks()[0](db)
}

func appendedQuery(db *sql.DB) { db.Query("SELECT body FROM appended_records") }
func copiedQuery(db *sql.DB)   { db.Query("SELECT body FROM copied_records") }

func PublicChainedCallbacks() []func(*sql.DB) {
	base := []func(*sql.DB){chainFirstQuery}
	extra := []func(*sql.DB){chainSecondQuery}
	return append(base, extra...)
}

func PublicCopiedRecordCallbacks() []CallbackInner {
	source := []CallbackInner{{Query: copiedRecordQuery}}
	destination := make([]CallbackInner, len(source))
	copy(destination, source)
	return destination
}

func PublicDeferredCopyCallbacks() []func(*sql.DB) {
	source := []func(*sql.DB){deferredCopyQuery}
	destination := make([]func(*sql.DB), len(source))
	defer copy(destination, source)
	return destination
}

func PublicGoCopyCallbacks() []func(*sql.DB) {
	source := []func(*sql.DB){goCopyQuery}
	destination := make([]func(*sql.DB), len(source))
	go copy(destination, source)
	return destination
}

func SliceBuiltinSiblingCaller() {
	db := OpenOrdersDatabase()
	chainFirstQuery(db)
	chainSecondQuery(db)
	copiedRecordQuery(db)
	deferredCopyQuery(db)
	goCopyQuery(db)
	PublicChainedCallbacks()[0](db)
	PublicChainedCallbacks()[1](db)
	PublicCopiedRecordCallbacks()[0].Query(db)
	PublicDeferredCopyCallbacks()[0](db)
	PublicGoCopyCallbacks()[0](db)
}

func chainFirstQuery(db *sql.DB)   { db.Query("SELECT body FROM chain_first_records") }
func chainSecondQuery(db *sql.DB)  { db.Query("SELECT body FROM chain_second_records") }
func copiedRecordQuery(db *sql.DB) { db.Query("SELECT body FROM copied_record_records") }
func deferredCopyQuery(db *sql.DB) { db.Query("SELECT body FROM deferred_copy_records") }
func goCopyQuery(db *sql.DB)       { db.Query("SELECT body FROM go_copy_records") }

func ClosedSliceBuiltinCaller() { closedSliceBuiltinFactory()[0](OpenOrdersDatabase()) }

func closedSliceBuiltinFactory() []func(*sql.DB) {
	var empty []func(*sql.DB)
	source := append(empty, closedSliceBuiltinQuery)
	destination := make([]func(*sql.DB), len(source))
	copy(destination, source)
	return destination
}

func closedSliceBuiltinQuery(db *sql.DB) { db.Query("SELECT body FROM closed_slice_builtin_records") }

var PublicInstalledCallback func(*sql.DB)
var PublicLocationCallbacks = []func(string){
	privateTextConsumer, // location seed inside global initializer
}
var PublicInstalledRecord CallbackInner
var PublicPointerRecord *CallbackInner
var privateInstalledCallback func(*sql.DB)

var privateLocationState = struct{ value string }{
	value: "initial", // private global location seed
}
var privateUnrelatedState string

func writePrivateLocationState(value string) {
	setPrivateLocationState(&privateLocationState.value, value)
}
func setPrivateLocationState(target *string, value string) { *target = value }
func readPrivateLocationState() string                     { return privateLocationState.value }
func siblingPrivateLocationState() string                  { return privateLocationState.value }
func writeUnrelatedPrivateState(value string)              { privateUnrelatedState = value }

func installGlobalCallbacks() {
	PublicInstalledCallback = globalDirectQuery
	PublicInstalledRecord.Query = globalFieldQuery
	privateInstalledCallback = privateGlobalQuery
	PublicPointerRecord = &CallbackInner{}
	setGlobalPointer(PublicPointerRecord)
}

func setGlobalPointer(record *CallbackInner) { record.Query = globalPointerQuery }

func GlobalCallbackCaller() {
	installGlobalCallbacks()
	db := OpenOrdersDatabase()
	PublicInstalledCallback(db)
	PublicInstalledRecord.Query(db)
	privateInstalledCallback(db)
	PublicPointerRecord.Query(db)
}

func globalDirectQuery(db *sql.DB)  { db.Query("SELECT body FROM global_direct_records") }
func globalFieldQuery(db *sql.DB)   { db.Query("SELECT body FROM global_field_records") }
func privateGlobalQuery(db *sql.DB) { db.Query("SELECT body FROM private_global_records") }
func globalPointerQuery(db *sql.DB) { db.Query("SELECT body FROM global_pointer_records") }

var PublicGlobalDatabase *sql.DB
var PublicGlobalConnection *sql.Conn
var PublicGlobalTransaction *sql.Tx
var PublicGlobalStatement *sql.Stmt
var privateGlobalDatabase *sql.DB

type GlobalSQLRecord struct {
	Database *sql.DB
	hidden   *sql.DB
}

var PublicGlobalSQLRecord GlobalSQLRecord
var PublicGlobalSQLSlice []*sql.DB
var PublicGlobalSQLArray [1]*sql.DB
var PublicGlobalSQLMap map[string]*sql.DB

func publicGlobalSliceQuery(db *sql.DB) { db.Query("SELECT body FROM global_sql_slice_records") }
func publicGlobalArrayQuery(db *sql.DB) { db.Query("SELECT body FROM global_sql_array_records") }
func publicGlobalMapQuery(db *sql.DB)   { db.Query("SELECT body FROM global_sql_map_records") }

func GlobalDatabaseCaller() {
	db := OpenOrdersDatabase()
	PublicGlobalDatabase = db
	privateGlobalDatabase = db
	PublicGlobalConnection, _ = db.Conn(context.Background())
	PublicGlobalTransaction, _ = db.Begin()
	PublicGlobalStatement, _ = db.Prepare("SELECT body FROM global_statement_records")
	PublicGlobalSQLRecord = GlobalSQLRecord{Database: db, hidden: db}
	PublicGlobalSQLSlice = []*sql.DB{db}
	PublicGlobalSQLArray = [1]*sql.DB{db}
	PublicGlobalSQLMap = map[string]*sql.DB{"db": db}
	publicGlobalDatabaseQuery(PublicGlobalDatabase)
	privateGlobalDatabaseQuery(privateGlobalDatabase)
	publicGlobalRecordQuery(PublicGlobalSQLRecord.Database)
	privateGlobalRecordQuery(PublicGlobalSQLRecord.hidden)
	publicGlobalSliceQuery(PublicGlobalSQLSlice[0])
	publicGlobalArrayQuery(PublicGlobalSQLArray[0])
	publicGlobalMapQuery(PublicGlobalSQLMap["db"])
	PublicGlobalConnection.QueryContext(context.Background(), "SELECT body FROM global_connection_records")
	PublicGlobalTransaction.Query("SELECT body FROM global_transaction_records")
	PublicGlobalStatement.Query()
}

func publicGlobalDatabaseQuery(db *sql.DB) { db.Query("SELECT body FROM global_database_records") }
func privateGlobalDatabaseQuery(db *sql.DB) {
	db.Query("SELECT body FROM private_global_database_records")
}

func publicGlobalRecordQuery(db *sql.DB)  { db.Query("SELECT body FROM global_record_database_records") }
func privateGlobalRecordQuery(db *sql.DB) { db.Query("SELECT body FROM private_global_record_records") }
