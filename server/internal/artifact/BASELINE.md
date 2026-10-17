# Artifact baseline (A00 / A01)

Pinned source: `18fa9f728a9e25f2465df2789631edb2a75442ad` on `klinsmaya/multica` (`main` at the time this slice started). LobsterAI is reference-only and is not vendored. This slice does not change attachment upload, download, or the CLI.

## How a file gets in today

There is no artifact id. A file is an `attachment` row plus an object in the shared storage backend.

- CLI: `server/cmd/multica/cmd_attachment.go`. `attachment upload <path>` reads a local file and calls `UploadChatAttachment` with `MULTICA_TASK_ID` or `--task`. `attachment download <id>` calls `GET /api/attachments/{id}` and then the returned `download_url`.
- HTTP upload: `Handler.UploadFile` in `server/internal/handler/file.go` (`POST /api/upload-file`). The body limit is `maxUploadSize` (100 MiB). The server sniffs the content type and only then trusts a small extension map (`extContentTypes`). The object key is `workspaces/{workspaceID}/{uuid}{ext}` or `users/{userID}/{uuid}{ext}`. The extension is taken from the client filename; the filename itself is stored on the row, not used as the key.
- Row insert: `CreateAttachment` in `server/pkg/db/queries/attachment.sql`. Parents are nullable columns on the same row: `issue_id`, `comment_id`, `chat_session_id`, `chat_message_id`, `task_id`, `source_context_id`.
- Schema origin: `server/migrations/029_attachment.up.sql` (workspace, issue, comment, uploader, filename, url, content type, size). Later columns: chat (`083_attachment_chat_columns.up.sql`), task (`164_attachment_task_id.up.sql`), source context (`407_issue_source_context.up.sql`). Those early tables still declare foreign keys with `ON DELETE CASCADE`. New artifact tables do not.

## Where bytes live

`server/internal/storage/storage.go` defines `Storage`. `Upload` writes bytes and returns a URL. `GetReader` streams them back. `LocalStorage` (`server/internal/storage/local.go`) and `S3Storage` (`server/internal/storage/s3.go`) are the two backends. The local backend keeps the original filename in a sidecar, not in the object key. Artifact commits call this same `Storage` interface. They do not add a backend and they do not write attachment rows.

## Who can upload or read

Upload (`UploadFile`):

- The caller must be authenticated (`requireUserID`).
- With a workspace header, `getWorkspaceMember` must succeed or the handler returns 403.
- `issue_id` must be an issue in that workspace (`GetIssueInWorkspace`), otherwise 403.
- `comment_id` must be a non-deleted comment in that workspace.
- `chat_session_id` must pass `gatePublicChatSessionForUser`.
- `task_id` is only accepted when `X-Actor-Source` is `task_token`, `X-Task-ID` equals the form value, the task belongs to the workspace, and the actor is that task's agent on a chat task. The middleware stamps those headers from a `mat_` token and strips client-supplied agent/task identity (`server/migrations/108_task_token.up.sql`). A task token is bound to one task, agent, workspace, and user. It is not a workspace admin PAT.

Read:

- `GET /api/attachments/{id}` (`loadAttachmentForRequest`) requires a workspace header and loads the row with both id and workspace id. Missing rows are 404.
- `GET /api/attachments/{id}/download` and `download-metadata` (`loadAttachmentForDownload`) load by id, then require the caller to be a member of the row's workspace. Non-members and missing ids both return 404, so the route is not an existence oracle. Routes are registered in `server/cmd/server/router.go` (`/api/upload-file`, `/api/attachments/{id}/download`, and the workspace-scoped metadata/content/delete routes).

Workspace teardown deletes attachment rows in `DeleteWorkspaceLeafData` (`server/pkg/db/queries/workspace_delete.sql`) and deletes source-context objects after commit. Artifact rows are deleted beside that, in `DeleteWorkspaceArtifacts`, because they have no foreign keys.

## What is not an artifact identity

`packages/core/attachments/deliverables.ts` groups comment attachments into "versions" by normalized content type plus trimmed filename (`deliverableKey`). That grouping is a view over attachments. It is not a stable id, and two different files with the same name stay two attachments. This slice keeps that behavior and does not use the filename as an artifact id.

## Text edit is not an attachment

A07 opens an `artifact_edit_sessions` row for Markdown, TXT, JSON, and CSV. A draft object lives at `workspaces/{workspace_id}/artifact-drafts/{session_id}`. Saving that draft does not insert an attachment and does not move `head_revision_id`. A formal save is still `POST /api/artifacts/{id}/revisions`. `POST /api/upload-file` and `multica attachment` are unchanged. An expired lease or a failed edit leaves the draft in place.

A08 restore and `artifact_events` write artifact rows only. Restore reuses an existing blob and inserts a new revision. Events store ids and an event type. Neither path inserts an attachment, changes attachment upload or download, or changes `multica attachment`.
