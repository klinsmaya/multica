import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { forwardRef, useImperativeHandle } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Issue, IssueThreadSummary } from "@multica/core/types";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import {
  IssuePeekActionsContext,
  type IssuePeekActions,
} from "../surface/peek-context";
import { FeedView } from "./feed-view";

const mockListThreadSummaries = vi.hoisted(() => vi.fn());
const virtuoso = vi.hoisted(() => ({
  scrollToIndex: vi.fn(),
  atBottomStateChange: undefined as ((atBottom: boolean) => void) | undefined,
}));

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: { listThreadSummaries: mockListThreadSummaries },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/acme/issues/${id}` }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: (_type: string, id: string) => `name:${id}` }),
}));

vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({
    categoryOf: () => "unstarted",
    colorOf: () => null,
    iconOf: () => null,
    entryOf: () => undefined,
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="avatar">{actorId}</span>
  ),
}));

vi.mock("./issue-agent-activity-indicator", () => ({
  IssueAgentActivityIndicator: () => null,
}));

vi.mock("react-virtuoso", () => ({
  Virtuoso: forwardRef(function MockVirtuoso(
    { data, itemContent, initialTopMostItemIndex, atBottomStateChange }: any,
    ref,
  ) {
    useImperativeHandle(ref, () => ({ scrollToIndex: virtuoso.scrollToIndex }));
    virtuoso.atBottomStateChange = atBottomStateChange;
    return (
      <div
        data-testid="virtuoso"
        data-initial-index={JSON.stringify(initialTopMostItemIndex)}
      >
        {(data ?? []).map((item: any, i: number) => (
          <div key={item.id}>{itemContent(i, item)}</div>
        ))}
      </div>
    );
  }),
}));

const composerFocus = vi.hoisted(() => vi.fn());

// The composer has its own tests; here it only reports a sent issue and
// exposes the focus handle the quick actions call.
vi.mock("./feed-composer", () => ({
  FeedComposer: forwardRef(function MockComposer({ projectId, onSent }: any, ref) {
    useImperativeHandle(ref, () => ({ focus: composerFocus }));
    return (
      <button data-testid="composer" onClick={() => onSent?.({ id: "sent" })}>
        {projectId}
      </button>
    );
  }),
}));

const navigation: NavigationAdapter = {
  push: vi.fn(),
  replace: vi.fn(),
  back: vi.fn(),
  pathname: "/acme/projects/p1",
  searchParams: new URLSearchParams(),
  hash: "",
  getShareableUrl: (path) => `https://app.example${path}`,
};

const peek: IssuePeekActions = {
  open: vi.fn(),
  toggle: vi.fn(),
  close: vi.fn(),
  publishColumns: vi.fn(),
};

function makeIssue(overrides: Partial<Issue>): Issue {
  return {
    id: "issue-1",
    identifier: "MUL-1",
    title: "Issue",
    description: null,
    status: "todo",
    priority: "none",
    creator_type: "member",
    creator_id: "user-1",
    parent_issue_id: null,
    labels: [],
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...overrides,
  } as Issue;
}

function summary(overrides: Partial<IssueThreadSummary>): IssueThreadSummary {
  return {
    issue_id: "issue-1",
    reply_count: 0,
    last_reply_at: null,
    open_thread_count: 0,
    participants: [],
    ...overrides,
  };
}

function renderFeed(issues: Issue[], projectId: string | null = null) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const tree = (current: Issue[]) => (
    <QueryClientProvider client={queryClient}>
      <NavigationProvider value={navigation}>
        <IssuePeekActionsContext.Provider value={peek}>
          <FeedView issues={current} projectId={projectId} />
        </IssuePeekActionsContext.Provider>
      </NavigationProvider>
    </QueryClientProvider>
  );
  const view = renderWithI18n(tree(issues));
  return Object.assign(view, {
    update: (next: Issue[]) => view.rerender(tree(next)),
  });
}

