# M01 Multica consumer capability

Checked at commit `18fa9f728a9e25f2465df2789631edb2a75442ad` (`git rev-parse HEAD`).

This table is the M01 evidence for using Multica as a market consumer. The proposed prefix `/market-runtime/v1` is not in the repository. A search for `market-runtime` returned no matches.

## How to read a status

The status words are Supported, Partial, Missing, and UNKNOWN.

Supported means the cited handler or schema does the thing. Partial means a related behavior exists and a market installer cannot treat it as the plan contract. Missing means that contract behavior is absent. UNKNOWN means this pass did not read the code or run the test that would prove the row.

A command under Verification was executed in this pass. A test name with "not run" was only located.

## Verification

Both commands were run from `server/` and exited 0.

```text
go test -count=1 -timeout 60s ./internal/service/ -run TestIsDuplicatePendingTaskErr
go test -count=1 -timeout 90s ./internal/middleware/ -run 'TestAuth_StripsClientSuppliedActorSource|TestAuth_StripsForgedAgentIdentityHeaders|TestRequestLogger_RedactsWebhookTokenInPath'
```

`TestIsDuplicatePendingTaskErr` checks the Go classifier for a Postgres unique violation. It does not open a database, so it does not prove that a second insert is rejected.

No Postgres, daemon, or HTTP server was started.

## Summary

| Plan question | Status | What a caller can do |
| --- | --- | --- |
| Create or bind an agent, skill, MCP server, squad, runtime, or credential | Partial | Workspace routes create the first five. There is no credential resource and no client install key. |
| Unique Task or Run creation on retry (A19) | Missing | Agent tasks have no caller key. An autopilot `Idempotency-Key` dedupes only while the quota gate is on, and nothing looks that key up. |
| Query and cancel when the outcome is unknown | Partial | Cancel by server task id sticks even if the daemon is down. A timed-out create cannot be found by a caller key. |
| Controlled credential storage | Partial | Some columns use AES-256-GCM. Agent env, MCP config, and autopilot signing secrets are plaintext. |
| Actor spoofing and task-token scope | Partial | Auth drops client actor headers. A task token can still call routes that mint a human PAT or CLI JWT. |
| Squad handoff (A20, A21) | Partial | Members keep their own agent rows. Assignment starts the leader only, and a failed briefing still claims the task as a normal agent turn. |
| Webhook, outbox, or event feed for an install projection (I04) | Missing | Inbound webhooks and internal plugin hooks exist. Nothing stores an external market callback URL. |

## 1. Objects an install can create or bind

Workspace routes sit behind session, personal-access-token, or task-token auth, then workspace membership. The actor is the authenticated user. Create bodies do not carry `owner_id` or `actor_id`.

| Object | Status | Create | Retry and lookup | Evidence |
| --- | --- | --- | --- | --- |
| Agent | Supported | `POST /api/agents` (`CreateAgent`) | Same workspace and name returns 409. The response does not include the existing row. `GET /api/agents/{id}` is UUID only. | `server/internal/handler/agent.go`, `server/cmd/server/router.go`, `server/migrations/046_agent_unique_name.up.sql`. `TestCreateAgent_RejectsDuplicateName` not run. |
| Skill | Supported | `POST /api/skills`. Bind with `POST /api/agents/{id}/skills/add`. | `UNIQUE(workspace_id, name)` on create. `POST /api/skills/import` with `on_conflict=skip` returns the existing skill by name. Bind uses `ON CONFLICT DO NOTHING`. | `server/internal/handler/skill.go`, `server/migrations/008_structured_skills.up.sql`. Import tests not run. |
| MCP server | Supported | Owner or admin `POST /api/workspaces/{id}/mcp-servers`. Bind with `POST /api/agents/{id}/mcp-servers`. | Library name is unique per workspace and a clash is 409. A repeated bind is a no-op. | `server/cmd/server/router.go`, `server/migrations/316_workspace_mcp_server_name_unique.up.sql`. HTTP tests not run. |
| Squad | Supported | `POST /api/squads` with `name` and `leader_id`. | Names are not unique. The same request creates another squad. Member add conflicts on `(squad_id, member_type, member_id)`. Lookup is UUID only. | `server/internal/handler/squad.go`, `server/migrations/087_squad_name_not_unique.up.sql`. HTTP tests not run. |
| Runtime | Partial | A daemon calls `POST /api/daemon/register`. An owner or admin creates a profile with `POST /api/workspaces/{id}/runtime-profiles`. | Register upserts on daemon id plus provider, or daemon id plus profile id. There is no user `POST /api/runtimes`. | `server/internal/handler/daemon.go`, `server/pkg/db/queries/runtime.sql`, `server/internal/handler/runtime_profile.go`. Register tests not run. |
| Credential | Partial | No credential resource. Agent env is `PUT /api/agents/{id}/env`. A plugin secret is stored on plugin config. `POST /api/tokens` mints a user PAT. | Env replace keeps a value when the client sends `****`. Plugin secrets upsert on `(installation_id, key)`. PAT names are not unique. | `server/migrations/040_agent_custom_env.up.sql`, `server/internal/handler/agent_env.go`, `server/internal/handler/personal_access_token.go`. HTTP tests not run. |

