package sample

import (
	"context"
	"database/sql"
)

func PublicSQLFactories(database func() *sql.DB, connection func() *sql.Conn, transaction func() *sql.Tx, statement func() (*sql.Stmt, error)) {
	openDatabaseFactoryQuery(database())
	openConnectionFactoryQuery(connection())
	openTransactionFactoryQuery(transaction())
	prepared, _ := statement()
	openStatementFactoryQuery(prepared)
}

func SQLFactoryCaller() {
	PublicSQLFactories(knownSQLFactoryDatabase, localConnectionFactory, localTransactionFactory, localStatementFactory)
	closedSQLFactories(knownSQLFactoryDatabase, localConnectionFactory, localTransactionFactory, localStatementFactory)
}

func localConnectionFactory() *sql.Conn {
	value, _ := knownSQLFactoryDatabase().Conn(context.Background())
	return value
}
func localTransactionFactory() *sql.Tx { value, _ := knownSQLFactoryDatabase().Begin(); return value }
func localStatementFactory() (*sql.Stmt, error) {
	return knownSQLFactoryDatabase().Prepare("SELECT body FROM factory_statement_records")
}
func openDatabaseFactoryQuery(db *sql.DB) { db.Query("SELECT body FROM factory_database_records") }
func openConnectionFactoryQuery(conn *sql.Conn) {
	conn.QueryContext(context.Background(), "SELECT body FROM factory_connection_records")
}
func openTransactionFactoryQuery(tx *sql.Tx) {
	tx.Query("SELECT body FROM factory_transaction_records")
}
func openStatementFactoryQuery(stmt *sql.Stmt) { stmt.Query() }

func closedSQLFactories(database func() *sql.DB, connection func() *sql.Conn, transaction func() *sql.Tx, statement func() (*sql.Stmt, error)) {
	closedDatabaseFactoryQuery(database())
	closedConnectionFactoryQuery(connection())
	closedTransactionFactoryQuery(transaction())
	prepared, _ := statement()
	closedStatementFactoryQuery(prepared)
}
func closedDatabaseFactoryQuery(db *sql.DB) {
	db.Query("SELECT body FROM closed_factory_database_records")
}
func closedConnectionFactoryQuery(conn *sql.Conn) {
	conn.QueryContext(context.Background(), "SELECT body FROM closed_factory_connection_records")
}
func closedTransactionFactoryQuery(tx *sql.Tx) {
	tx.Query("SELECT body FROM closed_factory_transaction_records")
}
func closedStatementFactoryQuery(stmt *sql.Stmt) { stmt.Query() }

func knownSQLFactoryDatabase() *sql.DB { db, _ := sql.Open("sqlite", "factory.db"); return db }
