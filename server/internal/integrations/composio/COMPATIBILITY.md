# Native gateway compatibility development

Official Composio remains the default. To select the experimental local
multica-core-v1 gateway, configure COMPOSIO_BACKEND=compat,
COMPOSIO_BASE_URL (ending in /api/v3.1), COMPOSIO_MCP_ORIGIN (origin only),
and the existing project key/callback/state-secret settings. HTTPS is
required; COMPOSIO_ALLOW_LOOPBACK=true admits HTTP on literal loopback IPs
for isolated development. No redirect or retry is allowed on compat control
requests. There is no automatic backend fallback.

The authenticated `/api/integrations/composio/verify` route accepts a short
verification ticket. Its subject comes exclusively from Auth's verified JWT
session-cookie context; bearer JWTs, personal access tokens and machine
credentials cannot substitute for browser login. A browser without a session
must sign in before reopening the verification URL. No unverified activation
fallback is provided. A dedicated login-continuation UI is not implemented.

The server completes the gateway transaction, validates the returned callback
origin/path and signed state, and performs existing account ownership checks
before saving the mirror. Compat additionally requires ACTIVE status and
matching toolkit plus both auth-config identity representations. Failed local
mirror persistence can retry the same short-lived ticket and retrieve the
same gateway completion without repeating activation. After ticket expiry,
reconciliation remains an operator/background-worker concern; no retry may
resurrect a revoked mapping.

Compat MCP responses must include exactly one session Authorization bearer.
The URL must match the configured MCP origin and the response's session ID.
The project key is never returned by MCPAuthHeaders in compat mode. Official
mode retains its current header behavior. The session response alone is not
permission to run: claim-time policy validation and lifecycle revocation are
still being implemented in the gateway integration.

Validation here uses real SDK parsing and real Auth JWT-cookie middleware
with isolated upstream/store fixtures. The companion gateway fork separately
runs real SDK + gateway + PostgreSQL + native OpenConnector HTTP integration
with an offline OAuth exchange. Neither is a live provider or daemon E2E.