`POST /api/agents/mika` is a separate server-owned create, idempotent per workspace. `POST /api/cloud-runtime/nodes` proxies to an external fleet and does not insert `agent_runtime`.

## 2. Task and Run creation

The agent work row is `agent_task_queue`. `CreateAgentTask` inserts a server UUIDv7. The insert has no `idempotency_key`, `correlation_id`, or `client_request_id` (`server/pkg/db/queries/agent.sql`).

A second issue enqueue while a task is `queued` or `dispatched` for the same issue, agent, and comment thread hits `idx_one_pending_task_per_issue_agent_thread` (`server/migrations/452_agent_task_pending_thread_unique.up.sql`). That slot is not a caller request id. Chat send and quick-create do not use it, so a retry inserts another row. Comment create folds the new comment into the pending task. Assign ignores `ErrDuplicatePendingTask`.

`POST /api/autopilots/{id}/trigger` reads `Idempotency-Key` and mints one when the header is empty (`server/internal/handler/autopilot.go`). The key is stored on `autopilot_quota_reservation` only when the quota gate is on. With the gate off, `createAutopilotRunWithQuota` inserts a new run and drops the key (`server/internal/service/autopilot_quota.go`). A reused reservation returns the existing run and does not resume a dispatch that stopped after the run insert (`server/internal/service/autopilot.go`). The run row has no key column. No route looks up a run by that header. Two concurrent calls with the same key can hit the unique index and return HTTP 500.

`task_supplement.client_request_id` dedupes an extra message on a run that already exists (`server/internal/handler/task_supplement.go`). It does not create the run.

| Capability | Status | Retry behavior |
| --- | --- | --- |
| Idempotent agent-task create | Missing | Chat and quick-create insert another task. The pending-slot index is not a caller key. |
| Idempotent autopilot run | Partial | Same key returns the first run only while the quota gate is on. |
| Query by caller key | Missing | `GetAgentTask` and `GetAutopilotRun` filter on server id. |
| Unique creation on retry | Missing | An adapter cannot promise one task when the create response is lost. |

`TestIsDuplicatePendingTaskErr` passed. It only classifies the constraint name, including `idx_one_pending_task_per_issue_agent_thread`. The database tests named by the enqueue and quota paths were not run.

## 3. Query and cancel

| Capability | Status | Behavior | Evidence |
| --- | --- | --- | --- |
| Get one task by server id | Supported | Daemon `GET /api/daemon/tasks/{taskId}/status`. User routes list by issue or agent. There is no user `GET /api/tasks/{taskId}`. | `server/internal/handler/daemon.go`, `server/cmd/server/router.go`. Not run. |
| Get one autopilot run | Supported | `GET /api/autopilots/{id}/runs/{runId}`. | `server/internal/handler/autopilot.go`. Not run. |
| List | Supported | Lists are scoped to an issue, agent, autopilot, or wakeup. They do not filter on a caller key. | `server/pkg/db/queries/agent.sql`. Not run. |
| Cancel a task | Supported | `queued`, `dispatched`, `running`, `waiting_local_directory`, and `deferred` become `cancelled`. A second cancel of a terminal row reloads that row and returns it. The database status does not wait for the daemon. | `server/pkg/db/queries/agent.sql` `CancelAgentTask`, `server/internal/service/task.go`. `TestCancelTask_SameIssue_Succeeds` not run. |
| Unknown create | Missing | The insert is the only record. A client that times out after commit has no key to query. Retrying the original call can create another task. | `server/pkg/db/queries/agent.sql`. Not run. |
| Cancel an issue | Missing for in-flight runs | Setting an issue to `cancelled` does not cancel its tasks or child-issue tasks. Delete still cancels tasks whose `issue_id` is the deleted issue. | `server/internal/handler/issue.go`, `server/internal/service/task.go` `CancelTasksForIssue`. Not run. |

