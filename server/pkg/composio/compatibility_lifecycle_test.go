package composio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompatibilityLifecycleNeverUsesOfficialBackend(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client, err := NewClient(Options{APIKey: "fixture-control", BaseURL: server.URL + "/api/v3.1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = client.SyncCompatibilityPolicy(ctx, "agent", CompatibilityPolicy{OwnerUserID: "owner", SourceRevision: 1}); err == nil {
		t.Fatal("official policy request sent")
	}
	if err = client.RevokeCompatibilityTask(ctx, "task"); err == nil {
		t.Fatal("official revoke request sent")
	}
	if _, err = client.CreateSession(ctx, CreateSessionRequest{UserID: "owner", CompatContext: &CompatibilityContext{TaskID: "task"}}); err == nil {
		t.Fatal("official private context sent")
	}
	if calls != 0 {
		t.Fatalf("official backend received %d private requests", calls)
	}
}
