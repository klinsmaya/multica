import { fireEvent, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { IssueThreadChild, IssueThreadSummary } from "@multica/core/types";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import {
  IssuePeekActionsContext,
  type IssuePeekActions,
} from "../surface/peek-context";
import { FeedThreadCards } from "./feed-thread-cards";

const mockActivity = vi.hoisted(() => ({ working: new Set<string>() }));

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
  ActorAvatar: ({ actorType, actorId }: { actorType: string; actorId: string }) => (
    <span data-testid="avatar" data-actor-type={actorType}>
      {actorId}
    </span>
  ),
}));

vi.mock("../surface/activity", () => ({
  useIssueSurfaceActivity: () => ({
    activityByIssueId: new Map(
      [...mockActivity.working].map((id) => [id, { isWorking: true }]),
    ),
    runningIssueIds: mockActivity.working,
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

function child(id: string, overrides: Partial<IssueThreadChild> = {}): IssueThreadChild {
  return {
    id,
    identifier: `MUL-${id}`,
    title: `Task ${id}`,
    status: "in_progress",
    assignee_type: null,
    assignee_id: null,
    reply_count: 0,
    last_reply_at: null,
    ...overrides,
  };
}

function summaryOf(children: IssueThreadChild[], childTotal = children.length): IssueThreadSummary {
  return {
    issue_id: "parent-1",
    reply_count: 0,
    last_reply_at: null,
    last_reply_excerpt: null,
    last_reply: null,
    open_thread_count: 0,
    participants: [],
    last_read_at: null,
    awaiting_me: [],
    latest_progress: null,
    pull_requests: [],
    deliverable_count: 0,
    last_deliverable_at: null,
    children,
    child_total: childTotal,
  };
}

function renderCards(summary: IssueThreadSummary | undefined, withPeek = true) {
  const cards = <FeedThreadCards issueId="parent-1" summary={summary} />;
  return renderWithI18n(
    <NavigationProvider value={navigation}>
      {withPeek ? (
        <IssuePeekActionsContext.Provider value={peek}>{cards}</IssuePeekActionsContext.Provider>
      ) : (
        cards
      )}
    </NavigationProvider>,
  );
}

describe("FeedThreadCards", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockActivity.working = new Set();
  });

  it("renders nothing without a summary or without children", () => {
    const { container, rerender } = renderCards(undefined);
    expect(container).toBeEmptyDOMElement();

    rerender(
      <NavigationProvider value={navigation}>
        <FeedThreadCards issueId="parent-1" summary={summaryOf([])} />
      </NavigationProvider>,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows identifier, title and status of each child, in the given order", () => {
    renderCards(summaryOf([child("2"), child("1")]));

    const cards = screen.getAllByRole("button");
    expect(cards).toHaveLength(2);
    expect(cards[0]).toHaveTextContent("MUL-2");
    expect(cards[0]).toHaveTextContent("Task 2");
    expect(cards[1]).toHaveTextContent("Task 1");
    expect(screen.getAllByRole("img", { name: "In Progress" })).toHaveLength(2);
  });

  it("shows the assignee only when there is one", () => {
    renderCards(
      summaryOf([
        child("1", { assignee_type: "agent", assignee_id: "agent-7" }),
        child("2"),
      ]),
    );

    const avatars = screen.getAllByTestId("avatar");
    expect(avatars).toHaveLength(1);
    expect(avatars[0]).toHaveTextContent("agent-7");
    expect(avatars[0]).toHaveAttribute("data-actor-type", "agent");
  });

  it("shows the reply count only when there are replies", () => {
    renderCards(summaryOf([child("1", { reply_count: 3 }), child("2", { reply_count: 1 })]));

    expect(screen.getByText("3 replies")).toBeInTheDocument();
    expect(screen.getByText("1 reply")).toBeInTheDocument();
    renderCards(summaryOf([child("3")]));
    expect(screen.queryByText(/0 repl/)).not.toBeInTheDocument();
  });

  it("marks only the children an agent is working on", () => {
    mockActivity.working = new Set(["1"]);
    renderCards(summaryOf([child("1"), child("2")]));

    const pulses = screen.getAllByRole("img", { name: "Working" });
    expect(pulses).toHaveLength(1);
    expect(screen.getAllByRole("button")[0]).toContainElement(pulses[0]!);
  });

  it("opens the clicked child in the peek", () => {
    renderCards(summaryOf([child("1"), child("2")]));

    fireEvent.click(screen.getByText("Task 2"));

    expect(peek.open).toHaveBeenCalledTimes(1);
    expect(peek.open).toHaveBeenCalledWith("2");
  });

  it("opens a child from the keyboard", () => {
    renderCards(summaryOf([child("1")]));

    fireEvent.keyDown(screen.getByRole("button"), { key: "Enter" });

    expect(peek.open).toHaveBeenCalledWith("1");
  });

  it("falls back to the issue page outside a peek host", () => {
    renderCards(summaryOf([child("1")]), false);

    fireEvent.click(screen.getByText("Task 1"));

    expect(navigation.push).toHaveBeenCalledWith("/acme/issues/1");
  });

  it("adds no extra row when every child is listed", () => {
    renderCards(summaryOf([child("1"), child("2")], 2));

    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("ends with a row to the parent issue when more children exist than listed", () => {
    renderCards(summaryOf([child("1"), child("2")], 7));

    const more = screen.getByRole("link", { name: "5 more sub-issues" });
    expect(more).toHaveAttribute("href", "/acme/issues/parent-1");
    fireEvent.click(more);
    expect(navigation.push).toHaveBeenCalledWith("/acme/issues/parent-1");
    expect(peek.open).not.toHaveBeenCalled();
  });
});
