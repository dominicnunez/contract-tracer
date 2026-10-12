package sample

import (
 _ "embed"
 "database/sql"
)

//go:embed embedded/schema.sql
var embeddedSchema string

func LoadEmbedded(db *sql.DB) { db.Exec(embeddedSchema) }
func EmbeddedRead(db *sql.DB) { db.Query("SELECT body FROM embedded_records") }
