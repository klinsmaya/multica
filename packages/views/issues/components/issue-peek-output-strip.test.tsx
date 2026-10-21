import { screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { IssueThreadSummary } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { IssuePeekOutputStrip } from "./issue-peek-output-strip";

const mockListThreadSummaries = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: { listThreadSummaries: mockListThreadSummaries },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function summary(overrides: Partial<IssueThreadSummary>): IssueThreadSummary {
  return {
    issue_id: "issue-1",
    reply_count: 0,
    last_reply_at: null,
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

function renderStrip(issueId = "issue-1") {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <IssuePeekOutputStrip issueId={issueId} />
    </QueryClientProvider>,
  );
}

describe("IssuePeekOutputStrip", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("asks for just the peeked issue and shows its PRs and deliverables", async () => {
    mockListThreadSummaries.mockResolvedValue({
      summaries: [
        summary({
          pull_requests: [
            {
              id: "pr-1",
              number: 7,
              title: "Ship it",
              url: "https://github.com/acme/app/pull/7",
              state: "open",
              additions: 1,
              deletions: 1,
              checks_state: "pending",
            },
          ],
          deliverable_count: 2,
          last_deliverable_at: "2026-10-02T00:00:00Z",
        }),
      ],
    });
    renderStrip();

    expect(await screen.findByTestId("issue-output-strip")).toBeInTheDocument();
    expect(mockListThreadSummaries).toHaveBeenCalledWith(["issue-1"]);
    expect(screen.getByTestId("output-pull-request")).toHaveAttribute(
      "aria-label",
      "Pull request #7, Open, Checks running",
    );
    // Already in the issue, so the count is a label, not a link back to it.
    const files = screen.getByTestId("output-deliverables");
    expect(files).toHaveTextContent("2 deliverables");
    expect(files.tagName).not.toBe("BUTTON");
  });

  it("renders nothing when the issue has neither PRs nor deliverables", async () => {
    mockListThreadSummaries.mockResolvedValue({
      summaries: [summary({ reply_count: 3 })],
    });
    const { container } = renderStrip();

    await waitFor(() => expect(mockListThreadSummaries).toHaveBeenCalled());
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it("renders nothing when the server has no row for the issue", async () => {
    mockListThreadSummaries.mockResolvedValue({ summaries: [] });
    const { container } = renderStrip();

    await waitFor(() => expect(mockListThreadSummaries).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
