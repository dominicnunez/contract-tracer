package sample

import "database/sql"

type QueryRunner interface{ Run(string) }
type SQLRunner struct{ DB *sql.DB }

func (r *SQLRunner) Run(query string)           { r.DB.Exec(query) }
func throughRunner(r QueryRunner, query string) { r.Run(query) }
func InterfaceWrite(db *sql.DB) {
	throughRunner(&SQLRunner{DB: db}, "INSERT INTO interface_records(id) VALUES (1)")
}
func InterfaceRead(db *sql.DB) { db.Query("SELECT id FROM interface_records") }

type TopicProvider interface{ Topic() string }
type topicValue struct{ Kind string }

func (v topicValue) Topic() string          { return v.Kind }
func throughProvider(p TopicProvider) Event { return Event{EventType: p.Topic()} }
func InterfaceEvent() Event                 { return throughProvider(topicValue{Kind: "INTERFACE_EVENT"}) }
func InterfaceConsume(e Event) bool         { return e.EventType == "INTERFACE_EVENT" }

type PairProvider interface{ Pair() (string, string) }
type pairValue struct{}

func (pairValue) Pair() (string, string) { return "PAIR_FIRST", "PAIR_SECOND" }
func InterfacePair(p PairProvider) Event { _, kind := p.Pair(); return Event{EventType: kind} }
func PairEntry() Event                   { return InterfacePair(pairValue{}) }

type BoolCheck interface{ Check(int) bool }
type CheckFunc func(int) bool

func (f CheckFunc) Check(n int) bool    { return f(n) }
func throughCheck(check BoolCheck) bool { return check.Check(1) }
func InterfaceCallback() bool           { return throughCheck(CheckFunc(Validate)) }

type NestedTopic struct{ Value topicValue }

func (v NestedTopic) Topic() string { return v.Value.Kind }
func NestedInterfaceEvent() Event {
	return throughProvider(NestedTopic{Value: topicValue{Kind: "NESTED_INTERFACE"}})
}

type PromotedTopic struct{ topicValue }

func PromotedInterfaceEvent() Event {
	return throughProvider(&PromotedTopic{topicValue: topicValue{Kind: "PROMOTED_INTERFACE"}})
}
func InterfaceAsync(r QueryRunner) {
	go r.Run("INSERT INTO async_records(id) VALUES (1)")
	defer r.Run("INSERT INTO deferred_records(id) VALUES (1)")
}
func AsyncEntry(db *sql.DB)   { InterfaceAsync(&SQLRunner{DB: db}) }
func AsyncRead(db *sql.DB)    { db.Query("SELECT id FROM async_records") }
func DeferredRead(db *sql.DB) { db.Query("SELECT id FROM deferred_records") }

type MissingRunner interface{ Handle(string) }

func ExternalInterface(r MissingRunner) { r.Handle("EXTERNAL_VALUE") }
