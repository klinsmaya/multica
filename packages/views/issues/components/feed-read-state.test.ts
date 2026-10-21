// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { IssueThreadSummary } from "@multica/core/types";
import { getFeedReadState } from "./feed-read-state";

const ME = "user-me";

function summary(overrides: Partial<IssueThreadSummary> = {}): IssueThreadSummary {
  return {
    issue_id: "issue-1",
    reply_count: 1,
    last_reply_at: "2026-10-02T00:00:00Z",
    open_thread_count: 0,
    participants: [{ type: "agent", id: "agent-1" }],
    last_read_at: null,
    awaiting_me: [],
    latest_progress: null,
    pull_requests: [],
    deliverable_count: 0,
    last_deliverable_at: null,
    ...overrides,
  };
}

describe("getFeedReadState", () => {
  it("is read without a summary or without any counted comment", () => {
    expect(getFeedReadState(undefined, ME)).toBe("read");
    expect(getFeedReadState(summary({ last_reply_at: null, participants: [] }), ME)).toBe("read");
  });

  it("counts a comment with no cursor as unread", () => {
    expect(getFeedReadState(summary({ last_read_at: null }), ME)).toBe("awaiting");
  });

  it("is read when the cursor is at or after the newest comment", () => {
    expect(getFeedReadState(summary({ last_read_at: "2026-10-02T00:00:00Z" }), ME)).toBe("read");
    expect(getFeedReadState(summary({ last_read_at: "2026-10-03T00:00:00Z" }), ME)).toBe("read");
  });

  it("is unread again once a comment lands after the cursor", () => {
    expect(getFeedReadState(summary({ last_read_at: "2026-10-01T00:00:00Z" }), ME)).toBe("awaiting");
  });

  it("compares sub-second timestamps, not strings", () => {
    const base = { last_reply_at: "2026-10-02T00:00:00.100Z" };
    expect(getFeedReadState(summary({ ...base, last_read_at: "2026-10-02T00:00:00.050Z" }), ME)).toBe("awaiting");
    expect(getFeedReadState(summary({ ...base, last_read_at: "2026-10-02T00:00:00.200Z" }), ME)).toBe("read");
  });

  it("is awaiting only when the latest participant is someone else", () => {
    const mine = summary({ participants: [{ type: "member", id: ME }, { type: "agent", id: "agent-1" }] });
    expect(getFeedReadState(mine, ME)).toBe("unread");

    const theirs = summary({ participants: [{ type: "agent", id: "agent-1" }, { type: "member", id: ME }] });
    expect(getFeedReadState(theirs, ME)).toBe("awaiting");
  });

  it("does not mistake an agent or another member with the same id for the user", () => {
    expect(getFeedReadState(summary({ participants: [{ type: "agent", id: ME }] }), ME)).toBe("awaiting");
    expect(getFeedReadState(summary({ participants: [{ type: "member", id: "user-2" }] }), ME)).toBe("awaiting");
  });

  it("treats everyone as someone else while the user is unknown", () => {
    expect(getFeedReadState(summary({ participants: [{ type: "member", id: ME }] }), undefined)).toBe("awaiting");
  });
});
