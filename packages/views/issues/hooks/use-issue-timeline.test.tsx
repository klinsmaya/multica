import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";

// Mock @multica/core/issues/mutations to mimic TanStack Query v5's contract:
// useMutation returns a fresh result wrapper on every render, but the
// `mutate` / `mutateAsync` functions inside it are stable across renders.
// This is exactly the shape that previously fooled the original deps lists
// in useIssueTimeline — guarding against a regression here means future code
// can't accidentally pull the whole mutation result into a useCallback dep.
const stableHandles = vi.hoisted(() => ({
  markReadMutate: vi.fn(),
  createMutateAsync: vi.fn(async () => ({})),
  updateMutateAsync: vi.fn(async () => ({})),
  deleteMutateAsync: vi.fn(async () => ({})),
  resolveMutateAsync: vi.fn(async () => ({})),
  toggleMutate: vi.fn(),
}));

// WS event registry — captured handlers per event name so tests can simulate
// server pushes by invoking them directly.
const wsHandlers = vi.hoisted(() => new Map<string, (payload: unknown) => void>());

vi.mock("@multica/core/issues/mutations", () => ({
  useCreateComment: () => ({
    mutateAsync: stableHandles.createMutateAsync,
    mutate: vi.fn(),
    isPending: false,
  }),
  useUpdateComment: () => ({
    mutateAsync: stableHandles.updateMutateAsync,
    mutate: vi.fn(),
    isPending: false,
  }),
  useDeleteComment: () => ({
    mutateAsync: stableHandles.deleteMutateAsync,
    mutate: vi.fn(),
    isPending: false,
  }),
  useResolveComment: () => ({
    mutateAsync: stableHandles.resolveMutateAsync,
    mutate: vi.fn(),
    isPending: false,
  }),
  useToggleCommentReaction: () => ({
    mutateAsync: vi.fn(),
    mutate: stableHandles.toggleMutate,
    isPending: false,
  }),
  useMarkIssueRead: () => ({
    mutateAsync: vi.fn(),
    mutate: stableHandles.markReadMutate,
    isPending: false,
  }),
}));

vi.mock("@multica/core/issues/queries", () => ({
  issueTimelineInfiniteOptions: (id: string) => ({
    queryKey: ["issues", "timeline", id, "pages"],
    queryFn: () => Promise.resolve({ entries: [] }),
  }),
  issueKeys: {
    timeline: (id: string) => ["issues", "timeline", id],
    timelinePages: (id: string) => ["issues", "timeline", id, "pages"],
  },
}));

// Hoisted state controllable from tests — represents what the infinite query
// would return for the current render. `data` is the flat entry list of a
// single page; `pages` (when set) is a multi-page cache, newest page first.
const queryState = vi.hoisted(() => ({
  data: undefined as unknown,
  pages: null as null | Array<Record<string, unknown>>,
  isLoading: false,
  hasNextPage: false,
  isFetchingNextPage: false,
  isFetchNextPageError: false,
  fetchNextPage: vi.fn(),
}));

// Track the latest cache-update fn the hook hands to setQueryData so tests
// can assert what would have been written. `last` is the flat entry list of
// the written cache, `lastCache` the pages structure itself.
const cacheUpdates = vi.hoisted(() => ({
  last: null as unknown,
  lastCache: null as null | { pages: Array<{ entries: Array<{ id: string }> }> },
  invalidations: 0,
}));

function currentCache() {
  if (queryState.pages) return { pages: queryState.pages, pageParams: queryState.pages.map(() => null) };
  return {
    pages: [{
      entries: queryState.data ?? [],
      has_more_before: false,
      has_more_after: false,
      next_before: null,
      next_before_id: null,
    }],
    pageParams: [null],
  };
}

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useInfiniteQuery: () => ({
      data: queryState.data === undefined && !queryState.pages ? undefined : currentCache(),
      isLoading: queryState.isLoading,
      hasNextPage: queryState.hasNextPage,
      isFetchingNextPage: queryState.isFetchingNextPage,
      isFetchNextPageError: queryState.isFetchNextPageError,
      fetchNextPage: queryState.fetchNextPage,
    }),
    useQueryClient: () => ({
      invalidateQueries: vi.fn(() => {
        cacheUpdates.invalidations += 1;
      }),
      setQueryData: vi.fn((_key: unknown, updater: unknown) => {
        const next = typeof updater === "function"
          ? (updater as (old: unknown) => unknown)(currentCache())
          : updater;
        cacheUpdates.lastCache = next as typeof cacheUpdates.lastCache;
        cacheUpdates.last = flattenTimelinePages(next as { pages: never[] } | undefined);
      }),
      getQueryData: vi.fn(),
      cancelQueries: vi.fn(),
    }),
    useMutationState: () => [],
  };
});

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: (event: string, handler: (payload: unknown) => void) => {
    wsHandlers.set(event, handler);
  },
  useWSReconnect: vi.fn(),
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { flattenTimelinePages } from "@multica/core/issues/timeline-pages";
import { useIssueTimeline } from "./use-issue-timeline";

