import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ProjectPullRequest, ProjectPullRequestsResponse } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

const mocks = vi.hoisted(() => ({
  listProjectPullRequests: vi.fn(),
  openIssue: vi.fn(),
}));

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: { listProjectPullRequests: mocks.listProjectPullRequests },
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("../../issues/components/feed-message-card", () => ({
  useOpenFeedIssue: (issueId: string) => () => mocks.openIssue(issueId),
}));

import { ProjectPullRequestsPanel } from "./project-pull-requests-panel";

function makePR(overrides: Partial<ProjectPullRequest> = {}): ProjectPullRequest {
  return {
    id: "pr-1",
    provider: "github",
    workspace_id: "ws-1",
    repo_owner: "acme",
    repo_name: "widget",
    number: 7,
    title: "Add the widget",
    state: "open",
    html_url: "https://github.com/acme/widget/pull/7",
    branch: null,
    author_login: null,
    author_avatar_url: null,
    merged_at: null,
    closed_at: null,
    pr_created_at: "2026-10-01T00:00:00Z",
    pr_updated_at: "2026-10-02T00:00:00Z",
    additions: 12,
    deletions: 3,
    issues: [{ id: "issue-1", identifier: "MUL-1", title: "First issue" }],
    ...overrides,
  };
}

const COUNTS = { open: 2, merged: 1, closed: 1 };

// Serves each state's slice of the fixture, the way the endpoint does.
function serve(byState: Record<string, ProjectPullRequest[]>, counts = COUNTS) {
  mocks.listProjectPullRequests.mockImplementation(
    async (_projectId: string, params: { state?: string }) => {
      const rows = byState[params.state ?? "open"] ?? [];
      const response: ProjectPullRequestsResponse = {
        pull_requests: rows,
        total: counts[(params.state ?? "open") as keyof typeof counts],
        counts,
      };
      return response;
    },
  );
}

function renderPanel() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  return renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <ProjectPullRequestsPanel projectId="project-1" />
    </QueryClientProvider>,
  );
}

describe("ProjectPullRequestsPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows every group's count, expands Open only, and fetches the others on expand", async () => {
    serve({
      open: [
        makePR({ id: "pr-1", number: 7, title: "Add the widget" }),
        makePR({ id: "pr-2", number: 8, title: "Fix the widget", state: "draft" }),
      ],
      merged: [makePR({ id: "pr-3", number: 5, title: "Ship the gadget", state: "merged" })],
      closed: [makePR({ id: "pr-4", number: 4, title: "Abandoned idea", state: "closed" })],
    });
    renderPanel();

    const open = await screen.findByTestId("project-pull-requests-open");
    expect(await within(open).findByText("Add the widget")).toBeInTheDocument();
    expect(within(open).getByText("Fix the widget")).toBeInTheDocument();
    expect(within(open).getByRole("button", { name: /Open/ })).toHaveTextContent("2");
    expect(within(screen.getByTestId("project-pull-requests-merged")).getByRole("button")).toHaveTextContent("1");
    expect(within(screen.getByTestId("project-pull-requests-closed")).getByRole("button")).toHaveTextContent("1");

    // Collapsed groups are not fetched, and their rows are not rendered.
    expect(screen.queryByText("Ship the gadget")).not.toBeInTheDocument();
    expect(mocks.listProjectPullRequests).toHaveBeenCalledTimes(1);
    expect(mocks.listProjectPullRequests).toHaveBeenCalledWith("project-1", { state: "open", limit: 20, offset: 0 });

    await userEvent.click(within(screen.getByTestId("project-pull-requests-merged")).getByRole("button"));
    expect(await screen.findByText("Ship the gadget")).toBeInTheDocument();
    expect(mocks.listProjectPullRequests).toHaveBeenCalledWith("project-1", { state: "merged", limit: 20, offset: 0 });
    expect(screen.queryByText("Abandoned idea")).not.toBeInTheDocument();
  });

  it("opens the issue from an issue chip without opening the PR", async () => {
    serve({
      open: [
        makePR({
          issues: [
            { id: "issue-1", identifier: "MUL-1", title: "First issue" },
            { id: "issue-2", identifier: "MUL-2", title: "Second issue" },
          ],
        }),
      ],
    });
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "MUL-2" }));
    expect(mocks.openIssue).toHaveBeenCalledWith("issue-2");
    expect(screen.getByRole("button", { name: "MUL-1" })).toHaveAttribute("title", "First issue");
  });

  it("links the row to the PR URL and shows repo, number and diff", async () => {
    serve({ open: [makePR()] });
    renderPanel();

    const row = await screen.findByTestId("project-pull-request-row");
    const link = within(row).getByRole("link");
    expect(link).toHaveAttribute("href", "https://github.com/acme/widget/pull/7");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveTextContent("widget#7");
    expect(link).toHaveTextContent("+12");
    expect(link).toHaveTextContent("−3");
  });

  it("hides the diff when both sides are zero and shows the CI verdict", async () => {
    serve(
      {
        open: [makePR({ id: "pr-1", additions: 0, deletions: 0, checks_rollup: "failure", snapshot_available: true })],
      },
      { open: 1, merged: 0, closed: 0 },
    );
    renderPanel();

    const row = await screen.findByTestId("project-pull-request-row");
    expect(row).not.toHaveTextContent("+0");
    expect(within(row).getByRole("img", { name: "Checks failed" })).toBeInTheDocument();
  });

  it("loads the next page of a group while it has more rows than loaded", async () => {
    const page = (from: number, count: number) =>
      Array.from({ length: count }, (_, i) =>
        makePR({ id: `pr-${from + i}`, number: from + i, title: `PR number ${from + i}`, issues: [] }),
      );
    mocks.listProjectPullRequests.mockImplementation(
      async (_projectId: string, params: { offset?: number }) => ({
        pull_requests: params.offset ? page(21, 2) : page(1, 20),
        total: 22,
        counts: { open: 22, merged: 0, closed: 0 },
      }),
    );
    renderPanel();

    expect(await screen.findByText("PR number 1")).toBeInTheDocument();
    expect(screen.queryByText("PR number 21")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("PR number 21")).toBeInTheDocument();
    expect(mocks.listProjectPullRequests).toHaveBeenCalledWith("project-1", { state: "open", limit: 20, offset: 20 });
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument());
  });

  it("renders one empty line when the project has no pull requests", async () => {
    serve({}, { open: 0, merged: 0, closed: 0 });
    renderPanel();

    expect(await screen.findByText("No pull requests yet")).toBeInTheDocument();
    expect(screen.queryByTestId("project-pull-requests-open")).not.toBeInTheDocument();
  });
});
