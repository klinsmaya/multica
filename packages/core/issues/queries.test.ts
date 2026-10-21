// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { QueryClient, QueryObserver } from "@tanstack/react-query";

import { setApiInstance } from "../api";
import { ApiError, type ApiClient } from "../api/client";
import type {
  Issue,
  IssueTableRowsRequest,
  IssueThreadSummary,
  IssueTableRowsResponse,
  ListIssuesCache,
  ListIssuesParams,
  ListIssuesResponse,
} from "../types";
import { pruneDeletedIssueFromListCaches } from "./delete-cache";
import {
  CHILDREN_BY_PARENTS_CHUNK_SIZE,
  ISSUE_PAGE_SIZE,
  PROJECT_GANTT_MAX_ISSUES,
  PROJECT_GANTT_PAGE_LIMIT,
  THREAD_SUMMARIES_CHUNK_SIZE,
  childrenByParentsOptions,
  childIssuesOptions,
  findIssueInListCache,
  flattenIssueBuckets,
  issueIdentifierOptions,
  issueKeys,
  issueListOptions,
  issueTableRowPageOptions,
  issueTimelineInfiniteOptions,
  issueTimelineOptions,
  projectGanttIssuesOptions,
  sourceContextPreviewOptions,
  threadSummariesOptions,
  childIssueProgressOptions,
  childIssueProgressForParentsOptions,
  CHILD_PROGRESS_PARENTS_CHUNK_SIZE,
} from "./queries";
import { onIssueUpdated } from "./ws-updaters";
import { TIMELINE_PAGE_SIZE } from "./timeline-pages";

const WS_ID = "ws-1";
const PROJECT_ID = "project-1";

describe("sourceContextPreviewOptions", () => {
  it("isolates preview cache entries by workspace", () => {
    expect(sourceContextPreviewOptions("ws-1", "comment-1").queryKey).toEqual([
      "source-context",
      "preview",
      "ws-1",
      "comment-1",
    ]);
    expect(sourceContextPreviewOptions("ws-2", "comment-1").queryKey).not.toEqual(
      sourceContextPreviewOptions("ws-1", "comment-1").queryKey,
    );
  });
});

function makeIssue(idx: number, overrides: Partial<Issue> = {}): Issue {
  return {
    id: `issue-${idx}`,
    workspace_id: WS_ID,
    number: idx,
    identifier: `MUL-${idx}`,
    title: `Issue ${idx}`,
    description: null,
    status: "todo",
    priority: "none",
    assignee_type: null,
    assignee_id: null,
    creator_type: "member",
    creator_id: "user-1",
    parent_issue_id: null,
    project_id: PROJECT_ID,
    position: idx,
    stage: null,
    start_date: "2026-05-01T00:00:00Z",
    due_date: null,
    labels: [],
    metadata: {},
  properties: {},
    created_at: "2025-01-01T00:00:00Z",
    updated_at: "2025-01-01T00:00:00Z",
    ...overrides,
  };
}

// Type-only shim — only the methods the queries.ts code path under test calls.
function installFakeApi(listIssues: (params?: ListIssuesParams) => Promise<ListIssuesResponse>) {
  setApiInstance({ listIssues } as unknown as ApiClient);
}

function installFakeChildrenApi(
  listChildrenByParents: (parentIds: string[]) => Promise<{ issues: Issue[] }>,
) {
  setApiInstance({ listChildrenByParents } as unknown as ApiClient);
}

function installFakeChildApi(
  listChildIssues: (
    parentId: string,
    page?: { limit: number; offset: number },
  ) => Promise<{ issues: Issue[] }>,
) {
  setApiInstance({ listChildIssues } as unknown as ApiClient);
}

function installFakeIssueApi(
  getIssue: (id: string, options?: { signal?: AbortSignal }) => Promise<Issue>,
) {
  setApiInstance({ getIssue } as unknown as ApiClient);
}

