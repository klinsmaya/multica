import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import type { Attachment, TimelineEntry } from "@multica/core/types";
import { issueTimelineOptions } from "@multica/core/issues/queries";
import { renderWithI18n } from "../../../test/i18n";

const { listTimelineMock, textContentMock, taskSnapshotMock } = vi.hoisted(() => ({
  listTimelineMock: vi.fn(),
  textContentMock: vi.fn(),
  taskSnapshotMock: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listTimeline: listTimelineMock,
    getAttachment: vi.fn(() => Promise.reject(new Error("no re-sign"))),
    getAttachmentTextContent: textContentMock,
    getAgentTaskSnapshot: taskSnapshotMock,
  },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));

// The real viewer panel, without the editor package's tiptap import surface.
vi.mock("../../../editor", async () => {
  const panel = await import("../../../editor/attachment-preview-modal");
  return {
    AttachmentPreviewPanel: panel.AttachmentPreviewPanel,
    ChromeButton: panel.ChromeButton,
    ChromeDivider: panel.ChromeDivider,
    ownsArrowKeys: panel.ownsArrowKeys,
  };
});
vi.mock("../../../editor/use-download-attachment", () => ({ useDownloadAttachment: () => vi.fn() }));
vi.mock("../../../editor/hooks/use-inline-media-url", () => ({
  useResignedInlineMedia: (_id: string | undefined, url: string) => ({ url, pending: false }),
}));
vi.mock("@multica/core/workspace/avatar-url", () => ({ resolvePublicFileUrl: (url: string) => url }));
vi.mock("../../../platform", () => ({ useImmersiveMode: () => {} }));
vi.mock("../../../navigation", () => ({ useNavigation: () => ({}), useAppOrigin: () => "https://app.example.test" }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (select: (s: { user: { id: string } }) => unknown) => select({ user: { id: "user-1" } }),
}));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspaceSlug: () => "acme",
  useCurrentWorkspace: () => ({ id: "ws-1", slug: "acme" }),
}));

// The page behind it is IssueDetail's own business (issue-detail.test.tsx owns
// the thread variant). The stub keeps a reply draft in local state, so a
// remount on navigation would lose it.
vi.mock("../issue-detail", () => ({
  IssueDetail: ({
    issueId,
    variant,
    replyContext,
  }: {
    issueId: string;
    variant?: string;
    replyContext?: { attachmentId: string; filename: string };
  }) => {
    const [draft, setDraft] = useState("");
    return (
      <div
        data-testid="thread"
        data-issue={issueId}
        data-variant={variant}
        data-reply-attachment={replyContext?.attachmentId}
        data-reply-filename={replyContext?.filename}
      >
        <input aria-label="Reply" value={draft} onChange={(e) => setDraft(e.target.value)} />
      </div>
    );
  },
}));

import { DeliverableFocusModal } from "./deliverable-focus";

function shot(id: string, filename: string, createdAt: string, over: Partial<Attachment> = {}): Attachment {
  return {
    id,
    workspace_id: "ws-1",
    issue_id: "issue-1",
    comment_id: `c-${id}`,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "agent",
    uploader_id: "lambda",
    filename,
    url: `https://cdn.example.test/${id}.png`,
    download_url: `https://cdn.example.test/${id}.png`,
    markdown_url: `https://cdn.example.test/${id}.png`,
    content_type: "image/png",
    size_bytes: 2048,
    created_at: createdAt,
    ...over,
  };
}

function comment(a: Attachment): TimelineEntry {
  return {
    type: "comment",
    id: a.comment_id!,
    actor_type: "agent",
    actor_id: "lambda",
    created_at: a.created_at,
    content: "Done",
    attachments: [a],
  } as TimelineEntry;
}

// Newest first, as the deliverable lists are: c, b, a.
const A = shot("a", "a.png", "2026-09-20T10:00:00Z");
const B = shot("b", "b.png", "2026-09-21T10:00:00Z");
const C = shot("c", "c.png", "2026-09-22T10:00:00Z");

function setWide(wide: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: wide,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    onchange: null,
    dispatchEvent: () => false,
  }));
  window.matchMedia = globalThis.matchMedia;
}

function renderFocus(attachmentId: string, onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <DeliverableFocusModal onClose={onClose} data={{ issueId: "issue-1", attachmentId }} />
    </QueryClientProvider>,
  );
  return onClose;
}

beforeEach(() => {
  vi.clearAllMocks();
  setWide(true);
  listTimelineMock.mockResolvedValue([comment(A), comment(B), comment(C)]);
  taskSnapshotMock.mockResolvedValue([]);
});

