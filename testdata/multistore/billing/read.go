package billing

import "database/sql"

func Read(db *sql.DB) { db.Query("SELECT id FROM records") }