describe("childIssuesOptions", () => {
  it("fetches the full list without a page and keeps it apart from page caches", async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    const parentId = "parent-1";
    const child = makeIssue(1, { parent_issue_id: parentId });
    const listChildIssues = vi.fn().mockResolvedValue({ issues: [child] });
    installFakeChildApi(listChildIssues);

    await qc.fetchQuery(childIssuesOptions(WS_ID, parentId));

    expect(listChildIssues).toHaveBeenCalledWith(parentId);
    expect(qc.getQueryData(issueKeys.children(WS_ID, parentId))).toEqual([child]);
    expect(
      qc.getQueryData(issueKeys.childrenPage(WS_ID, parentId, { limit: 50, offset: 0 })),
    ).toBeUndefined();
  });

  it("fetches one page under its own key without touching the full-list cache", async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    const parentId = "parent-1";
    const page = { limit: 50, offset: 50 };
    const child = makeIssue(51, { parent_issue_id: parentId });
    const listChildIssues = vi.fn().mockResolvedValue({ issues: [child] });
    installFakeChildApi(listChildIssues);

    await qc.fetchQuery(childIssuesOptions(WS_ID, parentId, page));

    expect(listChildIssues).toHaveBeenCalledWith(parentId, page);
    expect(qc.getQueryData(issueKeys.childrenPage(WS_ID, parentId, page))).toEqual([child]);
    expect(qc.getQueryData(issueKeys.children(WS_ID, parentId))).toBeUndefined();
  });

  it("is invalidated together with the full list by the per-parent children key", async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    const parentId = "parent-1";
    const page = { limit: 50, offset: 0 };
    qc.setQueryData(issueKeys.childrenPage(WS_ID, parentId, page), []);

    await qc.invalidateQueries({ queryKey: issueKeys.children(WS_ID, parentId) });

    expect(
      qc.getQueryState(issueKeys.childrenPage(WS_ID, parentId, page))?.isInvalidated,
    ).toBe(true);
  });
});

