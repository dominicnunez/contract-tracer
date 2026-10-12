package sample

import "database/sql"

func ExecuteRecords(statement *sql.Stmt) {
	statement.Exec("SELECT body FROM decoy_payload")
}

func ExecuteRebound(statement *sql.Stmt) {
	statement.Exec("SELECT body FROM rebound_parameter_decoy")
}
