import {
  infiniteQueryOptions,
  keepPreviousData,
  queryOptions,
  type QueryClient,
} from "@tanstack/react-query";
import { api, ApiError } from "../api";
import type {
  Issue,
  IssueThreadSummary,
  IssueAssigneeType,
  IssueStatusCategory,
  IssueTableFacetsRequest,
  IssueTableGroupSpec,
  IssueTableGroupsRequest,
  IssueTableQuerySpec,
  IssueTableRowsRequest,
  ListGroupedIssuesParams,
  ListIssuesParams,
  ListIssuesCache,
  TimelinePageCursor,
} from "../types";
import { ALL_STATUSES } from "./config";
import { TIMELINE_PAGE_SIZE, nextTimelinePageCursor } from "./timeline-pages";
import { issueColumnCategory } from "./status-category";

export function issueTasksOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.tasks(issueId),
    queryFn: () => api.listTasksByIssue(issueId),
    enabled: !!issueId,
    staleTime: 30_000,
    refetchOnWindowFocus: true,
  });
}

export interface IssueSortParam {
  sort_by?: ListIssuesParams["sort_by"];
  sort_direction?: ListIssuesParams["sort_direction"];
  date_field?: ListIssuesParams["date_field"];
  date_start?: ListIssuesParams["date_start"];
  date_end?: ListIssuesParams["date_end"];
  /** Server-side custom-property filter (definition id → accepted values).
   *  Lives in the sort/window bag so every list surface, query key, and
   *  load-more page carries it automatically. */
  properties?: ListIssuesParams["properties"];
}

