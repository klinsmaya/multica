import { parseMentions } from "./comment-trigger-outcomes";
import type { IssueAssigneeType } from "../types";

export interface FeedMessageDraft {
  title: string;
  /** Markdown after the first line; absent when the message is one line. */
  description?: string;
  /** First explicit @agent / @squad in the message, in reading order. */
  assignee: { type: Extract<IssueAssigneeType, "agent" | "squad">; id: string } | null;
}

// A mention the way the editor serializes it: `[@Go](mention://agent/UUID)`.
const MENTION_MARKUP = /\[@?((?:\\.|[^\]\\])+)\]\(mention:\/\/(\w+)\/[^)]+\)/g;

// Titles are plain text, so a mention in the first line reads as its pill does.
function titleText(line: string): string {
  return line
    .replace(MENTION_MARKUP, (_m, label: string, type: string) => {
      const text = label.replace(/\\([[\]\\()])/g, "$1");
      return type === "issue" || type === "project" ? text : `@${text}`;
    })
    .trim();
}

/**
 * Turns what was typed into the feed composer into the fields of a new issue:
 * the first non-blank line is the title, the rest is the description, and the
 * first @agent / @squad mention is the assignee. Returns null for a message
 * with no title.
 */
export function parseFeedMessage(markdown: string): FeedMessageDraft | null {
  const lines = markdown.replace(/\r\n?/g, "\n").split("\n");
  const first = lines.findIndex((line) => line.trim().length > 0);
  if (first === -1) return null;

  const title = titleText(lines[first]!);
  if (!title) return null;
  const description = lines.slice(first + 1).join("\n").trim();

  const mention = parseMentions(markdown).find(
    (m) => m.type === "agent" || m.type === "squad",
  );
  return {
    title,
    description: description || undefined,
    assignee: mention
      ? { type: mention.type as "agent" | "squad", id: mention.id }
      : null,
  };
}
