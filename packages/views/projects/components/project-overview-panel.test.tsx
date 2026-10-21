import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentTask, Issue, IssueThreadSummary } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ProjectOverviewPanel } from "./project-overview-panel";

const mocks = vi.hoisted(() => ({
  listIssueTableRows: vi.fn(),
  listThreadSummaries: vi.fn(),
  getChildIssueProgress: vi.fn(),
  getAgentTaskSnapshot: vi.fn(),
  open: vi.fn(),
  openedIssueIds: [] as string[],
}));

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: {
    listIssueTableRows: mocks.listIssueTableRows,
    listThreadSummaries: mocks.listThreadSummaries,
    getChildIssueProgress: mocks.getChildIssueProgress,
    getAgentTaskSnapshot: mocks.getAgentTaskSnapshot,
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "user-me" } }),
}));

vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({
    categoryOf: (status: string) =>
      status === "done" ? "done" : status === "cancelled" ? "closed" : "started",
    colorOf: () => null,
    iconOf: () => null,
    entryOf: () => undefined,
  }),
}));

vi.mock("../../issues/components/feed-message-card", () => ({
  useOpenFeedIssue: (issueId: string) => () => {
    mocks.openedIssueIds.push(issueId);
    mocks.open(issueId);
  },
}));

function makeIssue(overrides: Partial<Issue> & { id: string }): Issue {
  return {
    identifier: overrides.id.toUpperCase(),
    title: `Title ${overrides.id}`,
    description: null,
    status: "in_progress",
    priority: "none",
    creator_type: "member",
    creator_id: "user-1",
    parent_issue_id: null,
    labels: [],
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    last_activity_at: null,
    ...overrides,
  } as Issue;
}

function makeSummary(
  overrides: Partial<IssueThreadSummary> & { issue_id: string },
): IssueThreadSummary {
  return {
    reply_count: 1,
    last_reply_at: "2026-10-02T00:00:00Z",
    last_reply_excerpt: null,
    open_thread_count: 0,
    participants: [],
    last_read_at: "2026-10-03T00:00:00Z",
    awaiting_me: [],
    latest_progress: null,
    pull_requests: [],
    deliverable_count: 0,
    last_deliverable_at: null,
    ...overrides,
  };
}

function rowsResponse(issues: Issue[]) {
  return {
    query_fingerprint: "fp",
    group_key: null,
    parent_id: null,
    total: issues.length,
    branch_total: issues.length,
    next_cursor: null,
    rows: issues.map((issue) => ({ issue, direct_child_count: 0 })),
  };
}

function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <ProjectOverviewPanel projectId="project-1" />
    </QueryClientProvider>,
  );
}

const runningTask = { id: "t1", issue_id: "run", status: "running", agent_id: "a1" } as AgentTask;

describe("ProjectOverviewPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.openedIssueIds.length = 0;
    mocks.listIssueTableRows.mockResolvedValue(rowsResponse([]));
    mocks.listThreadSummaries.mockResolvedValue({ summaries: [] });
    mocks.getChildIssueProgress.mockResolvedValue({ progress: [] });
    mocks.getAgentTaskSnapshot.mockResolvedValue([]);
  });

  function seedFourGroups() {
    mocks.listIssueTableRows.mockResolvedValue(
      rowsResponse([
        makeIssue({ id: "ask", title: "Needs an answer" }),
        makeIssue({ id: "run", title: "Agent at work" }),
        makeIssue({ id: "idle", title: "Quiet one" }),
        makeIssue({ id: "idle2", title: "Quiet two" }),
        makeIssue({ id: "fin", title: "Shipped", status: "done" }),
        makeIssue({ id: "child", title: "A sub issue", parent_issue_id: "idle" }),
      ]),
    );
    mocks.getAgentTaskSnapshot.mockResolvedValue([runningTask]);
    mocks.listThreadSummaries.mockResolvedValue({
      summaries: [
        makeSummary({
          issue_id: "ask",
          last_reply_excerpt: "Should I ship it?",
          awaiting_me: [
            { comment_id: "c1", actor_id: "agent-1", excerpt: "Should I ship it?", created_at: "2026-10-02T00:00:00Z" },
          ],
          deliverable_count: 2,
        }),
        makeSummary({ issue_id: "idle", last_reply_excerpt: "Looks fine" }),
      ],
    });
    mocks.getChildIssueProgress.mockResolvedValue({
      progress: [{ parent_issue_id: "idle", total: 4, done: 1 }],
    });
  }

  it("requests the project's top-level issues in one Table page", async () => {
    renderPanel();
    await waitFor(() => expect(mocks.listIssueTableRows).toHaveBeenCalledTimes(1));
    const request = mocks.listIssueTableRows.mock.calls[0]![0];
    expect(request.query.scope).toEqual({ kind: "project", project_id: "project-1" });
    expect(request.query.filters).toEqual({ include_sub_issues: false });
    expect(request.group).toEqual({ kind: "none" });
  });

  it("renders the four groups with counts and keeps Resolved collapsed", async () => {
    seedFourGroups();
    renderPanel();

    await screen.findByText("Should I ship it?");
    const needs = screen.getByRole("button", { name: /Needs you/ });
    expect(needs).toHaveTextContent("1");
    expect(screen.getByRole("button", { name: /Running/ })).toHaveTextContent("1");
    expect(screen.getByRole("button", { name: /Idle/ })).toHaveTextContent("2");
    const resolved = screen.getByRole("button", { name: /Resolved/ });
    expect(resolved).toHaveTextContent("1");

    expect(resolved).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Shipped")).not.toBeInTheDocument();
    expect(screen.getByText("Needs an answer")).toBeInTheDocument();
    expect(screen.getByText("Agent at work")).toBeInTheDocument();
    expect(screen.queryByText("A sub issue")).not.toBeInTheDocument();

    fireEvent.click(resolved);
    expect(screen.getByText("Shipped")).toBeInTheDocument();
  });

  it("shows the newest reply, sub-issue progress and file count on a row", async () => {
    seedFourGroups();
    renderPanel();

    // Rows move groups once the summaries land, so look the row up by its
    // excerpt rather than holding a node from the first render.
    const askRow = (await screen.findByText("Should I ship it?")).closest("button")!;
    expect(within(askRow).getByText("Needs an answer")).toBeInTheDocument();
    expect(askRow).toHaveTextContent("2");

    const idleRow = (await screen.findByText("Looks fine")).closest("button")!;
    expect(within(idleRow).getByText("Quiet one")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText("Quiet one").closest("button")).toHaveTextContent("1/4"));
  });

  it("opens the issue through the feed helper when a row is clicked", async () => {
    seedFourGroups();
    renderPanel();

    fireEvent.click((await screen.findByText("Agent at work")).closest("button")!);
    expect(mocks.open).toHaveBeenCalledWith("run");
  });

  it("collapses a group and says so in one line when it has no issues", async () => {
    mocks.listIssueTableRows.mockResolvedValue(
      rowsResponse([makeIssue({ id: "idle", title: "Quiet one" })]),
    );
    renderPanel();

    expect(await screen.findByText("Nothing is waiting on you")).toBeInTheDocument();
    expect(screen.getByText("No agents are working")).toBeInTheDocument();

    const idle = screen.getByRole("button", { name: /Idle/ });
    fireEvent.click(idle);
    expect(idle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Quiet one")).not.toBeInTheDocument();
  });

  it("shows a single empty state for a project without issues", async () => {
    renderPanel();

    expect(await screen.findByText("No issues in this project yet")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Needs you/ })).not.toBeInTheDocument();
    expect(mocks.listThreadSummaries).not.toHaveBeenCalled();
  });
});
