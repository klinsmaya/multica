import { fireEvent, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { IssueAwaitingMe, IssueThreadSummary } from "@multica/core/types";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import {
  IssuePeekActionsContext,
  type IssuePeekActions,
} from "../surface/peek-context";
import { FeedDecisionCard } from "./feed-decision-card";

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/acme/issues/${id}` }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: (_type: string, id: string) => `name:${id}` }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorType, actorId }: { actorType: string; actorId: string }) => (
    <span data-testid="avatar" data-actor-type={actorType}>
      {actorId}
    </span>
  ),
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

function question(id: string, overrides: Partial<IssueAwaitingMe> = {}): IssueAwaitingMe {
  return {
    comment_id: `comment-${id}`,
    actor_id: `agent-${id}`,
    excerpt: `Question ${id}`,
    created_at: "2026-10-02T00:00:00Z",
    ...overrides,
  };
}

function summaryOf(awaiting_me: IssueAwaitingMe[]): IssueThreadSummary {
  return {
    issue_id: "issue-1",
    reply_count: awaiting_me.length,
    last_reply_at: null,
    open_thread_count: awaiting_me.length,
    participants: [],
    last_read_at: null,
    awaiting_me,
    latest_progress: null,
  };
}

function renderCard(summary: IssueThreadSummary | undefined, withPeek = true) {
  const card = <FeedDecisionCard issueId="issue-1" summary={summary} />;
  return renderWithI18n(
    <NavigationProvider value={navigation}>
      {withPeek ? (
        <IssuePeekActionsContext.Provider value={peek}>{card}</IssuePeekActionsContext.Provider>
      ) : (
        card
      )}
    </NavigationProvider>,
  );
}

describe("FeedDecisionCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows the asking agent's avatar, the label and the excerpt", () => {
    renderCard(summaryOf([question("1", { excerpt: "Ship @Me?" })]));

    expect(screen.getByText("Waiting on you")).toBeInTheDocument();
    expect(screen.getByText("Ship @Me?")).toBeInTheDocument();
    const avatar = screen.getByTestId("avatar");
    expect(avatar).toHaveTextContent("agent-1");
    expect(avatar).toHaveAttribute("data-actor-type", "agent");
    expect(screen.queryByText(/^\+\d/)).not.toBeInTheDocument();
  });

  it("folds several questions into the newest excerpt with a +N count", () => {
    renderCard(summaryOf([question("3"), question("2"), question("1")]));

    expect(screen.getByText("Question 3")).toBeInTheDocument();
    expect(screen.queryByText("Question 2")).not.toBeInTheDocument();
    expect(screen.getByText("+2")).toBeInTheDocument();
    expect(screen.getByTestId("avatar")).toHaveTextContent("agent-3");
  });

  it("opens the peek on the newest question's comment", () => {
    renderCard(summaryOf([question("2"), question("1")]));

    fireEvent.click(screen.getByRole("button", { name: "View thread" }));

    expect(peek.open).toHaveBeenCalledTimes(1);
    expect(peek.open).toHaveBeenCalledWith("issue-1", { commentId: "comment-2" });
  });

  it("falls back to the issue page at the comment outside a peek host", () => {
    renderCard(summaryOf([question("1")]), false);

    fireEvent.click(screen.getByRole("button", { name: "View thread" }));

    expect(navigation.push).toHaveBeenCalledWith("/acme/issues/issue-1#comment-comment-1");
  });

  it.each([
    ["an empty list", summaryOf([])],
    ["no summary", undefined],
  ])("renders nothing for %s", (_name, summary) => {
    const { container } = renderCard(summary);

    expect(container).toBeEmptyDOMElement();
  });
});
