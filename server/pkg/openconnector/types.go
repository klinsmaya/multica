package openconnector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Auth type values seen in provider auth lists. Unknown values must be tolerated.
const (
	AuthTypeOAuth2           = "oauth2"
	AuthTypeAPIKey           = "api_key"
	AuthTypeCustomCredential = "custom_credential"
	AuthTypeNoAuth           = "no_auth"
)

// Risk values of an authorization option. Unknown values must be tolerated.
const (
	RiskStandard    = "standard"
	RiskSensitive   = "sensitive"
	RiskDestructive = "destructive"
)

// Connection request statuses.
const (
	RequestStatusInitiated = "initiated"
	RequestStatusConnected = "connected"
	RequestStatusFailed    = "failed"
	RequestStatusExpired   = "expired"
)

// Connection statuses accepted by ListConnections.
const (
	ConnectionStatusActive         = "active"
	ConnectionStatusReauthRequired = "reauth_required"
	ConnectionStatusError          = "error"
	ConnectionStatusDisconnected   = "disconnected"
)

// Category is a provider category.
type Category struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// Provider is one entry of the provider catalog (GET /v1/providers).
type Provider struct {
	Service     string     `json:"service"`
	DisplayName string     `json:"displayName"`
	IconURL     *string    `json:"iconUrl"` // null for most providers
	HomepageURL string     `json:"homepageUrl"`
	Categories  []Category `json:"categories"`
	Scenario    string     `json:"scenario"`
	// AuthTypes may hold several values, e.g. oauth2 and api_key.
	AuthTypes []string `json:"authTypes"`
}

// SupportsAuthType reports whether the provider lists the auth type.
func (p Provider) SupportsAuthType(t string) bool { return containsString(p.AuthTypes, t) }

// Field is an input field of an API key or custom credential form.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	InputType   string `json:"inputType"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	Placeholder string `json:"placeholder,omitempty"`
	Description string `json:"description,omitempty"`
}

// AuthorizationOption is a selectable OAuth scope group.
type AuthorizationOption struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Description     string   `json:"description"`
	Required        bool     `json:"required"`
	DefaultSelected bool     `json:"defaultSelected"`
	Risk            string   `json:"risk"`
	Requires        []string `json:"requires,omitempty"`
}

