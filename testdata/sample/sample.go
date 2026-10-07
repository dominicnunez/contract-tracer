package sample

import (
    "context"
    "database/sql"
)

func Validate(n int) bool { return n > 0 }
func Live() bool { return Validate(1) }
func apply(check func(int) bool) bool { return check(1) }
func Recovery() bool { return apply(Validate) }
type Reader interface { Read() bool }
type Store struct{}
func (Store) Read() bool { return Validate(1) }
func Entry(r Reader) bool { return r.Read() }
func unrelated() {}
func UnusedCheck(n int) bool { return n < 0 }

const records = "records"
func Write(db *sql.DB) { db.Exec("INSERT INTO " + records + " (body) VALUES (?)", "value") }
func Read(db *sql.DB) { db.Query("SELECT body FROM records") }
func Rebuild(db *sql.DB) { db.Query("SELECT body FROM records ORDER BY version") }
func Other(db *sql.DB) { db.Query("SELECT body FROM unrelated") }
func Dynamic(db *sql.DB, table string) { db.Query("SELECT body FROM " + table) }

const completed = "TASK_COMPLETED"
type Event struct { EventType string }
func Publish() Event { return Event{EventType: completed} }
func Consume(e Event) bool { return e.EventType == completed }
func Another() Event { return Event{EventType: "TASK_FAILED"} }
func Stop(ctx context.Context) { child, cancel := context.WithCancel(ctx); defer cancel(); _ = child }
