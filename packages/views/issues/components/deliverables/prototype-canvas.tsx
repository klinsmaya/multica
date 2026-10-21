"use client";

import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react";
import { Loader2, Maximize, Play } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import type { DeliverableFile } from "@multica/core/attachments/deliverables";
import {
  centerOnFrame,
  fitAllTransform,
  moveFrame,
  parsePrototypeCanvas,
  zoomAround,
  type CanvasFrame,
  type CanvasTransform,
} from "@multica/core/attachments/prototype-canvas";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { composeDeliverableReply } from "@multica/core/issues/deliverable-reply";
import { useCreateCommentOnIssue } from "@multica/core/issues/mutations";
import type { Attachment } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../../i18n";
import { CodeBlockIframe } from "../../../editor/code-block-iframe";
import { useAttachmentHtmlText } from "../../../editor/hooks/use-attachment-html-text";
import { HtmlViewportFrame } from "../../../editor/html-viewport-frame";
import { getPreviewKind } from "../../../editor/utils/preview";
import { withFragmentNavShim } from "../../../editor/utils/iframe-fragment-nav";

// Wheel deltas to zoom: a pinch arrives as a ctrl-wheel with small deltas.
const WHEEL_ZOOM = 0.0015;
const PINCH_ZOOM = 0.01;
// Screen px a pointer must travel before a press on a frame is a drag.
const DRAG_THRESHOLD = 3;
const GRID = 24;

interface Moves {
  [key: string]: { pageId: string; frameId: string; x: number; y: number };
}

function moveKey(pageId: string, frameId: string): string {
  return `${pageId}\u0000${frameId}`;
}

function isTypingTarget(target: EventTarget | null): boolean {
  return (
    target instanceof Element &&
    !!target.closest('input, textarea, select, button, [contenteditable="true"]')
  );
}

/** The latest HTML deliverable of the issue named `filename`, if any. */
function resolveHtmlFile(
  files: ReadonlyArray<DeliverableFile>,
  filename: string,
): Attachment | undefined {
  const wanted = filename.trim();
  return files.find(
    (file) =>
      file.latest.filename.trim() === wanted &&
      getPreviewKind(file.latest.content_type, file.latest.filename) === "html",
  )?.latest;
}

/**
 * The stage of a `canvas.json` deliverable: an infinite pan / zoom canvas of
 * the issue's HTML deliverables (see `@multica/core/attachments/prototype-canvas`).
 * Frames can be moved; the moved layout is saved as a new version of the
 * canvas file, the way an agent would re-upload it.
 */
