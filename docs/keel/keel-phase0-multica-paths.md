# Keel Phase 0 — Multica path inventory

Status: Gate 0 path lock. Docs only. This file does not add a Keel runtime, a `keel_cloud` provider, or a Managed Agents client.

Verified: 2026-11-03, against the pins in section 1 and the open pull request in section 2.

Every claim is tagged:

| Tag | Meaning |
| --- | --- |
| **verified in tree** | Present on this fork's `main` pin. |
| **verified on upstream main, absent from this pin** | Present on `multica-ai/multica` `main`, not on this pin. |
| **only in #9761** | Present on the head of [multica-ai/multica#9761](https://github.com/multica-ai/multica/pull/9761), not on this pin and not on upstream `main`. |
| **design hypothesis** | Not an API or type in any of those trees. |

`keel_cloud` does not occur in this pin, upstream `main`, or `#9761`. **verified in tree** / **verified on upstream main, absent from this pin** / **only in #9761**.

## 1. Pins

| Repo | Ref | SHA | Commit time | Subject |
| --- | --- | --- | --- | --- |
| This fork (`origin`, `klinsmaya/multica`) | `main` / `HEAD` | `38a65f498891f7b4f5db0dc6eb54811793f8bfae` | 2026-10-31 16:37:21 +0800 | `MUL-8425 feat(sidebar): right-click quick actions on sidebar issues (#9749)` |
| Upstream `multica-ai/multica` | `main` | `82492f94d669e504a2fda36fb6c76f4ac7d65f2e` | 2026-11-03 15:08:43 +0800 | `MUL-8510: fix(ghsnapshot): drop cancelled workflow runs that a newer run of the same workflow replaced from the PR rollup (#9860)` |

**verified in tree** and **verified on upstream main, absent from this pin**:

