package orders

import "database/sql"

func Write(db *sql.DB) { db.Exec("INSERT INTO records(id) VALUES (1)") }
