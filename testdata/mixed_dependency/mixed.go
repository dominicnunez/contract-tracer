package mixed

import (
	"database/sql"
	"example.com/replacement"
)

type Factory interface {
	DB() *sql.DB
	Use(func(*sql.DB), map[string]*sql.DB)
}

type localFactory struct{}

func (localFactory) DB() *sql.DB {
	db, _ := sql.Open("sqlite3", "local")
	return db
}
func (localFactory) Use(callback func(*sql.DB), values map[string]*sql.DB) {
	db, _ := sql.Open("sqlite3", "local-callback")
	callback(db)
}

var _ Factory = localFactory{}
var _ Factory = replacement.Factory{}

func helper(factory Factory, callback func(*sql.DB), values map[string]*sql.DB) {
	db := factory.DB()
	db.Query("SELECT * FROM mixed_records")
	factory.Use(callback, values)
	values["known"].Query("SELECT * FROM map_records")
}

func MixedEntry() {
	var factory Factory = localFactory{}
	db, _ := sql.Open("sqlite3", "entry")
	values := map[string]*sql.DB{"known": db}
	helper(factory, func(db *sql.DB) { db.Query("SELECT * FROM callback_records") }, values)
	var dependencyFactory Factory = replacement.Factory{}
	_ = dependencyFactory
}
