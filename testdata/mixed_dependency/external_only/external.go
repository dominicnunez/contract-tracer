package externalonly

import (
	"database/sql"
	"example.com/replacement"
)

type Factory interface {
	DB() *sql.DB
}

func ExternalEntry() {
	var factory Factory = replacement.Factory{}
	db := factory.DB()
	db.Query("SELECT * FROM outside_only_records")
}