export const issueKeys = {
  all: (wsId: string) => ["issues", wsId] as const,
  /** PREFIX for invalidation — no sort. */
  list: (wsId: string) => [...issueKeys.all(wsId), "list"] as const,
  /** FULL KEY for queryOptions — includes sort. */
  listSorted: (wsId: string, sort?: IssueSortParam) =>
    [...issueKeys.list(wsId), sort ?? {}] as const,
  flatAll: (wsId: string) => [...issueKeys.all(wsId), "flat"] as const,
  flat: (
    wsId: string,
    scope: string,
    filter: IssueFlatFilter,
    sort?: IssueSortParam,
  ) => [...issueKeys.flatAll(wsId), scope, filter, sort ?? {}] as const,
  flatExport: (
    wsId: string,
    scope: string,
    filter: IssueFlatFilter,
    sort?: IssueSortParam,
  ) => [...issueKeys.flatAll(wsId), "export", scope, filter, sort ?? {}] as const,
  tableAll: (wsId: string) => [...issueKeys.all(wsId), "table-query"] as const,
  tableGroups: (
    wsId: string,
    query: IssueTableQuerySpec,
    group: IssueTableGroupsRequest["group"],
  ) => [...issueKeys.tableAll(wsId), "groups", query, group] as const,
  tableFacets: (
    wsId: string,
    request: IssueTableFacetsRequest,
  ) => [...issueKeys.tableAll(wsId), "facets", request] as const,
  tableRows: (
    wsId: string,
    query: IssueTableQuerySpec,
    group: IssueTableGroupSpec,
    groupKey: string | null,
    hierarchy: boolean,
    parentId: string | null,
  ) => [
    ...issueKeys.tableAll(wsId),
    "rows",
    query,
    group,
    groupKey,
    { hierarchy, parentId },
  ] as const,
  assigneeGroupsAll: (wsId: string) =>
    [...issueKeys.all(wsId), "assignee-groups"] as const,
  assigneeGroups: (wsId: string, filter: AssigneeGroupedIssuesFilter) =>
    [...issueKeys.assigneeGroupsAll(wsId), filter] as const,
  /** All "my issues" queries — use for bulk invalidation. */
  myAll: (wsId: string) => [...issueKeys.all(wsId), "my"] as const,
  /** PREFIX for per-scope invalidation — no sort. */
  myList: (wsId: string, scope: string, filter: MyIssuesFilter) =>
    [...issueKeys.myAll(wsId), scope, filter] as const,
  /** FULL KEY for queryOptions — includes sort. */
  myListSorted: (wsId: string, scope: string, filter: MyIssuesFilter, sort?: IssueSortParam) =>
    [...issueKeys.myList(wsId, scope, filter), sort ?? {}] as const,
  myAssigneeGroupsAll: (wsId: string) =>
    [...issueKeys.myAll(wsId), "assignee-groups"] as const,
  myAssigneeGroups: (
    wsId: string,
    scope: string,
    filter: AssigneeGroupedIssuesFilter,
  ) => [...issueKeys.myAssigneeGroupsAll(wsId), scope, filter] as const,
  /** All Project Gantt queries — prefix-match key for cross-project invalidation. */
  projectGanttAll: (wsId: string) =>
    [...issueKeys.all(wsId), "project-gantt"] as const,
  /**
   * Per-project Gantt issue list (scheduled-only). Uses its own cache key
   * rather than reusing the bucketed `myList` cache so WS handlers and
   * cache helpers don't have to special-case a non-bucketed shape under
   * the `my` prefix.
   */
  projectGantt: (
    wsId: string,
    projectId: string,
    assigneeTypes?: IssueAssigneeType[],
  ) =>
    [
      ...issueKeys.projectGanttAll(wsId),
      projectId,
      assigneeTypes ?? null,
    ] as const,
  detail: (wsId: string, id: string) =>
    [...issueKeys.all(wsId), "detail", id] as const,
  /** Resolve a bare issue identifier (e.g. "MUL-123") to an issue. */
  identifier: (wsId: string, identifier: string) =>
    [...issueKeys.all(wsId), "identifier", identifier] as const,
  /** Prefix for every per-issue duplicate-relation query in a workspace. */
  duplicatesAll: (wsId: string) =>
    [...issueKeys.all(wsId), "duplicates"] as const,
  duplicates: (wsId: string, id: string) =>
    [...issueKeys.duplicatesAll(wsId), id] as const,
  /** Prefix for every per-parent children query in a workspace. */
  childrenAll: (wsId: string) =>
    [...issueKeys.all(wsId), "children"] as const,
  children: (wsId: string, id: string) =>
    [...issueKeys.childrenAll(wsId), id] as const,
  /**
   * One page of a parent's children. Nested under `children(wsId, id)` so the
   * per-parent invalidation and the Issue[] cache patchers reach it, but a
   * separate entry from the full list: a partial page must never be read by a
   * consumer that expects every child.
   */
  childrenPage: (
    wsId: string,
    id: string,
    page: { limit: number; offset: number },
  ) => [...issueKeys.children(wsId, id), "page", page.offset, page.limit] as const,
  /** Prefix for invalidating all batched-children queries in a workspace. */
  childrenByParentsAll: (wsId: string) =>
    [...issueKeys.all(wsId), "children-by-parents"] as const,
  /** Full key — includes sorted parent ids for cache stability. */
  childrenByParents: (wsId: string, parentIds: readonly string[]) =>
    [...issueKeys.childrenByParentsAll(wsId), parentIds] as const,
  threadSummariesAll: (wsId: string) =>
    [...issueKeys.all(wsId), "thread-summaries"] as const,
  /** idsKey is the sorted, deduplicated id list joined by commas. */
  threadSummaries: (wsId: string, idsKey: string) =>
    [...issueKeys.threadSummariesAll(wsId), idsKey] as const,
  childProgress: (wsId: string) =>
    [...issueKeys.all(wsId), "child-progress"] as const,
  /** Prefix-match keys for invalidating the per-issue caches below across
   *  all issues. These keys carry no wsId, so `issueKeys.all(wsId)` does NOT
   *  cover them — WS reconnect recovery must invalidate these `*All`
   *  prefixes explicitly, or missed events leave them stale forever under
   *  the staleTime: Infinity default (#3953). */
  timelineAll: () => ["issues", "timeline"] as const,
  /** Prefix of everything cached for one issue's timeline. Invalidating or
   *  removing it covers the paged cache below. */
  timeline: (issueId: string) =>
    [...issueKeys.timelineAll(), issueId] as const,
  /** The paged timeline (infinite query, newest page first). */
  timelinePages: (issueId: string) =>
    [...issueKeys.timeline(issueId), "pages"] as const,
  /** Prefix across all issues — WS task lifecycle events invalidate here so
   *  an open composer's trigger preview refreshes when an agent's queue
   *  state changes (the dedup guard makes the answer queue-dependent). */
  commentTriggerPreviewAll: () => ["issues", "comment-trigger-preview"] as const,
  /** PREFIX for invalidation — the composer hook appends parent + content signature. */
  commentTriggerPreview: (issueId: string) =>
    [...issueKeys.commentTriggerPreviewAll(), issueId] as const,
  /** Prefix across all issue-trigger previews (assign/status/create/batch).
   *  WS task lifecycle events invalidate here so the answer revalidates when an
   *  agent's queue state changes (the status source's pending dedup makes it
   *  queue-dependent, mirroring commentTriggerPreviewAll). */
  issueTriggerPreviewAll: () => ["issues", "issue-trigger-preview"] as const,
  /** PREFIX — the picker hook appends a signature of the prospective write. */
  issueTriggerPreview: (signature: string) =>
    [...issueKeys.issueTriggerPreviewAll(), signature] as const,
  reactionsAll: () => ["issues", "reactions"] as const,
  reactions: (issueId: string) =>
    [...issueKeys.reactionsAll(), issueId] as const,
  subscribersAll: () => ["issues", "subscribers"] as const,
  subscribers: (issueId: string) =>
    [...issueKeys.subscribersAll(), issueId] as const,
  usageAll: () => ["issues", "usage"] as const,
  usage: (issueId: string) => [...issueKeys.usageAll(), issueId] as const,
  attachmentsAll: () => ["issues", "attachments"] as const,
  /** Issue-level attachments — used by the description editor so its
   *  inline file-card / image NodeViews can re-sign download URLs at
   *  click time. */
  attachments: (issueId: string) =>
    [...issueKeys.attachmentsAll(), issueId] as const,
  /** Prefix-match key for invalidating tasks across all issues — used by
   *  the global WS task: prefix path so any task lifecycle event refreshes
   *  every per-issue list, regardless of which issue is currently mounted. */
  tasksAll: () => ["issues", "tasks"] as const,
  /** Per-issue task list (issue-detail Execution log section). */
  tasks: (issueId: string) => [...issueKeys.tasksAll(), issueId] as const,
  sourceContextPreview: (wsId: string, anchorCommentId: string) =>
    ["source-context", "preview", wsId, anchorCommentId] as const,
};

