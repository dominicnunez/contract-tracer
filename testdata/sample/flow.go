package sample

import "database/sql"

func identity(s string) string { return s }
func sqlWrapper(db *sql.DB, q string) { db.Exec(identity(q)) }
func WrappedWrite(db *sql.DB) { sqlWrapper(db, "INSERT INTO audit_log (body) VALUES (?)") }
func AuditRead(db *sql.DB) { db.Query("SELECT body FROM audit_log") }
func makeKind() string { return "WRAPPED_COMPLETED" }
func publishKind(kind string) Event { return Event{EventType: kind} }
func WrappedPublish() Event { return publishKind(identity(makeKind())) }
func WrappedConsume(e Event) bool { return e.EventType == "WRAPPED_COMPLETED" }
func pass(check func(int) bool) func(int) bool { return check }
func runCheck(check func(int) bool) bool { return pass(check)(1) }
func FlowEntry() bool { return runCheck(Validate) }

var registered = Validate
func RegisteredEntry() bool { return registered(1) }
type callbackSlot struct { check func(int) bool }
func FieldEntry() bool { slot := &callbackSlot{check: Validate}; return slot.check(1) }
