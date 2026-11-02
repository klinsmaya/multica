// Twin of commentExcerpt in server/internal/handler/issue_thread_summary.go:
// the live patch for a thread summary's last_reply_excerpt must read like the
// server's value.
const MENTION_MARKUP = /\[@?(.+?)\]\(mention:\/\/(member|agent|squad|issue|all)\/(?:[0-9a-fA-F-]+|all)\)/g;

/**
 * Flattens comment Markdown into a one-line preview: mention links collapse to
 * their label ("@Name"; issue links keep the bare identifier), whitespace runs
 * to a single space, and the result is cut to maxChars characters (by code
 * point) with an ellipsis.
 */
export function commentExcerpt(content: string, maxChars: number): string {
  const flat = content
    .replace(MENTION_MARKUP, (_m, label: string, type: string) =>
      type === "issue" ? label : `@${label}`,
    )
    .replace(/\s+/g, " ")
    .trim();
  const chars = Array.from(flat);
  if (chars.length <= maxChars) return flat;
  return `${chars.slice(0, maxChars).join("").trim()}…`;
}
