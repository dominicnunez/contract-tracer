package contracttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPanicAndDirectDeferredRecoveryAreConnected(t *testing.T) {
	_, analysis, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"PanicBare"}, Depth: 1, MaxNodes: 10})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ReadAnalysis(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Explore(context.Background(), saved, ExploreOptions{Seeds: []string{"PanicRecovered", "PanicIndirectRecovery", "PanicBare", "PanicClosureRecovery"}, Depth: 4, MaxNodes: 250})
	if err != nil {
		t.Fatal(err)
	}
	recoverSite := ""
	closureSite := ""
	for _, e := range r.Relationships {
		if strings.HasSuffix(e.From, "::RecoveryDirect") && e.Kind == "recover_observe" {
			recoverSite = e.To
		}
		if strings.HasSuffix(e.From, "::PanicClosureRecovery") && e.Kind == "recover_observe" {
			closureSite = e.To
		}
	}
	directRegistration, indirectRegistration, closureRegistration, panicSite := "", "", "", ""
	bare := false
	for _, e := range r.Relationships {
		if e.Kind == "defer_registration" {
			if strings.HasSuffix(e.From, "::PanicRecovered") {
				directRegistration = e.To
			}
			if strings.HasSuffix(e.From, "::PanicIndirectRecovery") {
				indirectRegistration = e.To
			}
			if strings.HasSuffix(e.From, "::PanicClosureRecovery") {
				closureRegistration = e.To
			}
		}
		if e.Kind == "panic_site" && strings.HasSuffix(e.From, "::PanicRecovered") {
			panicSite = e.To
		}
		bare = bare || e.Kind == "panic_site" && strings.HasSuffix(e.From, "::PanicBare")
	}
	direct, panicPath, closure := false, false, false
	for _, e := range r.Relationships {
		if e.Kind == "cleanup_recover_candidate" && e.To == recoverSite {
			if e.From == indirectRegistration {
				t.Fatal("ordinary nested helper recover was treated as direct deferred recovery")
			}
			direct = direct || e.From == directRegistration
		}
		panicPath = panicPath || e.From == directRegistration && e.Kind == "cleanup_exit_candidate" && e.To == panicSite
		closure = closure || e.From == closureRegistration && e.Kind == "cleanup_recover_candidate" && e.To == closureSite
	}
	if recoverSite == "" || directRegistration == "" || indirectRegistration == "" || closureRegistration == "" || closureSite == "" || !closure || !direct || !bare || !panicPath {
		t.Fatalf("panic/recovery investigation path missing: recover=%s direct=%t bare=%t panicPath=%t closure=%t", recoverSite, direct, bare, panicPath, closure)
	}
}
