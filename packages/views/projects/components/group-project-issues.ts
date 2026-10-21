import type { Issue, IssueStatusCategory, IssueThreadSummary } from "@multica/core/types";
import { getFeedReadState } from "../../issues/components/feed-read-state";

export type ProjectIssueGroupKey = "needs_you" | "running" | "idle" | "resolved";

export type ProjectIssueGroups = Record<ProjectIssueGroupKey, Issue[]>;

export const PROJECT_ISSUE_GROUP_ORDER: readonly ProjectIssueGroupKey[] = [
  "needs_you",
  "running",
  "idle",
  "resolved",
];

export interface GroupProjectIssuesInput {
  issues: readonly Issue[];
  summaries: ReadonlyMap<string, IssueThreadSummary>;
  currentUserId: string | null | undefined;
  /** Issues with a running or queued agent task. */
  runningIssueIds: ReadonlySet<string>;
  categoryOf: (status: string) => IssueStatusCategory;
}

/** When the issue last moved: newest reply, else last activity, else creation. */
export function projectIssueActivityAt(
  issue: Issue,
  summary: IssueThreadSummary | undefined,
): string {
  return summary?.last_reply_at ?? issue.last_activity_at ?? issue.created_at;
}

function time(value: string): number {
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? 0 : ms;
}

/**
 * Sorts a project's top-level issues into the overview's four groups. Each
 * issue lands in exactly one, by the first rule that matches:
 *
 * 1. Resolved: the status category is done or closed. A finished issue stays
 *    here even with a pending question or a task still winding down.
 * 2. Needs you: an agent question waits on the caller, or the thread is
 *    unread and someone else spoke last. Wins over Running, because a running
 *    issue that asks the caller something is waiting on the caller.
 * 3. Running: an agent task is running or queued.
 * 4. Idle: everything else.
 *
 * Sub-issues are skipped. Each group is ordered newest activity first.
 */
export function groupProjectIssues({
  issues,
  summaries,
  currentUserId,
  runningIssueIds,
  categoryOf,
}: GroupProjectIssuesInput): ProjectIssueGroups {
  const groups: ProjectIssueGroups = {
    needs_you: [],
    running: [],
    idle: [],
    resolved: [],
  };
  for (const issue of issues) {
    if (issue.parent_issue_id != null) continue;
    const summary = summaries.get(issue.id);
    const category = categoryOf(issue.status);
    if (category === "done" || category === "closed") {
      groups.resolved.push(issue);
    } else if (
      (summary?.awaiting_me.length ?? 0) > 0 ||
      getFeedReadState(summary, currentUserId) === "awaiting"
    ) {
      groups.needs_you.push(issue);
    } else if (runningIssueIds.has(issue.id)) {
      groups.running.push(issue);
    } else {
      groups.idle.push(issue);
    }
  }
  const at = (issue: Issue) =>
    time(projectIssueActivityAt(issue, summaries.get(issue.id)));
  for (const key of PROJECT_ISSUE_GROUP_ORDER) {
    groups[key].sort((a, b) => at(b) - at(a));
  }
  return groups;
}