// AuthOption is one element of ProviderSetup.Auth. Which fields are set depends on Type.
type AuthOption struct {
	Type string `json:"type"`
	// oauth2
	ClientFields         []Field               `json:"clientFields,omitempty"`
	Scopes               []string              `json:"scopes,omitempty"`
	AuthorizationOptions []AuthorizationOption `json:"authorizationOptions,omitempty"`
	// api_key and custom_credential
	Fields []Field `json:"fields,omitempty"`
	// custom_credential
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// DefaultAuthorizationOptionIDs returns the minimal option set: required or
// default-selected options, excluding destructive ones. Sending no option IDs
// to BeginOAuthConnect would request every scope.
func (a AuthOption) DefaultAuthorizationOptionIDs() []string {
	return DefaultAuthorizationOptionIDs(a.AuthorizationOptions)
}

// DefaultAuthorizationOptionIDs is the free-function form of
// AuthOption.DefaultAuthorizationOptionIDs. The result is never nil.
func DefaultAuthorizationOptionIDs(opts []AuthorizationOption) []string {
	ids := []string{}
	for _, o := range opts {
		if (o.Required || o.DefaultSelected) && o.Risk != RiskDestructive {
			ids = append(ids, o.ID)
		}
	}
	return ids
}

// OAuthClient describes the OAuth client configuration state of a provider.
type OAuthClient struct {
	Configured            bool     `json:"configured"`
	CustomClientAvailable bool     `json:"customClientAvailable"`
	ExpectedRedirectURI   string   `json:"expectedRedirectUri"`
	MissingFields         []string `json:"missingFields"`
}

// ProviderSetup is the data of GET /v1/providers/:service/setup.
type ProviderSetup struct {
	Service string       `json:"service"`
	Auth    []AuthOption `json:"auth"`
	// OAuthClient is nil for providers without oauth2.
	OAuthClient *OAuthClient `json:"oauthClient,omitempty"`
}

// FindAuth returns the first auth option of the given type.
func (s *ProviderSetup) FindAuth(authType string) (AuthOption, bool) {
	for _, a := range s.Auth {
		if a.Type == authType {
			return a, true
		}
	}
	return AuthOption{}, false
}

// OAuthConfigAuth is the subset of the oauth2 auth description in OAuthConfig.
type OAuthConfigAuth struct {
	Type                 string                `json:"type"`
	Scopes               []string              `json:"scopes,omitempty"`
	AuthorizationOptions []AuthorizationOption `json:"authorizationOptions,omitempty"`
}

// OAuthConfig is one element of GET /api/oauth/configs (the full list of OAuth
// providers, configured or not) and the result of PutOAuthConfig.
type OAuthConfig struct {
	Service               string          `json:"service"`
	Configured            bool            `json:"configured"`
	CustomClientAvailable bool            `json:"customClientAvailable"`
	ClientID              *string         `json:"clientId"`
	ExpectedRedirectURI   string          `json:"expectedRedirectUri"`
	RedirectURI           *string         `json:"redirectUri"`
	MissingFields         []string        `json:"missingFields"`
	Auth                  OAuthConfigAuth `json:"auth"`
	RequestedScopes       []string        `json:"requestedScopes"`
	EffectiveScopes       []string        `json:"effectiveScopes"`
}

// OAuthConfigInput is the body of PutOAuthConfig.
type OAuthConfigInput struct {
	ClientID string `json:"clientId"`
	// ClientSecret is a secret; String and slog output redact it.
	ClientSecret string `json:"clientSecret"`
}

// String redacts the client secret.
func (i OAuthConfigInput) String() string {
	return fmt.Sprintf("OAuthConfigInput{ClientID:%q ClientSecret:REDACTED}", i.ClientID)
}

// GoString redacts the client secret.
func (i OAuthConfigInput) GoString() string { return i.String() }

// LogValue redacts the client secret in slog output.
func (i OAuthConfigInput) LogValue() slog.Value {
	return slog.GroupValue(slog.String("client_id", i.ClientID), slog.String("client_secret", "REDACTED"))
}

// BeginOAuthConnectRequest is the body of BeginOAuthConnect.
type BeginOAuthConnectRequest struct {
	// ReturnURI receives the 302 redirect after the callback
	// (?status=success|error&service=...). Do not trust its query for writes.
	ReturnURI string
	// AuthorizationOptionIDs selects scope options. A nil slice is omitted
	// from the request, which makes the server request ALL scopes (including
	// destructive ones); pass DefaultAuthorizationOptionIDs instead.
	AuthorizationOptionIDs []string
}

func (r BeginOAuthConnectRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if r.ReturnURI != "" {
		m["returnUri"] = r.ReturnURI
	}
	if r.AuthorizationOptionIDs != nil {
		m["authorizationOptionIds"] = r.AuthorizationOptionIDs
	}
	return json.Marshal(m)
}

// ConnectionRequest is the result of BeginOAuthConnect.
type ConnectionRequest struct {
	AuthorizationURL    string    `json:"authorizationUrl"`
	StateHandle         string    `json:"stateHandle"`
	ConnectionRequestID string    `json:"connectionRequestId"`
	Status              string    `json:"status"`
	ExpiresAt           time.Time `json:"expiresAt"`
}

// ConnectionRequestStatus is the polled state of a connection request.
type ConnectionRequestStatus struct {
	ConnectionRequestID string
	Service             string
	// Status is one of the RequestStatus* constants; unknown values are passed through.
	Status string
	// AppID is the connection ID once Status is connected.
	AppID        *string
	ErrorCode    *string // e.g. request_superseded, invalid_input
	ErrorMessage *string
	ExpiresAt    time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UnmarshalJSON accepts ISO strings and millisecond numbers for the time fields.
func (s *ConnectionRequestStatus) UnmarshalJSON(b []byte) error {
	var w struct {
		ConnectionRequestID string          `json:"connectionRequestId"`
		Service             string          `json:"service"`
		Status              string          `json:"status"`
		AppID               *string         `json:"appId"`
		ErrorCode           *string         `json:"errorCode"`
		ErrorMessage        *string         `json:"errorMessage"`
		ExpiresAt           json.RawMessage `json:"expiresAt"`
		CreatedAt           json.RawMessage `json:"createdAt"`
		UpdatedAt           json.RawMessage `json:"updatedAt"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	var err error
	out := ConnectionRequestStatus{
		ConnectionRequestID: w.ConnectionRequestID, Service: w.Service, Status: w.Status,
		AppID: w.AppID, ErrorCode: w.ErrorCode, ErrorMessage: w.ErrorMessage,
	}
	if out.ExpiresAt, err = parseFlexTime(w.ExpiresAt); err != nil {
		return fmt.Errorf("expiresAt: %w", err)
	}
	if out.CreatedAt, err = parseFlexTime(w.CreatedAt); err != nil {
		return fmt.Errorf("createdAt: %w", err)
	}
	if out.UpdatedAt, err = parseFlexTime(w.UpdatedAt); err != nil {
		return fmt.Errorf("updatedAt: %w", err)
	}
	*s = out
	return nil
}

// parseFlexTime parses an ISO-8601 string or epoch milliseconds. Missing or
// null values yield the zero time.
func parseFlexTime(raw json.RawMessage) (time.Time, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return time.Time{}, err
		}
		if s == "" {
			return time.Time{}, nil
		}
		return time.Parse(time.RFC3339Nano, s)
	}
	var ms float64
	if err := json.Unmarshal(raw, &ms); err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(int64(ms)).UTC(), nil
}

// APIKeyConnectRequest is the body of ConnectWithAPIKey. The server rejects
// unknown fields and offers no way to choose the connection name.
type APIKeyConnectRequest struct {
	// APIKey is forwarded as-is; do not store or log it.
	APIKey  string            `json:"apiKey"`
	Extra   map[string]string `json:"extra,omitempty"`
	Comment string            `json:"comment,omitempty"`
}

// String redacts the API key.
func (r APIKeyConnectRequest) String() string { return "APIKeyConnectRequest{APIKey:REDACTED}" }

// GoString redacts the API key.
func (r APIKeyConnectRequest) GoString() string { return r.String() }

// LogValue redacts the API key in slog output.
func (r APIKeyConnectRequest) LogValue() slog.Value { return slog.StringValue(r.String()) }

// Connection is a stored connection (/v1/connections*, /v1/apps shape).
type Connection struct {
	ID                string `json:"id"`
	ProviderAccountID string `json:"providerAccountId"`
	Service           string `json:"service"`
	Status            string `json:"status"`
	// ConnectionName is the alias used by MCP calls. For API key connections it
	// is a server-generated UUID.
	ConnectionName string   `json:"alias"`
	AuthType       string   `json:"authType"`
	DisplayName    string   `json:"displayName"`
	AccountLabel   string   `json:"accountLabel"`
	IsDefault      bool     `json:"isDefault"`
	Scopes         []string `json:"scopes"`
	Comment        *string  `json:"comment"`
}

// CreateRuntimeTokenRequest is the body of CreateRuntimeToken. Nil slices are
// serialized as [] because omitting a field resets or rejects it.
type CreateRuntimeTokenRequest struct {
	Name               string
	AllowedActions     []string
	BlockedActions     []string
	AllowedProxies     []string
	AllowedTriggers    []string
	AllowedConnections []string
}

func (r CreateRuntimeTokenRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name               string   `json:"name"`
		AllowedActions     []string `json:"allowedActions"`
		BlockedActions     []string `json:"blockedActions"`
		AllowedProxies     []string `json:"allowedProxies"`
		AllowedTriggers    []string `json:"allowedTriggers"`
		AllowedConnections []string `json:"allowedConnections"`
	}{
		r.Name,
		nonNil(r.AllowedActions), nonNil(r.BlockedActions), nonNil(r.AllowedProxies),
		nonNil(r.AllowedTriggers), nonNil(r.AllowedConnections),
	})
}

// RuntimeToken is the stored record of a runtime token (never includes the plaintext).
type RuntimeToken struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	AllowedActions     []string  `json:"allowedActions"`
	BlockedActions     []string  `json:"blockedActions"`
	AllowedProxies     []string  `json:"allowedProxies"`
	AllowedConnections []string  `json:"allowedConnections"`
	AllowedTriggers    []string  `json:"allowedTriggers"`
	CreatedAt          time.Time `json:"createdAt"`
}

// CreatedRuntimeToken is the result of CreateRuntimeToken.
type CreatedRuntimeToken struct {
	// Token is the plaintext bearer token, returned exactly once. Sensitive:
	// never log it, store it in the database, or return it from an API.
	// String, GoString and slog output redact it.
	Token  string       `json:"token"`
	Record RuntimeToken `json:"record"`
}

// String redacts the token.
func (t CreatedRuntimeToken) String() string {
	return fmt.Sprintf("CreatedRuntimeToken{ID:%s Name:%q Token:REDACTED}", t.Record.ID, t.Record.Name)
}

// GoString redacts the token.
func (t CreatedRuntimeToken) GoString() string { return t.String() }

// LogValue redacts the token in slog output.
func (t CreatedRuntimeToken) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", t.Record.ID), slog.String("name", t.Record.Name), slog.String("token", "REDACTED"))
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}
