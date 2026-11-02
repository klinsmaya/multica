import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Issue, Project } from "@multica/core/types";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import {
  IssuePeekActionsContext,
  type IssuePeekActions,
} from "../surface/peek-context";
import { FeedQuickActions } from "./feed-quick-actions";

const mockGetProject = vi.hoisted(() => vi.fn());
const mockCreateIssue = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: { getProject: mockGetProject, createIssue: mockCreateIssue },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/acme/issues/${id}` }),
}));

vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({
    categoryOf: (status: string) =>
      status === "done" ? "done" : status === "cancelled" ? "closed" : "started",
  }),
}));

vi.mock("sonner", () => ({ toast: { error: mockToastError } }));

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
    title: "Issue",
    status: "todo",
    parent_issue_id: null,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    last_activity_at: null,
    ...overrides,
  } as Issue;
}

function makeProject(overrides: Partial<Project>): Project {
  return {
    id: "p1",
    lead_type: null,
    lead_id: null,
    ...overrides,
  } as Project;
}

function renderActions(props: {
  issues: Issue[];
  projectId?: string | null;
  onNewIssue?: () => void;
}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <NavigationProvider value={navigation}>
        <IssuePeekActionsContext.Provider value={peek}>
          <FeedQuickActions
            issues={props.issues}
            projectId={props.projectId === undefined ? "p1" : props.projectId}
            onNewIssue={props.onNewIssue ?? vi.fn()}
          />
        </IssuePeekActionsContext.Provider>
      </NavigationProvider>
    </QueryClientProvider>,
  );
}

describe("FeedQuickActions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetProject.mockResolvedValue(makeProject({}));
  });

  it("hides the plan action when the project has no agent lead", async () => {
    mockGetProject.mockResolvedValue(
      makeProject({ lead_type: "member", lead_id: "user-1" }),
    );
    renderActions({ issues: [makeIssue({})] });

    await waitFor(() => expect(mockGetProject).toHaveBeenCalledWith("p1"));
    expect(screen.queryByRole("button", { name: "Make a plan" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue recent work" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New issue" })).toBeInTheDocument();
  });

  it("hides the plan action outside a project", () => {
    renderActions({ issues: [], projectId: null });

    expect(mockGetProject).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Make a plan" })).not.toBeInTheDocument();
  });

  it("disables continue when every issue is finished", () => {
    renderActions({
      issues: [
        makeIssue({ id: "a", status: "done" }),
        makeIssue({ id: "b", status: "cancelled" }),
      ],
    });

    expect(screen.getByRole("button", { name: "Continue recent work" })).toBeDisabled();
  });

  it("peeks the unfinished top-level issue with the latest activity", () => {
    renderActions({
      issues: [
        makeIssue({ id: "stale", last_activity_at: "2026-10-02T00:00:00Z" }),
        makeIssue({ id: "latest", last_activity_at: "2026-10-05T00:00:00Z" }),
        makeIssue({
          id: "finished",
          status: "done",
          last_activity_at: "2026-10-09T00:00:00Z",
        }),
        makeIssue({
          id: "child",
          parent_issue_id: "stale",
          last_activity_at: "2026-10-10T00:00:00Z",
        }),
        // No activity yet: falls back to updated_at.
        makeIssue({ id: "fresh", updated_at: "2026-10-04T00:00:00Z" }),
      ],
    });

    fireEvent.click(screen.getByRole("button", { name: "Continue recent work" }));

    expect(peek.open).toHaveBeenCalledWith("latest");
  });

  it("creates a plan issue for the agent lead and peeks it after the server answers", async () => {
    mockGetProject.mockResolvedValue(
      makeProject({ lead_type: "agent", lead_id: "agent-1" }),
    );
    let resolveCreate!: (issue: Issue) => void;
    mockCreateIssue.mockReturnValue(
      new Promise<Issue>((resolve) => {
        resolveCreate = resolve;
      }),
    );
    renderActions({ issues: [] });

    const plan = await screen.findByRole("button", { name: "Make a plan" });
    fireEvent.click(plan);

    await waitFor(() =>
      expect(mockCreateIssue).toHaveBeenCalledWith({
        title: "Make a plan",
        project_id: "p1",
        assignee_type: "agent",
        assignee_id: "agent-1",
      }),
    );
    await waitFor(() => expect(plan).toBeDisabled());
    expect(plan).toHaveAttribute("aria-busy", "true");
    expect(peek.open).not.toHaveBeenCalled();

    resolveCreate(makeIssue({ id: "planned" }));

    await waitFor(() => expect(peek.open).toHaveBeenCalledWith("planned"));
  });

  it("reports a failed plan creation without opening anything", async () => {
    mockGetProject.mockResolvedValue(
      makeProject({ lead_type: "agent", lead_id: "agent-1" }),
    );
    mockCreateIssue.mockRejectedValue(new Error("boom"));
    renderActions({ issues: [] });

    fireEvent.click(await screen.findByRole("button", { name: "Make a plan" }));

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith("Failed to create issue"),
    );
    expect(peek.open).not.toHaveBeenCalled();
  });

  it("hands the new-issue action to the caller", () => {
    const onNewIssue = vi.fn();
    renderActions({ issues: [], onNewIssue });

    fireEvent.click(screen.getByRole("button", { name: "New issue" }));

    expect(onNewIssue).toHaveBeenCalledTimes(1);
  });
});