export function sourceContextPreviewOptions(
  wsId: string,
  anchorCommentId: string | null | undefined,
) {
  return queryOptions({
    queryKey: issueKeys.sourceContextPreview(wsId, anchorCommentId ?? ""),
    queryFn: () => api.getCommentSubIssuePreview(anchorCommentId!),
    enabled: !!anchorCommentId,
    staleTime: 0,
  });
}

export type MyIssuesFilter = Pick<
  ListIssuesParams,
  | "assignee_id"
  | "assignee_ids"
  | "assignee_types"
  | "creator_id"
  | "project_id"
  | "involves_user_id"
>;

/** Server-side contract for the flat table window. These facets must travel
 * with every offset page (and live in the query key); post-filtering a loaded
 * page can silently omit matching issues after the current offset. */
export type IssueFlatFilter = MyIssuesFilter &
  Pick<
    ListIssuesParams,
    | "q"
    | "statuses"
    | "priorities"
    | "assignee_filters"
    | "include_no_assignee"
    | "creator_filters"
    | "project_ids"
    | "include_no_project"
    | "label_ids"
    | "top_level_only"
    | "ids"
  >;

export type AssigneeGroupedIssuesFilter = Omit<
  ListGroupedIssuesParams,
  "group_by" | "limit" | "offset" | "group_assignee_type" | "group_assignee_id"
>;

/** Size of the issue list's first page, all categories together. */
export const ISSUE_PAGE_SIZE = 50;

/**
 * CATEGORIES fetched and paginated into the list/board cache — all four,
 * `closed` included. It lives in the cache even when hidden by the surface's
 * display preferences; there is no separate
 * "visible board" subset. This constant governs fetch/cache membership.
 *
 * These are internal legacy cache buckets, not user-facing columns.
 * Board/List use independently paged exact-key table branches; Swimlane uses
 * compound status branches. Never derive visible column identity from this cache.
 */
export const PAGINATED_CATEGORIES: readonly IssueStatusCategory[] = ALL_STATUSES;

