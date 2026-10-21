/**
 * @vitest-environment jsdom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import type { WSClient } from "../api/ws-client";
import { issueKeys, type IssueSortParam } from "../issues/queries";
import type { Issue, IssueThreadSummary, ListIssuesCache } from "../types";
import { useRealtimeSync, type RealtimeSyncStores } from "./use-realtime-sync";

vi.mock("../platform/workspace-storage", () => ({
  getCurrentWsId: () => "ws-1",
  getCurrentSlug: () => "test-ws",
  // Draft stores are now loaded transitively (storage-cleanup → register-all-drafts)
  // so their persist wiring must resolve against this mock.
  createWorkspaceAwareStorage: (adapter: unknown) => adapter,
  registerForWorkspaceRehydration: () => {},
}));

vi.mock("../paths", () => ({
  useHasOnboarded: () => true,
  resolvePostAuthDestination: () => "/",
}));

// Records every ws.on handler by event name so a test can fire one directly.
function createRecordingWs(): {
  ws: WSClient;
  handlers: Record<string, (p: unknown) => void>;
} {
  const handlers: Record<string, (p: unknown) => void> = {};
  const ws = {
    on: vi.fn((event: string, handler: (p: unknown) => void) => {
      handlers[event] = handler;
      return () => {};
    }),
    onAny: vi.fn(() => () => {}),
    onReconnect: vi.fn(() => () => {}),
  } as unknown as WSClient;
  return { ws, handlers };
}

function createStores(): RealtimeSyncStores {
  return {
    authStore: Object.assign(() => ({}), {
      getState: () => ({ user: { id: "u1" } }),
      subscribe: () => () => {},
      setState: () => {},
      destroy: () => {},
    }),
  } as unknown as RealtimeSyncStores;
}

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

const updatedSort: IssueSortParam = {
  sort_by: "updated_at",
  sort_direction: "desc",
};
const positionSort: IssueSortParam = { sort_by: "position" };
const updatedBoardKey = issueKeys.listSorted("ws-1", updatedSort);
const positionBoardKey = issueKeys.listSorted("ws-1", positionSort);
const lastActivityBoardKey = issueKeys.listSorted("ws-1", {
  sort_by: "last_activity",
  sort_direction: "desc",
});

function bucketed(): ListIssuesCache {
  return { byStatus: { unstarted: { issues: [], total: 1 } } };
}

describe("useRealtimeSync — comment activity cache coherence", () => {
  let qc: QueryClient;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  });

  afterEach(() => {
    qc.clear();
    vi.clearAllMocks();
  });

  it("invalidates the updated_at-sorted board but leaves the position board", () => {
    qc.setQueryData<ListIssuesCache>(updatedBoardKey, bucketed());
    qc.setQueryData<ListIssuesCache>(positionBoardKey, bucketed());
    qc.setQueryData<ListIssuesCache>(lastActivityBoardKey, bucketed());
    qc.setQueryData(issueKeys.timeline("issue-1"), []);

    const { ws, handlers } = createRecordingWs();
    renderHook(() => useRealtimeSync(ws, createStores()), {
      wrapper: createWrapper(qc),
    });

    handlers["comment:created"]?.({
      comment: { id: "c1", issue_id: "issue-1" },
    });

    expect(qc.getQueryState(updatedBoardKey)?.isInvalidated).toBe(true);
    expect(qc.getQueryState(lastActivityBoardKey)?.isInvalidated).toBe(true);
    expect(qc.getQueryState(positionBoardKey)?.isInvalidated).toBe(false);
    // The per-issue timeline is still invalidated as before.
    expect(
      qc.getQueryState(issueKeys.timeline("issue-1"))?.isInvalidated,
    ).toBe(true);
  });

  it("uses update/delete owner revisions and re-sorts only last_activity", () => {
    const detailKey = issueKeys.detail("ws-1", "issue-1");
    qc.setQueryData<Issue>(detailKey, {
      id: "issue-1",
      workspace_id: "ws-1",
      revision: 1,
    } as Issue);
    qc.setQueryData<ListIssuesCache>(updatedBoardKey, bucketed());
    qc.setQueryData<ListIssuesCache>(positionBoardKey, bucketed());
    qc.setQueryData<ListIssuesCache>(lastActivityBoardKey, bucketed());
    qc.setQueryData(issueKeys.timeline("issue-1"), []);

    const { ws, handlers } = createRecordingWs();
    renderHook(() => useRealtimeSync(ws, createStores()), {
      wrapper: createWrapper(qc),
    });

    handlers["comment:updated"]?.({
      comment: { id: "c1", issue_id: "issue-1" },
      issue_revision: 2,
    });

    expect(qc.getQueryState(detailKey)?.isInvalidated).toBe(true);
    expect(qc.getQueryState(lastActivityBoardKey)?.isInvalidated).toBe(true);
    expect(qc.getQueryState(updatedBoardKey)?.isInvalidated).toBe(false);
    expect(qc.getQueryState(positionBoardKey)?.isInvalidated).toBe(false);

    qc.setQueryData<Issue>(detailKey, {
      id: "issue-1",
      workspace_id: "ws-1",
      revision: 2,
    } as Issue);
    qc.setQueryData<ListIssuesCache>(lastActivityBoardKey, bucketed());

    handlers["comment:deleted"]?.({
      comment_id: "c1",
      issue_id: "issue-1",
      issue_revision: 3,
    });

    expect(qc.getQueryState(detailKey)?.isInvalidated).toBe(true);
    expect(qc.getQueryState(lastActivityBoardKey)?.isInvalidated).toBe(true);
  });

  it("ignores a comment event with no issue_id", () => {
    qc.setQueryData<ListIssuesCache>(updatedBoardKey, bucketed());

    const { handlers } = (() => {
      const rec = createRecordingWs();
      renderHook(() => useRealtimeSync(rec.ws, createStores()), {
        wrapper: createWrapper(qc),
      });
      return rec;
    })();

    handlers["comment:created"]?.({ comment: { id: "c1" } });

    expect(qc.getQueryState(updatedBoardKey)?.isInvalidated).toBe(false);
  });

  describe("thread summaries", () => {
    const key = issueKeys.threadSummaries("ws-1", "issue-1");
    const otherKey = issueKeys.threadSummaries("ws-1", "issue-2");
    const row: IssueThreadSummary = {
      issue_id: "issue-1",
      reply_count: 1,
      last_reply_at: "2026-10-01T00:00:00Z",
      last_reply_excerpt: null,
      open_thread_count: 1,
      participants: [{ type: "member", id: "u1" }],
      last_read_at: null,
      awaiting_me: [],
      latest_progress: null,
      pull_requests: [],
      deliverable_count: 0,
      last_deliverable_at: null,
    };

    function mount() {
      qc.setQueryData<IssueThreadSummary[]>(key, [row]);
      qc.setQueryData<IssueThreadSummary[]>(otherKey, []);
      const { ws, handlers } = createRecordingWs();
      renderHook(() => useRealtimeSync(ws, createStores()), {
        wrapper: createWrapper(qc),
      });
      return handlers;
    }

    it("patches the matching summary cache on comment:created", () => {
      const handlers = mount();

      handlers["comment:created"]?.({
        comment: {
          id: "c2",
          issue_id: "issue-1",
          author_type: "agent",
          author_id: "a1",
          type: "comment",
          parent_id: "c1",
          created_at: "2026-10-02T00:00:00Z",
          resolved_at: null,
        },
      });

      expect(qc.getQueryData<IssueThreadSummary[]>(key)?.[0]).toMatchObject({
        reply_count: 2,
        open_thread_count: 1,
        participants: [
          { type: "agent", id: "a1" },
          { type: "member", id: "u1" },
        ],
      });
      expect(qc.getQueryData(otherKey)).toEqual([]);
    });

    it.each([
      ["comment:deleted", { issue_id: "issue-1", comment_id: "c1" }],
      ["comment:resolved", { comment: { id: "c1", issue_id: "issue-1" } }],
      ["comment:unresolved", { comment: { id: "c1", issue_id: "issue-1" } }],
    ])("invalidates every summary cache on %s", (event, payload) => {
      const handlers = mount();

      handlers[event]?.(payload);

      expect(qc.getQueryState(key)?.isInvalidated).toBe(true);
      expect(qc.getQueryState(otherKey)?.isInvalidated).toBe(true);
    });
  });
});