## 4. Credential storage

There is no single vault. `server/internal/util/secretbox/secretbox.go` is AES-256-GCM with one 32-byte env key and no key id.

| Store | Status | At rest | Read back |
| --- | --- | --- | --- |
| `plugin_secret.ciphertext`, several channel secrets, VCS access token and webhook secret | Supported | `secretbox` ciphertext. | Plugin GET returns names. The daemon credential route can return a decrypted value. VCS connect returns the webhook secret once. |
| PAT, task token, daemon token | Supported | Hash only. | The create response returns the raw secret once. |
| `agent.custom_env` | Partial | Plaintext JSONB (`server/migrations/040_agent_custom_env.up.sql`). | Agent JSON omits the map and returns key counts (`server/internal/handler/agent.go`). `GET /api/agents/{id}/env` returns the map to the owner or a workspace owner or admin, and writes an audit row of keys. |
| `agent.mcp_config`, `workspace_mcp_server.config` | Partial | Plaintext JSONB. | Agent list redacts MCP config for an agent actor and for a member who cannot view that agent's secrets (`server/internal/handler/agent.go`). A permitted member receives the map. Claim still sends env and MCP config to the daemon. |
| Autopilot `webhook_token` and `signing_secret` | Partial | Plaintext. Migration `093_webhook_deliveries.up.sql` says HMAC needs the cleartext. | Writers who can get the autopilot receive the webhook token. The signing secret is returned as a short hint. |

No workspace, agent, or skill export handler was found that selects `custom_env`, `ciphertext`, or `signing_secret`. Skill file content is stored as the caller saved it, so a secret pasted into a skill is returned by the skill file API.

`TestRequestLogger_RedactsWebhookTokenInPath` passed. The logger rewrites `/api/webhooks/autopilots/{token}` to `[redacted]`. It does not log `Authorization` or the request body. Other webhook paths are logged as given. Whether a reverse proxy stores full URLs or response bodies is UNKNOWN.

## 5. Workspace ACL and actor identity

`Auth` deletes client `X-Actor-Source`, `X-Agent-ID`, and `X-Task-ID` before it accepts the credential (`server/internal/middleware/auth.go`). `TestAuth_StripsClientSuppliedActorSource` and `TestAuth_StripsForgedAgentIdentityHeaders` passed. Both send a JWT with forged headers and observe empty headers in the next handler.

A `mat_` task token is minted at claim for 24 hours. The row stores a hash and binds task, agent, workspace, and the runtime owner (`server/internal/handler/daemon.go`). `Auth` then sets `X-User-ID` to that owner and `X-Actor-Source` to `task_token`. `resolveActor` returns `("agent", agent_id)` from those server headers (`server/internal/handler/handler.go`).

Workspace middleware returns 403 when a task token's workspace does not match the resolved workspace (`server/internal/middleware/workspace.go`). Membership is still the runtime owner's membership. Roles used on admin routes are `owner` and `admin`. A non-member does not pass `GetMemberByUserAndWorkspace`. The workspace-override test was not run.

`RequireHumanActor` rejects `task_token` and `cloud_pat` (`server/internal/handler/actor_guards.go`). `POST /api/tokens` and `POST /api/cli-token` are inside `Auth` and do not use that middleware (`server/cmd/server/router.go`). Both handlers mint a credential for `X-User-ID` (`CreatePersonalAccessToken`, `IssueCliToken`). A task token can therefore ask for a human PAT or a CLI JWT for the runtime owner. That HTTP call was not executed. `DaemonAuth` accepts `mdt_`, cloud PAT, and `mul_`. It has no `mat_` branch.

No market-adapter service key exists. The closest machine credentials are plugin tokens, daemon tokens, and cloud-node PATs.

`origin_type=quick_create` on issue create is client-supplied provenance. The creator still comes from `resolveActor`.

## 6. Squad and multi-agent handoff

A squad is a leader agent plus `squad_member` rows of type `agent` or `member` (`server/migrations/084_squad.up.sql`). Each member agent keeps its own instructions, model, runtime, env, and MCP config. Skills are the live `agent_skill` join. There is no per-member lock hash.