/** Flatten a bucketed response to a single Issue[] for consumers that want the whole list. */
export function flattenIssueBuckets(data: ListIssuesCache) {
  const out = [];
  for (const status of PAGINATED_CATEGORIES) {
    const bucket = data.byStatus[status];
    if (bucket) out.push(...bucket.issues);
  }
  return out;
}

/**
 * One request for the list's first page, grouped into the category buckets
 * here. Every reader of this cache looks an issue up by id and none pages a
 * bucket, so one shared window serves them all. Each bucket's `total` is its
 * own row count. A custom status the server did not resolve falls back to
 * `issueColumnCategory`'s bucket, so no row is dropped.
 */
async function fetchFirstPages(filter: MyIssuesFilter = {}, sort?: IssueSortParam): Promise<ListIssuesCache> {
  const res = await api.listIssues({ limit: ISSUE_PAGE_SIZE, offset: 0, ...sort, ...filter });
  const byStatus: ListIssuesCache["byStatus"] = {};
  for (const category of PAGINATED_CATEGORIES) {
    const issues = res.issues.filter((issue) => issueColumnCategory(issue) === category);
    byStatus[category] = { issues, total: issues.length };
  }
  return { byStatus };
}

export function issueTableGroupsOptions(
  wsId: string,
  query: IssueTableQuerySpec,
  group: IssueTableGroupsRequest["group"],
) {
  return infiniteQueryOptions({
    queryKey: issueKeys.tableGroups(wsId, query, group),
    initialPageParam: null as string | null,
    queryFn: ({ pageParam }) =>
      api.listIssueTableGroups({
        query,
        group,
        page: { limit: 100, cursor: pageParam },
    }),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    placeholderData: keepPreviousData,
    retry: false,
  });
}

/** One independently-addressable Table branch page.
 *
 * Table keeps every cursor page under its own query key so a refreshed head
 * can detach stale tail cursors before their responses land. Keep the request,
 * key shape, transition placeholder, and retry policy together here — the
 * dynamic branch graph in views should not duplicate this API contract. */
export function issueTableRowPageOptions(
  wsId: string,
  request: IssueTableRowsRequest,
) {
  const cursor = request.page?.cursor ?? null;
  return queryOptions({
    queryKey: [
      ...issueKeys.tableRows(
        wsId,
        request.query,
        request.group,
        request.group_key,
        request.hierarchy.enabled,
        request.parent_id,
      ),
      "page",
      cursor,
    ] as const,
    queryFn: () => api.listIssueTableRows(request),
    placeholderData: keepPreviousData,
    retry: false,
    // Dynamic useQueries observers detach/reinstall as sibling branches enter
    // and leave the viewport. Keep an errored page errored until the user hits
    // Retry — but still let a successful-but-invalidated page refetch on
    // reattach, so a row page that gets WS-invalidated while its observer is
    // detached doesn't stay stranded stale under the global `staleTime: Infinity`
    // default (correct count, missing issue).
    //
    // Both guards are needed: `retryOnMount` covers a first-load error (no
    // data), while the `refetchOnMount` guard covers a background-refetch error
    // on a page that still holds data — TanStack flags such a page
    // `isInvalidated`, so a plain `refetchOnMount: true` would re-fire the
    // failing request on every reattach instead of waiting for explicit Retry.
    retryOnMount: false,
    refetchOnMount: (query) => query.state.status !== "error",
  });
}

export function issueTableFacetsOptions(
  wsId: string,
  request: IssueTableFacetsRequest,
) {
  return queryOptions({
    queryKey: issueKeys.tableFacets(wsId, request),
    queryFn: () => api.listIssueTableFacets(request),
  });
}

/**
 * CACHE SHAPE NOTE: The raw cache stores {@link ListIssuesCache} (buckets keyed
 * by status, each with `{ issues, total }`), and `select` flattens it to
 * `Issue[]` for consumers. Mutations and ws-updaters must use
 * `setQueryData<ListIssuesCache>(...)` and preserve the byStatus shape.
 *
 * Fetches the first page in one request and buckets it by category.
 */
export function issueListOptions(wsId: string, sort?: IssueSortParam) {
  return queryOptions({
    queryKey: issueKeys.listSorted(wsId, sort),
    queryFn: () => fetchFirstPages({}, sort),
    select: flattenIssueBuckets,
    placeholderData: keepPreviousData,
  });
}

