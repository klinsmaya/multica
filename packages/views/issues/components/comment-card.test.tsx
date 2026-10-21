import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { forwardRef, useState, type ReactElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const { getAttachmentTextContentMock } = vi.hoisted(() => ({
  getAttachmentTextContentMock: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    getAttachmentTextContent: getAttachmentTextContentMock,
    getAttachment: vi.fn(),
  },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));

// The attachment viewer reads useNavigation() + useWorkspaceSlug() for its
// Open-in-new-tab button. Mock both so these tests do not need the
// surrounding NavigationProvider / WorkspaceSlugProvider tree.
vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/issues",
    searchParams: new URLSearchParams(),
    hash: "",
    openInNewTab: vi.fn(),
    getShareableUrl: (p: string) => `https://app.example${p}`,
  }),
}));

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useWorkspaceSlug: () => "acme",
  };
});

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Ada" }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

// Only the editor surface CommentCard mounts is stubbed; the attachment
// components under test below stay real.
vi.mock("../../editor", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../editor")>()),
  useEditorUpload: () => ({ uploadWithToast: vi.fn(), upload: vi.fn(), uploading: false }),
  useFileDropZone: () => ({ isDragOver: false, dropZoneProps: {} }),
  FileDropOverlay: () => null,
  ReadonlyContent: ({ content }: { content: string }) => <div>{content}</div>,
  ContentEditor: forwardRef(function MockContentEditor() {
    return <textarea data-testid="editor" />;
  }),
}));

vi.mock("../hooks/use-comment-trigger-preview", () => ({
  useCommentTriggerPreview: () => ({ agents: [], blocked: [] }),
}));

import type { TimelineEntry } from "@multica/core/types";
import { collectDeliverableFiles } from "@multica/core/attachments/deliverables";
import { renderWithI18n } from "../../test/i18n";
import { AttachmentList, CommentCard } from "./comment-card";
import { ResolvedThreadBar } from "./resolved-thread-bar";
import { AttachmentVersionsProvider } from "./deliverables/attachment-versions";

