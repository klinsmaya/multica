import type { Attachment } from "./attachment";

/** Which group of the project library to list: agent output or member uploads. */
export type ProjectAttachmentSource = "deliverable" | "upload";

export interface ProjectAttachmentIssue {
  id: string;
  identifier: string;
  title: string;
}

/** A library row: the regular attachment plus the issue it hangs off. */
export interface ProjectAttachment extends Attachment {
  issue: ProjectAttachmentIssue;
}

export interface ProjectAttachmentsParams {
  source?: ProjectAttachmentSource;
  limit?: number;
  offset?: number;
}

export interface ListProjectAttachmentsResponse {
  attachments: ProjectAttachment[];
  /** Rows of the requested source (both groups when no source is given). */
  total: number;
  /** Always the agent-uploaded count, whichever source was requested. */
  deliverable_total: number;
  /** Always the member-uploaded count, whichever source was requested. */
  upload_total: number;
}

export interface ProjectLibraryIssue {
  id: string;
  identifier: string;
}