describe("DeliverableFocusModal", () => {
  it("pages through the issue's deliverables from the file it opened on", async () => {
    renderFocus("b");

    expect(await screen.findByText("2 / 3")).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "b.png" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "b.png" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("3 / 3")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Previous" }));
    fireEvent.click(screen.getByRole("button", { name: "Previous" }));
    expect(await screen.findByText("1 / 3")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();
  });

  it("moves with the arrow keys unless a field has them", async () => {
    renderFocus("b");
    await screen.findByText("2 / 3");

    fireEvent.keyDown(document.body, { key: "ArrowRight" });
    expect(await screen.findByText("3 / 3")).toBeInTheDocument();

    fireEvent.keyDown(screen.getByLabelText("Reply"), { key: "ArrowLeft" });
    expect(screen.getByText("3 / 3")).toBeInTheDocument();
  });

  it("shows the issue's thread beside the file, and keeps its draft while paging", async () => {
    renderFocus("c");
    await screen.findByText("1 / 3");

    const thread = screen.getByTestId("thread");
    expect(thread).toHaveAttribute("data-issue", "issue-1");
    expect(thread).toHaveAttribute("data-variant", "thread");

    fireEvent.change(screen.getByLabelText("Reply"), { target: { value: "half a thought" } });
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    await screen.findByText("2 / 3");
    expect(screen.getByLabelText("Reply")).toHaveValue("half a thought");
  });

  it("tells the thread which file is on screen, and follows the paging", async () => {
    renderFocus("b");
    await screen.findByText("2 / 3");
    expect(screen.getByTestId("thread")).toHaveAttribute("data-reply-attachment", "b");
    expect(screen.getByTestId("thread")).toHaveAttribute("data-reply-filename", "b.png");

    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    await screen.findByText("3 / 3");
    expect(screen.getByTestId("thread")).toHaveAttribute("data-reply-attachment", "a");
    expect(screen.getByTestId("thread")).toHaveAttribute("data-reply-filename", "a.png");
  });

  it("collapses the thread from the top bar", async () => {
    renderFocus("c");
    await screen.findByText("1 / 3");

    fireEvent.click(screen.getByRole("button", { name: "Comments" }));
    expect(screen.queryByTestId("thread")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Comments" }));
    expect(screen.getByTestId("thread")).toBeInTheDocument();
  });

  it("starts with the thread collapsed where there is no room beside the file", async () => {
    setWide(false);
    renderFocus("c");
    await screen.findByText("1 / 3");
    expect(screen.queryByTestId("thread")).not.toBeInTheDocument();
  });

  it("closes on Escape and from the close button", async () => {
    const onClose = renderFocus("c");
    await screen.findByText("1 / 3");

    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(2));
  });

  it("says so when the file is no longer a deliverable of the issue", async () => {
    renderFocus("gone");
    expect(await screen.findByText("This deliverable is no longer available.")).toBeInTheDocument();
  });

  it("marks a re-uploaded file with its version", async () => {
    const B2 = shot("b2", "b.png", "2026-09-23T10:00:00Z");
    listTimelineMock.mockResolvedValue([comment(A), comment(B), comment(B2)]);
    renderFocus("b");
    // Two files, not three: b.png's second upload is its version, and the
    // requested upload is the one on screen.
    expect(await screen.findByText("1 / 2")).toBeInTheDocument();
    expect(screen.getByText("v1")).toBeInTheDocument();
  });

  describe("a document written section by section", () => {
    const outline = "# Report\n\n<!-- pending: Background -->\n\n<!-- pending: Findings -->\n\n<!-- pending: Next steps -->";
    const second = outline.replace("<!-- pending: Background -->", "Background, written.");

    function document(id: string, createdAt: string) {
      return shot(id, "report.md", createdAt, { content_type: "text/markdown" });
    }

    beforeEach(() => {
      listTimelineMock.mockResolvedValue([
        comment(document("r1", "2026-09-20T10:00:00Z")),
        comment(document("r2", "2026-09-20T10:05:00Z")),
      ]);
      textContentMock.mockImplementation(async (id: string) => ({
        text: id === "r1" ? outline : second,
        originalContentType: "text/markdown",
      }));
    });

    it("counts the written sections beside the name while the agent works", async () => {
      taskSnapshotMock.mockResolvedValue([
        { id: "t1", issue_id: "issue-1", status: "running", agent_id: "lambda" },
      ]);
      renderFocus("r2");

      expect(await screen.findByText("Writing · 1/3 sections")).toBeInTheDocument();
      expect(screen.getByText("Findings")).toBeInTheDocument();
    });

    it("says the document is unfinished once no agent run is left", async () => {
      renderFocus("r2");

      expect(await screen.findByText("Unfinished · 1/3")).toBeInTheDocument();
    });

    it("follows a new version as it lands and marks what it added", async () => {
      taskSnapshotMock.mockResolvedValue([
        { id: "t1", issue_id: "issue-1", status: "running", agent_id: "lambda" },
      ]);
      listTimelineMock.mockResolvedValue([comment(document("r1", "2026-09-20T10:00:00Z"))]);
      const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      renderWithI18n(
        <QueryClientProvider client={qc}>
          <DeliverableFocusModal onClose={vi.fn()} data={{ issueId: "issue-1", attachmentId: "r1" }} />
        </QueryClientProvider>,
      );
      expect(await screen.findByText("Writing · 0/3 sections")).toBeInTheDocument();
      expect(screen.queryByText("Claude")).not.toBeInTheDocument();

      listTimelineMock.mockResolvedValue([
        comment(document("r1", "2026-09-20T10:00:00Z")),
        comment(document("r2", "2026-09-20T10:05:00Z")),
      ]);
      await qc.invalidateQueries({ queryKey: issueTimelineOptions("issue-1").queryKey });

      expect(await screen.findByText("Writing · 1/3 sections")).toBeInTheDocument();
      expect(screen.getByText("v2")).toBeInTheDocument();
      expect(screen.getByText("Background, written.")).toBeInTheDocument();
      expect(screen.getAllByText("Claude")).toHaveLength(1);
    });

    it("shows no progress for a document without markers", async () => {
      textContentMock.mockResolvedValue({ text: "# Done\n\nAll written.", originalContentType: "text/markdown" });
      renderFocus("r2");

      expect(await screen.findByText("All written.")).toBeInTheDocument();
      expect(screen.queryByTestId("writing-progress")).not.toBeInTheDocument();
    });
  });
});
