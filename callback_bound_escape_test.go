package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundExternalMethodRetainsReceiverCallbacks(t *testing.T) {
	root := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ./dep\n")
	write("dep/go.mod", "module example.com/dep\n\ngo 1.27.0\n")
	write("dep/record.go", `package dep

import "database/sql"

type Record struct { Callback func(*sql.DB, error, func()) }
type Outer struct { *Record }
type Runner interface { ExternalMethod() }

func (*Record) ExternalMethod() {}
`)
	write("app.go", `package app

import (
	"database/sql"
	"example.com/dep"
)

func retainedQuery(db *sql.DB, err error, next func()) {
	db.Query("SELECT id FROM bound_callback_records")
	if err != nil { return }
	next()
}

func localOnlyQuery(db *sql.DB, err error, next func()) {}
func expressionQuery(db *sql.DB, err error, next func()) {}

type LocalRecord struct { Callback func(*sql.DB, error, func()) }
func (*LocalRecord) LocalMethod() {}

func Caller() {
	record := &dep.Record{Callback: retainedQuery}
	retainedQuery(nil, nil, func() {})
	run := record.ExternalMethod
	run()
}

func MixedCaller(useExternal bool) {
	external := &dep.Record{Callback: retainedQuery}
	local := &LocalRecord{Callback: localOnlyQuery}
	var run func()
	if useExternal { run = external.ExternalMethod } else { run = local.LocalMethod }
	run()
}

func LocalOnlyCaller() {
	local := &LocalRecord{Callback: localOnlyQuery}
	run := local.LocalMethod
	run()
}

func PromotedCaller() {
	outer := &dep.Outer{Record: &dep.Record{Callback: retainedQuery}}
	run := outer.ExternalMethod
	run()
}

func SQLHandleCaller(db *sql.DB) {
	run := db.Query
	run("SELECT id FROM opaque_sql_records")
}

func UnknownReceiverCaller(record dep.Runner) {
	run := record.ExternalMethod
	run()
}

func ExpressionCaller() {
	record := &dep.Record{Callback: expressionQuery}
	var run func(*dep.Record)
	run = (*dep.Record).ExternalMethod
	run(record)
}
`)

	options := Options{Root: root, Seeds: []string{"Caller", "MixedCaller", "LocalOnlyCaller", "PromotedCaller", "ExpressionCaller", "SQLHandleCaller", "UnknownReceiverCaller"}, Depth: 4, MaxNodes: 300}
	report, analysis, err := TraceWithAnalysis(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	assertBoundReceiverCallbacks(t, report)

	var snapshot bytes.Buffer
	if err := WriteAnalysis(&snapshot, analysis, true); err != nil {
		t.Fatal(err)
	}
	resumed, err := ReadAnalysis(bytes.NewReader(snapshot.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resumedReport, err := Explore(context.Background(), resumed, ExploreOptions{Seeds: options.Seeds, Depth: options.Depth, MaxNodes: options.MaxNodes})
	if err != nil {
		t.Fatal(err)
	}
	assertBoundReceiverCallbacks(t, resumedReport)
}

func assertBoundReceiverCallbacks(t *testing.T, report Report) {
	t.Helper()
	wantEscapes := map[string]bool{
		"example.com/app::Caller":         false,
		"example.com/app::MixedCaller":    false,
		"example.com/app::PromotedCaller": false,
	}
	localEscape, localOnlyEscape, expressionEscape := false, false, false
	spuriousSQLReceiverBoundary, unknownCallbackReceiverBoundary := false, false
	externalCallback, escapedSQL, outsideError, unresolvedFunction, tableRead := false, false, false, false, false
	for _, edge := range report.Relationships {
		if edge.To == "example.com/app::retainedQuery" && edge.Kind == "callback_escape" {
			if _, ok := wantEscapes[edge.From]; ok {
				wantEscapes[edge.From] = true
			}
		}
		expressionEscape = expressionEscape || edge.From == "example.com/app::ExpressionCaller" && edge.To == "example.com/app::expressionQuery" && edge.Kind == "callback_escape"
		localEscape = localEscape || edge.From == "example.com/app::MixedCaller" && edge.To == "example.com/app::localOnlyQuery" && edge.Kind == "callback_escape"
		localOnlyEscape = localOnlyEscape || edge.From == "example.com/app::LocalOnlyCaller" && edge.To == "example.com/app::localOnlyQuery" && edge.Kind == "callback_escape"
		tableRead = tableRead || edge.From == "example.com/app::retainedQuery" && edge.To == "table:bound_callback_records" && edge.Kind == "sql_read"
	}
	for _, boundary := range report.Boundaries {
		externalCallback = externalCallback || boundary.Node == "example.com/app::retainedQuery" && boundary.Kind == "external_callback"
		escapedSQL = escapedSQL || boundary.Node == "example.com/app::retainedQuery" && boundary.Kind == "escaped_sql_input"
		outsideError = outsideError || boundary.Kind == "outside_error_input" && boundary.Evidence.File == "app.go" && boundary.Evidence.Snippet == "func retainedQuery(db *sql.DB, err error, next func()) {"
		unresolvedFunction = unresolvedFunction || boundary.Node == "example.com/app::retainedQuery" && (boundary.Kind == "partial_function_call" || boundary.Kind == "unresolved_function_call") && boundary.Evidence.Snippet == "next()"
		spuriousSQLReceiverBoundary = spuriousSQLReceiverBoundary || boundary.Kind == "unresolved_callback_receiver" && boundary.Evidence.Snippet == `run("SELECT id FROM opaque_sql_records")`
		unknownCallbackReceiverBoundary = unknownCallbackReceiverBoundary || boundary.Node == "example.com/app::UnknownReceiverCaller" && boundary.Kind == "unresolved_callback_receiver" && boundary.Evidence.Snippet == "run()"
	}
	for owner, found := range wantEscapes {
		if !found {
			t.Errorf("%s lost callback from its bound receiver or explicit receiver argument", owner)
		}
	}
	if localEscape || localOnlyEscape || !expressionEscape || spuriousSQLReceiverBoundary || !unknownCallbackReceiverBoundary || !externalCallback || !escapedSQL || !outsideError || !unresolvedFunction || !tableRead {
		t.Fatalf("bound method callback scope is wrong: unrelatedLocalEscape=%t localOnlyEscape=%t expressionEscape=%t spuriousSQLReceiverBoundary=%t unknownCallbackReceiver=%t external=%t sqlInput=%t errorInput=%t functionInput=%t tableRead=%t", localEscape, localOnlyEscape, expressionEscape, spuriousSQLReceiverBoundary, unknownCallbackReceiverBoundary, externalCallback, escapedSQL, outsideError, unresolvedFunction, tableRead)
	}
}
