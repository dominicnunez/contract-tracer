package sample

import (
	"context"
	"database/sql"
)

func PrepareRecords(db *sql.DB) *sql.Stmt {
	statement, _ := db.Prepare("INSERT INTO prepared_records (body) VALUES (?)")
	return statement
}
func PreparedStart(db *sql.DB) {
	statement := PrepareRecords(db)
	defer statement.Close()
	ExecuteRecords(statement)
}
func UnknownStatement(statement *sql.Stmt) { statement.Exec("SELECT body FROM unknown_payload") }

func TransactionStatement(db *sql.DB) {
	transaction, _ := db.Begin()
	defer transaction.Rollback()
	statement, _ := transaction.Prepare("SELECT body FROM transaction_records")
	defer statement.Close()
	statement.Query()
	transaction.Commit()
}

func ReboundStatement(db *sql.DB) {
	statement := PrepareRecords(db)
	transaction, _ := db.Begin()
	defer transaction.Rollback()
	first := transaction.Stmt(statement)
	second := transaction.StmtContext(context.Background(), first)
	defer second.Close()
	ExecuteRebound(second)
}

func ReboundUnknown(db *sql.DB, statement *sql.Stmt) {
	transaction, _ := db.Begin()
	rebound := transaction.Stmt(statement)
	rebound.Exec("SELECT body FROM rebound_decoy")
}