describe("issueTableRowPageOptions", () => {
  // Reproduces the "count correct, issue missing until page refresh" bug: a row
  // page gets invalidated while its dynamic useQueries observer is detached, then
  // the observer reattaches. Under the global `staleTime: Infinity` default the
  // page is stale only because it is invalidated, so the reattaching observer
  // MUST refetch it. `refetchOnMount: false` used to suppress that refetch and
  // strand the row stale; `retryOnMount: false` does not.
  const request: IssueTableRowsRequest = {
    query: {
      scope: { kind: "workspace" },
      filters: {},
      sort: { field: "position", direction: "asc" },
    },
    group: { kind: "status" },
    group_key: "todo",
    hierarchy: { enabled: false },
    parent_id: null,
    page: { limit: 50, cursor: null },
  };

  function makeRowsResponse(issues: Issue[]): IssueTableRowsResponse {
    return {
      query_fingerprint: "fp",
      group_key: "todo",
      parent_id: null,
      total: issues.length,
      rows: issues.map((issue) => ({ issue, direct_child_count: 0 })),
      branch_total: issues.length,
      next_cursor: null,
    };
  }

  function rowIssueIds(response: IssueTableRowsResponse | undefined): string[] {
    return response?.rows.map((row) => row.issue.id) ?? [];
  }

  function installFakeTableRowsApi(
    listIssueTableRows: (
      params: IssueTableRowsRequest,
    ) => Promise<IssueTableRowsResponse>,
  ) {
    setApiInstance({ listIssueTableRows } as unknown as ApiClient);
  }

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("refetches an invalidated page when its observer reattaches", async () => {
    // Global default: server state stays fresh until explicitly invalidated.
    const qc = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity } },
    });
    const listIssueTableRows = vi
      .fn<
        (params: IssueTableRowsRequest) => Promise<IssueTableRowsResponse>
      >()
      // Head snapshot, then the post-move snapshot that includes the new issue.
      .mockResolvedValueOnce(makeRowsResponse([makeIssue(1)]))
      .mockResolvedValueOnce(makeRowsResponse([makeIssue(1), makeIssue(2)]));
    installFakeTableRowsApi(listIssueTableRows);

    const options = issueTableRowPageOptions(WS_ID, request);

    const observer1 = new QueryObserver(qc, options);
    const unsubscribe1 = observer1.subscribe(() => {});
    await vi.waitFor(() => {
      expect(listIssueTableRows).toHaveBeenCalledTimes(1);
      expect(rowIssueIds(observer1.getCurrentResult().data)).toEqual([
        "issue-1",
      ]);
    });

    // Observer detaches (sibling branch left the viewport), then the row page is
    // invalidated while no observer is active — it only gets marked stale.
    unsubscribe1();
    await qc.invalidateQueries({ queryKey: options.queryKey });
    const cached = qc.getQueryState(options.queryKey);
    expect(cached?.isInvalidated).toBe(true);
    expect(cached?.fetchStatus).toBe("idle");

    // Observer reattaches: the invalidated page must refetch and pick up issue-2.
    const observer2 = new QueryObserver(qc, options);
    const unsubscribe2 = observer2.subscribe(() => {});
    await vi.waitFor(() => {
      expect(listIssueTableRows).toHaveBeenCalledTimes(2);
      expect(rowIssueIds(observer2.getCurrentResult().data)).toEqual([
        "issue-1",
        "issue-2",
      ]);
    });

    unsubscribe2();
    qc.clear();
  });

  it("keeps an errored page errored on reattach (no auto-retry)", async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity } },
    });
    const listIssueTableRows = vi
      .fn<
        (params: IssueTableRowsRequest) => Promise<IssueTableRowsResponse>
      >()
      .mockRejectedValue(new Error("boom"));
    installFakeTableRowsApi(listIssueTableRows);

    const options = issueTableRowPageOptions(WS_ID, request);

    const observer1 = new QueryObserver(qc, options);
    const unsubscribe1 = observer1.subscribe(() => {});
    await vi.waitFor(() => {
      expect(observer1.getCurrentResult().status).toBe("error");
    });
    expect(listIssueTableRows).toHaveBeenCalledTimes(1);
    unsubscribe1();

    // Reattaching an errored page stays idle — `retryOnMount: false` blocks the
    // automatic retry; only an explicit Retry re-runs it. The fetch decision is
    // synchronous, so the observer never enters `fetching`.
    const observer2 = new QueryObserver(qc, options);
    const unsubscribe2 = observer2.subscribe(() => {});
    expect(observer2.getCurrentResult().status).toBe("error");
    expect(observer2.getCurrentResult().fetchStatus).toBe("idle");
    expect(listIssueTableRows).toHaveBeenCalledTimes(1);

    unsubscribe2();
    qc.clear();
  });

  it("does not auto-retry a background-refetch error on a page that still has data", async () => {
    // The tricky case: a page loads OK, then an invalidation-triggered background
    // refetch fails. TanStack flags such a page `isInvalidated: true` (see its
    // "error" reducer), so it is stale AND errored while keeping the old data. A
    // plain `refetchOnMount: true` would re-fire the failing request on every
    // observer reattach; `retryOnMount: false` alone does NOT cover this path
    // because it only guards no-data first-load errors. The `refetchOnMount`
    // status guard is what keeps the errored page stable until an explicit Retry.
    const qc = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity } },
    });
    const listIssueTableRows = vi
      .fn<
        (params: IssueTableRowsRequest) => Promise<IssueTableRowsResponse>
      >()
      .mockResolvedValueOnce(makeRowsResponse([makeIssue(1)])) // initial load
      .mockRejectedValueOnce(new Error("refetch failed")) // background refetch
      .mockResolvedValueOnce(makeRowsResponse([makeIssue(1), makeIssue(2)])); // explicit Retry
    installFakeTableRowsApi(listIssueTableRows);

    const options = issueTableRowPageOptions(WS_ID, request);

    const observer1 = new QueryObserver(qc, options);
    const unsubscribe1 = observer1.subscribe(() => {});
    await vi.waitFor(() => {
      expect(observer1.getCurrentResult().status).toBe("success");
      expect(rowIssueIds(observer1.getCurrentResult().data)).toEqual([
        "issue-1",
      ]);
    });

    // Invalidate while the observer is active: the background refetch fires and
    // fails, leaving the page errored-with-data and flagged invalidated.
    void qc.invalidateQueries({ queryKey: options.queryKey }).catch(() => {});
    await vi.waitFor(() => {
      expect(observer1.getCurrentResult().status).toBe("error");
    });
    expect(listIssueTableRows).toHaveBeenCalledTimes(2);
    expect(rowIssueIds(observer1.getCurrentResult().data)).toEqual(["issue-1"]);
    expect(qc.getQueryState(options.queryKey)?.isInvalidated).toBe(true);

    // Detach + reattach must NOT re-fire the failing request.
    unsubscribe1();
    const observer2 = new QueryObserver(qc, options);
    const unsubscribe2 = observer2.subscribe(() => {});
    expect(observer2.getCurrentResult().status).toBe("error");
    expect(observer2.getCurrentResult().fetchStatus).toBe("idle");
    expect(listIssueTableRows).toHaveBeenCalledTimes(2);

    // Only an explicit Retry re-runs it — and then the fresh page renders.
    await observer2.refetch();
    expect(listIssueTableRows).toHaveBeenCalledTimes(3);
    expect(rowIssueIds(observer2.getCurrentResult().data)).toEqual([
      "issue-1",
      "issue-2",
    ]);

    unsubscribe2();
    qc.clear();
  });
});