- `origin/main` equals this pin. This fork is not ahead of upstream.
- Merge base of this pin and upstream `main` is `38a65f498891f7b4f5db0dc6eb54811793f8bfae`.
- `git rev-list --count` : this pin is **0 ahead** and **56 behind**. Upstream `main` is a fast-forward of this pin.
- Qoder Cloud Agents from [#9402](https://github.com/multica-ai/multica/pull/9402) is already in this pin: `0fe2ebf02` `MUL-8045: feat(runtimes): Qoder Cloud Agent as a managed cloud runtime (#9402)`.

Do not treat this pin as current upstream `main`. Section 4 lists the cloud-path files that landed in those 56 commits.

## 2. Upstream #9761

**only in #9761** unless a row says otherwise. Recorded with `gh pr view 9761 --repo multica-ai/multica` and `git fetch` of `pull/9761/head` on 2026-11-03.

| Field | Value |
| --- | --- |
| Title | `feat(runtimes): add Claude Managed Agents as a cloud agent provider (MUL-8242)` |
| State | `OPEN`. `mergedAt` is null. `isDraft` is false. |
| URL | https://github.com/multica-ai/multica/pull/9761 |
| Head | `23dcaa19884d3b202e4e6eb882e81902f53ec6c3` (`feat/claude-managed-agents`, merge commit `Merge upstream main into feat/claude-managed-agents`, 2026-11-03 14:31:05 +0800) |
| Recorded base SHA | `2115a513a375bfe2910b76725b617504faee4b25` (`MUL-7686: feat(runtime): add native ZCode protocol support (#9514)`, 2026-11-03 14:30:18 +0800) |
| Diff size | 31 commits not in upstream `main`; 116 files (`git diff --name-only <base>...<head>`) |
| Updated | `2026-11-03T06:31:54Z` |

**verified on upstream main, absent from this pin**: the recorded base is an ancestor of upstream `main` and is itself 3 commits behind `82492f94`. Those 3 commits (`82492f94d`, `049ca20ec`, `e06640a56`) do not touch `server/internal/qoderruntime`, `server/internal/claudemanagedruntime`, `server/internal/handler/chat.go`, `server/internal/handler/qoder_catalog.go`, `packages/core/runtimes`, or `packages/views/runtimes`.

`isCloudAgentProvider` is absent from this pin and from upstream `main`. It exists only on the PR head, in:

- `server/internal/handler/qoder_catalog.go` — `provider == qoderruntime.Provider || provider == claudemanagedruntime.Provider`
- `server/internal/handler/agent.go`
- `server/internal/handler/daemon.go`
- `server/internal/handler/qoder_disconnect.go`

### Files in #9761 that touch the cloud-agent surface

Provider string introduced by the PR: `claude_managed` (`server/internal/claudemanagedruntime/bridge.go`). Daemon id prefix: `claude-managed-` (`server/internal/handler/claude_connection.go`).

| Path | Role on the PR head |
| --- | --- |
| `server/internal/claudemanagedruntime/http.go` | Managed Agents HTTP client. Fixed base `https://api.anthropic.com/v1`. Auth headers below. |
| `server/internal/claudemanagedruntime/bridge.go` | Session create/get, event send, interrupt-cancel, environment and vault checks. Registers with Multica as a daemon. |
| `server/internal/claudemanagedruntime/events.go` | `GET /sessions/{id}/events` paging. |
| `server/internal/claudemanagedruntime/catalog.go` | Catalog and access check against the same Anthropic base URL. |
| `server/internal/claudemanagedruntime/prompt.go` | Refuses chat, empty issue id, and leader/squad tasks: issue runs only. |
| `server/cmd/claude-managed-runtime/main.go` | Standalone bridge process. |
| `server/internal/handler/claude_connection.go` | Workspace connection CRUD, encrypted config, in-process supervisor, reserved daemon token. |
| `server/internal/handler/claude_disconnect.go` | Disconnect and managed-runtime ownership. |
| `server/cmd/server/router.go` | Routes under `/api/workspaces/{id}/claude-managed` (get, agents, put, check, environments, stop, resume, delete). |
| `server/internal/handler/chat.go` | `SendChatMessage` returns 422 when the agent's provider is `claude_managed`. Qoder is not refused here. |
| `server/internal/handler/issue.go` | After the Qoder quick-create allow (`return 0, nil` for `qoderruntime.Provider`), `cloudAgentServiceName` refuses other cloud providers, including Claude, with "existing issue runs only". |
| `server/internal/middleware/managed_daemon.go` | Prefix map `qoder-managed-` → `qoder_cloud`, `claude-managed-` → `claude_managed`. |
| `server/internal/middleware/daemon_auth.go` | Unbound-token exception uses `ManagedDaemonProvider` instead of a hard-coded `qoder-managed-` prefix. |
| `server/internal/handler/daemon.go` | Managed registration accepts the one provider named by the daemon id prefix. |
| `server/internal/handler/qoder_connection.go` | `managedCredentialAllowed` looks up `cloud_agent_connection.provider` from that prefix. |
| `server/pkg/db/queries/qoder_issue_lifecycle.sql` | Issue status sync matches `provider IN ('qoder_cloud', 'claude_managed')`. |
| `server/internal/handler/workspace.go` | Workspace delete removes both providers' connection rows and daemon tokens. |
| `packages/core/runtimes/claude-managed.ts` | Client hooks for the connection API. |
| `packages/core/runtimes/claude-managed-schema.ts` | Zod parse of the connection payload. |
| `packages/core/agents/claude-runtime-config.ts` | Reads `claude_agent_id` from agent `runtime_config`. |
| `packages/views/agents/create/runtime-choice.ts` | Cloud tile beside Qoder. |
| `packages/views/runtimes/components/claude-managed-page.tsx` | Connection settings page. |
| `packages/views/agents/components/tabs/claude-agent-tab.tsx` | Agent binding tab. |

The other changed files are locales, docs (`apps/docs/content/docs/daemon-runtimes*.mdx`), landing copy, tests, and UI that follows the same provider string. Full list is the 116-file diff.

### Managed Agents HTTP contract on the PR head

**only in #9761**. Taken from `claudemanagedruntime`, not from Anthropic's public docs.

- Base URL is the constant `DefaultAnthropicURL = "https://api.anthropic.com/v1"`. The workspace connection handler passes that constant into the bridge. There is no per-workspace base URL allowlist (Qoder has one; see section 3).
- Anthropic client headers: `Authorization: Bearer <token>`, `anthropic-version: 2023-06-01`, `anthropic-beta: managed-agents-2026-04-01`.
- `anthropic-workspace-id` is not sent. A body that asks for it is classified as "workspace-scoped API key required".
- Calls observed in the bridge and catalog client: `POST /sessions`, `GET /sessions`, `GET /sessions/{id}`, `POST /sessions/{id}/events`, `GET /sessions/{id}/events`, `GET /environments/{id}`, `GET /vaults/{id}`.
- Cancel posts `{"events":[{"type":"user.interrupt"}]}` to `/sessions/{id}/events`. The source comment states Claude has no cancel endpoint. Sessions are not archived or deleted by this bridge.
- Multica-side calls still use `Authorization: Bearer` plus `X-Workspace-ID` on the daemon client (`newAPI` in the same `http.go`).

## 3. Paths on this pin

Cloud execution on this pin is the Qoder bridge. Local CLI providers `qoder` and `qoderclicn` are a different surface (section 3.4).

### 3.1 Provider identity and registration

There is no cloud-provider registry. `qoder_cloud` is a string compared at each call site. **verified in tree**.

| Path | Role |
| --- | --- |
| `server/internal/qoderruntime/bridge.go` | `const Provider = "qoder_cloud"`. Bridge config, journal, `POST /api/daemon/register` with that provider. |
| `server/internal/handler/daemon.go` | `isQoderManagedDaemonID` (`qoder-managed-` prefix). Registration must be a single `qoder_cloud` runtime with no custom profile. `qoderCredentialAllowed` required. |
| `server/internal/middleware/daemon_auth.go` | Daemon tokens whose id starts with `qoder-managed-` may stay unbound; other unbound tokens take the legacy owner lookup. |
| `server/internal/handler/qoder_connection.go` | Saves `cloud_agent_connection` with `provider='qoder_cloud'`, mints daemon id `qoder-managed-` + workspace id, starts the in-process bridge. |
| `server/cmd/server/router.go` | Member: `GET /api/workspaces/{id}/qoder`, `GET .../qoder/agents`. Admin: `PUT/DELETE .../qoder`, `POST .../qoder/check`, `.../environments`, `.../stop`, `.../resume`. |
| `server/migrations/586_cloud_agent_connection.up.sql` | Table `cloud_agent_connection`. Comment: secrets and journal sealed with `MULTICA_VCS_SECRET_KEY`. Unique key is added by `587_cloud_agent_connection_provider_index.up.sql` on `(workspace_id, provider)`. |
| `server/internal/handler/qoder_connection.go` | Returns 503 when `MULTICA_VCS_SECRET_KEY` is absent (`openVCSSecret` / `sealVCSSecret`). |
| `packages/core/runtimes/display.ts` | Display name `qoder_cloud` → `Qoder Cloud Agents`. |
| `packages/views/agents/create/runtime-choice.ts` | `QODER_PROVIDER = "qoder_cloud"`. Pulled out of the generic cloud list into `qoderRuntime`. |
| `packages/views/runtimes/components/runtimes-page.tsx` | Renders the Qoder connection separately from machine runtimes. |
| `packages/views/runtimes/components/runtime-settings-page.tsx` | `provider === "qoder_cloud"` selects `QoderDetailPage`. |
| `packages/core/runtimes/qoder.ts` | Query hooks for the connection and agent catalog. |
| `packages/core/runtimes/qoder-schema.ts` | Zod schema. Region URLs match the server allowlist. |
| `packages/core/agents/qoder-runtime-config.ts` | Reads `qoder_agent_id` and optional default `repository` from `runtime_config`. |
| `packages/core/agents/draft.ts` | Create-agent draft treats `qoder_cloud` as its own provider. |

Agent create/update rejects Qoder execution overrides in `server/internal/handler/agent.go` (around the `runtime.Provider == "qoder_cloud"` checks) and `server/internal/handler/qoder_catalog.go` `qoderOverrides.validate`. The bridge repeats that refusal in `server/internal/qoderruntime/prompt.go` `prepare`. **verified in tree**.

Issue status transitions for this provider are SQL-filtered, not generic: `server/pkg/db/queries/qoder_issue_lifecycle.sql` requires `r.provider = 'qoder_cloud'`. The Go side is `server/internal/service/qoder_issue_lifecycle.go`. **verified in tree**.

Workspace delete removes only `provider='qoder_cloud'` rows: `server/internal/handler/workspace.go`. **verified in tree**.

### 3.2 baseURL and auth (Qoder)

**verified in tree**.

| Path | Role |
| --- | --- |
| `server/internal/handler/qoder_catalog.go` | Allowlist `qoderAPIURLs`: `https://api.qoder.com/api/v1/cloud` and `https://api.qoder.com.cn/api/v1/cloud`. Empty input defaults to the global URL. |
| `server/internal/handler/qoder_connection.go` | `qoderSettings.BaseURL` is stored in the encrypted config and passed to the bridge as `QoderURL`. Rejects a URL outside the allowlist. |
| `server/internal/qoderruntime/http.go` | `newAPI` requires http(s) with a host, no userinfo, query, or fragment. Non-loopback HTTP is rejected. Sets `Authorization: Bearer` and, when a workspace id was configured, `X-Workspace-ID`. |
| `server/internal/qoderruntime/bridge.go` | Multica client gets the workspace id. Qoder client is constructed with an empty workspace id, so Qoder requests do not send `X-Workspace-ID`. |
| `server/internal/handler/qoder_catalog.go` `fetchQoderCatalog` | Server-side `GET {base}/{kind}` with `Authorization: Bearer` and `after_id` paging. Kinds used by the handlers are `agents` and `environments`. |

External Qoder routes the bridge calls (`bridge.go`, `events.go`): `POST /sessions`, `GET /sessions/{id}`, `POST /sessions/{id}/events`, `GET /sessions/{id}/events`, `POST /sessions/{id}/cancel`, `GET /environments/{id}`. **verified in tree**.

### 3.3 Chat and quick-create

**verified in tree** on this pin:

| Path | Behavior |
| --- | --- |
| `server/internal/qoderruntime/prompt.go` | Chat (`ChatSessionID != ""`) is built into a prompt. A task that is neither a plain issue nor a chat, or that is a leader task, fails with "issue and chat runs only". Chat attachments fail before execution. |
| `server/internal/qoderruntime/bridge_test.go` | `TestChatRun`, `TestChatAudience`, `TestChatAttachmentsFailBeforeExecution`. |
| `server/internal/handler/chat.go` | No `qoder_cloud` branch. `SendChatMessage` does not refuse this provider. |
| `packages/views/agents/components/agent-detail-inspector.tsx` | Chat preview is not hidden for Qoder. |
| `packages/views/agents/components/agent-detail-inspector.starters.test.tsx` | Test name: "offers starters and a chat preview for a Qoder agent". |
| `server/internal/handler/issue.go` `checkQuickCreateDaemonVersionAtLeast` | `qoder_cloud` returns 422 `agent_unavailable`: "supports existing issue runs only". |
| `server/internal/handler/qoder_connection_test.go` | `TestQoderQuickCreateUnsupported`. |
| `packages/views/modals/quick-create-issue.tsx` | Drops agents whose runtime provider is `qoder_cloud` from the picker. |
| `packages/views/agents/components/agent-detail-page.tsx` | Hides the skills editor when `provider === "qoder_cloud"`. Skills stay on the Qoder agent. |

### 3.4 Daemon bridge process

**verified in tree**. This is the merged #9402 path plus follow-ups already in this pin (logo rename `ac61e5ed3`, #9418).

| Path | Role |
| --- | --- |
| `server/cmd/qoder-runtime/main.go` | Standalone process. `--check` calls `Bridge.Check` and does not register. |
| `server/cmd/qoder-runtime/README.md` | Operator notes for the standalone bridge. |
| `server/internal/handler/qoder_connection.go` | In-process supervisor: one bridge per enabled connection, journal encrypted in `cloud_agent_connection`. |
| `server/internal/qoderruntime/bridge.go` | Multica daemon routes used in production code: `POST /api/daemon/register`, `POST /api/daemon/heartbeat`, `POST /api/daemon/runtimes/{id}/tasks/claim`, `GET /api/daemon/tasks/{id}/status`, `POST /api/daemon/tasks/{id}/complete`, `POST /api/daemon/tasks/{id}/fail`. |
| `server/internal/qoderruntime/claim.go` | Claim payload fields include `chat_session_id`, `chat_message`, attachments, channel type. |
| `server/internal/handler/qoder_cleanup.go` | On stop, fails in-flight runs with reason `qoder_cloud_execution` and clears the journal. |
| `server/internal/handler/qoder_disconnect.go` | Disconnect refuses to drop a connection that still has a journal; generic runtime delete is refused while the connection exists (`qoderManagesRuntime`). |

Task credential check in `prepare`: the claim token must start with `mat_`. **verified in tree** (`prompt.go`).

### 3.5 Local CLI `qoder` (not the cloud bridge)

**verified in tree**. These are daemon-launched CLIs. They are not `qoder_cloud` and they do not use `cloud_agent_connection`.

| Path | Role |
| --- | --- |
| `server/pkg/agent/agent.go` | `SupportedTypes` includes `qoder` and `qoderclicn`. Comment: both share one ACP backend; the keys exist so the daemon can register the international and China binaries separately. `qoder_cloud` is not in this list. |
| `server/pkg/agent/qoder.go` | Local ACP backend. |
| `server/migrations/134_runtime_profile_add_qoder.up.sql` | Profile family `qoder`. |
| `server/migrations/242_runtime_profile_add_qoderclicn.up.sql` | Profile family `qoderclicn`. |

## 4. Upstream main since this pin

**verified on upstream main, absent from this pin**. `git diff --name-status HEAD refs/remotes/upstream/main` under `server/internal/qoderruntime`:

| Path | Change | Commit on upstream `main` |
| --- | --- | --- |
| `server/internal/qoderruntime/quickcreate.go` | added | `950e16bf1` `fix(qoder): support quick-create with Qoder Cloud Agents (MUL-8412) (#9728)` |
| `server/internal/qoderruntime/attachments.go` | added | `ed7c00177` `MUL-8496 feat(qoder): run issues with attachments through Qoder Files and image blocks (#9814)` |
| `server/internal/qoderruntime/usage.go` | added | `87ec077e5` `MUL-8539: feat: report Qoder Cloud Agents credits usage (#9857)` |
| `bridge.go`, `claim.go`, `events.go`, `http.go`, `prompt.go`, `state.go`, `server/internal/handler/issue.go`, `server/cmd/qoder-runtime/README.md` | modified | same range |

`prompt.go` on upstream `main` accepts quick-create as a third run kind ("issue, chat and quick-create runs only") and no longer rejects all issue attachments in that function. `issue.go` replaces the unconditional 422 with `checkQuickCreateQoderLimits`, which still rejects squad leadership and attachments. **verified on upstream main, absent from this pin**.

`isCloudAgentProvider` and `claude_managed` remain absent on upstream `main`. **verified on upstream main, absent from this pin**.

`server/internal/handler/daemon.go` differs (commits `#9857`, `#9844`, `#9791`). A search of the added and removed lines for `qoder`, `cloud_agent`, and `managed` found none. `server/internal/middleware/daemon_auth.go` is unchanged between this pin and upstream `main`. **verified on upstream main, absent from this pin**.

## 5. Minimal integration surface

No Keel endpoint exists to call. The two options below are what the trees actually implement.

### 5.1 Dedicated `keel_cloud` provider

**design hypothesis**, bounded by the Qoder shape that is **verified in tree**:

A second cloud provider is not a config flag. On this pin every gate is a `qoder_cloud` comparison or a `qoder-managed-` prefix. A dedicated provider would have to add a provider string and thread it through the same files section 3 lists: `cloud_agent_connection.provider`, a reserved daemon id, `DaemonRegister`, `daemon_auth.go`, connection routes, the bridge's Multica daemon client (`register`, `heartbeat`, `claim`, `status`, `complete`, `fail`), issue-lifecycle SQL, workspace delete, create-agent override checks, and the runtime picker.

What the Qoder bridge already requires of any such adapter, **verified in tree**:

- Speak Multica's daemon task protocol with a bearer token and `X-Workspace-ID`.
- Claim tasks whose auth token starts with `mat_`.
- Accept issue and chat runs; reject leader/squad tasks and chat attachments.
- Refuse quick-create at `checkQuickCreateDaemonVersionAtLeast` (this pin only; upstream `main` has already narrowed that refusal).
- Keep skills, model, MCP, custom args, and custom env on the remote agent, not on the Multica agent.
- Store connection secrets only through `MULTICA_VCS_SECRET_KEY`.

**only in #9761**: `isCloudAgentProvider`, `cloudAgentServiceName`, and `managedDaemonPrefixes` are the first shared switches. They know `qoder_cloud` and `claude_managed` only. Adding `keel_cloud` is still a code change to that map and to the SQL `IN` list; the PR does not load providers from the database.

**design hypothesis**: copying the Qoder bridge is the path that matches this pin. Waiting for `#9761` and extending its prefix map is a smaller patch only after that PR merges. Neither path is implemented.

### 5.2 Consuming the Managed Agents-compatible API

**only in #9761**. Upstream `main` and this pin have no Managed Agents client, no `claude_managed` provider, and no `https://api.anthropic.com/v1` constant in the cloud bridge.

What that PR's client actually does is section 2. A Keel deployment that wanted Multica to treat it as Claude Managed Agents would have to satisfy that client, not a guessed OpenAPI document:

- Serve the paths and headers in section 2, at a base URL the PR hard-codes to `https://api.anthropic.com/v1` unless Multica is changed.
- Create sessions, accept `user.message` events, page events, and stop a turn with `user.interrupt`. The PR does not send `/cancel` (that path is Qoder's, **verified in tree**).
- Identify resources as `/environments/{id}` and `/vaults/{id}`.
- Run issue tasks only. Chat and quick-create are refused before the bridge, or inside `prepare` for chat and leader tasks.
- Still connect to Multica as a daemon (`claude-managed-<workspace id>`), with a row in `cloud_agent_connection` and a task token prefixed `mat_`.

**design hypothesis**: pointing this PR's `AnthropicURL` at a Keel base URL would be a Multica change. The PR's connection handler does not accept a caller-supplied base URL. Compatibility without a `keel_cloud` provider is therefore blocked on both an unmerged PR and a hard-coded host.

## 6. Unknowns and blockers for Gate 0

1. **This fork is 56 commits behind upstream `main`.** **verified on upstream main, absent from this pin**. Any `keel_cloud` design taken from this pin will miss Qoder quick-create (`#9728`), issue attachments (`#9814`), and credit reporting (`#9857`). Re-pin before writing a bridge.
2. **`#9761` is open and unmerged.** **only in #9761**. `isCloudAgentProvider`, Chat refusal for Claude, the Anthropic base URL, and the beta header are not on `main`. ADR-004 in the Keel design cannot treat Managed Agents as available.
3. **`#9761` is 3 commits behind current upstream `main`.** Those 3 commits do not touch the cloud paths listed in section 2, but the PR can still conflict later. **verified on upstream main, absent from this pin**.
4. **No provider plugin API.** A new provider string requires source edits at every hard-coded check in section 3, or at the `#9761` map if that lands. **verified in tree** for the hard-coded checks; **only in #9761** for the map.
5. **Chat support is provider-specific and already diverges.** This pin: Qoder chat is implemented in the bridge and not refused by `SendChatMessage`; quick-create is refused. Upstream `main`: quick-create is partially allowed. `#9761`: Claude chat is refused and Claude `prepare` allows issue runs only. A Keel bridge cannot assume one policy.
6. **baseURL policy differs.** Qoder: two-host allowlist, caller-supplied. `#9761` Claude: one constant, no caller base URL. A Keel host is not on either allowlist.
7. **Cancel differs.** Qoder: `POST /sessions/{id}/cancel`. `#9761`: `user.interrupt` on the events endpoint, and the code says sessions are not deleted. A shared "cloud session" adapter would need both.
8. **Managed Agents request and response schemas beyond the paths above were not compared to Anthropic.** This inventory records only the calls and headers in the PR source. Event payload compatibility, session create body, and vault/environment semantics still need a contract test against a pinned API revision.
9. **Secret handling is operational, not specified here.** Both bridges seal config with `MULTICA_VCS_SECRET_KEY`. This pass did not read key material or run a server.
10. **Local CLI `qoder` is easy to confuse with `qoder_cloud`.** They share a brand and a different provider key. Keel does not plug into `server/pkg/agent/qoder.go`.
