package composio

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type IdentityCompletion struct {
	ConnectedAccountID string `json:"connected_account_id"`
	CallbackURL        string `json:"callback_url"`
}

// VerifyCompatibilityIdentity sends a subject obtained from the caller's real
// login session. This extension is never sent to the official backend.
func (c *Client) VerifyCompatibilityIdentity(ctx context.Context, ticket, subject string) (*IdentityCompletion, error) {
	if c.backend != "compat" || ticket == "" || subject == "" {
		return nil, errors.New("composio: compatibility verification unavailable")
	}
	var out IdentityCompletion
	if err := c.do(c.newRequest(ctx).SetBody(map[string]string{"verify_ticket": ticket, "subject": subject}), http.MethodPost, "/internal/connect/complete", &out); err != nil {
		return nil, err
	}
	if out.ConnectedAccountID == "" || out.CallbackURL == "" {
		return nil, errors.New("composio: invalid verification response")
	}
	return &out, nil
}

// CompatibilitySessionHeaders rejects key fallback and destination/header
// injection before any session credential can enter a task overlay.
func (c *Client) CompatibilitySessionHeaders(resp *CreateSessionResponse, origin string, loopback bool) (map[string]string, error) {
	denied := errors.New("composio: invalid compatibility session")
	if c.backend != "compat" || resp == nil || resp.SessionID == "" || strings.ContainsAny(resp.SessionID, "/?#\\") || resp.MCP.Type != "http" {
		return nil, denied
	}
	u, err := url.Parse(resp.MCP.URL)
	allowed, e := url.Parse(origin)
	if err != nil || e != nil || !safeCompatibilityURL(u, loopback) || !safeCompatibilityURL(allowed, loopback) || allowed.Path != "" || allowed.RawQuery != "" || u.Scheme != allowed.Scheme || u.Host != allowed.Host || u.Path != "/sessions/"+resp.SessionID+"/mcp" || u.RawQuery != "" || len(resp.MCP.Headers) != 1 {
		return nil, denied
	}
	value, ok := resp.MCP.Headers["Authorization"]
	token, prefix := strings.CutPrefix(value, "Bearer ")
	if !ok || !prefix || len(token) < 32 || token == c.apiKey || strings.ContainsAny(token, " \t\r\n,") {
		return nil, denied
	}
	return map[string]string{"Authorization": value}, nil
}