describe("issueListOptions", () => {
  const todoIssue = makeIssue(1, { status: "todo", status_category: "unstarted" });
  const startedIssue = makeIssue(2, { status: "in_progress", status_category: "started" });
  const doneIssue = makeIssue(3, { status: "done", status_category: "done" });
  // A custom status the server did not resolve to a category.
  const customIssue = makeIssue(4, { status: "qa" });

  let qc: QueryClient;
  let listIssues: Mock<(params?: ListIssuesParams) => Promise<ListIssuesResponse>>;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    // Like the real client, the fake ignores `status_category`: every call
    // returns the same first page.
    listIssues = vi.fn<(params?: ListIssuesParams) => Promise<ListIssuesResponse>>().mockResolvedValue({
      issues: [todoIssue, startedIssue, doneIssue, customIssue],
      total: 120,
    });
    installFakeApi(listIssues);
  });

  afterEach(() => {
    qc.clear();
  });

  async function fetchList() {
    const options = issueListOptions(WS_ID);
    await qc.fetchQuery(options);
    return () => qc.getQueryData<ListIssuesCache>(options.queryKey)!;
  }

  it("sends one list request per fetch", async () => {
    await fetchList();

    expect(listIssues).toHaveBeenCalledTimes(1);
    expect(listIssues).toHaveBeenCalledWith({ limit: ISSUE_PAGE_SIZE, offset: 0 });
  });

  it("puts each issue in its own category's bucket only", async () => {
    const cache = (await fetchList())();

    expect(cache.byStatus).toEqual({
      unstarted: { issues: [todoIssue, customIssue], total: 2 },
      started: { issues: [startedIssue], total: 1 },
      done: { issues: [doneIssue], total: 1 },
      closed: { issues: [], total: 0 },
    });
    expect(flattenIssueBuckets(cache).map((issue) => issue.id)).toEqual([
      todoIssue.id,
      customIssue.id,
      startedIssue.id,
      doneIssue.id,
    ]);
  });

  it("drops a deleted issue from every bucket", async () => {
    const readCache = await fetchList();

    pruneDeletedIssueFromListCaches(qc, WS_ID, todoIssue.id);

    expect(flattenIssueBuckets(readCache()).map((issue) => issue.id)).not.toContain(todoIssue.id);
  });

  it("keeps only the updated copy of an issue moved to done", async () => {
    const readCache = await fetchList();

    onIssueUpdated(
      qc,
      WS_ID,
      { id: todoIssue.id, status: "done", status_category: "done" },
      { statusChanged: true },
    );

    const copies = flattenIssueBuckets(readCache()).filter((issue) => issue.id === todoIssue.id);
    expect(copies).toEqual([{ ...todoIssue, status: "done", status_category: "done" }]);
  });
});

describe("projectGanttIssuesOptions", () => {
  let qc: QueryClient;

  beforeEach(() => {
    qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("returns the first page directly when it fits under PROJECT_GANTT_PAGE_LIMIT", async () => {
    const listIssues = vi
      .fn<(params?: ListIssuesParams) => Promise<ListIssuesResponse>>()
      .mockResolvedValue({
        issues: [makeIssue(1), makeIssue(2)],
        total: 2,
      });
    installFakeApi(listIssues);

    const data = await qc.fetchQuery(projectGanttIssuesOptions(WS_ID, PROJECT_ID));

    expect(listIssues).toHaveBeenCalledTimes(1);
    expect(listIssues).toHaveBeenCalledWith({
      project_id: PROJECT_ID,
      scheduled: true,
      limit: PROJECT_GANTT_PAGE_LIMIT,
      offset: 0,
    });
    expect(data).toHaveLength(2);
  });

  it("loops through pages until total is satisfied (no silent truncation)", async () => {
    const total = PROJECT_GANTT_PAGE_LIMIT + 7;
    const firstPage = Array.from({ length: PROJECT_GANTT_PAGE_LIMIT }, (_, i) =>
      makeIssue(i),
    );
    const secondPage = Array.from({ length: 7 }, (_, i) =>
      makeIssue(PROJECT_GANTT_PAGE_LIMIT + i),
    );

    const listIssues = vi
      .fn<(params?: ListIssuesParams) => Promise<ListIssuesResponse>>()
      .mockImplementation(async (params) => {
        if (!params) throw new Error("expected params");
        const offset = params.offset ?? 0;
        if (offset === 0)
          return { issues: firstPage, total };
        if (offset === PROJECT_GANTT_PAGE_LIMIT)
          return { issues: secondPage, total };
        throw new Error(`unexpected offset ${offset}`);
      });
    installFakeApi(listIssues);

    const data = await qc.fetchQuery(projectGanttIssuesOptions(WS_ID, PROJECT_ID));

    expect(listIssues).toHaveBeenCalledTimes(2);
    expect(data).toHaveLength(total);
  });

  it("stops looping when the server reports a smaller-than-limit page (safety net for total drift)", async () => {
    // Server says `total` is huge but only ever returns short pages — the
    // loop must terminate on the first short page to avoid an infinite fetch.
    const listIssues = vi
      .fn<(params?: ListIssuesParams) => Promise<ListIssuesResponse>>()
      .mockResolvedValue({
        issues: [makeIssue(1)],
        total: PROJECT_GANTT_MAX_ISSUES,
      });
    installFakeApi(listIssues);

    const data = await qc.fetchQuery(projectGanttIssuesOptions(WS_ID, PROJECT_ID));

    expect(listIssues).toHaveBeenCalledTimes(1);
    expect(data).toHaveLength(1);
  });

  it("uses the project-scoped Gantt cache key", () => {
    const options = projectGanttIssuesOptions(WS_ID, PROJECT_ID);
    expect(options.queryKey).toEqual(issueKeys.projectGantt(WS_ID, PROJECT_ID));
  });

  it("threads the assignee-type tab into the request and the cache key", async () => {
    const listIssues = vi
      .fn<(params?: ListIssuesParams) => Promise<ListIssuesResponse>>()
      .mockResolvedValue({ issues: [makeIssue(1)], total: 1 });
    installFakeApi(listIssues);

    const agentsTab = projectGanttIssuesOptions(WS_ID, PROJECT_ID, [
      "agent",
      "squad",
    ]);
    await qc.fetchQuery(agentsTab);

    expect(listIssues).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: PROJECT_ID,
        scheduled: true,
        assignee_types: ["agent", "squad"],
      }),
    );
    // Distinct tabs must not share a cache entry.
    expect(agentsTab.queryKey).not.toEqual(
      projectGanttIssuesOptions(WS_ID, PROJECT_ID).queryKey,
    );
    // The unrestricted tab never sends the param.
    const unrestricted = vi
      .fn<(params?: ListIssuesParams) => Promise<ListIssuesResponse>>()
      .mockResolvedValue({ issues: [], total: 0 });
    installFakeApi(unrestricted);
    await qc.fetchQuery(projectGanttIssuesOptions(WS_ID, PROJECT_ID));
    expect(unrestricted.mock.calls[0]![0]).not.toHaveProperty("assignee_types");
  });
});

