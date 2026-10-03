package composio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompatibilitySessionRejectsFallbackAndDestinationInjection(t *testing.T) {
	key := strings.Repeat("k", 40)
	client, err := NewClient(Options{APIKey: key, Backend: "compat", BaseURL: "https://control.example/api/v3.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.MCPAuthHeaders()) != 0 {
		t.Fatal("compat returned project headers")
	}
	for name, change := range map[string]func(*CreateSessionResponse){
		"valid":          func(*CreateSessionResponse) {},
		"no headers":     func(r *CreateSessionResponse) { r.MCP.Headers = nil },
		"project key":    func(r *CreateSessionResponse) { r.MCP.Headers["Authorization"] = "Bearer " + key },
		"extra header":   func(r *CreateSessionResponse) { r.MCP.Headers["x-api-key"] = key },
		"foreign origin": func(r *CreateSessionResponse) { r.MCP.URL = "https://evil.example/sessions/s/mcp" },
		"query secret":   func(r *CreateSessionResponse) { r.MCP.URL += "?token=secret" },
		"wrong session":  func(r *CreateSessionResponse) { r.SessionID = "other" },
		"path injection": func(r *CreateSessionResponse) { r.SessionID = "../s" },
	} {
		t.Run(name, func(t *testing.T) {
			r := &CreateSessionResponse{SessionID: "s", MCP: MCPDescriptor{Type: "http", URL: "https://mcp.example/sessions/s/mcp", Headers: map[string]string{"Authorization": "Bearer " + strings.Repeat("t", 40)}}}
			change(r)
			_, err := client.CompatibilitySessionHeaders(r, "https://mcp.example", false)
			if (err == nil) != (name == "valid") {
				t.Fatalf("validation %v", err)
			}
		})
	}
	official, _ := NewClient(Options{APIKey: key})
	if official.MCPAuthHeaders()["x-api-key"] != key {
		t.Fatal("official behavior changed")
	}
	if _, err = official.VerifyCompatibilityIdentity(context.Background(), "ticket", "subject"); err == nil {
		t.Fatal("official sent private extension")
	}
}

func TestCompatibilityIdentityUsesControlChannelAndRefusesRedirect(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v3.1/internal/connect/complete" || r.Header.Get("x-api-key") != "control" {
			t.Error("wrong boundary")
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["subject"] != "login-user" {
			t.Error("wrong identity")
		}
		http.Redirect(w, r, "https://evil.example/", http.StatusFound)
	}))
	defer server.Close()
	client, err := NewClient(Options{APIKey: "control", Backend: "compat", BaseURL: server.URL + "/api/v3.1", AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.VerifyCompatibilityIdentity(context.Background(), "ticket", "login-user"); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls != 1 {
		t.Fatal("identity completion retried")
	}
}
