// @vitest-environment node
import { describe, expect, it } from "vitest";
import type {
  Issue,
  IssueStatusCategory,
  IssueThreadPullRequest,
  IssueThreadSummary,
} from "@multica/core/types";
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
    last_reply: null,
    open_thread_count: 0,
    participants: [],
    last_read_at: null,
    awaiting_me: [],
    latest_progress: null,
    pull_requests: [],
    deliverable_count: 0,
    last_deliverable_at: null,
    children: [],
    child_total: 0,
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
    needs_reply: result.needs_reply.map((i) => i.id),
    needs_review: result.needs_review.map((i) => i.id),
    running: result.running.map((i) => i.id),
    idle: result.idle.map((i) => i.id),
    resolved: result.resolved.map((i) => i.id),
  };
}

const none = { needs_reply: [], needs_review: [], running: [], idle: [], resolved: [] };

const awaitingQuestion = {
  comment_id: "c",
  actor_id: "agent-1",
  excerpt: "ok?",
  created_at: "2026-10-02T00:00:00Z",
};

function pr(
  state: string,
  checks_state: IssueThreadPullRequest["checks_state"],
): IssueThreadPullRequest {
  return {
    id: `${state}-${checks_state}`,
    number: 1,
    title: "PR",
    url: "https://example.com/pr/1",
    state,
    additions: 1,
    deletions: 0,
    checks_state,
  };
}

describe("groupProjectIssues", () => {
  it("puts an issue with an open agent question under needs_reply", () => {
    expect(
      group([issue({ id: "a" })], [summary("a", { awaiting_me: [awaitingQuestion] })]),
    ).toEqual({ ...none, needs_reply: ["a"] });
  });

  it("puts an unread thread where someone else spoke last under needs_review", () => {
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
    expect(grouped.needs_review).toEqual(["theirs"]);
    expect(grouped.needs_reply).toEqual([]);
    expect(grouped.idle.sort()).toEqual(["mine", "read"]);
  });

  it("puts an issue with an open PR whose checks passed or are absent under needs_review", () => {
    const grouped = group(
      [
        issue({ id: "passed" }),
        issue({ id: "nochecks" }),
        issue({ id: "failed" }),
        issue({ id: "pending" }),
        issue({ id: "merged" }),
        issue({ id: "closed" }),
      ],
      [
        summary("passed", { pull_requests: [pr("open", "passed")] }),
        summary("nochecks", { pull_requests: [pr("open", "none")] }),
        summary("failed", { pull_requests: [pr("open", "failed")] }),
        summary("pending", { pull_requests: [pr("open", "pending")] }),
        summary("merged", { pull_requests: [pr("merged", "passed")] }),
        summary("closed", { pull_requests: [pr("closed", "none")] }),
      ],
    );
    expect(grouped.needs_review.sort()).toEqual(["nochecks", "passed"]);
    expect(grouped.idle.sort()).toEqual(["closed", "failed", "merged", "pending"]);
  });

  it("puts issues with a running or queued task under running", () => {
    expect(group([issue({ id: "a" }), issue({ id: "b" })], [], ["a"])).toEqual({
      ...none,
      running: ["a"],
      idle: ["b"],
    });
  });

  it("puts done and closed issues under resolved", () => {
    expect(
      group([
        issue({ id: "d", status: "done" }),
        issue({ id: "x", status: "cancelled" }),
        issue({ id: "o" }),
      ]),
    ).toEqual({ ...none, idle: ["o"], resolved: ["d", "x"] });
  });

  it("ranks a running issue that asks the caller under needs_reply", () => {
    expect(
      group(
        [issue({ id: "a" })],
        [summary("a", { awaiting_me: [awaitingQuestion] })],
        ["a"],
      ),
    ).toEqual({ ...none, needs_reply: ["a"] });
  });

  it("ranks needs_reply above needs_review", () => {
    expect(
      group(
        [issue({ id: "a" })],
        [
          summary("a", {
            awaiting_me: [awaitingQuestion],
            last_reply_at: "2026-10-04T00:00:00Z",
            participants: [{ type: "agent", id: "agent-1" }],
            pull_requests: [pr("open", "passed")],
          }),
        ],
      ),
    ).toEqual({ ...none, needs_reply: ["a"] });
  });

  it("ranks needs_review above running", () => {
    expect(
      group(
        [issue({ id: "a" })],
        [summary("a", { pull_requests: [pr("open", "passed")] })],
        ["a"],
      ),
    ).toEqual({ ...none, needs_review: ["a"] });
  });

  it("keeps a done issue under resolved even when it awaits the caller, has a reviewable PR or a task", () => {
    expect(
      group(
        [issue({ id: "a", status: "done" })],
        [
          summary("a", {
            awaiting_me: [awaitingQuestion],
            pull_requests: [pr("open", "passed")],
          }),
        ],
        ["a"],
      ),
    ).toEqual({ ...none, resolved: ["a"] });
  });

  it("skips sub-issues", () => {
    expect(group([issue({ id: "child", parent_issue_id: "a" })], [], ["child"])).toEqual(none);
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
