package openconnector

import (
	"context"
	"net/http"
)

// ListProviders returns the provider catalog (GET /v1/providers). Unpaginated.
func (c *Client) ListProviders(ctx context.Context) ([]Provider, error) {
	var out []Provider
	if err := c.do(c.newRequest(ctx), http.MethodGet, "/v1/providers", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetProviderSetup returns connect-time information for a service.
func (c *Client) GetProviderSetup(ctx context.Context, service string) (*ProviderSetup, error) {
	var out ProviderSetup
	if err := c.do(c.newRequest(ctx), http.MethodGet, "/v1/providers/"+esc(service)+"/setup", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListOAuthConfigs returns every OAuth provider (configured or not). Filter on
// Configured, or use ConfiguredOAuthServices.
func (c *Client) ListOAuthConfigs(ctx context.Context) ([]OAuthConfig, error) {
	var out []OAuthConfig
	if err := c.do(c.newRequest(ctx), http.MethodGet, "/api/oauth/configs", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ConfiguredOAuthServices returns the set of services whose OAuth client is configured.
func (c *Client) ConfiguredOAuthServices(ctx context.Context) (map[string]struct{}, error) {
	cfgs, err := c.ListOAuthConfigs(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, cfg := range cfgs {
		if cfg.Configured {
			set[cfg.Service] = struct{}{}
		}
	}
	return set, nil
}

// PutOAuthConfig sets the OAuth client id and secret of a service. The response does not echo the secret.
func (c *Client) PutOAuthConfig(ctx context.Context, service string, in OAuthConfigInput) (*OAuthConfig, error) {
	var out OAuthConfig
	req := c.newRequest(ctx).SetBody(in)
	if err := c.do(req, http.MethodPut, "/api/oauth/configs/"+esc(service), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BeginOAuthConnect starts an OAuth connection. A new request for a service
// supersedes any pending one (the old request becomes failed/request_superseded).
func (c *Client) BeginOAuthConnect(ctx context.Context, service string, in BeginOAuthConnectRequest) (*ConnectionRequest, error) {
	var out ConnectionRequest
	req := c.newRequest(ctx).SetBody(in)
	if err := c.do(req, http.MethodPost, "/v1/connections/"+esc(service)+"/connect", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetConnectionRequest returns the state of a connection request.
func (c *Client) GetConnectionRequest(ctx context.Context, id string) (*ConnectionRequestStatus, error) {
	var out ConnectionRequestStatus
	if err := c.do(c.newRequest(ctx), http.MethodGet, "/v1/connection-requests/"+esc(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ConnectWithAPIKey creates a connection from an API key. The returned
// connection is not the default and its name is a server-generated UUID.
func (c *Client) ConnectWithAPIKey(ctx context.Context, service string, in APIKeyConnectRequest) (*Connection, error) {
	var out Connection
	req := c.newRequest(ctx).SetBody(in)
	if err := c.do(req, http.MethodPost, "/v1/connections/"+esc(service)+"/connect/api-key", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetConnectionByID returns one stored connection; a missing one yields ErrNotFound.
func (c *Client) GetConnectionByID(ctx context.Context, appID string) (*Connection, error) {
	var out Connection
	if err := c.do(c.newRequest(ctx), http.MethodGet, "/v1/connections/by-id/"+esc(appID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListConnections lists stored connections (virtual no_auth ones excluded).
// status is optional: active, reauth_required, error or disconnected.
func (c *Client) ListConnections(ctx context.Context, status string) ([]Connection, error) {
	req := c.newRequest(ctx)
	if status != "" {
		req.SetQueryParam("status", status)
	}
	var out []Connection
	if err := c.do(req, http.MethodGet, "/v1/connections", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteConnection removes a connection, addressed by service and name (alias).
// It is idempotent: the server answers 200 for unknown connections, and a 404
// is also treated as success.
func (c *Client) DeleteConnection(ctx context.Context, service, connectionName string, revoke bool) error {
	body := struct {
		ConnectionName string `json:"connectionName"`
		Revoke         bool   `json:"revoke"`
	}{connectionName, revoke}
	req := c.newRequest(ctx).SetBody(body)
	err := c.do(req, http.MethodDelete, "/api/connections/"+esc(service), nil)
	if err != nil && isNotFound(err) {
		return nil
	}
	return err
}

// CreateRuntimeToken issues a runtime token. Token plaintext is only in this response.
func (c *Client) CreateRuntimeToken(ctx context.Context, in CreateRuntimeTokenRequest) (*CreatedRuntimeToken, error) {
	var out CreatedRuntimeToken
	req := c.newRequest(ctx).SetBody(in)
	if err := c.do(req, http.MethodPost, "/api/runtime-tokens", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListRuntimeTokens lists token records (no plaintext), newest first.
func (c *Client) ListRuntimeTokens(ctx context.Context) ([]RuntimeToken, error) {
	var out []RuntimeToken
	if err := c.do(c.newRequest(ctx), http.MethodGet, "/api/runtime-tokens", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteRuntimeToken revokes a token. A repeated delete returns an error
// matching ErrNotFound.
func (c *Client) DeleteRuntimeToken(ctx context.Context, id string) error {
	return c.do(c.newRequest(ctx), http.MethodDelete, "/api/runtime-tokens/"+esc(id), nil)
}
