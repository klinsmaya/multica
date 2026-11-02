import { fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { IssueLastReply, IssueThreadSummary } from "@multica/core/types";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import {
  IssuePeekActionsContext,
  type IssuePeekActions,
} from "../surface/peek-context";
import { FeedReplyBubble } from "./feed-reply-bubble";

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

// Comments render their body through ReadonlyContent; the bubble must hand it
// the stored Markdown untouched.
vi.mock("../../editor", () => ({
  ReadonlyContent: ({ content }: { content: string }) => (
    <div data-testid="readonly-content">{content}</div>
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

function replyOf(overrides: Partial<IssueLastReply> = {}): IssueLastReply {
  return {
    comment_id: "comment-9",
    author_type: "agent",
    author_id: "agent-1",
    content: "Shipped **the fix**",
    created_at: new Date(Date.now() - 5 * 60_000).toISOString(),
    ...overrides,
  };
}

function summaryOf(last_reply: IssueLastReply | null): IssueThreadSummary {
  return {
    issue_id: "issue-1",
    reply_count: 1,
    last_reply_at: last_reply?.created_at ?? null,
    last_reply_excerpt: null,
    last_reply,
    open_thread_count: 0,
    participants: [],
    last_read_at: null,
    awaiting_me: [],
    latest_progress: null,
    pull_requests: [],
    deliverable_count: 0,
    last_deliverable_at: null,
    children: [],
    child_total: 0,
  };
}

function renderBubble(summary: IssueThreadSummary | undefined, withPeek = true) {
  const bubble = <FeedReplyBubble issueId="issue-1" identifier="MUL-1" summary={summary} />;
  return renderWithI18n(
    <NavigationProvider value={navigation}>
      {withPeek ? (
        <IssuePeekActionsContext.Provider value={peek}>{bubble}</IssuePeekActionsContext.Provider>
      ) : (
        bubble
      )}
    </NavigationProvider>,
  );
}

// jsdom has no layout, so the clamp measurement reads these two properties.
function stubLayout(scrollHeight: number, clientHeight: number) {
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
    configurable: true,
    get() {
      return scrollHeight;
    },
  });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", {
    configurable: true,
    get() {
      return clientHeight;
    },
  });
}

describe("FeedReplyBubble", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    Reflect.deleteProperty(HTMLElement.prototype, "scrollHeight");
    Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
  });

  it("shows the author's avatar and name, the age and the Markdown body", () => {
    renderBubble(summaryOf(replyOf()));

    const avatar = screen.getByTestId("avatar");
    expect(avatar).toHaveTextContent("agent-1");
    expect(avatar).toHaveAttribute("data-actor-type", "agent");
    expect(screen.getByText("name:agent-1")).toBeInTheDocument();
    expect(screen.getByText("5m ago")).toBeInTheDocument();
    expect(screen.getByTestId("readonly-content")).toHaveTextContent("Shipped **the fix**");
  });

  it("renders nothing without a last reply, while loading, or for an empty body", () => {
    const { container, rerender } = renderBubble(summaryOf(null));
    expect(container).toBeEmptyDOMElement();

    rerender(
      <NavigationProvider value={navigation}>
        <FeedReplyBubble issueId="issue-1" identifier="MUL-1" summary={undefined} />
      </NavigationProvider>,
    );
    expect(container).toBeEmptyDOMElement();

    rerender(
      <NavigationProvider value={navigation}>
        <FeedReplyBubble
          issueId="issue-1"
          identifier="MUL-1"
          summary={summaryOf(replyOf({ content: "  \n" }))}
        />
      </NavigationProvider>,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("offers no expand toggle when the body fits", () => {
    stubLayout(80, 80);
    renderBubble(summaryOf(replyOf()));

    expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
  });

  it("clamps an overflowing body and toggles it open and closed", () => {
    stubLayout(400, 120);
    renderBubble(summaryOf(replyOf()));
    const body = screen.getByTestId("readonly-content").parentElement!;
    expect(body).toHaveClass("line-clamp-6");

    fireEvent.click(screen.getByRole("button", { name: "Show more" }));
    expect(body).not.toHaveClass("line-clamp-6");
    expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );

    fireEvent.click(screen.getByRole("button", { name: "Show less" }));
    expect(body).toHaveClass("line-clamp-6");
    expect(screen.getByRole("button", { name: "Show more" })).toBeInTheDocument();
  });

  it("opens the peek on the reply's comment", () => {
    renderBubble(summaryOf(replyOf()));

    fireEvent.click(screen.getByRole("button", { name: /View thread/ }));

    expect(peek.open).toHaveBeenCalledTimes(1);
    expect(peek.open).toHaveBeenCalledWith("issue-1", { commentId: "comment-9" });
  });

  it("falls back to the issue page at the comment outside a peek host", () => {
    renderBubble(summaryOf(replyOf()), false);

    fireEvent.click(screen.getByRole("button", { name: /View thread/ }));

    expect(navigation.push).toHaveBeenCalledWith("/acme/issues/issue-1#comment-comment-9");
  });
});