/**
 * Read-only lookup of `issueId` across every cached workspace list. It never
 * fetches and never subscribes, so a detail view can seed from a list another
 * surface already loaded without loading the whole list itself.
 */
export function findIssueInListCache(
  qc: QueryClient,
  wsId: string,
  issueId: string,
): Issue | undefined {
  for (const [, data] of qc.getQueriesData<ListIssuesCache>({
    queryKey: issueKeys.list(wsId),
  })) {
    const found = data && flattenIssueBuckets(data).find((i) => i.id === issueId);
    if (found) return found;
  }
  return undefined;
}

/**
 * Page size for the scheduled-issue fetch. The Gantt view always pulls every
 * scheduled issue (no client pagination), so this is just the chunk size we
 * use to walk the server's `(limit, offset)` window until we hit `total`.
 */
export const PROJECT_GANTT_PAGE_LIMIT = 500;

/**
 * Paranoia cap on the loop in {@link fetchProjectGanttIssues}. Real projects
 * shouldn't come close to this — a single project carrying 50k scheduled
 * issues is already a product problem, not a Gantt-rendering one — but the
 * guard prevents a buggy server `total` from spinning the loop forever.
 */
export const PROJECT_GANTT_MAX_ISSUES = 10_000;

async function fetchProjectGanttIssues(
  projectId: string,
  assigneeTypes?: IssueAssigneeType[],
) {
  const issues = [];
  let offset = 0;
  while (offset < PROJECT_GANTT_MAX_ISSUES) {
    const res = await api.listIssues({
      project_id: projectId,
      scheduled: true,
      ...(assigneeTypes?.length ? { assignee_types: assigneeTypes } : {}),
      limit: PROJECT_GANTT_PAGE_LIMIT,
      offset,
    });
    issues.push(...res.issues);
    if (res.issues.length < PROJECT_GANTT_PAGE_LIMIT) break;
    if (issues.length >= res.total) break;
    offset += PROJECT_GANTT_PAGE_LIMIT;
  }
  return issues;
}

/**
 * One-shot fetch of every scheduled issue (`start_date` or `due_date` set)
 * for a project. The Project Gantt view consumes this directly — no status
 * bucketing, no client-side pagination, no Load-all affordance — because
 * the scheduled subset is bounded enough to come back in a small handful of
 * requests.
 *
 * Backed by `GET /api/issues?scheduled=true&project_id=…`; the SQL filter
 * mirrors the same `(start_date IS NOT NULL OR due_date IS NOT NULL)`
 * predicate the Gantt view applies on the client. Pages are walked until
 * `total` is reached so an oversized project can't silently lose bars past
 * the first page.
 */
export function projectGanttIssuesOptions(
  wsId: string,
  projectId: string,
  // The page's assignee-type tab narrows the Gantt exactly like every
  // other mode — same scope, same single mapping upstream.
  assigneeTypes?: IssueAssigneeType[],
) {
  return queryOptions({
    queryKey: issueKeys.projectGantt(wsId, projectId, assigneeTypes),
    queryFn: () => fetchProjectGanttIssues(projectId, assigneeTypes),
  });
}

export function issueDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: issueKeys.detail(wsId, id),
    queryFn: () => api.getIssue(id),
  });
}

/**
 * Resolve a bare issue identifier ("MUL-123") to its issue, or `null`.
 *
 * Backs the Linear-style autolink. This is an EXACT lookup, so it goes to
 * `GET /api/issues/{identifier}` — the server parses `PREFIX-NUMBER`, checks
 * the prefix against the workspace's own `issue_prefix`, and reads the issue
 * through the unique `(workspace_id, number)` index. A non-existent or
 * wrong-prefix identifier 404s, which maps to `null` here and renders as
 * plain text — the same contract the previous implementation had.
 *
 * It deliberately does NOT use `/api/issues/search`: that endpoint runs the
 * workspace-wide full-text query (title/description/comment `LIKE`, ranking,
 * and snippet subqueries) which is orders of magnitude more expensive than a
 * point read, and autolink resolution was the dominant caller of it
 * (MUL-6268).
 *
 * Server state → TanStack Query; the key includes `wsId` and the identifier,
 * so identical identifiers across the app share one request. Caller gates
 * `enabled` (identifier shape + workspace prefix).
 */