describe("FeedView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListThreadSummaries.mockResolvedValue({ summaries: [] });
    virtuoso.atBottomStateChange = undefined;
  });

  it("renders only top-level issues, oldest first, opened at the newest", () => {
    renderFeed([
      makeIssue({ id: "new", title: "Newest", created_at: "2026-10-03T00:00:00Z" }),
      makeIssue({
        id: "child",
        title: "Sub issue",
        parent_issue_id: "old",
        created_at: "2026-10-04T00:00:00Z",
      }),
      makeIssue({ id: "old", title: "Oldest", created_at: "2026-10-01T00:00:00Z" }),
      makeIssue({ id: "mid", title: "Middle", created_at: "2026-10-02T00:00:00Z" }),
    ]);

    expect(screen.queryByText("Sub issue")).not.toBeInTheDocument();
    const titles = ["Oldest", "Middle", "Newest"].map((title) =>
      screen.getByText(title),
    );
    for (let i = 1; i < titles.length; i++) {
      expect(
        titles[i - 1]!.compareDocumentPosition(titles[i]!) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    }
    expect(JSON.parse(screen.getByTestId("virtuoso").dataset.initialIndex!)).toEqual({
      index: 2,
      align: "end",
    });
  });

  it("requests summaries for the rendered issues only", async () => {
    renderFeed([
      makeIssue({ id: "b" }),
      makeIssue({ id: "child", parent_issue_id: "b" }),
      makeIssue({ id: "a" }),
    ]);

    await waitFor(() => expect(mockListThreadSummaries).toHaveBeenCalledTimes(1));
    expect(mockListThreadSummaries).toHaveBeenCalledWith(["a", "b"]);
  });

  it("shows reply count, participants and the open-thread badge on the matching card", async () => {
    mockListThreadSummaries.mockResolvedValue({
      summaries: [
        summary({
          issue_id: "issue-2",
          reply_count: 4,
          last_reply_at: new Date().toISOString(),
          open_thread_count: 2,
          participants: [
            { type: "member", id: "user-9" },
            { type: "agent", id: "agent-3" },
          ],
        }),
      ],
    });
    renderFeed([
      makeIssue({ id: "issue-1", title: "Quiet", created_at: "2026-10-01T00:00:00Z" }),
      makeIssue({ id: "issue-2", title: "Busy", created_at: "2026-10-02T00:00:00Z" }),
    ]);

    expect(await screen.findByText("4 replies")).toBeInTheDocument();
    expect(screen.getByText("2 open threads")).toBeInTheDocument();
    expect(screen.getAllByText(/^Last reply/)).toHaveLength(1);
    // The quiet issue has no row: only the open-thread affordance, no counters.
    expect(screen.getAllByText("Open thread")).toHaveLength(1);
    expect(screen.getByText("user-9")).toBeInTheDocument();
    expect(screen.getByText("agent-3")).toBeInTheDocument();
  });

  it("keeps the cards when the summaries request fails", async () => {
    mockListThreadSummaries.mockRejectedValue(new Error("404"));
    renderFeed([makeIssue({ id: "issue-1", title: "Still here" })]);

    await waitFor(() => expect(mockListThreadSummaries).toHaveBeenCalled());
    expect(screen.getByText("Still here")).toBeInTheDocument();
    expect(screen.getByText("Open thread")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("opens the peek from the card and from the thread bar", async () => {
    mockListThreadSummaries.mockResolvedValue({
      summaries: [summary({ issue_id: "issue-1", reply_count: 1 })],
    });
    renderFeed([makeIssue({ id: "issue-1", title: "Clickable" })]);

    fireEvent.click(screen.getByText("Clickable"));
    expect(peek.open).toHaveBeenLastCalledWith("issue-1");

    vi.mocked(peek.open).mockClear();
    const bar = (await screen.findByText("1 reply")).closest('[role="button"]')!;
    fireEvent.click(within(bar as HTMLElement).getByText("1 reply"));
    expect(peek.open).toHaveBeenCalledWith("issue-1");
  });

  it("shows the empty state when nothing top-level remains", () => {
    renderFeed([makeIssue({ id: "child", parent_issue_id: "p" })]);

    expect(screen.getByText("No issues in this feed")).toBeInTheDocument();
  });

  it("renders the quick actions below the stream, with or without messages", () => {
    const { unmount } = renderFeed([makeIssue({ id: "a" })]);
    const stream = screen.getByTestId("virtuoso");
    const newIssue = screen.getByRole("button", { name: "New issue" });
    expect(
      stream.compareDocumentPosition(newIssue) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    unmount();

    renderFeed([]);
    expect(screen.getByRole("button", { name: "New issue" })).toBeInTheDocument();
  });

  it("focuses the composer from the new-issue action", () => {
    renderFeed([makeIssue({ id: "a" })], "p-1");

    fireEvent.click(screen.getByRole("button", { name: "New issue" }));

    expect(composerFocus).toHaveBeenCalledTimes(1);
  });

  it("marks the open-thread count with the brand accent only when threads are open", async () => {
    mockListThreadSummaries.mockResolvedValue({
      summaries: [summary({ issue_id: "issue-1", reply_count: 2, open_thread_count: 1 })],
    });
    renderFeed([makeIssue({ id: "issue-1" })]);

    expect(await screen.findByText("1 open thread")).toHaveClass("text-brand");
  });

  describe("new messages", () => {
    const base = [
      makeIssue({ id: "a", title: "First", created_at: "2026-10-01T00:00:00Z" }),
      makeIssue({ id: "b", title: "Second", created_at: "2026-10-02T00:00:00Z" }),
    ];
    const next = makeIssue({ id: "c", title: "Third", created_at: "2026-10-03T00:00:00Z" });

    it("follows a new message to the bottom while the reader is at the bottom", () => {
      const view = renderFeed(base);
      act(() => virtuoso.atBottomStateChange?.(true));

      view.update([...base, next]);

      expect(virtuoso.scrollToIndex).toHaveBeenCalledWith(
        expect.objectContaining({ index: "LAST", align: "end" }),
      );
      expect(screen.queryByRole("button", { name: "New messages" })).not.toBeInTheDocument();
    });

    it("offers a jump button instead when the reader has scrolled up", () => {
      const view = renderFeed(base);
      act(() => virtuoso.atBottomStateChange?.(false));

      view.update([...base, next]);

      expect(virtuoso.scrollToIndex).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: "New messages" }));
      expect(virtuoso.scrollToIndex).toHaveBeenCalledWith(
        expect.objectContaining({ index: "LAST", align: "end" }),
      );
      expect(screen.queryByRole("button", { name: "New messages" })).not.toBeInTheDocument();
    });

    it("hides the button once the reader reaches the bottom by scrolling", () => {
      const view = renderFeed(base);
      act(() => virtuoso.atBottomStateChange?.(false));
      view.update([...base, next]);
      expect(screen.getByRole("button", { name: "New messages" })).toBeInTheDocument();

      act(() => virtuoso.atBottomStateChange?.(true));

      expect(screen.queryByRole("button", { name: "New messages" })).not.toBeInTheDocument();
      expect(virtuoso.scrollToIndex).not.toHaveBeenCalled();
    });

    it("ignores re-renders that add nothing and the first population of an empty feed", () => {
      const view = renderFeed([]);
      view.update(base);
      act(() => virtuoso.atBottomStateChange?.(false));
      view.update([...base]);

      expect(virtuoso.scrollToIndex).not.toHaveBeenCalled();
      expect(screen.queryByRole("button", { name: "New messages" })).not.toBeInTheDocument();
    });
  });

  it("mounts the composer only for a project, also under the empty state", () => {
    const { unmount } = renderFeed([makeIssue({ id: "a" })]);
    expect(screen.queryByTestId("composer")).not.toBeInTheDocument();
    unmount();

    renderFeed([], "p-1");
    expect(screen.getByText("No issues in this feed")).toBeInTheDocument();
    expect(screen.getByTestId("composer")).toHaveTextContent("p-1");
  });

  it("scrolls to the bottom once the sent issue reaches the list", () => {
    const older = makeIssue({ id: "old", created_at: "2026-10-01T00:00:00Z" });
    const { update } = renderFeed([older], "p-1");

    fireEvent.click(screen.getByTestId("composer"));
    expect(virtuoso.scrollToIndex).not.toHaveBeenCalled();

    act(() => {
      update([
        older,
        makeIssue({ id: "sent", created_at: "2026-10-02T00:00:00Z" }),
      ]);
    });
    expect(virtuoso.scrollToIndex).toHaveBeenCalledWith(
      expect.objectContaining({ index: "LAST", align: "end" }),
    );

  });
});
