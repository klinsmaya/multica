import { fireEvent, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Issue, IssueThreadPullRequest, IssueThreadSummary } from "@multica/core/types";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import {
  IssuePeekActionsContext,
  type IssuePeekActions,
} from "../surface/peek-context";
import { FeedThreadBar } from "./feed-thread-bar";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/acme/issues/${id}` }),
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
  ActorAvatar: ({ actorId }: { actorId: string }) => <span data-testid="avatar">{actorId}</span>,
}));

vi.mock("./issue-agent-activity-indicator", () => ({
  IssueAgentActivityIndicator: () => null,
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

const issue = {
  id: "issue-1",
  identifier: "MUL-1",
  title: "Issue",
  status: "todo",
} as Issue;

function pr(overrides: Partial<IssueThreadPullRequest>): IssueThreadPullRequest {
  return {
    id: "pr-1",
    number: 12,
    title: "Fix the thing",
    url: "https://github.com/acme/app/pull/12",
    state: "open",
    additions: 10,
    deletions: 2,
    checks_state: "none",
    ...overrides,
  };
}

function summary(overrides: Partial<IssueThreadSummary> = {}): IssueThreadSummary {
  return {
    issue_id: "issue-1",
    reply_count: 2,
    last_reply_at: "2026-10-02T00:00:00Z",
    last_reply_excerpt: null,
    open_thread_count: 0,
    participants: [],
    last_read_at: null,
    awaiting_me: [],
    latest_progress: null,
    pull_requests: [],
    deliverable_count: 0,
    last_deliverable_at: null,
    ...overrides,
  };
}

function renderBar(value: IssueThreadSummary | undefined) {
  return renderWithI18n(
    <NavigationProvider value={navigation}>
      <IssuePeekActionsContext.Provider value={peek}>
        <FeedThreadBar issue={issue} summary={value} />
      </IssuePeekActionsContext.Provider>
    </NavigationProvider>,
  );
}

describe("FeedThreadBar output strip", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows one chip per PR with its own state and CI", () => {
    renderBar(
      summary({
        pull_requests: [
          pr({ id: "pr-2", number: 13, state: "open", checks_state: "failed", url: "https://github.com/acme/app/pull/13" }),
          pr({ id: "pr-1", number: 12, state: "merged", checks_state: "passed" }),
        ],
      }),
    );

    const chips = screen.getAllByTestId("output-pull-request");
    expect(chips).toHaveLength(2);
    expect(chips[0]).toHaveAttribute("aria-label", "Pull request #13, Open, Checks failed");
    expect(chips[0]).toHaveAttribute("href", "https://github.com/acme/app/pull/13");
    expect(chips[0]).toHaveAttribute("target", "_blank");
    expect(chips[0]).toHaveAttribute("rel", "noreferrer noopener");
    expect(chips[0]).toHaveAttribute("title", "Fix the thing");
    expect(chips[0]).toHaveTextContent("#13");
    // A merged PR has no CI left to report, whatever the last verdict was.
    expect(chips[1]).toHaveAttribute("aria-label", "Pull request #12, Merged");
    expect(chips[1]).toHaveTextContent("#12");
    // Distinct states draw distinct icons.
    expect(chips[0]!.querySelector("svg")!.getAttribute("class")).not.toBe(
      chips[1]!.querySelector("svg")!.getAttribute("class"),
    );
    expect(chips[0]!.querySelectorAll("svg")).toHaveLength(2);
    expect(chips[1]!.querySelectorAll("svg")).toHaveLength(1);
  });

  it("reports pending and passed CI on an open PR, and nothing for no checks", () => {
    renderBar(
      summary({
        pull_requests: [
          pr({ id: "a", number: 1, checks_state: "pending" }),
          pr({ id: "b", number: 2, checks_state: "passed" }),
          pr({ id: "c", number: 3, checks_state: "none" }),
        ],
      }),
    );

    const labels = screen.getAllByTestId("output-pull-request").map((chip) => chip.getAttribute("aria-label"));
    expect(labels).toEqual([
      "Pull request #1, Open, Checks running",
      "Pull request #2, Open, Checks passed",
      "Pull request #3, Open",
    ]);
  });

  it("opens the PR without opening the issue", () => {
    renderBar(summary({ pull_requests: [pr({})] }));

    fireEvent.click(screen.getByTestId("output-pull-request"));

    expect(peek.open).not.toHaveBeenCalled();
  });

  it("draws a state this build does not know with the plain PR icon and its raw name", () => {
    renderBar(summary({ pull_requests: [pr({ state: "queued" })] }));

    expect(screen.getByTestId("output-pull-request")).toHaveAttribute(
      "aria-label",
      "Pull request #12, queued",
    );
  });

  it("does not link a PR whose url is not a web address", () => {
    renderBar(summary({ pull_requests: [pr({ url: "javascript:alert(1)" })] }));

    expect(screen.getByTestId("output-pull-request")).not.toHaveAttribute("href");
  });

  it("shows no strip when the issue has produced nothing", () => {
    renderBar(summary());
    expect(screen.queryByTestId("issue-output-strip")).not.toBeInTheDocument();

    renderBar(undefined);
    expect(screen.queryByTestId("issue-output-strip")).not.toBeInTheDocument();
  });

  it("counts deliverables with when the newest changed, and opens the issue on click", () => {
    renderBar(
      summary({
        deliverable_count: 3,
        last_deliverable_at: new Date(Date.now() - 3 * 3_600_000).toISOString(),
      }),
    );

    const chip = screen.getByTestId("output-deliverables");
    expect(chip).toHaveTextContent("3 deliverables");
    expect(chip).toHaveTextContent("3h ago");
    expect(chip.getAttribute("title")).toMatch(/^Last updated /);

    fireEvent.click(chip);

    // The chip's own handler, not the bar's click bubbling up too.
    expect(peek.open).toHaveBeenCalledTimes(1);
    expect(peek.open).toHaveBeenCalledWith("issue-1");
  });

  it("uses the singular for one deliverable and tolerates a missing date", () => {
    renderBar(summary({ deliverable_count: 1, last_deliverable_at: null }));

    expect(screen.getByTestId("output-deliverables")).toHaveTextContent(/^1 deliverable$/);
  });

  it("shows PRs and deliverables together", () => {
    renderBar(
      summary({
        pull_requests: [pr({})],
        deliverable_count: 2,
        last_deliverable_at: "2026-10-02T00:00:00Z",
      }),
    );

    expect(screen.getAllByTestId("output-pull-request")).toHaveLength(1);
    expect(screen.getByTestId("output-deliverables")).toHaveTextContent("2 deliverables");
  });
});