export function issueIdentifierOptions(wsId: string, identifier: string) {
  return queryOptions({
    queryKey: issueKeys.identifier(wsId, identifier),
    // Keep this small, cacheable lookup alive when the last mention unmounts.
    // A remount can then share its request instead of aborting and restarting it.
    queryFn: async () => {
      try {
        return await api.getIssue(identifier);
      } catch (err) {
        // Unknown identifier / wrong workspace prefix → render as plain text.
        // Any other failure (401/5xx) must keep propagating so the query
        // can retry instead of being cached as "no such issue".
        if (err instanceof ApiError && err.status === 404) return null;
        throw err;
      }
    },
    // Identifier→issue mapping is effectively immutable; avoid refetch churn
    // when the same key renders across many comments/messages.
    staleTime: 5 * 60_000,
  });
}

export function childIssueProgressOptions(wsId: string) {
  return queryOptions({
    queryKey: issueKeys.childProgress(wsId),
    queryFn: () => api.getChildIssueProgress(),
    select: (data) => {
      const map = new Map<string, { done: number; total: number }>();
      for (const entry of data.progress) {
        map.set(entry.parent_issue_id, { done: entry.done, total: entry.total });
      }
      return map;
    },
  });
}

/** Both sides of an issue's duplicate relation: its original and its duplicates. */
export function issueDuplicatesOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: issueKeys.duplicates(wsId, id),
    queryFn: () => api.listIssueDuplicates(id),
    // Same reason as childIssuesOptions: a mark written while this workspace
    // is not the active realtime subscription would otherwise leave the
    // Infinity-stale snapshot wrong when the issue is opened again.
    refetchOnMount: "always",
  });
}

/**
 * A parent's children. Without `page` this is the full list (cached under
 * `issueKeys.children`); with `page` it is one slice, cached separately.
 * Realtime child events invalidate both (see ws-updaters).
 */
export function childIssuesOptions(
  wsId: string,
  id: string,
  page?: { limit: number; offset: number },
) {
  return queryOptions({
    queryKey: page
      ? issueKeys.childrenPage(wsId, id, page)
      : issueKeys.children(wsId, id),
    queryFn: () =>
      (page ? api.listChildIssues(id, page) : api.listChildIssues(id)).then(
        (r) => r.issues,
      ),
  });
}

/**
 * Server cap on parent_ids per `GET /api/issues/children` request — must
 * match `listChildrenByParentsLimit` in server/internal/handler/issue.go.
 * Exceeding it returns 400, so the client chunks larger requests.
 */
export const CHILDREN_BY_PARENTS_CHUNK_SIZE = 200;

/**
 * Batched variant of {@link childIssuesOptions}: fetches children for all
 * given parents in `GET /api/issues/children?parent_ids=…` requests, chunked
 * to {@link CHILDREN_BY_PARENTS_CHUNK_SIZE} parents each. The queryFn also
 * hydrates each parent's per-parent issueKeys.children cache so other
 * surfaces (issue-detail sub-issues panel, set-parent modal) hit the primed
 * cache instead of re-fetching. Hydration happens in queryFn (not a
 * useEffect) to avoid the setQueryData → re-render → effect loop.
 *
 * Used by SwimLaneView to resolve parent lanes without an N-request fan-out.
 * parentIds must be sorted + deduplicated by the caller for a stable cache key.
 */
async function fetchAndHydrateChildrenByParents(
  qc: QueryClient,
  wsId: string,
  parentIds: readonly string[],
) {
  // Chunk to respect the server cap (parallel, since chunks are independent).
  const chunks: string[][] = [];
  for (let i = 0; i < parentIds.length; i += CHILDREN_BY_PARENTS_CHUNK_SIZE) {
    chunks.push([...parentIds.slice(i, i + CHILDREN_BY_PARENTS_CHUNK_SIZE)]);
  }
  const responses = await Promise.all(chunks.map((c) => api.listChildrenByParents(c)));
  const grouped = new Map<string, Issue[]>();
  for (const response of responses) {
    for (const issue of response.issues) {
      if (!issue.parent_issue_id) continue;
      const bucket = grouped.get(issue.parent_issue_id);
      if (bucket) {
        bucket.push(issue);
      } else {
        grouped.set(issue.parent_issue_id, [issue]);
      }
    }
  }
  for (const [parentId, children] of grouped) {
    // Only hydrate if the per-parent cache is empty — don't overwrite a
    // fresher result that another query (e.g. issue-detail) may have written.
    // This relies on useUpdateIssue.onMutate writing into the per-parent
    // cache (not creating an empty one) — if that contract changes, batch
    // hydration here would silently stop seeding new lanes.
    const existing = qc.getQueryData<Issue[]>(issueKeys.children(wsId, parentId));
    if (!existing || existing.length === 0) {
      qc.setQueryData(issueKeys.children(wsId, parentId), children);
    }
  }
  return grouped;
}