describe("childrenByParentsOptions chunking", () => {
  let qc: QueryClient;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("issues a single request when parentIds fit under the chunk size", async () => {
    const parentIds = Array.from({ length: 50 }, (_, i) => `p-${i}`);
    const listChildrenByParents = vi
      .fn<(ids: string[]) => Promise<{ issues: Issue[] }>>()
      .mockResolvedValue({ issues: [] });
    installFakeChildrenApi(listChildrenByParents);

    await qc.fetchQuery(childrenByParentsOptions(WS_ID, parentIds, qc));

    expect(listChildrenByParents).toHaveBeenCalledTimes(1);
    expect(listChildrenByParents).toHaveBeenCalledWith(parentIds);
  });

  it("chunks parentIds into multiple requests when over the server cap", async () => {
    // 2.5 chunks worth of parents → 3 parallel requests.
    const count = CHILDREN_BY_PARENTS_CHUNK_SIZE * 2 + 17;
    const parentIds = Array.from({ length: count }, (_, i) => `p-${i}`);
    const calls: string[][] = [];
    const listChildrenByParents = vi
      .fn<(ids: string[]) => Promise<{ issues: Issue[] }>>()
      .mockImplementation(async (ids) => {
        calls.push(ids);
        return { issues: [] };
      });
    installFakeChildrenApi(listChildrenByParents);

    await qc.fetchQuery(childrenByParentsOptions(WS_ID, parentIds, qc));

    expect(listChildrenByParents).toHaveBeenCalledTimes(3);
    expect(calls[0]).toHaveLength(CHILDREN_BY_PARENTS_CHUNK_SIZE);
    expect(calls[1]).toHaveLength(CHILDREN_BY_PARENTS_CHUNK_SIZE);
    expect(calls[2]).toHaveLength(17);
    // Together the chunks must cover every input parent id.
    expect(calls.flat().sort()).toEqual(parentIds.slice().sort());
  });

  it("merges children from all chunks into one grouped map", async () => {
    const parentIds = Array.from(
      { length: CHILDREN_BY_PARENTS_CHUNK_SIZE + 1 },
      (_, i) => `p-${i}`,
    );
    // First chunk returns a child of p-0, second chunk returns a child of
    // the last parent id (which lives alone in chunk 2).
    const lastId = parentIds[parentIds.length - 1]!;
    const listChildrenByParents = vi
      .fn<(ids: string[]) => Promise<{ issues: Issue[] }>>()
      .mockImplementation(async (ids) => {
        if (ids.includes(lastId)) {
          return { issues: [{ ...makeIssue(99), parent_issue_id: lastId }] };
        }
        return { issues: [{ ...makeIssue(1), parent_issue_id: "p-0" }] };
      });
    installFakeChildrenApi(listChildrenByParents);

    const grouped = await qc.fetchQuery(
      childrenByParentsOptions(WS_ID, parentIds, qc),
    );

    expect(grouped.get("p-0")).toHaveLength(1);
    expect(grouped.get(lastId)).toHaveLength(1);
  });
});

