import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Attachment, TimelineEntry } from "@multica/core/types";
import { useCommentDraftStore } from "@multica/core/issues/stores";
import { renderWithI18n } from "../../../test/i18n";

const { listTimelineMock, textMock } = vi.hoisted(() => ({
  listTimelineMock: vi.fn(),
  textMock: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listTimeline: listTimelineMock,
    getAttachmentTextContent: textMock,
    getAttachment: vi.fn(() => Promise.reject(new Error("no re-sign"))),
  },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: "user-1" } }),
}));
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
vi.mock("../../../navigation", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../navigation")>()),
  useNavigation: () => ({}),
}));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspaceSlug: () => "acme",
}));

// The thread beside the file is IssueDetail's business; its reply box is the
// ReplyAnnotations summary over the same `new:<issue>` draft.
vi.mock("../issue-detail", async () => {
  const { ReplyAnnotations } = await import("../reply-annotations");
  const { useCommentDraftStore } = await import("@multica/core/issues/stores");
  return {
    IssueDetail: ({ issueId, onEditAnnotationSource }: {
      issueId: string;
      onEditAnnotationSource?: (id: string) => boolean;
    }) => {
      const annotations = useCommentDraftStore((s) => s.getAnnotations(`new:${issueId}`));
      return (
        <div data-testid="thread">
          {annotations.length > 0 && (
            <ReplyAnnotations draftKey={`new:${issueId}`} annotations={annotations} disabled={false}
              onEditAnnotation={(id) => !!onEditAnnotationSource?.(id)} />
          )}
        </div>
      );
    },
  };
});

import { DeliverableFocusModal } from "./deliverable-focus";

function file(id: string, filename: string, contentType: string): Attachment {
  return {
    id, workspace_id: "ws-1", issue_id: "issue-1", comment_id: `c-${id}`,
    chat_session_id: null, chat_message_id: null, uploader_type: "agent", uploader_id: "lambda",
    filename, url: `https://cdn.example.test/${id}`, download_url: `https://cdn.example.test/${id}`,
    markdown_url: `https://cdn.example.test/${id}`, content_type: contentType, size_bytes: 2048,
    created_at: "2026-09-20T10:00:00Z",
  };
}
const comment = (a: Attachment) => ({
  type: "comment", id: a.comment_id!, actor_type: "agent", actor_id: "lambda",
  created_at: a.created_at, content: "Done", attachments: [a],
}) as TimelineEntry;

const REPORT = file("att-md", "report.md", "text/markdown");
const LOG = file("att-log", "run.log", "text/plain");
const SHOT = file("att-img", "shot.png", "image/png");

function renderFocus(attachmentId: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <DeliverableFocusModal onClose={vi.fn()} data={{ issueId: "issue-1", attachmentId }} />
    </QueryClientProvider>,
  );
}

function selectIn(source: HTMLElement, from: string, to = from) {
  const walker = document.createTreeWalker(source, NodeFilter.SHOW_TEXT);
  let start: Text | null = null;
  let end: Text | null = null;
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (!start && node.textContent?.includes(from)) start = node as Text;
    if (node.textContent?.includes(to)) end = node as Text;
  }
  const range = document.createRange();
  range.setStart(start!, start!.textContent!.indexOf(from));
  range.setEnd(end!, end!.textContent!.indexOf(to) + to.length);
  fireEvent.pointerDown(source);
  act(() => {
    window.getSelection()!.removeAllRanges();
    window.getSelection()!.addRange(range);
  });
  fireEvent.pointerUp(source);
}

beforeEach(() => {
  vi.clearAllMocks();
  useCommentDraftStore.setState({ drafts: {} });
  window.matchMedia = ((query: string) => ({
    matches: true, media: query, addEventListener: () => {}, removeEventListener: () => {},
    addListener: () => {}, removeListener: () => {}, onchange: null, dispatchEvent: () => false,
  })) as typeof window.matchMedia;
  Object.defineProperty(document.documentElement, "clientWidth", { configurable: true, value: 1024 });
  Object.defineProperty(document.documentElement, "clientHeight", { configurable: true, value: 768 });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(10, 10, 600, 400));
  Range.prototype.getBoundingClientRect = vi.fn(() => new DOMRect(10, 10, 120, 20));
  Range.prototype.getClientRects = vi.fn(() => [] as unknown as DOMRectList);
  listTimelineMock.mockResolvedValue([comment(REPORT), comment(LOG), comment(SHOT)]);
  textMock.mockImplementation(async (id: string) => ({
    text: id === "att-md" ? "# Plan\n\nShip the **beta** on Friday.\n" : "line one\nline two\nline three\n",
    originalContentType: "text/plain",
  }));
});

describe("annotating a deliverable in the focus view", () => {
  it("adds a selection of a Markdown file to the thread's reply draft, sourced to the file", async () => {
    renderFocus("att-md");
    const source = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-comment-content="attachment:att-md"]');
      expect(el).toHaveTextContent("Ship the beta on Friday.");
      return el!;
    });

    selectIn(source, "Ship the");
    fireEvent.click(await screen.findByRole("button", { name: "Annotate" }));
    const note = await screen.findByRole("textbox", { name: "Comment" });
    // Inside the dialog's tree, or its focus trap rejects the note field.
    expect(note.closest('[role="dialog"]')).not.toBeNull();
    fireEvent.change(note, { target: { value: "Which Friday?" } });
    fireEvent.click(screen.getByRole("button", { name: "Add annotation" }));

    const [saved] = useCommentDraftStore.getState().getAnnotations("new:issue-1");
    expect(saved).toMatchObject({
      sourceCommentId: "attachment:att-md",
      sourceActorName: "report.md",
      source: { type: "attachment", id: "att-md", filename: "report.md" },
      quote: "Ship the",
      note: "Which Friday?",
    });
  });

  it("keeps line breaks when the selection spans lines of a text file", async () => {
    renderFocus("att-log");
    const source = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-comment-content="attachment:att-log"]');
      expect(el).toHaveTextContent("line three");
      return el!;
    });

    selectIn(source, "line one", "line two");
    fireEvent.click(await screen.findByRole("button", { name: "Annotate" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Comment" }), { target: { value: "Merge these" } });
    fireEvent.click(screen.getByRole("button", { name: "Add annotation" }));

    expect(useCommentDraftStore.getState().getAnnotations("new:issue-1")[0]?.quote).toBe("line one\nline two");
  });

  it("offers no annotation on an image", async () => {
    renderFocus("att-img");
    await screen.findByRole("img", { name: "shot.png" });
    expect(document.querySelector("[data-comment-content]")).toBeNull();

    // Even the text that is on screen (the file name) is not a quotable source.
    const bar = screen.getByRole("dialog");
    selectIn(bar, "shot.png");
    expect(screen.queryByRole("button", { name: "Annotate" })).not.toBeInTheDocument();
  });

  it("returns a file chip to its quote in the document", async () => {
    renderFocus("att-md");
    const source = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-comment-content="attachment:att-md"]');
      expect(el).toHaveTextContent("Ship the beta on Friday.");
      return el!;
    });
    selectIn(source, "Ship the");
    fireEvent.click(await screen.findByRole("button", { name: "Annotate" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Comment" }), { target: { value: "Which Friday?" } });
    fireEvent.click(screen.getByRole("button", { name: "Add annotation" }));

    fireEvent.pointerEnter(await screen.findByRole("button", { name: "1 annotation" }));
    fireEvent.click(await screen.findByRole("button", { name: "Edit annotation 1" }));
    expect(await screen.findByRole("textbox", { name: "Comment" })).toHaveValue("Which Friday?");
  });
});
