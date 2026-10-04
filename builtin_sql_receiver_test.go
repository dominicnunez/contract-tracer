package contracttrace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinSQLCloseTracksBoundAndMethodExpressionReceivers(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": `module example.com/sqlcloseadapters

go 1.27.0
`,
		"adapters.go": `package sqlcloseadapters
import "database/sql"

func openAlpha() (*sql.DB, error) { return sql.Open("unused", "alpha") }
func openBeta() (*sql.DB, error) { return sql.Open("unused", "beta") }
func callClose(fn func() error) { _ = fn() }

func SQLSites() {
	alpha, _ := openAlpha()
	beta, _ := openBeta()
	alpha.Close() // direct alpha control
	beta.Close() // direct beta control
	closeAlpha := alpha.Close
	closeBeta := beta.Close
	callClose(closeAlpha) // bound helper alpha
	callClose(closeBeta) // bound helper beta
	closeExpr := (*sql.DB).Close
	_ = closeExpr(alpha) // method-expression alpha
}
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
	report, err := Trace(context.Background(), Options{Root: root, Seeds: []string{"SQLSites"}, Depth: 7, MaxNodes: 300})
	if err != nil {
		t.Fatal(err)
	}
	resourcesAt := func(snippet string) map[string]bool {
		resources := map[string]bool{}
		for _, edge := range report.Relationships {
			if edge.Kind == "database_close" && strings.Contains(edge.Evidence.Snippet, snippet) {
				resources[edge.To] = true
			}
		}
		return resources
	}
	alpha := resourcesAt("alpha.Close() // direct alpha control")
	beta := resourcesAt("beta.Close() // direct beta control")
	if len(alpha) != 1 || len(beta) != 1 || alpha == nil || beta == nil {
		t.Fatalf("direct Close controls did not establish resource identities: alpha=%v beta=%v", alpha, beta)
	}
	bound := map[string]bool{}
	for _, edge := range report.Relationships {
		if edge.From == "example.com/sqlcloseadapters::callClose" && edge.Kind == "database_close" && strings.Contains(edge.Evidence.Snippet, "fn()") {
			bound[edge.To] = true
		}
	}
	for resource := range alpha {
		if !bound[resource] {
			t.Errorf("bound SQL Close helper lost alpha resource %s: observed %v", resource, bound)
		}
	}
	for resource := range beta {
		if !bound[resource] {
			t.Errorf("bound SQL Close helper lost beta resource %s: observed %v", resource, bound)
		}
	}
	expression := resourcesAt("closeExpr(alpha)")
	for resource := range alpha {
		if !expression[resource] {
			t.Errorf("SQL Close method expression lost its explicit alpha receiver %s: observed %v", resource, expression)
		}
	}
}
