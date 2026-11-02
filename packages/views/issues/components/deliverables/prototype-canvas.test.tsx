import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { collectDeliverableFiles } from "@multica/core/attachments/deliverables";
import type { Attachment } from "@multica/core/types";
import { renderWithI18n } from "../../../test/i18n";

const { textContentMock, uploadFileMock, createCommentMock } = vi.hoisted(() => ({
  textContentMock: vi.fn(),
  uploadFileMock: vi.fn(),
  createCommentMock: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    getAttachmentTextContent: textContentMock,
    uploadFile: uploadFileMock,
    createComment: createCommentMock,
  },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

import { PrototypeCanvas } from "./prototype-canvas";

function upload(id: string, filename: string, contentType: string, createdAt: string): Attachment {
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
    url: `https://cdn.example.test/${id}`,
    download_url: `https://cdn.example.test/${id}`,
    markdown_url: `https://cdn.example.test/${id}`,
    content_type: contentType,
    size_bytes: 512,
    created_at: createdAt,
  };
}

const html = (id: string, filename: string, createdAt = "2026-09-20T10:00:00Z") =>
  upload(id, filename, "text/html", createdAt);
const canvasFile = (id: string, createdAt = "2026-09-20T10:00:00Z") =>
  upload(id, "canvas.json", "application/json", createdAt);

const CANVAS = {
  pages: [
    {
      id: "p1",
      name: "Flow",
      frames: [
        { id: "home", name: "Home", file: "home.html", x: 0, y: 0, w: 400, h: 300 },
        { id: "detail", name: "Detail", file: "detail.html", x: 500, y: 0, w: 400, h: 300, device: "phone" },
        { id: "gone", name: "Ghost", file: "missing.html", x: 1000, y: 0, w: 400, h: 300 },
      ],
    },
    { id: "p2", name: "Edge cases", frames: [{ id: "err", name: "Error", file: "home.html", x: 0, y: 0, w: 400, h: 300 }] },
  ],
};

const HOME = html("h-home", "home.html");
const DETAIL = html("h-detail", "detail.html");
const CANVAS_V1 = canvasFile("canvas-1");

function files(...attachments: Attachment[]) {
  return collectDeliverableFiles(
    attachments.map((a) => ({ id: a.comment_id!, type: "comment", attachments: [a] })),
  );
}

function renderCanvas(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrap = (node: ReactElement) => <QueryClientProvider client={qc}>{node}</QueryClientProvider>;
  const view = renderWithI18n(wrap(ui));
  // The focus swaps to a new version only once its body is cached; so do the tests.
  const preload = (id: string) =>
    qc.prefetchQuery({ queryKey: ["attachment-content", id], queryFn: () => textContentMock(id) });
  return { ...view, preload, rerenderCanvas: (next: ReactElement) => view.rerender(wrap(next)) };
}

function canvasElement(
  list: ReturnType<typeof files>,
  over: Partial<{ attachment: Attachment; latestId: string; onPlay: (id: string) => void }> = {},
) {
  return (
    <PrototypeCanvas
      issueId="issue-1"
      attachment={over.attachment ?? CANVAS_V1}
      latestId={over.latestId ?? CANVAS_V1.id}
      files={list}
      onPlay={over.onPlay ?? vi.fn()}
    />
  );
}

function readTransform() {
  const style = screen.getByTestId("canvas-layer").style.transform;
  const match = /translate\((-?[\d.]+)px, (-?[\d.]+)px\) scale\(([\d.]+)\)/.exec(style);
  if (!match) throw new Error(`unexpected transform: ${style}`);
  return { x: Number(match[1]), y: Number(match[2]), scale: Number(match[3]) };
}

function frame(id: string) {
  return document.querySelector<HTMLElement>(`[data-frame-id="${id}"]`)!;
}

const widthSpy = vi.spyOn(HTMLElement.prototype, "clientWidth", "get");
const heightSpy = vi.spyOn(HTMLElement.prototype, "clientHeight", "get");

beforeEach(() => {
  vi.clearAllMocks();
  widthSpy.mockReturnValue(1000);
  heightSpy.mockReturnValue(600);
  textContentMock.mockImplementation(async (id: string) => ({
    text: id.startsWith("canvas") ? JSON.stringify(CANVAS) : `<html><body>${id}</body></html>`,
    originalContentType: id.startsWith("canvas") ? "application/json" : "text/html",
  }));
});

afterEach(() => {
  widthSpy.mockReset();
  heightSpy.mockReset();
});

describe("PrototypeCanvas", () => {
  it("lays the frames out at their positions, labelled, in sandboxed iframes", async () => {
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));

    expect(await screen.findByTitle("Home")).toHaveAttribute("sandbox", "allow-scripts");
    expect(screen.getByTitle("Detail")).toBeInTheDocument();

    expect(frame("home").style).toMatchObject({ left: "0px", top: "0px", width: "400px", height: "300px" });
    expect(frame("detail").style).toMatchObject({ left: "500px", top: "0px", width: "400px", height: "300px" });
    expect(frame("gone").style).toMatchObject({ left: "1000px", top: "0px" });
    expect(within(frame("home")).getByText("Home")).toBeInTheDocument();
    expect(within(frame("detail")).getByText("Detail")).toBeInTheDocument();
    expect(screen.getByTestId("canvas-layer").style.transform).toMatch(/^translate\(.*\) scale\(/);
  });

  it("fits every frame of the page on first show", async () => {
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
    await screen.findByTitle("Home");

    // Bounds 1400 × 300 into 1000 × 600 with 48px around.
    const { scale, x } = readTransform();
    expect(scale).toBeCloseTo(904 / 1400, 3);
    expect(x).toBeCloseTo(48, 3);
  });

  it("says so inside a frame whose file is missing, without failing the canvas", async () => {
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
    await screen.findByTitle("Home");

    expect(within(frame("gone")).getByText("File not found: missing.html")).toBeInTheDocument();
    expect(within(frame("gone")).queryByRole("button")).not.toBeInTheDocument();
  });

  it("zooms around the cursor on wheel", async () => {
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
    await screen.findByTitle("Home");
    const before = readTransform();

    fireEvent.wheel(screen.getByTestId("prototype-canvas"), { deltaY: -200, clientX: 300, clientY: 200 });

    const after = readTransform();
    expect(after.scale).toBeGreaterThan(before.scale);
    // The canvas point under the cursor stays under it.
    expect((300 - before.x) / before.scale).toBeCloseTo((300 - after.x) / after.scale, 3);
    expect((200 - before.y) / before.scale).toBeCloseTo((200 - after.y) / after.scale, 3);
  });

  it("pans when the background is dragged", async () => {
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
    await screen.findByTitle("Home");
    const before = readTransform();
    const root = screen.getByTestId("prototype-canvas");

    fireEvent.pointerDown(root, { pointerId: 1, button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(root, { pointerId: 1, clientX: 140, clientY: 130 });
    fireEvent.pointerUp(root, { pointerId: 1 });

    const after = readTransform();
    expect(after.x - before.x).toBeCloseTo(40);
    expect(after.y - before.y).toBeCloseTo(30);
    expect(after.scale).toBe(before.scale);
  });

  it("fits all frames again from the control after the view has moved", async () => {
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
    await screen.findByTitle("Home");
    const fitted = readTransform();

    fireEvent.wheel(screen.getByTestId("prototype-canvas"), { deltaY: -300, clientX: 200, clientY: 200 });
    expect(readTransform()).not.toEqual(fitted);

    fireEvent.click(screen.getByRole("button", { name: "Fit all" }));
    expect(readTransform()).toEqual(fitted);
  });

  it("centers a frame from the Prototypes list", async () => {
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
    await screen.findByTitle("Home");
    const before = readTransform();

    const list = screen.getByRole("region", { name: "Prototypes" });
    expect(within(list).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["Home", "Detail", "Ghost"]);
    fireEvent.click(within(list).getByRole("button", { name: "Detail" }));

    const after = readTransform();
    expect(after.scale).toBe(before.scale);
    expect(after.x + (500 + 200) * after.scale).toBeCloseTo(500);
    expect(after.y + (0 + 150) * after.scale).toBeCloseTo(300);
  });

  it("plays a frame's HTML deliverable from the list and from its label", async () => {
    const onPlay = vi.fn();
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1), { onPlay }));
    await screen.findByTitle("Home");

    const list = screen.getByRole("region", { name: "Prototypes" });
    fireEvent.click(within(list).getByRole("button", { name: "Play Detail" }));
    expect(onPlay).toHaveBeenLastCalledWith("h-detail");

    fireEvent.click(within(frame("home")).getByRole("button", { name: "Play Home" }));
    expect(onPlay).toHaveBeenLastCalledWith("h-home");
    // A file that is not there has nothing to play.
    expect(within(list).queryByRole("button", { name: "Play Ghost" })).not.toBeInTheDocument();
  });

  it("switches pages when there are several", async () => {
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
    await screen.findByTitle("Home");

    fireEvent.change(screen.getByRole("combobox", { name: "Page" }), { target: { value: "p2" } });

    expect(screen.getAllByTestId("canvas-frame")).toHaveLength(1);
    expect(frame("err")).toBeInTheDocument();
    const list = screen.getByRole("region", { name: "Prototypes" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(1);
  });

  it("offers no page switcher for a single page", async () => {
    textContentMock.mockImplementation(async (id: string) => ({
      text: id.startsWith("canvas") ? JSON.stringify({ pages: [CANVAS.pages[0]] }) : "<p>x</p>",
      originalContentType: "text/plain",
    }));
    renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
    await screen.findByTitle("Home");
    expect(screen.queryByRole("combobox", { name: "Page" })).not.toBeInTheDocument();
  });

  it("says so when the file is not a canvas", async () => {
    textContentMock.mockImplementation(async () => ({ text: "not json", originalContentType: "application/json" }));
    renderCanvas(canvasElement(files(HOME, CANVAS_V1)));
    expect(await screen.findByText("This canvas can't be read")).toBeInTheDocument();
  });

  describe("moving frames and saving the layout", () => {
    function dragHome(dx: number, dy: number) {
      const home = frame("home");
      fireEvent.pointerDown(home, { pointerId: 2, button: 0, clientX: 100, clientY: 100 });
      fireEvent.pointerMove(home, { pointerId: 2, clientX: 100 + dx, clientY: 100 + dy });
      fireEvent.pointerUp(home, { pointerId: 2 });
    }

    it("offers Save layout only once a frame has moved", async () => {
      renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
      await screen.findByTitle("Home");
      expect(screen.queryByRole("button", { name: "Save layout" })).not.toBeInTheDocument();

      dragHome(60, 30);
      expect(screen.getByRole("button", { name: "Save layout" })).toBeInTheDocument();
    });

    it("moves the frame by the drag, in canvas units", async () => {
      renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
      await screen.findByTitle("Home");
      const { scale } = readTransform();

      dragHome(60, 30);

      expect(frame("home").style.left).toBe(`${Math.round(60 / scale)}px`);
      expect(frame("home").style.top).toBe(`${Math.round(30 / scale)}px`);
      expect(frame("detail").style.left).toBe("500px");
    });

    it("does not move a frame, or offer saving, for a plain click", async () => {
      renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
      await screen.findByTitle("Home");

      dragHome(1, 1);

      expect(frame("home").style.left).toBe("0px");
      expect(screen.queryByRole("button", { name: "Save layout" })).not.toBeInTheDocument();
    });

    it("keeps an older version read-only", async () => {
      const v2 = canvasFile("canvas-2", "2026-09-21T10:00:00Z");
      renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1, v2), { latestId: v2.id }));
      await screen.findByTitle("Home");

      dragHome(60, 30);
      expect(frame("home").style.left).toBe("0px");
    });

    it("uploads a new version with only that frame's x and y changed, then posts it", async () => {
      uploadFileMock.mockResolvedValue({ ...canvasFile("canvas-new"), id: "canvas-new" });
      createCommentMock.mockResolvedValue({
        id: "comment-new",
        author_type: "member",
        author_id: "user-1",
        content: "",
        created_at: "2026-09-22T10:00:00Z",
        updated_at: "2026-09-22T10:00:00Z",
        attachments: [],
      });
      renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
      await screen.findByTitle("Home");
      const { scale } = readTransform();

      dragHome(60, 30);
      fireEvent.click(screen.getByRole("button", { name: "Save layout" }));

      await waitFor(() => expect(createCommentMock).toHaveBeenCalledTimes(1));

      expect(uploadFileMock).toHaveBeenCalledTimes(1);
      const [file, opts] = uploadFileMock.mock.calls[0]!;
      expect(file.name).toBe("canvas.json");
      expect(file.type).toBe("application/json");
      expect(opts).toMatchObject({ issueId: "issue-1" });
      const saved = JSON.parse(await file.text());
      const expected = structuredClone(CANVAS);
      expected.pages[0]!.frames[0]!.x = Math.round(60 / scale);
      expected.pages[0]!.frames[0]!.y = Math.round(30 / scale);
      expect(saved).toEqual(expected);

      expect(createCommentMock).toHaveBeenCalledWith(
        "issue-1",
        expect.stringMatching(/^\/note\n\n!file\[canvas\.json\]\(\/api\/attachments\/canvas-new\/download\)$/),
        undefined,
        undefined,
        ["canvas-new"],
        undefined,
        undefined,
      );
      // Saved: nothing left to save.
      await waitFor(() => expect(screen.queryByRole("button", { name: "Save layout" })).not.toBeInTheDocument());
      // The moved frame stays where it was put until the new version arrives.
      expect(frame("home").style.left).toBe(`${Math.round(60 / scale)}px`);
    });

    it("shows pending state while saving", async () => {
      let release: (value: unknown) => void = () => {};
      uploadFileMock.mockReturnValue(new Promise((resolve) => { release = resolve; }));
      renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
      await screen.findByTitle("Home");

      dragHome(60, 30);
      fireEvent.click(screen.getByRole("button", { name: "Save layout" }));

      const saving = await screen.findByRole("button", { name: "Saving…" });
      expect(saving).toBeDisabled();
      expect(saving).toHaveAttribute("aria-busy", "true");
      await act(async () => {
        release(canvasFile("canvas-new"));
      });
    });

    it("keeps the moved layout and the button when the post fails, and retries without a second upload", async () => {
      uploadFileMock.mockResolvedValue(canvasFile("canvas-new"));
      createCommentMock.mockRejectedValueOnce(new Error("boom")).mockResolvedValue({
        id: "comment-new",
        author_type: "member",
        author_id: "user-1",
        content: "",
        created_at: "2026-09-22T10:00:00Z",
        updated_at: "2026-09-22T10:00:00Z",
        attachments: [],
      });
      renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
      await screen.findByTitle("Home");
      const { scale } = readTransform();

      dragHome(60, 30);
      fireEvent.click(screen.getByRole("button", { name: "Save layout" }));

      await waitFor(() => expect(createCommentMock).toHaveBeenCalledTimes(1));
      const retry = await screen.findByRole("button", { name: "Save layout" });
      await waitFor(() => expect(retry).not.toBeDisabled());
      expect(frame("home").style.left).toBe(`${Math.round(60 / scale)}px`);

      fireEvent.click(retry);
      await waitFor(() => expect(createCommentMock).toHaveBeenCalledTimes(2));
      expect(uploadFileMock).toHaveBeenCalledTimes(1);
    });
  });

  describe("when a new version arrives", () => {
    it("remounts only the frames whose HTML attachment changed", async () => {
      const { rerenderCanvas, preload } = renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
      const homeFrame = await screen.findByTitle("Home");
      const detailFrame = await screen.findByTitle("Detail");

      const HOME_V2 = html("h-home-2", "home.html", "2026-09-21T10:00:00Z");
      const CANVAS_V2 = canvasFile("canvas-2", "2026-09-21T10:00:00Z");
      await preload(CANVAS_V2.id);
      rerenderCanvas(
        canvasElement(files(HOME, DETAIL, HOME_V2, CANVAS_V1, CANVAS_V2), {
          attachment: CANVAS_V2,
          latestId: CANVAS_V2.id,
        }),
      );

      await waitFor(() => expect(screen.getByTitle("Home")).not.toBe(homeFrame));
      expect(homeFrame).not.toBeInTheDocument();
      expect(screen.getByTitle("Home")).toHaveAttribute("srcdoc", expect.stringContaining("h-home-2"));
      // The untouched frame keeps its element, and so its page state.
      expect(screen.getByTitle("Detail")).toBe(detailFrame);
    });

    it("keeps the reader's view and unsaved moves across the swap", async () => {
      const { rerenderCanvas, preload } = renderCanvas(canvasElement(files(HOME, DETAIL, CANVAS_V1)));
      await screen.findByTitle("Home");
      const home = frame("home");
      fireEvent.pointerDown(home, { pointerId: 2, button: 0, clientX: 100, clientY: 100 });
      fireEvent.pointerMove(home, { pointerId: 2, clientX: 160, clientY: 130 });
      fireEvent.pointerUp(home, { pointerId: 2 });
      fireEvent.wheel(screen.getByTestId("prototype-canvas"), { deltaY: -300, clientX: 200, clientY: 200 });
      const view = readTransform();
      const left = frame("home").style.left;

      const CANVAS_V2 = canvasFile("canvas-2", "2026-09-21T10:00:00Z");
      await preload(CANVAS_V2.id);
      rerenderCanvas(
        canvasElement(files(HOME, DETAIL, CANVAS_V1, CANVAS_V2), { attachment: CANVAS_V2, latestId: CANVAS_V2.id }),
      );
      await screen.findByTitle("Home");

      expect(readTransform()).toEqual(view);
      expect(frame("home").style.left).toBe(left);
      expect(screen.getByRole("button", { name: "Save layout" })).toBeInTheDocument();
    });
  });
});