describe("issueIdentifierOptions", () => {
  let qc: QueryClient;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("resolves the identifier through the exact-lookup endpoint, not search", async () => {
    const getIssue = vi
      .fn<(id: string, options?: { signal?: AbortSignal }) => Promise<Issue>>()
      .mockResolvedValue(makeIssue(7));
    installFakeIssueApi(getIssue);

    const data = await qc.fetchQuery(issueIdentifierOptions(WS_ID, "MUL-7"));

    expect(data?.id).toBe("issue-7");
    expect(getIssue).toHaveBeenCalledTimes(1);
    expect(getIssue.mock.calls[0]?.[0]).toBe("MUL-7");
  });

  it("returns null on 404 (unknown number or wrong workspace prefix)", async () => {
    // The server enforces the prefix now: TES-7 in a MUL workspace 404s.
    const getIssue = vi
      .fn<(id: string) => Promise<Issue>>()
      .mockRejectedValue(new ApiError("issue not found", 404, "Not Found"));
    installFakeIssueApi(getIssue);

    const data = await qc.fetchQuery(issueIdentifierOptions(WS_ID, "TES-7"));

    expect(data).toBeNull();
  });

  it("propagates non-404 failures instead of caching them as 'no such issue'", async () => {
    const getIssue = vi
      .fn<(id: string) => Promise<Issue>>()
      .mockRejectedValue(new ApiError("boom", 500, "Internal Server Error"));
    installFakeIssueApi(getIssue);

    await expect(
      qc.fetchQuery(issueIdentifierOptions(WS_ID, "MUL-9")),
    ).rejects.toThrow("boom");
  });

  it.each(["found", "missing"] as const)(
    "shares a pending %s lookup across rapid observer remounts and caches its result",
    async (outcome) => {
      let resolveLookup!: (issue: Issue) => void;
      let rejectLookup!: (error: Error) => void;
      let abortedLookups = 0;
      const getIssue = vi
        .fn<(id: string, options?: { signal?: AbortSignal }) => Promise<Issue>>()
        .mockImplementation((_id, options) => new Promise((resolve, reject) => {
          resolveLookup = resolve;
          rejectLookup = reject;
          options?.signal?.addEventListener("abort", () => {
            abortedLookups++;
            reject(new DOMException("Unmounted", "AbortError"));
          }, { once: true });
        }));
      installFakeIssueApi(getIssue);

      const options = issueIdentifierOptions(WS_ID, "MUL-7");
      // Streaming rich content can remove the last mention observer before
      // its response arrives, then render that same identifier again.
      for (let i = 0; i < 20; i++) {
        const observer = new QueryObserver(qc, options);
        const unsubscribe = observer.subscribe(() => {});
        unsubscribe();
      }

      expect(getIssue).toHaveBeenCalledTimes(1);
      expect(abortedLookups).toBe(0);
      const completed = qc.fetchQuery(options);
      if (outcome === "found") {
        resolveLookup(makeIssue(7));
      } else {
        rejectLookup(new ApiError("issue not found", 404, "Not Found"));
      }
      const expected = outcome === "found" ? makeIssue(7) : null;
      await expect(completed).resolves.toEqual(expected);

      const observer = new QueryObserver(qc, options);
      const unsubscribe = observer.subscribe(() => {});
      expect(observer.getCurrentResult().data).toEqual(expected);
      expect(getIssue).toHaveBeenCalledTimes(1);
      unsubscribe();
    },
  );

  it("keys the query by workspace and identifier", () => {
    expect(issueKeys.identifier(WS_ID, "MUL-7")).toEqual([
      "issues",
      WS_ID,
      "identifier",
      "MUL-7",
    ]);
  });
});