export function PrototypeCanvas({
  issueId,
  attachment,
  latestId,
  files,
  onPlay,
}: {
  issueId: string;
  /** The canvas version on screen. */
  attachment: Attachment;
  /** The canvas file's newest version; older ones are read-only. */
  latestId: string;
  files: ReadonlyArray<DeliverableFile>;
  /** Opens an HTML deliverable full screen. */
  onPlay: (attachmentId: string) => void;
}) {
  const { t } = useT("issues");
  const textQuery = useAttachmentHtmlText(attachment.id);
  const text = textQuery.data?.text;
  const canvas = useMemo(() => (text === undefined ? null : parsePrototypeCanvas(text)), [text]);

  const [pageId, setPageId] = useState<string | null>(null);
  const page = canvas?.pages.find((p) => p.id === pageId) ?? canvas?.pages[0];

  // Local moves, applied over the file's own positions until saved.
  const [moves, setMoves] = useState<Moves>({});
  // Set once the layout is saved from this version; the moves stay on screen
  // until the saved version replaces it, so frames do not jump back.
  const [savedFrom, setSavedFrom] = useState<string | null>(null);
  const editable = attachment.id === latestId;
  const dirty = editable && savedFrom === null && Object.keys(moves).length > 0;

  useEffect(() => {
    if (savedFrom !== null && savedFrom !== attachment.id) {
      setMoves({});
      setSavedFrom(null);
    }
  }, [savedFrom, attachment.id]);

  const frames = useMemo<CanvasFrame[]>(
    () =>
      (page?.frames ?? []).map((frame) => {
        const moved = page ? moves[moveKey(page.id, frame.id)] : undefined;
        return moved ? { ...frame, x: moved.x, y: moved.y } : frame;
      }),
    [page, moves],
  );

  // Viewport and transform.
  const rootRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  useLayoutEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const measure = () => {
      const width = root.clientWidth;
      const height = root.clientHeight;
      setSize((prev) => (prev.width === width && prev.height === height ? prev : { width, height }));
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(root);
    return () => observer.disconnect();
  }, []);

  const [transform, setTransform] = useState<CanvasTransform>({ x: 0, y: 0, scale: 1 });
  const transformRef = useRef(transform);
  useEffect(() => {
    transformRef.current = transform;
  }, [transform]);

  // Fit a page once when it first has both frames and a viewport; later
  // versions of the file keep the reader's own view.
  const fittedPage = useRef<string | null>(null);
  useEffect(() => {
    if (!page || size.width <= 0 || size.height <= 0 || fittedPage.current === page.id) return;
    fittedPage.current = page.id;
    setTransform(fitAllTransform(frames, size));
  }, [page, frames, size]);

  const fitAll = () => setTransform(fitAllTransform(frames, size));
  const centerFrame = (frame: CanvasFrame) =>
    setTransform((prev) => centerOnFrame(frame, size, prev.scale));

  // Wheel zooms around the cursor. A native listener: React's wheel handler
  // is passive, so it could not stop the page behind from scrolling.
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const onWheel = (e: WheelEvent) => {
      if (e.target instanceof Element && e.target.closest("[data-canvas-control]")) return;
      e.preventDefault();
      const rect = root.getBoundingClientRect();
      const anchor = { x: e.clientX - rect.left, y: e.clientY - rect.top };
      const delta = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
      const rate = e.ctrlKey ? PINCH_ZOOM : WHEEL_ZOOM;
      setTransform((prev) => zoomAround(prev, prev.scale * Math.exp(-delta * rate), anchor));
    };
    root.addEventListener("wheel", onWheel, { passive: false });
    return () => root.removeEventListener("wheel", onWheel);
  }, []);

  // Space + drag pans even from on top of a frame.
  const spaceRef = useRef(false);
  const [spaceDown, setSpaceDown] = useState(false);
  useEffect(() => {
    const down = (e: KeyboardEvent) => {
      if (e.code !== "Space" || isTypingTarget(e.target)) return;
      e.preventDefault();
      spaceRef.current = true;
      setSpaceDown(true);
    };
    const up = (e: KeyboardEvent) => {
      if (e.code !== "Space") return;
      spaceRef.current = false;
      setSpaceDown(false);
    };
    window.addEventListener("keydown", down);
    window.addEventListener("keyup", up);
    return () => {
      window.removeEventListener("keydown", down);
      window.removeEventListener("keyup", up);
    };
  }, []);

  // Panning.
  const panRef = useRef<{ pointerId: number; x: number; y: number; origin: CanvasTransform } | null>(null);
  const [panning, setPanning] = useState(false);
  const onRootPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 && e.button !== 1) return;
    panRef.current = { pointerId: e.pointerId, x: e.clientX, y: e.clientY, origin: transformRef.current };
    capturePointer(e);
    setPanning(true);
  };
  const onRootPointerMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    const pan = panRef.current;
    if (!pan || pan.pointerId !== e.pointerId) return;
    setTransform({ ...pan.origin, x: pan.origin.x + e.clientX - pan.x, y: pan.origin.y + e.clientY - pan.y });
  };
  const endPan = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (panRef.current?.pointerId !== e.pointerId) return;
    panRef.current = null;
    setPanning(false);
  };

  // Moving a frame.
  const dragRef = useRef<{
    pointerId: number;
    x: number;
    y: number;
    frame: CanvasFrame;
    moved: boolean;
  } | null>(null);
  const pageIdForDrag = page?.id;
  const onFramePointerDown = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>, frame: CanvasFrame) => {
      if (!editable || spaceRef.current || e.button !== 0) return;
      e.stopPropagation();
      dragRef.current = { pointerId: e.pointerId, x: e.clientX, y: e.clientY, frame, moved: false };
      capturePointer(e);
    },
    [editable],
  );
  const onFramePointerMove = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      const drag = dragRef.current;
      if (!drag || drag.pointerId !== e.pointerId || !pageIdForDrag) return;
      const dx = e.clientX - drag.x;
      const dy = e.clientY - drag.y;
      if (!drag.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
      drag.moved = true;
      const { scale } = transformRef.current;
      const x = Math.round(drag.frame.x + dx / scale);
      const y = Math.round(drag.frame.y + dy / scale);
      setMoves((prev) => ({
        ...prev,
        [moveKey(pageIdForDrag, drag.frame.id)]: { pageId: pageIdForDrag, frameId: drag.frame.id, x, y },
      }));
      setSavedFrom(null);
    },
    [pageIdForDrag],
  );
  const onFramePointerEnd = useCallback((e: ReactPointerEvent<HTMLDivElement>) => {
    if (dragRef.current?.pointerId === e.pointerId) dragRef.current = null;
  }, []);

  // Saving the layout as a new version of the canvas file.
  const { upload } = useFileUpload(api);
  const createComment = useCreateCommentOnIssue();
  const [saving, setSaving] = useState(false);
  // An upload that succeeded but whose comment failed is reused by the retry
  // instead of leaving another orphan behind.
  const uploaded = useRef<{ json: string; attachment: Attachment } | null>(null);
  const saveLayout = async () => {
    if (text === undefined || saving) return;
    setSaving(true);
    try {
      // The file's own JSON, not the defaulted parse: only moved frames change.
      let next = JSON.parse(text) as Parameters<typeof moveFrame>[0];
      for (const move of Object.values(moves)) {
        next = moveFrame(next, move.pageId, move.frameId, move.x, move.y);
      }
      const json = `${JSON.stringify(next, null, 2)}\n`;
      let file = uploaded.current?.json === json ? uploaded.current.attachment : null;
      if (!file) {
        const body = new File([json], attachment.filename, { type: attachment.content_type });
        const result = await upload(body, { issueId });
        if (!result) throw new Error("upload returned nothing");
        file = result;
        uploaded.current = { json, attachment: result };
      }
      // `/note` keeps the post from waking an agent: it carries a file, not a request.
      await createComment.mutateAsync({
        issueId,
        content: composeDeliverableReply("/note", { attachmentId: file.id, filename: file.filename }),
        attachmentIds: [file.id],
      });
      uploaded.current = null;
      setSavedFrom(attachment.id);
    } catch {
      toast.error(t(($) => $.deliverables.canvas.save_failed));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      ref={rootRef}
      data-testid="prototype-canvas"
      className={cn(
        "dark absolute inset-0 touch-none select-none overflow-hidden rounded-lg bg-background/40",
        panning ? "cursor-grabbing" : spaceDown ? "cursor-grab" : "cursor-default",
      )}
      style={{
        backgroundImage: "radial-gradient(circle, var(--border) 1px, transparent 1px)",
        backgroundSize: `${GRID * transform.scale}px ${GRID * transform.scale}px`,
        backgroundPosition: `${transform.x}px ${transform.y}px`,
      }}
      onPointerDown={onRootPointerDown}
      onPointerMove={onRootPointerMove}
      onPointerUp={endPan}
      onPointerCancel={endPan}
    >
      {textQuery.isPending ? (
        <Centered>
          <Loader2 className="size-4 animate-spin" />
        </Centered>
      ) : !page ? (
        <Centered>{t(($) => $.deliverables.canvas.invalid)}</Centered>
      ) : (
        <div
          data-testid="canvas-layer"
          className="absolute left-0 top-0"
          style={{
            transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.scale})`,
            transformOrigin: "0 0",
          }}
        >
          {frames.map((frame) => {
            const html = resolveHtmlFile(files, frame.file);
            return (
              <CanvasFrameView
                key={`${page.id}:${frame.id}:${html?.id ?? "missing"}`}
                frame={frame}
                html={html}
                scale={transform.scale}
                draggable={editable}
                onPlay={onPlay}
                onPointerDown={onFramePointerDown}
                onPointerMove={onFramePointerMove}
                onPointerEnd={onFramePointerEnd}
              />
            );
          })}
        </div>
      )}

      {canvas && (
        <>
          <div
            data-canvas-control
            className="absolute left-3 top-3 flex items-center gap-2"
            onPointerDown={(e) => e.stopPropagation()}
          >
            <Button type="button" variant="secondary" size="sm" onClick={fitAll}>
              <Maximize />
              {t(($) => $.deliverables.canvas.fit_all)}
            </Button>
            <span className="min-w-10 text-caption tabular-nums text-muted-foreground" aria-hidden>
              {Math.round(transform.scale * 100)}%
            </span>
            {canvas.pages.length > 1 && (
              <select
                aria-label={t(($) => $.deliverables.canvas.page)}
                className="h-7 rounded-md border border-input bg-secondary px-2 text-label text-foreground"
                value={page?.id ?? ""}
                onChange={(e) => setPageId(e.target.value)}
              >
                {canvas.pages.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            )}
            {dirty && (
              <Button
                type="button"
                size="sm"
                disabled={saving}
                aria-busy={saving}
                onClick={() => void saveLayout()}
              >
                {saving && <Loader2 className="animate-spin" />}
                {saving ? t(($) => $.deliverables.canvas.saving) : t(($) => $.deliverables.canvas.save_layout)}
              </Button>
            )}
          </div>
          <section
            data-canvas-control
            aria-label={t(($) => $.deliverables.canvas.prototypes)}
            className="absolute right-3 top-3 flex max-h-[60%] w-56 flex-col overflow-hidden rounded-lg border border-border bg-background/90 shadow-sm"
            onPointerDown={(e) => e.stopPropagation()}
          >
            <h2 className="shrink-0 px-3 pb-1 pt-2 text-caption font-medium text-muted-foreground">
              {t(($) => $.deliverables.canvas.prototypes)}
            </h2>
            <ul className="min-h-0 overflow-y-auto px-1 pb-1">
              {frames.map((frame) => {
                const html = resolveHtmlFile(files, frame.file);
                return (
                  <li key={frame.id} className="flex items-center gap-1 rounded-md hover:bg-secondary">
                    <button
                      type="button"
                      className="min-w-0 flex-1 truncate px-2 py-1.5 text-left text-label text-foreground"
                      onClick={() => centerFrame(frame)}
                    >
                      {frame.name}
                    </button>
                    {html && <PlayButton name={frame.name} onClick={() => onPlay(html.id)} />}
                  </li>
                );
              })}
            </ul>
          </section>
        </>
      )}
    </div>
  );
}

function capturePointer(e: ReactPointerEvent<HTMLElement>) {
  try {
    e.currentTarget.setPointerCapture(e.pointerId);
  } catch {
    // The pointer is already gone, or the host cannot capture: the gesture
    // then simply ends with the element's own events.
  }
}

function Centered({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center text-body text-muted-foreground">
      {children}
    </div>
  );
}

function PlayButton({ name, onClick }: { name: string; onClick: () => void }) {
  const { t } = useT("issues");
  return (
    <button
      type="button"
      className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      title={t(($) => $.deliverables.canvas.play)}
      aria-label={t(($) => $.deliverables.canvas.play_named, { name })}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={onClick}
    >
      <Play className="size-3.5" />
    </button>
  );
}

const CanvasFrameView = memo(function CanvasFrameView({
  frame,
  html,
  scale,
  draggable,
  onPlay,
  onPointerDown,
  onPointerMove,
  onPointerEnd,
}: {
  frame: CanvasFrame;
  html: Attachment | undefined;
  scale: number;
  draggable: boolean;
  onPlay: (attachmentId: string) => void;
  onPointerDown: (e: ReactPointerEvent<HTMLDivElement>, frame: CanvasFrame) => void;
  onPointerMove: (e: ReactPointerEvent<HTMLDivElement>) => void;
  onPointerEnd: (e: ReactPointerEvent<HTMLDivElement>) => void;
}) {
  const { t } = useT("issues");
  return (
    <div
      data-testid="canvas-frame"
      data-frame-id={frame.id}
      className={cn("absolute", draggable && "cursor-move")}
      style={{ left: frame.x, top: frame.y, width: frame.w, height: frame.h }}
      onPointerDown={(e) => onPointerDown(e, frame)}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerEnd}
      onPointerCancel={onPointerEnd}
    >
      {/* Counter-scaled so the name stays readable however far out the
          reader zooms; it follows the frame's top-left corner. */}
      <div
        className="absolute bottom-full left-0 mb-1 flex items-center gap-1 whitespace-nowrap text-label text-muted-foreground"
        style={{ transform: `scale(${1 / scale})`, transformOrigin: "0 100%", maxWidth: frame.w * scale }}
      >
        <span className="truncate">{frame.name}</span>
        {html && <PlayButton name={frame.name} onClick={() => onPlay(html.id)} />}
      </div>
      <div className="h-full w-full overflow-hidden rounded-md bg-background ring-1 ring-border">
        {html ? (
          <FrameDocument frame={frame} attachmentId={html.id} />
        ) : (
          <div className="flex h-full items-center justify-center p-4 text-center text-body text-muted-foreground">
            {t(($) => $.deliverables.canvas.file_not_found, { name: frame.file })}
          </div>
        )}
      </div>
    </div>
  );
});

function FrameDocument({ frame, attachmentId }: { frame: CanvasFrame; attachmentId: string }) {
  const { t } = useT("issues");
  const query = useAttachmentHtmlText(attachmentId);
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
      </div>
    );
  }
  if (query.data === undefined) {
    return (
      <div className="flex h-full items-center justify-center p-4 text-center text-body text-muted-foreground">
        {t(($) => $.deliverables.canvas.load_failed, { name: frame.file })}
      </div>
    );
  }
  return (
    <HtmlViewportFrame viewport={frame.device ?? "fill"}>
      {/* The frame is moved, not used: the iframe never takes the pointer. */}
      <CodeBlockIframe
        html={withFragmentNavShim(query.data.text)}
        title={frame.name}
        heightClassName="h-full"
        className="pointer-events-none rounded-none border-0"
      />
    </HtmlViewportFrame>
  );
}
