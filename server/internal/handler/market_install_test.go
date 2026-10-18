package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/marketinstall"
)

func TestCommitMarketOperationDisabled(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/market/operations", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.CommitMarketOperation(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestEveryBlockReasonHasStaleCode(t *testing.T) {
	seen := map[blockReason]struct{}{}
	for _, reason := range allBlockReasons() {
		seen[reason] = struct{}{}
		if _, ok := staleCodeFor(reason); !ok {
			t.Errorf("missing stale code for %s", reason)
		}
	}
	if len(seen) != len(allBlockReasons()) {
		t.Fatalf("duplicate block reasons: %d unique of %d", len(seen), len(allBlockReasons()))
	}
}

func TestOperationViewOmitsCatalogBytes(t *testing.T) {
	body, err := json.Marshal(operationView(marketinstall.Operation{
		ID:       "00000000-0000-4000-8000-000000000001",
		PlanHash: "sha256:abc",
		Steps: []marketinstall.Step{{
			Key: "agent", Kind: marketinstall.KindAgent, Phase: marketinstall.PhaseApplied,
			Name: "Reviewer",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, hidden := range []string{"digest", "instructions", "payload", "custom_env"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("response contains %s: %s", hidden, text)
		}
	}
}

func TestChoosePreflightIsStaleMapping(t *testing.T) {
	code := staleForReport(preflightReport{Model: modelCheck{Status: checkChoose}})
	if code != marketinstall.CodeStaleMapping {
		t.Fatalf("code = %s", code)
	}
}
