# openconnector

A small, standalone Go SDK for the admin REST API of a self-hosted
[open-connector](https://github.com/oomol-lab/open-connector) server. Its only
third-party dependency is `github.com/go-resty/resty/v2`; it imports no other
Multica package. The MCP endpoint is intentionally not wrapped: agents connect
to `/mcp` directly with a runtime token (header `Accept: application/json,
text/event-stream`, responses are single-event SSE).

## Usage

```go
c, err := openconnector.NewClient(openconnector.Options{
    BaseURL:    "http://127.0.0.1:3000",
    AdminToken: os.Getenv("OOMOL_CONNECT_ADMIN_TOKEN"), // sent as Authorization: Bearer
})

providers, _ := c.ListProviders(ctx)
setup, _ := c.GetProviderSetup(ctx, "github")
oauth, _ := setup.FindAuth(openconnector.AuthTypeOAuth2)

req, _ := c.BeginOAuthConnect(ctx, "github", openconnector.BeginOAuthConnectRequest{
    ReturnURI:              "https://app.example.com/callback",
    AuthorizationOptionIDs: oauth.DefaultAuthorizationOptionIDs(), // never omit, see below
})
st, _ := c.GetConnectionRequest(ctx, req.ConnectionRequestID) // poll, >= 2s apart

tok, _ := c.CreateRuntimeToken(ctx, openconnector.CreateRuntimeTokenRequest{
    Name: "task-123", AllowedActions: []string{"github.*"},
    AllowedConnections: []string{conn.ID},
})
defer c.DeleteRuntimeToken(ctx, tok.Record.ID)
```

## Response shapes differ by path family

| Path family | Success | Failure |
| --- | --- | --- |
| `/v1/*` | `{"success":true,"data":...,"meta":{}}` | `{"success":false,"message","errorCode","data":null}` (HTTP status is usually non-2xx, but `success=false` on 2xx is also handled) |
| `/api/*` | bare JSON object or array | `{"error":{"code","message"}}` |

The client unwraps both and returns `*APIError{Status, Code, Message}` for every
failure. `errors.Is(err, ErrNotFound | ErrUnauthorized | ErrForbidden)` matches
by HTTP status. Errors never contain request headers or bodies.

## Behaviors to know

- `GET /v1/providers` is the catalog (`iconUrl` is mostly null). A provider can
  list several auth types; iterate `ProviderSetup.Auth` instead of assuming one.
- `GET /api/oauth/configs` returns every OAuth provider (about 118 KB); filter
  on `Configured` or use `ConfiguredOAuthServices`.
- Omitting `AuthorizationOptionIDs` requests all scopes, including destructive
  ones. Pass `DefaultAuthorizationOptionIDs()` (required or default-selected,
  non-destructive).
- A new `BeginOAuthConnect` for a service supersedes the pending request of the
  same service (old one becomes `failed` / `request_superseded`); the connector
  has a single owner, so serialize connects per service.
- `GetConnectionRequest` time fields mix epoch milliseconds (`createdAt`,
  `updatedAt`) and ISO strings (`expiresAt`); both become `time.Time`.
- API key connections get a random UUID name (`Connection.ConnectionName`,
  JSON `alias`) and `IsDefault=false`; MCP calls must pass that name.
- `DeleteConnection` is addressed by service + name and is idempotent (the
  server answers 200 even for unknown ones). `DeleteRuntimeToken` is not: a
  repeated delete returns `ErrNotFound`.
- Runtime token lists are always serialized, nil becoming `[]`, because
  omitting `allowedTriggers` resets it. `allowedConnections` IDs are not
  validated by the server.
- `CreatedRuntimeToken.Token` is shown once. `String()`, `%+v`, `%#v` and slog
  output redact it, as they do for `OAuthConfigInput` and `APIKeyConnectRequest`.
