# Decisions for the artifact slices landed so far (A00, A01, A03, A04)

Baseline SHA: `18fa9f728a9e25f2465df2789631edb2a75442ad`. Behavior of today's attachments is recorded in `BASELINE.md`.

## In this change

- Six tables only: `artifacts`, `artifact_revisions`, `artifact_blobs`, `artifact_uploads`, `artifact_operations`, `artifact_links`.
- No foreign keys. Workspace delete lists storage keys, deletes the rows in `DeleteWorkspaceArtifacts`, then calls `Storage.DeleteKeys` after commit. The same `storage.Storage` interface attachments already use receives the bytes.
- `head_revision_id` moves only in `CasArtifactHead`, and only when it is still the caller's base (`IS NOT DISTINCT FROM`, so the first revision matches NULL).
- `artifact_revisions` reject updates with a trigger. A new commit inserts a new row.
- Unique: `(artifact_id, revision_no)`, `source_upload_id` (where not null), `commit_operation_id`, `(workspace_id, actor_id, idempotency_key)`, `storage_key`. Title and filename are not unique.
- Same idempotency key and same request hash returns the stored revision. Same key and a different hash returns `IDEMPOTENCY_KEY_REUSED` and does not insert a revision. A stale `base_revision_id` records `REVISION_CONFLICT` on that operation and a retry of that same request returns the same conflict.
- Object keys for sealed bytes are `workspaces/{workspace_id}/artifacts/{blob_id}`. The filename is passed to `Storage.Upload` only as the content-disposition name.
- `artifacts_v1` defaults off (`FF_ARTIFACTS_V1`). `MountArtifactRoutes` registers no routes either way. `RequireArtifactsV1` is the gate a later HTTP slice has to use. `POST /api/upload-file` and `multica attachment` are unchanged.
- A04 replaces A03's direct seal. `Commit` still uses the same request hash (artifact, base revision, sha256, size, declared media type, filename, source, task run). That hash does not include the upload id, so a retry peeks `artifact_operations` and returns the stored revision without opening a second upload. A consumed upload cannot seal another revision.
- Staging key is `workspaces/{workspace_id}/artifact-staging/{upload_id}`. The state path is `created → uploading → uploaded → verifying → scanning → ready`, then `committed` inside the same CAS transaction as the revision. `rejected`, `expired`, and `cancelled` drop the staging object. Verify checks size, the server-computed sha256, and a sniffed media type. A generic sniff (`text/plain` or `application/octet-stream`) may keep a more specific declared type; a contradicting specific sniff rejects the upload. Declared media type and filename are part of the immutable upload binding.
- The scanner is an interface on `Service`. A nil scanner uses `NopScanner`, which returns clean immediately. That is an explicit no-op, not a timeout. `ScanPending` and a scanner error leave the upload in `scanning` and do not seal. `ScanRejected` rejects the upload and does not create a revision. The sealed blob records `scan_status = clean` only after that pass. There is no malware-scanner vendor in this slice.
- A wrong `base_revision_id` still returns `REVISION_CONFLICT` and does not move head. The ready upload becomes `conflict_draft` with `expires_at` seven days out, and the staging object stays. The upload's base cannot be retargeted; a new head needs a new upload. `ExpireUploads` deletes overdue staging for one workspace. Nothing schedules it.
- Quota is a reservation check under `pg_advisory_xact_lock` for the workspace: open upload `expected_size_bytes` (states `created`, `uploading`, `uploaded`, `verifying`, `scanning`, `ready`, `conflict_draft`, and not yet expired) plus committed revision bytes. The ceilings are `DefaultWorkspaceArtifactQuotaBytes` (1 GiB) and `DefaultActorArtifactQuotaBytes` (256 MiB) unless the service fields are set. This is not a ledger. A14 owns settlement, refunds, and cross-workspace accounting.
- Seal copies the staging object to the blob key while the artifact row is locked, re-reads the sealed object, and requires the same size and sha256. The database transaction rolls back and the sealed object is deleted when CAS or a later insert fails. The staging object is deleted only after the transaction commits, so a conflict or a rollback does not drop a verified candidate. Local and S3 storage here have no object-lock ACL; deleting the staging key is what makes that URL unreadable. The row keeps `staging_key` because the binding is immutable.

## Deferred, and why

| Plan item | Why it is not in this PR |
| --- | --- |
| A02 Office license, format matrix, HTML threat model | No editor or preview work in this slice. |
| A04 remainder: presigned URLs, a real scanner, quota ledger, scheduled GC | Staging is an in-process `Storage` write, not a client-direct upload. `NopScanner` is a stub. `ExpireUploads` is not on a timer. Quota is a constant ceiling plus a sum, not the A14 ledger. |
| A05 workbench, cards, preview, version download | No UI and no content HTTP API. |
| A06 CLI `artifact` commands and task-token scopes for artifacts | The existing `attachment` CLI stays the only file command. |
| A07–A09 edit sessions, leases, fencing, HTML preview | `artifact_edit_sessions` is not created. |
| A10–A12 Office conversion, Web Office, local daemon bridge | Out of scope. No LobsterAI IPC. |
| A13 publish snapshots and public share | `artifact_publish_snapshots` is not created. Sharing stays off. |
| A14 trash, GC, quota settlement, audit | `artifact_audit_events` and derivative tables are not created. `ExpireUploads` can delete overdue staging, including a `conflict_draft` past its seven days, but no process calls it. Quota is not settled or refunded. A crash after a transaction commits and before an object delete can leave a blob or a staging object; that is the same best-effort shape as other post-commit deletes. |
| Outbox events (`artifact.revision.committed` and the rest) | No listener exists yet. The operation row is the idempotency record. |
| Cross-workspace dedup by sha256 | Blobs are not looked up by hash. `storage_key` is unique so one key cannot be retargeted. |
| Membership checks inside `artifact.Service` | The service requires the artifact's `workspace_id` to match the call. It does not decide whether the actor may write that workspace. That check belongs on the HTTP and CLI boundary, which is not open yet. |
| Plan names `size` / `expected_size` | Columns are `size_bytes` and `expected_size_bytes`, matching `attachment.size_bytes`. |
| Plan field list has no `filename` | `artifact_revisions.filename` stores the display name captured at commit. It is not an identity column. |
| `owner_scope` limited to `workspace`, `issue`, `task` | Comment and chat parents stay on links later, not on the owner columns. Links do not grant access; this slice does not write link rows from the service. |

## Conflict with the plan

The plan's upload chapter separates presigned staging, an external scan, and a later seal. This slice keeps that order inside `artifact.Service`: bytes hit a private staging key, verify and the scanner run, then finalize copies into the immutable blob key. There is no second storage stack and no HTTP upload route. The copy holds the artifact row lock instead of a worker. `conflict_draft` retains the verified candidate for seven days on the row; the periodic delete is `ExpireUploads`, which A14 still has to schedule. A stale base is not rewritten onto the new head.

A03 had inserted the upload already `committed` and stored `scan_status = unchecked`. That shortcut is gone. Revision uniqueness, CAS, and the commit idempotency hash are unchanged.
