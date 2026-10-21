import type { IssueThreadSummary } from "@multica/core/types";

export type FeedReadState = "read" | "unread" | "awaiting";

/**
 * How a feed card stands for the current user, from the thread rollup and the
 * caller's own read cursor.
 *
 * - unread: the newest counted comment is later than the cursor; a missing
 *   cursor with any comment counts as unread.
 * - awaiting: unread and the most recent participant is not the current user,
 *   i.e. the ball is in the user's court.
 *
 * An issue with no row or no comment has nothing to read.
 */
export function getFeedReadState(
  summary: IssueThreadSummary | undefined,
  currentUserId: string | null | undefined,
): FeedReadState {
  const lastReplyAt = summary?.last_reply_at;
  if (!summary || !lastReplyAt) return "read";

  const lastReadAt = summary.last_read_at;
  const unread =
    lastReadAt == null || Date.parse(lastReplyAt) > Date.parse(lastReadAt);
  if (!unread) return "read";

  const latest = summary.participants[0];
  const isMine =
    latest != null && latest.type === "member" && latest.id === currentUserId;
  return isMine ? "unread" : "awaiting";
}
