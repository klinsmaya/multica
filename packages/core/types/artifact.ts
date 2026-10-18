// Read model for the artifact authority. Storage keys are not part of this
// contract. A link to an artifact does not appear here and does not grant access.

export interface ArtifactSummary {
  id: string;
  workspace_id: string;
  owner_scope: string;
  owner_id: string;
  title: string;
  kind: string;
  status: string;
  head_revision_id?: string;
  revision_no?: number | null;
  media_type?: string;
  filename?: string;
  size_bytes?: number | null;
  updated_by?: string;
  created_at: string;
  updated_at: string;
  row_version: number;
}

export interface ArtifactList {
  artifacts: ArtifactSummary[];
}

export interface ArtifactRevision {
  id: string;
  artifact_id: string;
  revision_no: number;
  media_type: string;
  filename: string;
  size_bytes: number;
  source: string;
  actor_id: string;
  created_at: string;
}

export interface ArtifactRevisionList {
  revisions: ArtifactRevision[];
}

export interface ArtifactEditSession {
  edit_session_id: string;
  artifact_id: string;
  base_revision_id: string;
  fencing_token: number;
  lease_token?: string;
  lease_expires_at: string;
  editor_type: string;
  state: string;
  editable: boolean;
  draft_content?: string;
}

export interface ArtifactCommitResult {
  artifact_id: string;
  revision_id: string;
  operation_id: string;
  state: string;
  title?: string;
  filename?: string;
  revision_no?: number;
}

export interface ArtifactPreview {
  artifact_id: string;
  revision_id: string;
  filename: string;
  media_type: string;
  size_bytes: number;
  revision_no: number;
  preview_kind: string;
  content_path: string;
  content_disposition: string;
  static_preview_path?: string;
  active_content: boolean;
}
