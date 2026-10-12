package replacement

import "database/sql"

type Factory struct{}

func (Factory) DB() *sql.DB { return nil }
func (Factory) Use(callback func(*sql.DB), values map[string]*sql.DB) {
	callback(nil)
	values["known"] = nil
}
