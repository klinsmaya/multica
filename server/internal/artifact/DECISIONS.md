# Decisions for the first artifact slice (A00, A01, A03)

Baseline SHA: `18fa9f728a9e25f2465df2789631edb2a75442ad`. Behavior of today's attachments is recorded in `BASELINE.md`.

## In this change

- Six tables only: `artifacts`, `artifact_revisions`, `artifact_blobs`, `artifact_uploads`, `artifact_operations`, `artifact_links`.
- No foreign keys. Workspace delete lists storage keys, deletes the rows in `DeleteWorkspaceArtifacts`, then calls `Storage.DeleteKeys` after commit. The same `storage.Storage` interface attachments already use receives the bytes.
- `head_revision_id` moves only in `CasArtifactHead`, and only when it is still the caller's base (`IS NOT DISTINCT FROM`, so the first revision matches NULL).
- `artifact_revisions` reject updates with a trigger. A new commit inserts a new row.
- Unique: `(artifact_id, revision_no)`, `source_upload_id` (where not null), `commit_operation_id`, `(workspace_id, actor_id, idempotency_key)`, `storage_key`. Title and filename are not unique.
- Same idempotency key and same request hash returns the stored revision. Same key and a different hash returns `IDEMPOTENCY_KEY_REUSED` and does not insert a revision. A stale `base_revision_id` records `REVISION_CONFLICT` on that operation and a retry of that same request returns the same conflict.
- Object keys are `workspaces/{workspace_id}/artifacts/{blob_id}`. The filename is passed to `Storage.Upload` only as the content-disposition name.
- `artifacts_v1` defaults off (`FF_ARTIFACTS_V1`). `MountArtifactRoutes` registers no routes either way. `RequireArtifactsV1` is the gate a later HTTP slice has to use. `POST /api/upload-file` and `multica attachment` are unchanged.

## Deferred, and why

| Plan item | Why it is not in this PR |
| --- | --- |
| A02 Office license, format matrix, HTML threat model | No editor or preview work in this slice. |
| A04 staging state machine, malware scan, quotas, sealed-copy race | The upload row is written already `committed` inside the same transaction as the revision. There is no presigned URL, scan, or quota reservation yet. `scan_status` stays `unchecked`. |
| A05 workbench, cards, preview, version download | No UI and no content HTTP API. |
| A06 CLI `artifact` commands and task-token scopes for artifacts | The existing `attachment` CLI stays the only file command. |
| A07–A09 edit sessions, leases, fencing, HTML preview | `artifact_edit_sessions` is not created. |
| A10–A12 Office conversion, Web Office, local daemon bridge | Out of scope. No LobsterAI IPC. |
| A13 publish snapshots and public share | `artifact_publish_snapshots` is not created. Sharing stays off. |
| A14 trash, GC, quota settlement, audit | `artifact_audit_events` and derivative tables are not created. Soft-delete and byte GC beyond workspace teardown are not implemented. A crash after the workspace transaction commits and before `DeleteKeys` can leave an object; that is the same best-effort shape as other post-commit deletes, and a sweeper is A14. |
| Outbox events (`artifact.revision.committed` and the rest) | No listener exists yet. The operation row is the idempotency record. |
| Cross-workspace dedup by sha256 | Blobs are not looked up by hash. `storage_key` is unique so one key cannot be retargeted. |
| Membership checks inside `artifact.Service` | The service requires the artifact's `workspace_id` to match the call. It does not decide whether the actor may write that workspace. That check belongs on the HTTP and CLI boundary, which is not open yet. |
| Plan names `size` / `expected_size` | Columns are `size_bytes` and `expected_size_bytes`, matching `attachment.size_bytes`. |
| Plan field list has no `filename` | `artifact_revisions.filename` stores the display name captured at commit. It is not an identity column. |
| `owner_scope` limited to `workspace`, `issue`, `task` | Comment and chat parents stay on links later, not on the owner columns. Links do not grant access; this slice does not write link rows from the service. |

## Conflict with the plan

The plan's upload chapter says a staging object must not become the sealed blob until scan. This slice seals bytes in one transaction so the CAS and idempotency tests have a real revision to point at. That is a smaller path than A04, not a second storage stack. A04 should replace the direct `committed` insert with the staging state machine without changing the revision uniqueness rules.