function renderWithQuery(ui: ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

beforeEach(() => vi.clearAllMocks());
afterEach(() => vi.restoreAllMocks());

describe("AttachmentList — inline attachment filtering", () => {
  it("does not render a bottom attachment row when the body already has the stable file-card URL", () => {
    const id = "11111111-2222-3333-4444-555555555555";
    const href = `/api/attachments/${id}/download`;
    const attachment = {
      id,
      url: "/uploads/report.pdf",
      filename: "report.pdf",
      content_type: "application/pdf",
      size_bytes: 1024,
    } as any;

    const { container } = renderWithQuery(
      <AttachmentList
        attachments={[attachment]}
        content={`!file[report.pdf](${href})`}
      />,
    );

    expect(screen.queryByText("report.pdf")).toBeNull();
    expect(container.firstChild).toBeNull();
  });

  it("does not render a bottom attachment row when the body already has the response download_url", () => {
    const href = "https://cdn.example.test/report.pdf?Signature=stale";
    const attachment = {
      id: "11111111-2222-3333-4444-555555555555",
      url: "/uploads/report.pdf",
      download_url: "https://cdn.example.test/report.pdf?Signature=fresh",
      filename: "report.pdf",
      content_type: "application/pdf",
      size_bytes: 1024,
    } as any;

    const { container } = renderWithQuery(
      <AttachmentList
        attachments={[attachment]}
        content={`!file[report.pdf](${href})`}
      />,
    );

    expect(screen.queryByText("report.pdf")).toBeNull();
    expect(container.firstChild).toBeNull();
  });
});

describe("AttachmentList — layout (MUL-7649)", () => {
  const file = (id: string, filename: string, content_type: string) =>
    ({
      id,
      url: `/uploads/${filename}`,
      download_url: `/uploads/${filename}`,
      filename,
      content_type,
      size_bytes: 2048,
      created_at: "2026-09-20T10:00:00Z",
    }) as any;

  function renderList(ui: ReactElement) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    return renderWithI18n(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
  }

  it("shows files as cards, not full-width rows", () => {
    renderList(
      <AttachmentList
        attachments={[file("a", "notes.md", "text/markdown"), file("b", "data.csv", "text/csv")]}
        content=""
      />,
    );
    expect(screen.getByRole("button", { name: "notes.md" })).toBeTruthy();
    expect(screen.getByText("MD · 2 KB")).toBeTruthy();
    expect(screen.getByText("CSV · 2 KB")).toBeTruthy();
    // The row form's Eye button is gone — the whole card opens the file.
    expect(screen.queryByTitle("Preview")).toBeNull();
  });

  // MUL-7736: several screenshots used to shrink into one row of thumbnails,
  // so reading any of them took a click. Each keeps its full size instead.
  it("shows every image at full size, one under another", () => {
    const { container } = renderList(
      <AttachmentList
        attachments={[
          file("a", "a.png", "image/png"),
          file("b", "b.png", "image/png"),
          file("c", "c.png", "image/png"),
        ]}
        content=""
      />,
    );
    const figures = container.querySelectorAll(".image-figure");
    expect(figures).toHaveLength(3);
    expect([...container.querySelectorAll("img")].map((img) => img.getAttribute("alt"))).toEqual([
      "a.png",
      "b.png",
      "c.png",
    ]);
    // No tile box sized to a shared row height around any of them.
    for (const figure of figures) expect(figure).toHaveClass("image-standalone");
    expect(container.querySelector("[style*='height']")).toBeNull();
  });

  it("lays groups out images, then files, whatever the upload order", () => {
    const { container } = renderList(
      <AttachmentList
        attachments={[
          file("a", "notes.md", "text/markdown"),
          file("b", "a.png", "image/png"),
          file("c", "b.png", "image/png"),
        ]}
        content=""
      />,
    );
    const image = container.querySelector(".image-figure")!;
    const card = screen.getByRole("button", { name: "notes.md" });
    expect(image.compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  // Reverses the MUL-2330 pin. An uploaded HTML file is a deliverable, not
  // part of the text: it is a card that opens the viewer, and its contents are
  // not fetched to embed. HTML meant to be read in place is a ```html block.
  it("shows a standalone HTML file as a card, not an embedded preview", () => {
    renderList(
      <AttachmentList
        attachments={[file("h", "report.html", "text/html"), file("m", "notes.md", "text/markdown")]}
        content=""
      />,
    );
    expect(screen.getByRole("button", { name: "report.html" })).toBeTruthy();
    expect(screen.getByText("HTML · 2 KB")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
    expect(getAttachmentTextContentMock).not.toHaveBeenCalled();
  });

  it("marks a re-uploaded file with its version on the issue page", () => {
    const v1 = { ...file("v1", "report.md", "text/markdown"), comment_id: "c1" };
    const v2 = { ...file("v2", "report.md", "text/markdown"), comment_id: "c2", created_at: "2026-09-21T10:00:00Z" };
    const files = collectDeliverableFiles([
      { id: "c1", attachments: [v1] },
      { id: "c2", attachments: [v2] },
    ]);
    renderList(
      <AttachmentVersionsProvider files={files}>
        <AttachmentList attachments={[v2]} content="" />
      </AttachmentVersionsProvider>,
    );
    expect(screen.getByText("v2")).toBeTruthy();
  });
});

// A folded resolved thread shows a bar, not its rows: mounting every hidden
// reply made long resolved threads expensive to scroll past.
describe("folded resolved threads", () => {
  const REPLY_COUNT = 20;

  function entry(id: string, parentId: string | null, extra: Partial<TimelineEntry> = {}): TimelineEntry {
    return {
      type: "comment",
      id,
      actor_type: "member",
      actor_id: "user-1",
      actor_name: "Ada",
      content: `body ${id}`,
      parent_id: parentId,
      comment_type: "comment",
      reactions: [],
      attachments: [],
      created_at: "2026-09-11T07:00:00Z",
      updated_at: "2026-09-11T07:00:00Z",
      revision: 1,
      ...extra,
    } as TimelineEntry;
  }

  const replyBodies = Array.from({ length: REPLY_COUNT }, (_, i) => `folded reply ${i + 1}`);
  const replies = replyBodies.map((content, i) => entry(`r${i + 1}`, "root", { content }));

  const expectNoReplyBodies = () => {
    for (const body of replyBodies) expect(screen.queryByText(body)).toBeNull();
  };

  it("mounts no reply rows while the root-resolved thread sits behind its bar", () => {
    const root = entry("root", null, { content: "root body", resolved_at: "2026-09-11T09:00:00Z" });
    const onExpand = vi.fn();
    renderWithI18n(<ResolvedThreadBar entry={root} replies={replies} onExpand={onExpand} />);

    expect(screen.getByText(`${REPLY_COUNT + 1} resolved comments from Ada`)).toBeTruthy();
    expect(screen.queryByText("root body")).toBeNull();
    expectNoReplyBodies();
  });

  it("mounts no folded reply rows until a reply-resolved thread is expanded", () => {
    const resolution = entry("resolution", "root", {
      content: "resolution body",
      resolved_at: "2026-09-11T09:00:00Z",
    });

    function Thread() {
      const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
      return (
        <CommentCard
          issueId="issue-1"
          entry={entry("root", null, { content: "root body" })}
          replies={[...replies, resolution]}
          currentUserId="user-1"
          onReply={vi.fn().mockResolvedValue(true)}
          onEdit={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
          onToggleReaction={vi.fn()}
          expandedResolvedIds={expanded}
          onResolvedExpandChange={(rootId, expand) =>
            setExpanded((prev) => {
              const next = new Set(prev);
              if (expand) next.add(rootId);
              else next.delete(rootId);
              return next;
            })
          }
        />
      );
    }
    const { container } = renderWithI18n(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Thread />
      </QueryClientProvider>,
    );

    // Folded: root and resolution stay, the other replies are only counted.
    expect(screen.getByText("root body")).toBeTruthy();
    expect(screen.getByText("resolution body")).toBeTruthy();
    expect(screen.getByText(`${REPLY_COUNT} comments from Ada`)).toBeTruthy();
    expectNoReplyBodies();
    expect(container.querySelectorAll("[data-comment-block]")).toHaveLength(2);

    fireEvent.click(screen.getByText(`${REPLY_COUNT} comments from Ada`));

    for (const body of replyBodies) expect(screen.getByText(body)).toBeTruthy();
    expect(container.querySelectorAll("[data-comment-block]")).toHaveLength(REPLY_COUNT + 2);
  });
});
