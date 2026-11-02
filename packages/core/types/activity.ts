import type { CommentAuthorType, CommentSupplementReceipt, Reaction } from "./comment";
import type { Attachment } from "./attachment";

export interface AssigneeFrequencyEntry {
  assignee_type: string;
  assignee_id: string;
  frequency: number;
}

export interface TimelineEntry {
  type: "activity" | "comment";
  id: string;
  actor_type: string;
  actor_id: string;
  created_at: string;
  /** Display identity hydrated from the actor's global user row when available. */
  actor_name?: string;
  actor_avatar_url?: string;
  // Activity fields
  action?: string;
  details?: Record<string, unknown>;
  // Comment fields
  content?: string;
  parent_id?: string | null;
  updated_at?: string;
  revision?: number;
  comment_type?: string;
  /** Set only on comments a quick action produced (MUL-5465). Unforgeable. */
  quick_action_id?: string | null;
  reactions?: Reaction[];
  attachments?: Attachment[];
  resolved_at?: string | null;
  resolved_by_type?: CommentAuthorType | null;
  resolved_by_id?: string | null;
  source_task_id?: string | null;
  /** Every running turn this comment steered, one receipt per run. */
  supplements?: CommentSupplementReceipt[];
  /** Mirrors the first receipt; servers that predate `supplements` send only these. */
  supplement_task_id?: string;
  supplement_status?: "pending" | "delivering" | "delivered" | "failed";
  supplement_failure_reason?: string;
  supplement_delivered_at?: string;
  /**
   * Set only on a comment deleted while it still had replies: the server keeps
   * it as an empty tombstone so the replies keep their parent. Read it through
   * `isDeletedComment`.
   */
  deleted_at?: string | null;
  /** Set by frontend coalescing when consecutive identical activities are merged. */
  coalesced_count?: number;
}

/**
 * One page of the root-paged timeline: `limit` root comments (newest first) with
 * every reply of those roots, plus the activities in the page's time window.
 * `next_before` / `next_before_id` are the cursor to the next OLDER page; they
 * are null on a server that does not page (it answers with its whole timeline).
 */
export interface TimelinePage {
  /** Server order: newest first. Consumers sort. */
  entries: TimelineEntry[];
  has_more_before: boolean;
  has_more_after: boolean;
  next_before: string | null;
  next_before_id: string | null;
  /** Index of the `around` anchor inside `entries`, when one was requested. */
  target_index?: number;
}

/** Cursor of an older timeline page: roots strictly older than (before, beforeId). */
export interface TimelinePageCursor {
  before: string;
  beforeId: string;
}