Assigning an issue to a squad enqueues one task for the leader (`enqueueSquadLeaderTask` in `server/internal/handler/squad.go`). `dispatchIssueRun` ignores the boolean that says whether that enqueue happened (`server/internal/handler/issue_trigger.go`). The assign handler then returns HTTP 200 (`server/internal/handler/issue.go`).

The leader is prompted to mention another agent. That mention creates a new `agent_task_queue` row. Claim mints a new task token for the target agent. The server does not copy the parent task token or the parent agent's env. Comment text and `handoff_note` are free text, so a caller can paste a secret. The server does not attach one.

If the squad is gone or the leader changed before claim, briefing injection is skipped, `IsLeaderTask` is cleared, and the claim is still delivered as an ordinary agent turn (`server/internal/handler/daemon.go`).

Cancelling a parent issue does not cancel member tasks. There is no multi-member run object.

| Plan rule | Status |
| --- | --- |
| A20 independent preset and no credential handoff | Partial. Presets are the member agent rows. Credentials are not copied. Comment text can carry a secret. |
| A21 no silent success as one agent | Partial. Comment responses can name a blocked or coalesced mention. Assign still returns the issue when the leader task does not start. A missing briefing still claims the task. |

Squad HTTP tests were not run.

## 7. Events an install projection could use

| Feed | Status | What it is |
| --- | --- | --- |
| Register an external callback URL | Missing | `POST /api/webhooks/autopilots/{token}`, `/api/webhooks/github`, `/api/webhooks/vcs/{connectionId}`, and `/api/webhooks/stripe` are inbound. `webhook_delivery` records those inbound calls. |
| Plugin HTTPS hooks | Partial | A plugin manifest may name an HTTPS URL and seven events: `issue.created`, `issue.updated`, `issue.status_changed`, `comment.created`, `task.started`, `task.completed`, `task.failed` (`server/pkg/plugincontract/manifest.go`). Delivery sets `X-Multica-Signature` with HMAC (`server/internal/service/plugin_hook.go`). The dispatcher queue drops events when it is full (`server/internal/service/plugin_event_dispatch.go`). Event retries do not share one delivery id. Plugins must be enabled. |
| Websocket bus | Partial | Workspace clients receive events after the write. The event id is for fanout dedupe. Payloads are not signed. This is not a market subscription. |
| `activity_log`, `task_usage`, `issue_child_event`, `issue_wakeup_receipt` | Partial | Internal rows. They are not an external signed feed. |
| Task or install outbox | Missing | Task create has no outbox. `seat_capacity_outbox` is a billing intent. |

No plugin-hook test was run in this pass.

## Gaps that block M02 and M03

M02 needs a preflight over the real workspace, runtime, tools, connections, and knowledge ACL. M03 needs an installer whose commit is idempotent, whose unknown result can be queried, and whose ready state does not start a task.

1. Agent-task create has no caller key and no lookup by that key. A lost response cannot be reconciled, and a retry can create a second task. Autopilot `Idempotency-Key` does not cover this. M03 cannot claim unique creation.
2. Squad create is not idempotent. The same name creates another squad. M03 cannot retry a squad step safely.
3. There is no operation row, ready state, or market outbox. Object create and task create are separate routes, and nothing records an install that stays ready. Plugin hooks are not that outbox. They can drop events, and they require a consented plugin with a fixed event list.
4. There is no credential resource. Secrets are split across plaintext env, plaintext MCP config, and a few encrypted columns. M02 cannot bind a `credentialRef` to one controlled service.
5. A task token is workspace-bound and attributed as the agent, and `POST /api/tokens` plus `POST /api/cli-token` will mint a human credential for the runtime owner. A market caller must not be given a task token. Header spoofing is a separate, tested control and does not close this route.
6. Knowledge ACL was not traced. M02 cannot treat knowledge permission as verified.
7. Squad assignment can return HTTP 200 when the leader task was not enqueued, and a claim without a briefing still runs as one agent. That blocks treating a squad install or run as a multi-member success. It is the A21 gap M06 also needs.

## UNKNOWN

- Reverse proxies and host access logs. Only the in-process request logger was read.
- Whether a Google or Cloud Fleet error body echoes a token. Local code can log those bodies. The upstream bytes were not observed.
- A live `mat_` call to `POST /api/tokens` or `POST /api/cli-token`. The route and both handlers were read. The call was not sent.
- Postgres behavior for the pending-task index, autopilot quota reuse, cancel, skill import, and squad create. The SQL and handlers were read. Those tests were not run.
- A dedicated knowledge or wiki bind API.
