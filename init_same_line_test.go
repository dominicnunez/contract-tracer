package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSameLineInitializersKeepDistinctOwnersInSnapshots(t *testing.T) {
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
	write("go.mod", "module example.com/samelineinit\n\ngo 1.27.0\nrequire example.com/initapi v0.0.0\nreplace example.com/initapi => ./api\n")
	write("api/go.mod", "module example.com/initapi\n\ngo 1.27.0\n")
	write("api/api.go", `package initapi
func Publish(string) {}
func Query(string) {}
`)
	write("app.go", `package samelineinit
import "example.com/initapi"
func firstBody() { initapi.Publish("first.event") }
func secondBody() { initapi.Publish("second.event") }
func init() { firstBody(); initapi.Publish("first.init"); initapi.Query("SELECT * FROM first_table") }; func init() { secondBody(); initapi.Publish("second.init"); initapi.Query("SELECT * FROM second_table") }
`)
	write("ordinary.go", `package samelineinit
import "example.com/initapi"
func ordinaryBody() { initapi.Publish("ordinary.event") }
func init() { ordinaryBody() }
`)
	config := DefaultConfig()
	config.CallRules = []CallRule{
		{Symbol: "example.com/initapi::Publish", Kind: "event_publish", Argument: 0},
		{Symbol: "example.com/initapi::Query", Kind: "sql_query", Argument: 0, Namespace: "primary"},
	}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{
		Root: root, Seeds: []string{"example.com/samelineinit::(package init)"}, Depth: 5, MaxNodes: 150, Config: config,
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot bytes.Buffer
	if err := WriteAnalysis(&snapshot, analysis, true); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(bytes.NewReader(snapshot.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{
		Seeds: []string{"example.com/samelineinit::(package init)"}, Depth: 5, MaxNodes: 150,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		owners := map[int]map[string]bool{}
		for _, node := range report.Nodes {
			if node.Kind == "function" && node.Name == "init" && node.Evidence.File == "app.go" {
				owners[node.Evidence.Column] = map[string]bool{node.ID: true}
			}
		}
		if len(owners) != 2 || len(owners[1]) != 1 || len(owners[105]) != 1 {
			t.Errorf("same-line init declarations were merged or lost their source columns: owners=%v", owners)
		}
		callsByColumn := map[int]map[string]bool{}
		for _, edge := range report.Relationships {
			for column, ids := range owners {
				if ids[edge.From] && edge.Kind == "call" && edge.Evidence.File == "app.go" {
					if callsByColumn[column] == nil {
						callsByColumn[column] = map[string]bool{}
					}
					callsByColumn[column][edge.To] = true
				}
			}
		}
		for _, tc := range []struct {
			column int
			helper string
			event  string
			table  string
			other  string
		}{
			{column: 1, helper: "example.com/samelineinit::firstBody", event: "event:first.init", table: "table:primary:first_table", other: "secondBody"},
			{column: 105, helper: "example.com/samelineinit::secondBody", event: "event:second.init", table: "table:primary:second_table", other: "firstBody"},
		} {
			ids := owners[tc.column]
			var owner string
			for id := range ids {
				owner = id
			}
			if !callsByColumn[tc.column][tc.helper] || callsByColumn[tc.column]["example.com/samelineinit::"+tc.other] {
				t.Errorf("initializer at app.go:%d has incorrect helper ownership: %v", tc.column, callsByColumn[tc.column])
			}
			if !hasRelationship(report, owner, tc.event, "event_publish", "app.go", 5) ||
				!hasRelationship(report, owner, tc.table, "sql_read", "app.go", 5) {
				t.Errorf("initializer at app.go:%d lost its own event or SQL evidence", tc.column)
			}
			otherEvent := "event:second.init"
			otherTable := "table:primary:second_table"
			if tc.other == "firstBody" {
				otherEvent = "event:first.init"
				otherTable = "table:primary:first_table"
			}
			if hasRelationship(report, owner, otherEvent, "event_publish", "app.go", 5) ||
				hasRelationship(report, owner, otherTable, "sql_read", "app.go", 5) {
				t.Errorf("initializer at app.go:%d inherited the other initializer's evidence", tc.column)
			}
		}
	}
	located, err := Trace(context.Background(), Options{Root: root, Locations: []string{"ordinary.go:4"}, Depth: 3, MaxNodes: 100, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(located, "ordinary.event") {
		t.Error("source-location seed no longer reaches a separate-line initializer")
	}
}
