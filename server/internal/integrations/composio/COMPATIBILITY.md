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
permission to run: claim-time policy validation and lifecycle revocation are described below.

Validation here uses real SDK parsing and real Auth JWT-cookie middleware
with isolated upstream/store fixtures. The companion gateway fork separately
runs real SDK + gateway + PostgreSQL + native OpenConnector HTTP integration
with an offline OAuth exchange. Neither is a live provider or daemon E2E.

## Claim-time lifecycle implementation

Compat now defers token issuance until daemon claim. The handler uses current
invocation permission and strips the reserved saved `composio` MCP entry; errors
cannot fall back to a project-key entry or enqueue-time session. The service
registers the task durably, locks source authorization rows through registration,
rechecks owner/actor membership and invocation, synchronizes exact policy, then
creates a task-bound session. A database sequence orders policy snapshots.

Configure a separate random COMPOSIO_AUTHORITY_TOKEN (at least 32 characters).
Gateway `-source-origin` and `-source-token-file` reference this Multica instance
and the same dedicated token. `/internal/composio/authorize` accepts only this
service credential. Its boolean verdict reads current task, Agent, owner/actor
membership, invocation targets, connection pins and persistent task tombstones.
No permission cache or credential fallback is used.

Migrations 469–473 add registry/outbox and separate concurrent indexes without
foreign keys/cascades. Source terminal transitions, deletion, permission changes,
connection changes and relevant membership removal preserve outbox work. No-op
changes and unrelated workspaces/toolkits do not revoke tasks. `RunLifecycle`
retries gateway revocation with leases. Source authority rejects new admission
without waiting for this worker. A revoked run is never reopened: restart as a
new task ID after restoring permission. Sessions expire after one hour; running
CLIs do not receive a long-lived refresh token.

Tests use migrated isolated PostgreSQL plus actual gateway and native OC
subprocesses. They exercise daemon claim, prompt secret exclusion, source
withdrawal before outbox delivery, unexpired session revocation and retained
runtime-token ciphertext. They do not launch a third-party Agent CLI or prove
real-provider OAuth/revocation. Native non-destructive provider revoke and full
upgrade/restore/rollback acceptance remain outstanding.

## Local disconnect and remote cleanup

Compatibility disconnect now commits a revoked local mirror and a durable
connection outbox before network I/O. The endpoint returns 202 with
`local_disabled: true` and `remote_revocation` until remote cleanup is confirmed.
The `/api/integrations/composio/status` endpoint is owner-scoped and reports the
backend plus unresolved cleanup. Settings distinguish pending, unsupported and
blocked remote revocation from local disable. A replayed callback cannot revive
a connection with a disconnect tombstone. The gateway's confirmed DELETED
record supports retry after a lost cleanup response; unknown IDs are not success.

Migrations 474–475 add the connection outbox and a separate concurrent index.
They have no foreign keys/cascades. Pending native/provider recovery must be
reconciled before any rollback that would remove these tombstones. Official
mode retains its original SDK and disconnect contracts. Do not automatically
copy compatibility credentials into an official project.

Validation covers the actual daemon claim handler, real gateway/native OC,
PostgreSQL and offline provider fixtures. Target Agent CLIs, real provider grant
revocation and a full production rollout remain unverified. See the sibling OC
checkout's `gateway/docs/t09-t12-evidence.md` and `gateway/ops/RUNBOOK.md`.
