// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { Issue, IssueStatusCategory, IssueThreadSummary } from "@multica/core/types";
import { groupProjectIssues, projectIssueActivityAt } from "./group-project-issues";

const ME = "user-me";

function issue(overrides: Partial<Issue> & { id: string }): Issue {
  return {
    status: "todo",
    parent_issue_id: null,
    created_at: "2026-10-01T00:00:00Z",
    last_activity_at: null,
    ...overrides,
  } as Issue;
}

function summary(
  issueId: string,
  overrides: Partial<IssueThreadSummary> = {},
): IssueThreadSummary {
  return {
    issue_id: issueId,
    reply_count: 0,
    last_reply_at: null,
    last_reply_excerpt: null,
    open_thread_count: 0,
    participants: [],
    last_read_at: null,
    awaiting_me: [],
    latest_progress: null,
    pull_requests: [],
    deliverable_count: 0,
    last_deliverable_at: null,
    ...overrides,
  };
}

const categoryOf = (status: string): IssueStatusCategory =>
  status === "done" ? "done" : status === "cancelled" ? "closed" : "started";

function group(
  issues: Issue[],
  summaries: IssueThreadSummary[] = [],
  running: string[] = [],
) {
  const result = groupProjectIssues({
    issues,
    summaries: new Map(summaries.map((s) => [s.issue_id, s])),
    currentUserId: ME,
    runningIssueIds: new Set(running),
    categoryOf,
  });
  return {
    needs_you: result.needs_you.map((i) => i.id),
    running: result.running.map((i) => i.id),
    idle: result.idle.map((i) => i.id),
    resolved: result.resolved.map((i) => i.id),
  };
}

const awaitingQuestion = {
  comment_id: "c",
  actor_id: "agent-1",
  excerpt: "ok?",
  created_at: "2026-10-02T00:00:00Z",
};

describe("groupProjectIssues", () => {
  it("puts an issue with an open agent question under needs_you", () => {
    expect(
      group([issue({ id: "a" })], [summary("a", { awaiting_me: [awaitingQuestion] })]),
    ).toEqual({ needs_you: ["a"], running: [], idle: [], resolved: [] });
  });

  it("puts an unread thread where someone else spoke last under needs_you", () => {
    const grouped = group(
      [issue({ id: "theirs" }), issue({ id: "mine" }), issue({ id: "read" })],
      [
        summary("theirs", {
          last_reply_at: "2026-10-02T00:00:00Z",
          participants: [{ type: "agent", id: "agent-1" }],
        }),
        summary("mine", {
          last_reply_at: "2026-10-02T00:00:00Z",
          participants: [{ type: "member", id: ME }],
        }),
        summary("read", {
          last_reply_at: "2026-10-02T00:00:00Z",
          last_read_at: "2026-10-03T00:00:00Z",
          participants: [{ type: "agent", id: "agent-1" }],
        }),
      ],
    );
    expect(grouped.needs_you).toEqual(["theirs"]);
    expect(grouped.idle.sort()).toEqual(["mine", "read"]);
  });

  it("puts issues with a running or queued task under running", () => {
    expect(group([issue({ id: "a" }), issue({ id: "b" })], [], ["a"])).toEqual({
      needs_you: [],
      running: ["a"],
      idle: ["b"],
      resolved: [],
    });
  });

  it("puts done and closed issues under resolved", () => {
    expect(
      group([
        issue({ id: "d", status: "done" }),
        issue({ id: "x", status: "cancelled" }),
        issue({ id: "o" }),
      ]),
    ).toEqual({ needs_you: [], running: [], idle: ["o"], resolved: ["d", "x"] });
  });

  it("ranks a running issue that asks the caller under needs_you", () => {
    expect(
      group(
        [issue({ id: "a" })],
        [summary("a", { awaiting_me: [awaitingQuestion] })],
        ["a"],
      ),
    ).toEqual({ needs_you: ["a"], running: [], idle: [], resolved: [] });
  });

  it("keeps a done issue under resolved even when it awaits the caller or has a task", () => {
    expect(
      group(
        [issue({ id: "a", status: "done" })],
        [summary("a", { awaiting_me: [awaitingQuestion] })],
        ["a"],
      ),
    ).toEqual({ needs_you: [], running: [], idle: [], resolved: ["a"] });
  });

  it("skips sub-issues", () => {
    expect(group([issue({ id: "child", parent_issue_id: "a" })], [], ["child"])).toEqual({
      needs_you: [],
      running: [],
      idle: [],
      resolved: [],
    });
  });

  it("orders each group by newest activity", () => {
    const grouped = group(
      [
        issue({ id: "old", created_at: "2026-10-01T00:00:00Z" }),
        issue({ id: "active", created_at: "2026-09-01T00:00:00Z", last_activity_at: "2026-10-05T00:00:00Z" }),
        issue({ id: "replied", created_at: "2026-09-01T00:00:00Z" }),
      ],
      [summary("replied", { last_reply_at: "2026-10-09T00:00:00Z", last_read_at: "2026-10-10T00:00:00Z" })],
    );
    expect(grouped.idle).toEqual(["replied", "active", "old"]);
  });
});

describe("projectIssueActivityAt", () => {
  it("prefers the newest reply, then last activity, then creation", () => {
    const i = issue({ id: "a", last_activity_at: "2026-10-05T00:00:00Z" });
    expect(projectIssueActivityAt(i, summary("a", { last_reply_at: "2026-10-09T00:00:00Z" }))).toBe(
      "2026-10-09T00:00:00Z",
    );
    expect(projectIssueActivityAt(i, summary("a"))).toBe("2026-10-05T00:00:00Z");
    expect(projectIssueActivityAt(issue({ id: "b" }), undefined)).toBe("2026-10-01T00:00:00Z");
  });
});
