package contracttrace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixedLocalAndDependencyInterfaceEffects(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "mixed_dependency"))
	if err != nil {
		t.Fatal(err)
	}
	config := Config{StorageScopes: []StorageScope{{Namespace: "orders", DatabaseOrigins: []string{"example.com/mixed::localFactory.DB", "example.com/mixed::localFactory.Use"}}}}
	r, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"MixedEntry"}, Depth: 5, MaxNodes: 250, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	table, callbackTable := false, false
	unresolvedSQL, escapedCallback, mapOpenFallback := false, false, false
	for _, edge := range r.Relationships {
		if strings.HasSuffix(edge.From, "::helper") && edge.To == "table:orders:mixed_records" && (edge.Kind == "sql_read" || edge.Kind == "sql_write") {
			table = true
		}
		if edge.To == "table:orders:callback_records" && (edge.Kind == "sql_read" || edge.Kind == "sql_write") {
			callbackTable = true
		}
	}
	for _, boundary := range r.Boundaries {
		unresolvedSQL = unresolvedSQL || boundary.Kind == "unresolved_sql_handle" && strings.Contains(boundary.Evidence.Snippet, "db.Query")
		escapedCallback = escapedCallback || boundary.Kind == "escaped_sql_input" && strings.Contains(boundary.Evidence.Snippet, "factory.Use")
		mapOpenFallback = mapOpenFallback || boundary.Kind == "unresolved_sql_handle" && strings.Contains(boundary.Evidence.Snippet, "map_records")
	}
	if !table || !callbackTable || !unresolvedSQL || !escapedCallback || !mapOpenFallback {
		t.Fatalf("mixed interface effects missing: knownSQL=%t callbackSQL=%t unresolvedSQL=%t escapedCallback=%t mapOpenFallback=%t", table, callbackTable, unresolvedSQL, escapedCallback, mapOpenFallback)
	}
}

func TestExternalOnlyInterfaceRetainsBoundary(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "mixed_dependency", "external_only"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"ExternalEntry"}, Depth: 4, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	unresolvedInterface, unresolvedSQL := false, false
	for _, boundary := range r.Boundaries {
		unresolvedInterface = unresolvedInterface || boundary.Kind == "unresolved_interface_flow" && strings.Contains(boundary.Evidence.Snippet, "factory.DB")
		unresolvedSQL = unresolvedSQL || boundary.Kind == "unresolved_sql_handle" && strings.Contains(boundary.Evidence.Snippet, "db.Query")
	}
	if !unresolvedInterface || !unresolvedSQL {
		t.Fatalf("external-only interface boundary missing: unresolvedInterface=%t unresolvedSQL=%t", unresolvedInterface, unresolvedSQL)
	}
}