describe("findIssueInListCache", () => {
  const todoIssue = makeIssue(1, { status: "todo", status_category: "unstarted" });
  const startedIssue = makeIssue(2, { status: "in_progress", status_category: "started" });

  it("finds an issue in a cached sorted-list entry", async () => {
    const qc = new QueryClient();
    const listIssues = vi
      .fn<(params?: ListIssuesParams) => Promise<ListIssuesResponse>>()
      .mockResolvedValue({ issues: [todoIssue, startedIssue], total: 2 });
    installFakeApi(listIssues);
    await qc.fetchQuery(issueListOptions(WS_ID, { sort_by: "position" }));
    listIssues.mockClear();

    expect(findIssueInListCache(qc, WS_ID, startedIssue.id)).toEqual(startedIssue);
    expect(listIssues).not.toHaveBeenCalled();
    qc.clear();
  });

  it("returns undefined without fetching when the issue is not cached", () => {
    const qc = new QueryClient();
    const listIssues = vi.fn<(params?: ListIssuesParams) => Promise<ListIssuesResponse>>();
    installFakeApi(listIssues);
    qc.setQueryData<ListIssuesCache>(issueKeys.listSorted(WS_ID), {
      byStatus: { unstarted: { issues: [todoIssue], total: 1 } },
    });

    expect(findIssueInListCache(qc, WS_ID, "missing")).toBeUndefined();
    expect(findIssueInListCache(qc, "ws-other", todoIssue.id)).toBeUndefined();
    expect(findIssueInListCache(new QueryClient(), WS_ID, todoIssue.id)).toBeUndefined();
    expect(listIssues).not.toHaveBeenCalled();
    qc.clear();
  });
});

describe("threadSummariesOptions", () => {
  let qc: QueryClient;

  function installFakeThreadApi(
    listThreadSummaries: (ids: string[]) => Promise<{ summaries: IssueThreadSummary[] }>,
  ) {
    setApiInstance({ listThreadSummaries } as unknown as ApiClient);
  }

  function summary(issueId: string, replyCount = 2): IssueThreadSummary {
    return {
      issue_id: issueId,
      reply_count: replyCount,
      last_reply_at: null,
      open_thread_count: 0,
      participants: [],
      last_read_at: null,
      awaiting_me: [],
    };
  }

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("chunks ids at the server cap and fetches the chunks in parallel", async () => {
    const ids = Array.from(
      { length: THREAD_SUMMARIES_CHUNK_SIZE * 2 + 5 },
      (_, i) => `i-${String(i).padStart(4, "0")}`,
    );
    const calls: string[][] = [];
    installFakeThreadApi(async (chunk) => {
      calls.push(chunk);
      return { summaries: [] };
    });

    await qc.fetchQuery(threadSummariesOptions(WS_ID, ids));

    expect(calls.map((c) => c.length)).toEqual([100, 100, 5]);
    expect(calls.flat()).toEqual(ids);
  });

  it("sorts and dedupes ids so the cache key is stable", async () => {
    const listThreadSummaries = vi
      .fn<(ids: string[]) => Promise<{ summaries: IssueThreadSummary[] }>>()
      .mockResolvedValue({ summaries: [] });
    installFakeThreadApi(listThreadSummaries);

    await qc.fetchQuery(threadSummariesOptions(WS_ID, ["b", "a", "b"]));

    expect(listThreadSummaries).toHaveBeenCalledWith(["a", "b"]);
    expect(threadSummariesOptions(WS_ID, ["b", "a"]).queryKey).toEqual(
      threadSummariesOptions(WS_ID, ["a", "b", "a"]).queryKey,
    );
  });

  it("selects a map keyed by issue id and leaves rows-less ids absent", async () => {
    installFakeThreadApi(async () => ({ summaries: [summary("a", 3)] }));

    const observer = new QueryObserver(qc, threadSummariesOptions(WS_ID, ["a", "b"]));
    const result = await observer.refetch();
    const map = result.data!;

    expect(map.get("a")?.reply_count).toBe(3);
    expect(map.has("b")).toBe(false);
  });

  it("is disabled when there are no ids", () => {
    expect(threadSummariesOptions(WS_ID, []).enabled).toBe(false);
  });
});

