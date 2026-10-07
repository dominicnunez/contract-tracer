package contracttrace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSentinelTextRemainsARealCandidate(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/sentinelclient\n\ngo 1.27.0\nrequire example.com/sentinelapi v0.0.0\nreplace example.com/sentinelapi => ./api\n",
		"api/go.mod": "module example.com/sentinelapi\n\ngo 1.27.0\n",
		"api/api.go": `package sentinelapi
type Manager struct{}
func Publish(string) {}
func Query(string) {}
func (*Manager) Reserve(string) {}
func (*Manager) Release(string) {}
`,
		"app.go": `package sentinelclient
import (
    "database/sql"
    "example.com/sentinelapi"
)
const marker = "__contract_unknown__"
const holeLooking = "__ct_unknown_hole_0__"
var manager sentinelapi.Manager
type Envelope struct { EventType string }
func EmitSentinel() { sentinelapi.Publish(marker) }
func EmitContainingText() { sentinelapi.Publish("prefix" + marker + "suffix") }
func EmitHoleLookingText() { sentinelapi.Publish(holeLooking) }
func EmitMixed(flag bool, outside string) { value := marker; if flag { value = outside }; sentinelapi.Publish(value) }
func EmitOutside(outside string) { sentinelapi.Publish(outside) }
func EmitPartial(outside string) { sentinelapi.Publish("orders." + outside) }
func ConstructSentinel() { _ = Envelope{EventType: marker} }
func ConfiguredQuerySentinel() { sentinelapi.Query("SELECT id FROM " + marker) }
func ConfiguredPartialQuery(suffix string) { sentinelapi.Query("SELECT id FROM configured_records JOIN " + suffix + " ON 1=1") }
func ConfiguredQueryOutside(query string) { sentinelapi.Query(query) }
func DefaultQuerySentinel(db *sql.DB) { db.Query("SELECT id FROM " + marker) }
func DefaultQueryOutside(db *sql.DB, query string) { db.Query(query) }
func PartialQuery(db *sql.DB, suffix string) { db.Query("SELECT id FROM known_records JOIN " + suffix + " ON 1=1") }
func PartialTableName(db *sql.DB, suffix string) { db.Query("SELECT id FROM known_" + suffix) }
func PartialHoleLookingTable(db *sql.DB, suffix string) { db.Query("SELECT id FROM __ct_unknown_hole_0__ JOIN " + suffix + " ON 1=1") }
func ReserveSentinel() { manager.Reserve(marker); manager.Release(marker) }
func ReserveContainingText() { reserve := manager.Reserve; release := manager.Release; reserve("prefix" + marker + "suffix"); release("prefix" + marker + "suffix") }
func ReserveMixed(flag bool, outside string) { value := marker; if flag { value = outside }; manager.Reserve(value); manager.Release(value) }
func ReserveOutside(outside string) { manager.Reserve(outside); manager.Release(outside) }
`,
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	zero := 0
	config := DefaultConfig()
	config.CallRules = []CallRule{
		{Symbol: "example.com/sentinelapi::Publish", Kind: "event_publish", Argument: 0, Namespace: "bus"},
		{Symbol: "example.com/sentinelapi::Query", Kind: "sql_query", Argument: 0, Namespace: "api"},
	}
	config.LifecycleRules = []LifecycleRule{
		{Symbol: "example.com/sentinelapi::Manager.Reserve", Role: "acquire", Namespace: "keys", Identity: "value", Argument: &zero},
		{Symbol: "example.com/sentinelapi::Manager.Release", Role: "release", Namespace: "keys", Identity: "value", Argument: &zero},
	}
	seeds := []string{"EmitSentinel", "EmitContainingText", "EmitHoleLookingText", "EmitMixed", "EmitOutside", "EmitPartial", "ConstructSentinel", "ConfiguredQuerySentinel", "ConfiguredPartialQuery", "ConfiguredQueryOutside", "DefaultQuerySentinel", "DefaultQueryOutside", "PartialQuery", "PartialTableName", "PartialHoleLookingTable", "ReserveSentinel", "ReserveContainingText", "ReserveMixed", "ReserveOutside"}
	fresh, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: seeds, Depth: 5, MaxNodes: 200, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := WriteAnalysis(&encoded, analysis, true); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Explore(context.Background(), saved, ExploreOptions{Seeds: seeds, Depth: 5, MaxNodes: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []Report{fresh, resumed} {
		want := []struct{ owner, target, kind string }{
			{"EmitSentinel", "event:bus:__contract_unknown__", "event_publish"},
			{"EmitContainingText", "event:bus:prefix__contract_unknown__suffix", "event_publish"},
			{"EmitHoleLookingText", "event:bus:__ct_unknown_hole_0__", "event_publish"},
			{"EmitMixed", "event:bus:__contract_unknown__", "event_publish"},
			{"ConstructSentinel", "event:__contract_unknown__", "event_construct"},
			{"ConfiguredQuerySentinel", "table:api:__contract_unknown__", "sql_read"},
			{"ConfiguredPartialQuery", "table:api:configured_records", "sql_read"},
			{"DefaultQuerySentinel", "table:__contract_unknown__", "sql_read"},
			{"PartialQuery", "table:known_records", "sql_read"},
			{"PartialHoleLookingTable", "table:__ct_unknown_hole_0__", "sql_read"},
			{"ReserveSentinel", "lifecycle:keys:__contract_unknown__", "lifecycle_acquire"},
			{"ReserveSentinel", "lifecycle:keys:__contract_unknown__", "lifecycle_release"},
			{"ReserveContainingText", "lifecycle:keys:prefix__contract_unknown__suffix", "lifecycle_acquire"},
			{"ReserveContainingText", "lifecycle:keys:prefix__contract_unknown__suffix", "lifecycle_release"},
			{"ReserveMixed", "lifecycle:keys:__contract_unknown__", "lifecycle_acquire"},
			{"ReserveMixed", "lifecycle:keys:__contract_unknown__", "lifecycle_release"},
		}
		for _, edge := range want {
			if !hasRelationship(report, "example.com/sentinelclient::"+edge.owner, edge.target, edge.kind, "app.go", 0) {
				t.Errorf("%s lost exact candidate %s (%s)", edge.owner, edge.target, edge.kind)
			}
		}
		for _, owner := range []string{"EmitMixed", "EmitOutside", "EmitPartial"} {
			if !hasBoundaryKind(report, "example.com/sentinelclient::"+owner, "dynamic_api_value") {
				t.Errorf("%s lost its unresolved configured event input", owner)
			}
		}
		for _, owner := range []string{"ReserveMixed", "ReserveOutside"} {
			if !hasBoundaryKind(report, "example.com/sentinelclient::"+owner, "unresolved_lifecycle_resource") {
				t.Errorf("%s lost its unresolved lifecycle input", owner)
			}
		}
		for _, owner := range []string{"PartialQuery", "PartialTableName", "PartialHoleLookingTable"} {
			if !hasBoundaryKind(report, "example.com/sentinelclient::"+owner, "dynamic_sql") {
				t.Errorf("%s lost its unresolved suffix boundary", owner)
			}
		}
		if !hasBoundaryKind(report, "example.com/sentinelclient::ConfiguredPartialQuery", "dynamic_api_value") {
			t.Error("configured partial SQL lost its unresolved suffix boundary")
		}
		for _, owner := range []string{"ConfiguredQueryOutside", "DefaultQueryOutside"} {
			if !hasBoundaryKind(report, "example.com/sentinelclient::"+owner, map[string]string{"ConfiguredQueryOutside": "dynamic_api_value", "DefaultQueryOutside": "dynamic_sql"}[owner]) {
				t.Errorf("%s lost its unresolved SQL input boundary", owner)
			}
			for _, edge := range report.Relationships {
				if edge.From == "example.com/sentinelclient::"+owner && edge.Kind == "sql_read" {
					t.Errorf("%s fabricated a table from an unknown-only query: %+v", owner, edge)
				}
			}
		}
		if hasRelationship(report, "example.com/sentinelclient::PartialTableName", "table:known_", "sql_read", "app.go", 0) {
			t.Error("incomplete table-name prefix was emitted as a complete table")
		}
		for _, owner := range []string{"EmitOutside", "EmitPartial"} {
			if hasEdgeKind(report, "example.com/sentinelclient::"+owner, "event_publish") {
				t.Errorf("%s emitted a complete event from an unknown value", owner)
			}
		}
		if hasEdgeKind(report, "example.com/sentinelclient::PartialTableName", "sql_read") {
			t.Error("unknown-only table name produced a complete SQL table candidate")
		}
		for owner, expected := range map[string]string{
			"PartialQuery":           "table:known_records",
			"ConfiguredPartialQuery": "table:api:configured_records",
		} {
			for _, edge := range report.Relationships {
				if edge.From == "example.com/sentinelclient::"+owner && edge.Kind == "sql_read" && edge.To != expected {
					t.Errorf("%s emitted a table beyond its statically known fragment: %+v", owner, edge)
				}
			}
		}
		if hasEdgeKind(report, "example.com/sentinelclient::ReserveOutside", "lifecycle_acquire") ||
			hasEdgeKind(report, "example.com/sentinelclient::ReserveOutside", "lifecycle_release") {
			t.Error("unknown-only lifecycle key produced a concrete role")
		}
		if hasBoundaryKind(report, "example.com/sentinelclient::EmitSentinel", "dynamic_api_value") ||
			hasBoundaryKind(report, "example.com/sentinelclient::ConfiguredQuerySentinel", "dynamic_api_value") ||
			hasBoundaryKind(report, "example.com/sentinelclient::ReserveSentinel", "unresolved_lifecycle_resource") {
			t.Error("known exact marker text was incorrectly treated as an unknown value")
		}
	}
}

func hasEdgeKind(report Report, owner, kind string) bool {
	for _, edge := range report.Relationships {
		if edge.From == owner && edge.Kind == kind {
			return true
		}
	}
	return false
}

func hasBoundaryKind(report Report, owner, kind string) bool {
	for _, boundary := range report.Boundaries {
		if boundary.Node == owner && boundary.Kind == kind {
			return true
		}
	}
	return false
}
