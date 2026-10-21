// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TimelineEntry, TimelinePage } from "../types";
import { removeCommentSubtree } from "./comment-deletion";
import {
  flattenTimelinePages,
  hasOlderTimelinePage,
  nextTimelinePageCursor,
  singleTimelinePage,
  updateTimelinePages,
  type TimelinePagesCache,
} from "./timeline-pages";

function comment(id: string, at: string, parentId: string | null = null): TimelineEntry {
  return {
    type: "comment",
    id,
    actor_type: "member",
    actor_id: "u",
    created_at: at,
    parent_id: parentId,
    content: id,
  };
}

function page(entries: TimelineEntry[], older?: { at: string; id: string }): TimelinePage {
  return {
    entries,
    has_more_before: !!older,
    has_more_after: false,
    next_before: older?.at ?? null,
    next_before_id: older?.id ?? null,
  };
}

// pages[0] is the newest page, as in the real cache.
//   newest: root r3 (+ reply r3a, which is newer than r2), activity
//   older : root r2, root r1 (+ reply r1a)
function twoPages(): TimelinePagesCache {
  return {
    pages: [
      page(
        [
          comment("r3", "2026-01-03T00:00:00Z"),
          comment("r3a", "2026-01-03T00:05:00Z", "r3"),
          { type: "activity", id: "act", actor_type: "member", actor_id: "u", created_at: "2026-01-03T00:10:00Z", action: "x" },
        ],
        { at: "2026-01-03T00:00:00.000000Z", id: "r3" },
      ),
      page([
        comment("r2", "2026-01-02T00:00:00Z"),
        comment("r1", "2026-01-01T00:00:00Z"),
        comment("r1a", "2026-01-04T00:00:00Z", "r1"),
      ]),
    ],
    pageParams: [null, { before: "2026-01-03T00:00:00Z", beforeId: "r3" }],
  };
}

describe("nextTimelinePageCursor", () => {
  it("returns the cursor only while an older page exists and the cursor is complete", () => {
    expect(nextTimelinePageCursor(page([], { at: "t", id: "i" }))).toEqual({ before: "t", beforeId: "i" });
    expect(nextTimelinePageCursor(page([]))).toBeUndefined();
    // A server that reports has_more_before without a cursor (hard-cap clamp
    // on the unpaged path) cannot be walked.
    expect(nextTimelinePageCursor({ ...page([]), has_more_before: true })).toBeUndefined();
    expect(
      nextTimelinePageCursor({ ...page([], { at: "t", id: "i" }), next_before_id: null }),
    ).toBeUndefined();
  });
});

describe("flattenTimelinePages", () => {
  it("merges every page oldest first", () => {
    expect(flattenTimelinePages(twoPages()).map((e) => e.id)).toEqual(["r1", "r2", "r3", "r3a", "act", "r1a"]);
  });

  it("returns an empty list for a cache that has not loaded", () => {
    expect(flattenTimelinePages(undefined)).toEqual([]);
  });

  it("lists an entry present on two pages once, keeping the newest page's copy", () => {
    const newer = comment("dup", "2026-01-05T00:00:00Z");
    newer.content = "newest page";
    const older = comment("dup", "2026-01-05T00:00:00Z");
    older.content = "older page";
    const flat = flattenTimelinePages({
      pages: [page([newer]), page([older])],
      pageParams: [null, null],
    });
    expect(flat).toHaveLength(1);
    expect(flat[0]?.content).toBe("newest page");
  });
});

describe("hasOlderTimelinePage", () => {
  it("is true while the last loaded page points at an older one", () => {
    expect(hasOlderTimelinePage(undefined)).toBe(false);
    expect(hasOlderTimelinePage(singleTimelinePage([]))).toBe(false);
    expect(
      hasOlderTimelinePage({ pages: [page([], { at: "t", id: "i" })], pageParams: [null] }),
    ).toBe(true);
    // The newest page's cursor is consumed once the older page is loaded.
    expect(hasOlderTimelinePage(twoPages())).toBe(false);
  });
});

describe("updateTimelinePages", () => {
  it("leaves a cache that has not loaded alone", () => {
    expect(updateTimelinePages(undefined, (entries) => [...entries, comment("x", "2026-01-09T00:00:00Z")])).toBeUndefined();
  });

  it("returns the same cache when the edit hands back its input", () => {
    const data = twoPages();
    expect(updateTimelinePages(data, (entries) => entries)).toBe(data);
  });

  it("returns the same cache when a map changes nothing", () => {
    const data = twoPages();
    expect(updateTimelinePages(data, (entries) => entries.map((e) => e))).toBe(data);
  });

  it("puts a new root or activity on the newest page", () => {
    const next = updateTimelinePages(twoPages(), (entries) => [
      ...entries,
      comment("new", "2026-01-09T00:00:00Z"),
    ])!;
    expect(next.pages[0]!.entries.map((e) => e.id)).toContain("new");
    expect(next.pages[1]!.entries.map((e) => e.id)).not.toContain("new");
  });

  it("puts a new reply on the page of its parent", () => {
    const next = updateTimelinePages(twoPages(), (entries) => [
      ...entries,
      comment("late-reply", "2026-01-09T00:00:00Z", "r1"),
    ])!;
    expect(next.pages[1]!.entries.map((e) => e.id)).toContain("late-reply");
    expect(next.pages[0]!.entries.map((e) => e.id)).not.toContain("late-reply");
  });

  it("falls back to the newest page for a reply whose parent is not loaded", () => {
    const next = updateTimelinePages(twoPages(), (entries) => [
      ...entries,
      comment("orphan", "2026-01-09T00:00:00Z", "not-loaded"),
    ])!;
    expect(next.pages[0]!.entries.map((e) => e.id)).toContain("orphan");
  });

  it("applies a map to an entry on an older page and keeps the other pages", () => {
    const data = twoPages();
    const next = updateTimelinePages(data, (entries) =>
      entries.map((e) => (e.id === "r2" ? { ...e, content: "edited" } : e)),
    )!;
    expect(next.pages[1]!.entries.find((e) => e.id === "r2")?.content).toBe("edited");
    // The untouched newest page keeps its identity (memoized consumers).
    expect(next.pages[0]).toBe(data.pages[0]);
  });

  it("removes a thread subtree wherever it sits, across pages", () => {
    const data = twoPages();
    const next = updateTimelinePages(data, (entries) => removeCommentSubtree(entries, "r1"))!;
    const ids = flattenTimelinePages(next).map((e) => e.id);
    expect(ids).not.toContain("r1");
    expect(ids).not.toContain("r1a");
    expect(ids).toContain("r2");
    expect(next.pages[0]).toBe(data.pages[0]);
  });

  it("removes a subtree whose members were split across pages", () => {
    // A reply that a realtime event put on the newest page while its root
    // lives on an older one: deleting the root still takes the reply with it.
    const data = twoPages();
    data.pages[0]!.entries.push(comment("split", "2026-01-09T00:00:00Z", "r2"));
    const next = updateTimelinePages(data, (entries) => removeCommentSubtree(entries, "r2"))!;
    expect(flattenTimelinePages(next).map((e) => e.id)).not.toContain("split");
    expect(next.pages[0]!.entries.map((e) => e.id)).not.toContain("split");
  });

  it("keeps the paging state of every page", () => {
    const data = twoPages();
    const next = updateTimelinePages(data, (entries) => entries.filter((e) => e.id !== "r3a"))!;
    expect(next.pages[0]!.next_before_id).toBe("r3");
    expect(next.pages[0]!.has_more_before).toBe(true);
    expect(next.pageParams).toBe(data.pageParams);
  });
});