describe("childIssueProgressForParentsOptions", () => {
  let qc: QueryClient;

  type ProgressResponse = {
    progress: { parent_issue_id: string; total: number; done: number }[];
  };

  function installFakeProgressApi(
    getChildIssueProgress: (ids?: string[]) => Promise<ProgressResponse>,
  ) {
    setApiInstance({ getChildIssueProgress } as unknown as ApiClient);
  }

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("nests its key under the workspace-wide prefix so existing invalidations reach it", async () => {
    installFakeProgressApi(async () => ({ progress: [] }));
    await qc.fetchQuery(childIssueProgressForParentsOptions(WS_ID, ["a"]));

    const key = childIssueProgressForParentsOptions(WS_ID, ["a"]).queryKey;
    expect(key.slice(0, issueKeys.childProgress(WS_ID).length)).toEqual(
      issueKeys.childProgress(WS_ID),
    );

    await qc.invalidateQueries({ queryKey: issueKeys.childProgress(WS_ID) });
    expect(qc.getQueryState(key)?.isInvalidated).toBe(true);
  });

  it("does not share a cache entry with the workspace-wide query", () => {
    expect(childIssueProgressForParentsOptions(WS_ID, ["a"]).queryKey).not.toEqual(
      childIssueProgressOptions(WS_ID).queryKey,
    );
  });

  it("sorts and dedupes ids so the cache key is stable", async () => {
    const getChildIssueProgress = vi
      .fn<(ids?: string[]) => Promise<ProgressResponse>>()
      .mockResolvedValue({ progress: [] });
    installFakeProgressApi(getChildIssueProgress);

    await qc.fetchQuery(childIssueProgressForParentsOptions(WS_ID, ["b", "a", "b"]));

    expect(getChildIssueProgress).toHaveBeenCalledWith(["a", "b"]);
    expect(childIssueProgressForParentsOptions(WS_ID, ["b", "a"]).queryKey).toEqual(
      childIssueProgressForParentsOptions(WS_ID, ["a", "b", "a"]).queryKey,
    );
  });

  it("chunks ids at the server cap and merges the chunks", async () => {
    const ids = Array.from(
      { length: CHILD_PROGRESS_PARENTS_CHUNK_SIZE * 2 + 5 },
      (_, i) => `p-${String(i).padStart(4, "0")}`,
    );
    const calls: string[][] = [];
    installFakeProgressApi(async (chunk) => {
      calls.push(chunk ?? []);
      return {
        progress: (chunk ?? []).map((id) => ({ parent_issue_id: id, total: 2, done: 1 })),
      };
    });

    const observer = new QueryObserver(qc, childIssueProgressForParentsOptions(WS_ID, ids));
    const result = await observer.refetch();

    expect(calls.map((c) => c.length)).toEqual([100, 100, 5]);
    expect(calls.flat()).toEqual(ids);
    expect(result.data!.size).toBe(ids.length);
  });

  it("selects the same map shape as the workspace-wide query", async () => {
    installFakeProgressApi(async () => ({
      progress: [{ parent_issue_id: "a", total: 3, done: 2 }],
    }));

    const observer = new QueryObserver(qc, childIssueProgressForParentsOptions(WS_ID, ["a", "b"]));
    const map = (await observer.refetch()).data!;

    expect(map.get("a")).toEqual({ done: 2, total: 3 });
    expect(map.has("b")).toBe(false);
  });

  it("is disabled when there are no ids", () => {
    expect(childIssueProgressForParentsOptions(WS_ID, []).enabled).toBe(false);
  });
});

describe("issueTimelineInfiniteOptions", () => {
  function timelinePage(overrides: Record<string, unknown> = {}) {
    return {
      entries: [],
      has_more_before: false,
      has_more_after: false,
      next_before: null,
      next_before_id: null,
      ...overrides,
    };
  }

  it("lives under the issue's timeline prefix but apart from the unpaged cache", () => {
    const paged = issueTimelineInfiniteOptions("issue-1").queryKey;
    const unpaged = issueTimelineOptions("issue-1").queryKey;
    expect(paged.slice(0, unpaged.length)).toEqual([...unpaged]);
    expect(paged).not.toEqual(unpaged);
  });

  it("loads the newest page first, then walks older pages by cursor", async () => {
    const listTimeline = vi
      .fn()
      .mockResolvedValueOnce(
        timelinePage({ has_more_before: true, next_before: "2026-01-01T00:00:00.5Z", next_before_id: "root-9" }),
      )
      .mockResolvedValueOnce(timelinePage());
    setApiInstance({ listTimeline } as unknown as ApiClient);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const options = issueTimelineInfiniteOptions("issue-1");

    const data = await qc.fetchInfiniteQuery({ ...options, pages: 2 });
    expect(listTimeline).toHaveBeenNthCalledWith(1, "issue-1", { limit: TIMELINE_PAGE_SIZE });
    expect(listTimeline).toHaveBeenNthCalledWith(2, "issue-1", {
      limit: TIMELINE_PAGE_SIZE,
      before: "2026-01-01T00:00:00.5Z",
      beforeId: "root-9",
    });
    expect(data.pages).toHaveLength(2);
    qc.clear();
  });

  it("stops at a page without a usable cursor", async () => {
    const listTimeline = vi
      .fn()
      // has_more_before without a cursor: a server that cannot be walked.
      .mockResolvedValue(timelinePage({ has_more_before: true }));
    setApiInstance({ listTimeline } as unknown as ApiClient);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    const data = await qc.fetchInfiniteQuery({ ...issueTimelineInfiniteOptions("issue-1"), pages: 3 });
    expect(data.pages).toHaveLength(1);
    expect(listTimeline).toHaveBeenCalledTimes(1);
    qc.clear();
  });
});
