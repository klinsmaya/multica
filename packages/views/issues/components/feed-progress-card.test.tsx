import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { IssueLatestProgress } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { FeedProgressCard } from "./feed-progress-card";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({
    categoryOf: (status: string) =>
      status === "done" ? "done" : status === "cancelled" ? "closed" : "started",
  }),
}));

function progress(content: string): IssueLatestProgress {
  return { comment_id: "c-1", content, created_at: "2026-10-02T00:00:00Z" };
}

const SEVEN = [
  "- [x] one",
  "- [x] two",
  "- [ ] three",
  "- [ ] four",
  "- [ ] five",
  "- [ ] six",
  "- [ ] seven",
].join("\n");

describe("FeedProgressCard", () => {
  it("shows the count and a progress bar for an open issue", () => {
    renderWithI18n(<FeedProgressCard progress={progress(SEVEN)} status="in_progress" />);

    expect(screen.getByText("2/7 done")).toBeInTheDocument();
    const bar = screen.getByRole("progressbar");
    expect(bar).toHaveAttribute("aria-valuenow", "2");
    expect(bar).toHaveAttribute("aria-valuemax", "7");
  });

  it("lists the first five items and folds the rest into +N more", () => {
    renderWithI18n(<FeedProgressCard progress={progress(SEVEN)} status="in_progress" />);

    expect(screen.getAllByRole("listitem")).toHaveLength(5);
    expect(screen.getByText("five")).toBeInTheDocument();
    expect(screen.queryByText("six")).not.toBeInTheDocument();
    expect(screen.getByText("+2 more")).toBeInTheDocument();
  });

  it("shows no +N more when everything fits", () => {
    renderWithI18n(
      <FeedProgressCard progress={progress("- [x] a\n- [ ] b")} status="in_progress" />,
    );

    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    expect(screen.queryByText(/more/)).not.toBeInTheDocument();
  });

  it("marks done items struck through and open ones plain", () => {
    renderWithI18n(
      <FeedProgressCard progress={progress("- [x] shipped\n- [ ] pending")} status="in_progress" />,
    );

    expect(screen.getByText("shipped").closest("li")).toHaveClass("line-through");
    expect(screen.getByText("pending").closest("li")).not.toHaveClass("line-through");
  });

  it("reduces inline Markdown in an item to its text", () => {
    renderWithI18n(
      <FeedProgressCard
        progress={progress("- [ ] fix **bug** in [MUL-1](mention://issue/abc) `now`")}
        status="in_progress"
      />,
    );

    expect(screen.getByText("fix bug in MUL-1 now")).toBeInTheDocument();
  });

  it.each(["done", "cancelled"])("collapses to the count once the issue is %s", (status) => {
    renderWithI18n(<FeedProgressCard progress={progress(SEVEN)} status={status} />);

    expect(screen.getByRole("button", { name: "2/7 done" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });

  it("expands a collapsed card with its chevron and collapses it again", () => {
    renderWithI18n(<FeedProgressCard progress={progress(SEVEN)} status="done" />);
    const toggle = screen.getByRole("button", { name: "2/7 done" });

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getAllByRole("listitem")).toHaveLength(5);

    fireEvent.click(toggle);
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });

  it("renders nothing without progress", () => {
    const { container, rerender } = renderWithI18n(
      <FeedProgressCard progress={null} status="in_progress" />,
    );
    expect(container).toBeEmptyDOMElement();

    rerender(<FeedProgressCard progress={undefined} status="in_progress" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the report has no checklist", () => {
    const { container } = renderWithI18n(
      <FeedProgressCard progress={progress("Still thinking.\n- just a bullet")} status="in_progress" />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
