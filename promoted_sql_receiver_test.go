package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLCallRuleProjectsPromotedMethodExpressionReceiver(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": `module example.com/promotedsqltest

go 1.26.0
`,
		"client.go": `package promotedsqltest
import "database/sql"

type Lease struct { *sql.DB }
type Wrapped struct { *Lease }

func OpenPrimary() *sql.DB { db, _ := sql.Open("sqlite3", ":memory:"); return db }
func OpenArchive() *sql.DB { db, _ := sql.Open("sqlite3", ":memory:"); return db }
func PrimaryLease() *Lease { return &Lease{DB: OpenPrimary()} }
func ArchiveLease() *Lease { return &Lease{DB: OpenArchive()} }

func Scenario() {
	primary := &Wrapped{Lease: PrimaryLease()}
	archive := &Wrapped{Lease: ArchiveLease()}
	primary.Exec("SELECT id FROM records")
	archive.Exec("SELECT id FROM records")
	primaryAlias := primary.Exec
	primaryAlias("SELECT id FROM records")
	(*Wrapped).Exec(primary, "SELECT id FROM records")
	go (*Wrapped).Exec(primary, "SELECT id FROM records")
	defer (*Wrapped).Exec(archive, "SELECT id FROM records")
	runArchiveExpression((*Wrapped).Exec, archive)
	PublicExpression(archive)
}

func runArchiveExpression(fn func(*Wrapped, string, ...any) (sql.Result, error), wrapped *Wrapped) {
	fn(wrapped, "SELECT id FROM records")
}

func PublicExpression(wrapped *Wrapped) {
	(*Wrapped).Exec(wrapped, "SELECT id FROM records")
}
`,
		"migrations/primary.sql": "CREATE TABLE records (id INTEGER PRIMARY KEY);\n",
		"migrations/archive.sql": "CREATE TABLE records (id INTEGER PRIMARY KEY);\n",
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
	config := DefaultConfig()
	config.SQLFiles = []string{"migrations/*.sql"}
	config.StorageScopes = []StorageScope{
		{Namespace: "primary", SQLFiles: []string{"migrations/primary.sql"}, DatabaseOrigins: []string{"example.com/promotedsqltest::OpenPrimary"}},
		{Namespace: "archive", SQLFiles: []string{"migrations/archive.sql"}, DatabaseOrigins: []string{"example.com/promotedsqltest::OpenArchive"}},
	}
	config.CallRules = []CallRule{{Symbol: "database/sql::DB.Exec", Kind: "sql_query", Argument: 0}}
	report, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Scenario", "PublicExpression"}, Depth: 8, MaxNodes: 300, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ owner, target, evidence string }{
		{"example.com/promotedsqltest::Scenario", "table:primary:records", "primary.Exec("},
		{"example.com/promotedsqltest::Scenario", "table:archive:records", "archive.Exec("},
		{"example.com/promotedsqltest::Scenario", "table:primary:records", "primaryAlias("},
		{"example.com/promotedsqltest::Scenario", "table:primary:records", "(*Wrapped).Exec(primary"},
		{"example.com/promotedsqltest::Scenario", "table:primary:records", "go (*Wrapped).Exec(primary"},
		{"example.com/promotedsqltest::Scenario", "table:archive:records", "defer (*Wrapped).Exec(archive"},
		{"example.com/promotedsqltest::runArchiveExpression", "table:archive:records", "fn(wrapped,"},
		{"example.com/promotedsqltest::PublicExpression", "table:archive:records", "(*Wrapped).Exec(wrapped"},
	}
	for _, expected := range want {
		found, crossed := false, false
		otherNamespace := "table:archive:records"
		if strings.Contains(expected.target, ":archive:") {
			otherNamespace = "table:primary:records"
		}
		for _, edge := range analysis.Relationships {
			if edge.From != expected.owner || edge.Kind != "sql_read" || !strings.HasPrefix(strings.TrimSpace(edge.Evidence.Snippet), expected.evidence) {
				continue
			}
			found = found || edge.To == expected.target
			crossed = crossed || edge.To == otherNamespace
		}
		if !found || crossed {
			t.Errorf("SQL call at %s lost its receiver namespace or crossed stores: want %s (found=%t crossed=%t)", expected.evidence, expected.target, found, crossed)
		}
	}
	if report.ContractComplete {
		t.Fatal("static SQL analysis must retain incomplete contract coverage")
	}
	unknownPublicReceiver := false
	for _, boundary := range report.Boundaries {
		unknownPublicReceiver = unknownPublicReceiver || boundary.Node == "example.com/promotedsqltest::PublicExpression" &&
			boundary.Kind == "unresolved_database_namespace" && strings.Contains(boundary.Evidence.Snippet, "(*Wrapped).Exec(wrapped")
	}
	if !unknownPublicReceiver {
		t.Fatal("public promoted receiver lost its unresolved outside namespace boundary")
	}
}
