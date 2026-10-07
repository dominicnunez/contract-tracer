package contracttrace

import (
	"reflect"
	"testing"
)

func TestSQLInventory(t *testing.T) {
	cases := []struct {
		query string
		want  []sqlAccess
	}{
		{`SELECT 'FROM unrelated' FROM "records" -- JOIN wrong
   /* JOIN wrong */ JOIN tasks ON 1=1`, []sqlAccess{{"records", "read"}, {"tasks", "read"}}},
		{`INSERT INTO records SELECT body FROM events`, []sqlAccess{{"records", "write"}, {"events", "read"}}},
		{`WITH history AS (SELECT * FROM events) SELECT * FROM history`, []sqlAccess{{"events", "read"}}},
		{`DELETE FROM records`, []sqlAccess{{"records", "write"}}},
		{`UPDATE records SET body=?`, []sqlAccess{{"records", "write"}}},
		{`CREATE TABLE IF NOT EXISTS records (id TEXT)`, []sqlAccess{{"records", "schema"}}},
		{`DROP TABLE IF EXISTS records`, []sqlAccess{{"records", "schema"}}},
		{`SELECT * FROM main.records`, []sqlAccess{{"main.records", "read"}}},
		{`CREATE INDEX records_idx ON records(body)`, []sqlAccess{{"records", "schema"}}},
		{`CREATE TABLE child (parent_id REFERENCES parent(id))`, []sqlAccess{{"child", "schema"}, {"parent", "schema_ref"}}},
		{`CREATE TRIGGER record_audit AFTER INSERT ON records BEGIN INSERT INTO audit_log VALUES (NEW.body); END`, []sqlAccess{{"records", "schema"}, {"audit_log", "write"}}},
		{`WITH history(id) AS (SELECT id FROM events) SELECT * FROM history, records`, []sqlAccess{{"events", "read"}, {"records", "read"}}},
		{`SELECT (SELECT COUNT(*) FROM audit_log) FROM records`, []sqlAccess{{"audit_log", "read"}, {"records", "read"}}},
		{`SELECT * FROM (SELECT * FROM events) AS e JOIN tasks t ON e.id=t.id`, []sqlAccess{{"events", "read"}, {"tasks", "read"}}},
		{`WITH records AS (SELECT * FROM base) SELECT * FROM records`, []sqlAccess{{"base", "read"}}},
	}
	for _, c := range cases {
		if got := sqlTables(c.query); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.query, got, c.want)
		}
	}
}