export function childrenByParentsOptions(
  wsId: string,
  parentIds: readonly string[],
  qc: QueryClient,
) {
  return queryOptions({
    queryKey: issueKeys.childrenByParents(wsId, parentIds),
    queryFn: () => fetchAndHydrateChildrenByParents(qc, wsId, parentIds),
    enabled: parentIds.length > 0,
  });
}

/** Server cap on ids per `GET /api/issues/thread-summaries` request. */
export const THREAD_SUMMARIES_CHUNK_SIZE = 100;

/**
 * Comment-thread rollups for a set of issues, keyed by issue id. Ids without
 * a server row (no counted comments, or an older server) are absent from the
 * map; callers treat a missing entry as zeros.
 */
export function threadSummariesOptions(wsId: string, ids: readonly string[]) {
  const sortedIds = [...new Set(ids)].sort();
  return queryOptions({
    queryKey: issueKeys.threadSummaries(wsId, sortedIds.join(",")),
    queryFn: async () => {
      const chunks: string[][] = [];
      for (let i = 0; i < sortedIds.length; i += THREAD_SUMMARIES_CHUNK_SIZE) {
        chunks.push(sortedIds.slice(i, i + THREAD_SUMMARIES_CHUNK_SIZE));
      }
      const responses = await Promise.all(
        chunks.map((chunk) => api.listThreadSummaries(chunk)),
      );
      return responses.flatMap((response) => response.summaries);
    },
    select: (summaries): Map<string, IssueThreadSummary> =>
      new Map(summaries.map((summary) => [summary.issue_id, summary])),
    staleTime: 30_000,
    enabled: sortedIds.length > 0,
  });
}

/**
 * The issue timeline, paged by root comment: the first page is the newest
 * TIMELINE_PAGE_SIZE root comments with all their replies and the activities of
 * that time window; each further page is the next OLDER slice, reached through
 * the previous page's cursor. Older pages are the query's "next" pages, so
 * `pages[0]` is always the newest page. Read and patch the cache through
 * `timeline-pages.ts`, not by indexing `pages`.
 */
export function issueTimelineInfiniteOptions(issueId: string) {
  return infiniteQueryOptions({
    queryKey: issueKeys.timelinePages(issueId),
    initialPageParam: null as TimelinePageCursor | null,
    queryFn: ({ pageParam }) =>
      api.listTimeline(issueId, {
        limit: TIMELINE_PAGE_SIZE,
        ...(pageParam ?? {}),
      }),
    getNextPageParam: nextTimelinePageCursor,
  });
}

/**
 * The unpaged timeline: the full ordered set of comments + activities for an
 * issue (server caps at 2000 as a safety net) as one flat array. Nothing in the
 * app reads it any more — the issue page uses `issueTimelineInfiniteOptions` —
 * and it lives under a different key, so the two caches never mix.
 */
export function issueTimelineOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.timeline(issueId),
    queryFn: () => api.listTimeline(issueId),
  });
}

export function issueReactionsOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.reactions(issueId),
    queryFn: async () => {
      const issue = await api.getIssue(issueId);
      return issue.reactions ?? [];
    },
  });
}

export function issueSubscribersOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.subscribers(issueId),
    queryFn: () => api.listIssueSubscribers(issueId),
  });
}

export function issueUsageOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.usage(issueId),
    queryFn: () => api.getIssueUsage(issueId),
  });
}

// Backs the description editor's fresh-sign download flow: NodeViews resolve
// an attachment id by matching the markdown URL against this list. The list
// is workspace-private metadata and lives on the same cache lifetime as the
// rest of the issue detail surface.
export function issueAttachmentsOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.attachments(issueId),
    queryFn: () => api.listAttachments(issueId),
  });
}