describe("useIssueTimeline", () => {
  beforeEach(() => {
    wsHandlers.clear();
    stableHandles.createMutateAsync.mockClear();
    stableHandles.updateMutateAsync.mockClear();
    stableHandles.deleteMutateAsync.mockClear();
    stableHandles.resolveMutateAsync.mockClear();
    stableHandles.toggleMutate.mockClear();
    queryState.data = [];
    queryState.pages = null;
    queryState.isLoading = false;
    queryState.hasNextPage = false;
    queryState.isFetchingNextPage = false;
    queryState.isFetchNextPageError = false;
    queryState.fetchNextPage.mockClear();
    cacheUpdates.last = null;
    cacheUpdates.lastCache = null;
    cacheUpdates.invalidations = 0;
  });

  // CommentCard is wrapped in React.memo (perf fix for long timelines, see
  // multica#1968). The memo only pays off if the callbacks passed down keep
  // the same identity across unrelated parent re-renders. TanStack Query v5
  // returns a *new* mutation result wrapper on every render, so a useCallback
  // listing the whole mutation object as a dep flips its identity every time
  // — that is the exact regression this test guards against.
  it("submitReply / editComment / deleteComment / toggleReaction keep identity across unrelated re-renders", () => {
    const { result, rerender } = renderHook(() => useIssueTimeline("issue-1", "user-1"));

    const first = {
      submitComment: result.current.submitComment,
      submitReply: result.current.submitReply,
      editComment: result.current.editComment,
      deleteComment: result.current.deleteComment,
      toggleReaction: result.current.toggleReaction,
    };

    rerender();
    rerender();

    expect(result.current.submitReply).toBe(first.submitReply);
    expect(result.current.editComment).toBe(first.editComment);
    expect(result.current.deleteComment).toBe(first.deleteComment);
    expect(result.current.toggleReaction).toBe(first.toggleReaction);
    expect(result.current.submitComment).toBe(first.submitComment);
  });

  it("passes suppressed agent ids through editComment", async () => {
    const { result } = renderHook(() => useIssueTimeline("issue-1", "user-1"));

    await act(async () => {
      await result.current.editComment("comment-1", "updated", ["attachment-1"], ["agent-1"]);
    });

    expect(stableHandles.updateMutateAsync).toHaveBeenCalledWith({
      commentId: "comment-1",
      content: "updated",
      attachmentIds: ["attachment-1"],
      suppressAgentIds: ["agent-1"],
    });
  });

  it("passes the captured comment content through editComment", async () => {
    const { result } = renderHook(() => useIssueTimeline("issue-1", "user-1"));

    await act(async () => {
      await result.current.editComment(
        "comment-1",
        "updated",
        [],
        undefined,
        "original",
      );
    });

    expect(stableHandles.updateMutateAsync).toHaveBeenCalledWith({
      commentId: "comment-1",
      content: "updated",
      attachmentIds: [],
      suppressAgentIds: undefined,
      contentBase: "original",
    });
  });

  it("comment:created appends the new entry to the cache", () => {
    queryState.data = [];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));
    const handler = wsHandlers.get("comment:created");
    act(() => {
      handler!({
        comment: {
          id: "new-c",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "u",
          content: "hi",
          parent_id: null,
          created_at: "2026-05-06T05:00:00Z",
          updated_at: "2026-05-06T05:00:00Z",
          type: "comment",
          reactions: [],
          attachments: [],
        },
      });
    });
    const updated = cacheUpdates.last as Array<{ id: string }>;
    expect(updated.map((e) => e.id)).toEqual(["new-c"]);
  });

  it("comment:created inserts at the correct sorted position by created_at", () => {
    queryState.data = [
      { type: "comment", id: "c1", actor_type: "member", actor_id: "u", created_at: "2026-05-06T01:00:00Z" },
      { type: "comment", id: "c3", actor_type: "member", actor_id: "u", created_at: "2026-05-06T03:00:00Z" },
    ];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));
    const handler = wsHandlers.get("comment:created");
    act(() => {
      handler!({
        comment: {
          id: "c2",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "u",
          content: "",
          parent_id: null,
          created_at: "2026-05-06T02:00:00Z",
          updated_at: "2026-05-06T02:00:00Z",
          type: "comment",
          reactions: [],
          attachments: [],
        },
      });
    });
    const updated = cacheUpdates.last as Array<{ id: string }>;
    expect(updated.map((e) => e.id)).toEqual(["c1", "c2", "c3"]);
  });

  it("comment:created re-sorts when the new entry is oldest", () => {
    queryState.data = [
      { type: "comment", id: "c2", actor_type: "member", actor_id: "u", created_at: "2026-05-06T02:00:00Z" },
      { type: "comment", id: "c3", actor_type: "member", actor_id: "u", created_at: "2026-05-06T03:00:00Z" },
    ];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));
    const handler = wsHandlers.get("comment:created");
    act(() => {
      handler!({
        comment: {
          id: "c1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "u",
          content: "",
          parent_id: null,
          created_at: "2026-05-06T01:00:00Z",
          updated_at: "2026-05-06T01:00:00Z",
          type: "comment",
          reactions: [],
          attachments: [],
        },
      });
    });
    const updated = cacheUpdates.last as Array<{ id: string }>;
    expect(updated.map((e) => e.id)).toEqual(["c1", "c2", "c3"]);
  });

  it("ignores WS events for other issues", () => {
    queryState.data = [];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));
    const handler = wsHandlers.get("comment:created");
    act(() => {
      handler!({
        comment: {
          id: "x",
          issue_id: "different-issue",
          author_type: "member",
          author_id: "u",
          content: "",
          parent_id: null,
          created_at: "",
          updated_at: "",
          type: "comment",
          reactions: [],
          attachments: [],
        },
      });
    });
    // setQueryData should not have been invoked for a non-matching issue.
    expect(cacheUpdates.last).toBeNull();
  });

  it("rejects a delayed comment update older than the cached revision", () => {
    queryState.data = [{
      type: "comment",
      id: "c1",
      actor_type: "member",
      actor_id: "u",
      content: "latest",
      parent_id: null,
      created_at: "2026-05-06T01:00:00Z",
      updated_at: "2026-05-06T03:00:00Z",
      revision: 3,
      reactions: [],
      attachments: [],
    }];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));

    act(() => {
      wsHandlers.get("comment:updated")!({
        comment: {
          id: "c1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "u",
          content: "stale",
          parent_id: null,
          created_at: "2026-05-06T01:00:00Z",
          updated_at: "2026-05-06T02:00:00Z",
          revision: 2,
          type: "comment",
          reactions: [],
          attachments: [],
        },
      });
    });

    expect(cacheUpdates.last).toEqual(queryState.data);
  });

  it("preserves hydrated actor identity when applying a comment update", () => {
    queryState.data = [
      {
        type: "comment",
        id: "c1",
        actor_type: "member",
        actor_id: "departed-user",
        actor_name: "Former Member",
        actor_avatar_url: "https://profiles.example.com/former.png",
        content: "before",
        parent_id: null,
        created_at: "2026-05-06T01:00:00Z",
        updated_at: "2026-05-06T01:00:00Z",
        revision: 1,
        reactions: [],
        attachments: [],
      },
    ];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));

    act(() => {
      wsHandlers.get("comment:updated")!({
        comment: {
          id: "c1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "departed-user",
          content: "after",
          parent_id: null,
          created_at: "2026-05-06T01:00:00Z",
          updated_at: "2026-05-06T02:00:00Z",
          revision: 2,
          type: "comment",
          reactions: [],
          attachments: [],
        },
      });
    });

    expect(cacheUpdates.last).toMatchObject([
      {
        content: "after",
        actor_name: "Former Member",
        actor_avatar_url: "https://profiles.example.com/former.png",
      },
    ]);
  });

  it("refetches instead of applying an unversioned event over versioned cache state", () => {
    queryState.data = [{
      type: "comment",
      id: "c1",
      actor_type: "member",
      actor_id: "u",
      content: "versioned",
      parent_id: null,
      created_at: "2026-05-06T01:00:00Z",
      updated_at: "2026-05-06T03:00:00Z",
      revision: 3,
      reactions: [],
      attachments: [],
    }];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));

    act(() => {
      wsHandlers.get("comment:updated")!({
        comment: {
          id: "c1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "u",
          content: "unversioned",
          parent_id: null,
          created_at: "2026-05-06T01:00:00Z",
          updated_at: "2026-05-06T04:00:00Z",
          type: "comment",
          reactions: [],
          attachments: [],
        },
      });
    });

    expect(cacheUpdates.last).toEqual(queryState.data);
    expect(cacheUpdates.invalidations).toBeGreaterThan(0);
  });

  // The global useRealtimeSync handler now uses refetchType: "none" for
  // timeline events, which means useIssueTimeline must own the granular
  // cache update for every event that mutates the timeline — including
  // comment:resolved / comment:unresolved. Without these handlers the
  // resolve toggle on a thread root would only update the cache when the
  // user remounts IssueDetail (the stale flag triggers a refetch), so the
  // bar/expanded view would lag the click by a navigation cycle.
  it("comment:resolved updates the matching entry in place with the new resolved fields", () => {
    queryState.data = [
      {
        type: "comment",
        id: "c1",
        actor_type: "member",
        actor_id: "u",
        actor_name: "Former Member",
        actor_avatar_url: "https://profiles.example.com/former.png",
        content: "hello",
        parent_id: null,
        created_at: "2026-05-06T01:00:00Z",
        updated_at: "2026-05-06T01:00:00Z",
        reactions: [],
        attachments: [],
        resolved_at: null,
        resolved_by_type: null,
        resolved_by_id: null,
      },
      {
        type: "comment",
        id: "c2",
        actor_type: "member",
        actor_id: "u",
        content: "untouched",
        parent_id: null,
        created_at: "2026-05-06T02:00:00Z",
        updated_at: "2026-05-06T02:00:00Z",
        reactions: [],
        attachments: [],
        resolved_at: null,
        resolved_by_type: null,
        resolved_by_id: null,
      },
    ];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));
    const handler = wsHandlers.get("comment:resolved");
    expect(handler).toBeDefined();
    act(() => {
      handler!({
        comment: {
          id: "c1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "u",
          content: "hello",
          parent_id: null,
          created_at: "2026-05-06T01:00:00Z",
          updated_at: "2026-05-06T01:00:00Z",
          type: "comment",
          reactions: [],
          attachments: [],
          resolved_at: "2026-05-06T03:00:00Z",
          resolved_by_type: "member",
          resolved_by_id: "u",
        },
      });
    });
    const updated = cacheUpdates.last as Array<{
      id: string;
      resolved_at: string | null;
      resolved_by_type: string | null;
      resolved_by_id: string | null;
      actor_name?: string;
      actor_avatar_url?: string;
    }>;
    expect(updated.map((e) => e.id)).toEqual(["c1", "c2"]);
    expect(updated[0]!.resolved_at).toBe("2026-05-06T03:00:00Z");
    expect(updated[0]!.resolved_by_type).toBe("member");
    expect(updated[0]!.resolved_by_id).toBe("u");
    expect(updated[0]!.actor_name).toBe("Former Member");
    expect(updated[0]!.actor_avatar_url).toBe(
      "https://profiles.example.com/former.png",
    );
    // Sibling entry must not change (identity preserved by .map).
    expect(updated[1]!.resolved_at).toBeNull();
  });

  it("comment:unresolved clears the resolved fields on the matching entry", () => {
    queryState.data = [
      {
        type: "comment",
        id: "c1",
        actor_type: "member",
        actor_id: "u",
        content: "hello",
        parent_id: null,
        created_at: "2026-05-06T01:00:00Z",
        updated_at: "2026-05-06T01:00:00Z",
        reactions: [],
        attachments: [],
        resolved_at: "2026-05-06T03:00:00Z",
        resolved_by_type: "member",
        resolved_by_id: "u",
      },
    ];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));
    const handler = wsHandlers.get("comment:unresolved");
    expect(handler).toBeDefined();
    act(() => {
      handler!({
        comment: {
          id: "c1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "u",
          content: "hello",
          parent_id: null,
          created_at: "2026-05-06T01:00:00Z",
          updated_at: "2026-05-06T01:00:00Z",
          type: "comment",
          reactions: [],
          attachments: [],
          resolved_at: null,
          resolved_by_type: null,
          resolved_by_id: null,
        },
      });
    });
    const updated = cacheUpdates.last as Array<{
      id: string;
      resolved_at: string | null;
    }>;
    expect(updated[0]!.resolved_at).toBeNull();
  });

  it("comment:resolved ignores events from other issues", () => {
    queryState.data = [
      {
        type: "comment",
        id: "c1",
        actor_type: "member",
        actor_id: "u",
        content: "hello",
        parent_id: null,
        created_at: "2026-05-06T01:00:00Z",
        updated_at: "2026-05-06T01:00:00Z",
        reactions: [],
        attachments: [],
        resolved_at: null,
        resolved_by_type: null,
        resolved_by_id: null,
      },
    ];
    renderHook(() => useIssueTimeline("issue-1", "user-1"));
    const handler = wsHandlers.get("comment:resolved");
    act(() => {
      handler!({
        comment: {
          id: "c1",
          issue_id: "different-issue",
          author_type: "member",
          author_id: "u",
          content: "hello",
          parent_id: null,
          created_at: "2026-05-06T01:00:00Z",
          updated_at: "2026-05-06T01:00:00Z",
          type: "comment",
          reactions: [],
          attachments: [],
          resolved_at: "2026-05-06T03:00:00Z",
          resolved_by_type: "member",
          resolved_by_id: "u",
        },
      });
    });
    expect(cacheUpdates.last).toBeNull();
  });
  describe("paged cache", () => {
    function entry(id: string, at: string, parentId: string | null = null) {
      return {
        type: "comment",
        id,
        actor_type: "member",
        actor_id: "u",
        content: id,
        parent_id: parentId,
        created_at: at,
        updated_at: at,
        reactions: [],
        attachments: [],
      };
    }

    function page(entries: unknown[], older = false) {
      return {
        entries,
        has_more_before: older,
        has_more_after: false,
        next_before: older ? "2026-05-03T00:00:00Z" : null,
        next_before_id: older ? "newest-root" : null,
      };
    }

    // pages[0] is the newest page.
    function seedTwoPages() {
      queryState.pages = [
        page([entry("newest-root", "2026-05-03T00:00:00Z"), entry("newest-reply", "2026-05-03T00:01:00Z", "newest-root")], true),
        page([entry("old-root", "2026-05-01T00:00:00Z"), entry("old-reply", "2026-05-02T00:00:00Z", "old-root")]),
      ];
    }

    function createdPayload(overrides: Record<string, unknown>) {
      return {
        comment: {
          issue_id: "issue-1",
          author_type: "member",
          author_id: "u",
          content: "",
          parent_id: null,
          created_at: "2026-05-06T05:00:00Z",
          updated_at: "2026-05-06T05:00:00Z",
          type: "comment",
          reactions: [],
          attachments: [],
          ...overrides,
        },
      };
    }

    it("returns one flat, oldest-first list across pages", () => {
      seedTwoPages();
      const { result } = renderHook(() => useIssueTimeline("issue-1", "user-1"));
      expect(result.current.timeline.map((e) => e.id)).toEqual([
        "old-root",
        "old-reply",
        "newest-root",
        "newest-reply",
      ]);
    });

    it("comment:created lands on the newest page", () => {
      seedTwoPages();
      renderHook(() => useIssueTimeline("issue-1", "user-1"));
      act(() => wsHandlers.get("comment:created")!(createdPayload({ id: "fresh" })));

      const pages = cacheUpdates.lastCache!.pages;
      expect(pages[0]!.entries.map((e) => e.id)).toContain("fresh");
      expect(pages[1]!.entries.map((e) => e.id)).not.toContain("fresh");
      // The older page is untouched, so its paging state and entries survive.
      expect(pages[1]).toBe(queryState.pages![1]);
    });

    it("comment:created puts a reply on the page of its parent", () => {
      seedTwoPages();
      renderHook(() => useIssueTimeline("issue-1", "user-1"));
      act(() => wsHandlers.get("comment:created")!(createdPayload({ id: "late-reply", parent_id: "old-root" })));

      const pages = cacheUpdates.lastCache!.pages;
      expect(pages[1]!.entries.map((e) => e.id)).toContain("late-reply");
      expect(pages[0]!.entries.map((e) => e.id)).not.toContain("late-reply");
    });

    it("comment:created is idempotent for an entry already on an older page", () => {
      seedTwoPages();
      renderHook(() => useIssueTimeline("issue-1", "user-1"));
      act(() => wsHandlers.get("comment:created")!(createdPayload({ id: "old-reply", parent_id: "old-root" })));

      expect(flattenTimelinePages(cacheUpdates.lastCache as never).filter((e) => e.id === "old-reply")).toHaveLength(1);
    });

    it("comment:deleted removes the comment and its replies from every page", () => {
      seedTwoPages();
      // A reply a realtime event put on the newest page while its root is older.
      queryState.pages![0]!.entries = [
        ...(queryState.pages![0]!.entries as unknown[]),
        entry("split-reply", "2026-05-04T00:00:00Z", "old-root"),
      ];
      renderHook(() => useIssueTimeline("issue-1", "user-1"));
      act(() => wsHandlers.get("comment:deleted")!({ comment_id: "old-root", issue_id: "issue-1" }));

      const ids = flattenTimelinePages(cacheUpdates.lastCache as never).map((e) => e.id);
      expect(ids).toEqual(["newest-root", "newest-reply"]);
    });

    it("comment:updated applies to an entry on an older page", () => {
      seedTwoPages();
      renderHook(() => useIssueTimeline("issue-1", "user-1"));
      act(() => wsHandlers.get("comment:updated")!(createdPayload({ id: "old-root", content: "edited" })));

      const edited = cacheUpdates.lastCache!.pages[1]!.entries.find((e) => e.id === "old-root") as { content?: string };
      expect(edited.content).toBe("edited");
    });

    it("loadOlder fetches the next page only while one exists and none is loading", () => {
      seedTwoPages();
      queryState.hasNextPage = true;
      const { result, rerender } = renderHook(() => useIssueTimeline("issue-1", "user-1"));
      expect(result.current.hasOlder).toBe(true);

      act(() => result.current.loadOlder());
      expect(queryState.fetchNextPage).toHaveBeenCalledTimes(1);

      queryState.isFetchingNextPage = true;
      rerender();
      expect(result.current.loadingOlder).toBe(true);
      act(() => result.current.loadOlder());
      expect(queryState.fetchNextPage).toHaveBeenCalledTimes(1);

      queryState.isFetchingNextPage = false;
      queryState.hasNextPage = false;
      rerender();
      expect(result.current.hasOlder).toBe(false);
      act(() => result.current.loadOlder());
      expect(queryState.fetchNextPage).toHaveBeenCalledTimes(1);
    });

    it("keeps loading older pages until the deep-link anchor is present", () => {
      seedTwoPages();
      queryState.hasNextPage = true;
      renderHook(() => useIssueTimeline("issue-1", "user-1", { ensureCommentId: "ancient" }));
      expect(queryState.fetchNextPage).toHaveBeenCalledTimes(1);
    });

    it("stops loading once the anchor is on a loaded page, or on error, or with no older page", () => {
      seedTwoPages();
      queryState.hasNextPage = true;
      renderHook(() => useIssueTimeline("issue-1", "user-1", { ensureCommentId: "old-reply" }));
      expect(queryState.fetchNextPage).not.toHaveBeenCalled();

      renderHook(() => useIssueTimeline("issue-1", "user-1", { ensureCommentId: "ancient" }));
      expect(queryState.fetchNextPage).toHaveBeenCalledTimes(1);
      queryState.fetchNextPage.mockClear();

      queryState.isFetchNextPageError = true;
      renderHook(() => useIssueTimeline("issue-1", "user-1", { ensureCommentId: "ancient" }));
      expect(queryState.fetchNextPage).not.toHaveBeenCalled();

      queryState.isFetchNextPageError = false;
      queryState.hasNextPage = false;
      renderHook(() => useIssueTimeline("issue-1", "user-1", { ensureCommentId: "ancient" }));
      expect(queryState.fetchNextPage).not.toHaveBeenCalled();
    });
  });
});
