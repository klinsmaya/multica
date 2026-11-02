/**
 * Prototype canvas — a `canvas.json` deliverable that lays several HTML
 * deliverables of one issue out on an infinite canvas.
 *
 *   { "pages": [ { "id", "name", "frames": [
 *       { "id", "name", "file", "x", "y", "w", "h", "device" } ] } ] }
 *
 * `file` names an HTML deliverable of the same issue (its latest version is
 * shown). Everything but `file` is optional on the wire; the parser fills in
 * the rest so a hand-written or older canvas still renders.
 *
 * Pure — no React, no DOM, no platform APIs.
 */

import { z } from "zod";

export type CanvasDevice = "fill" | "desktop" | "tablet" | "phone";

const CANVAS_DEVICES = ["fill", "desktop", "tablet", "phone"] as const;

/** Default frame size per device, in CSS px. `fill` is the generic 1280 × 800. */
export const CANVAS_DEVICE_SIZES: Record<CanvasDevice, { w: number; h: number }> = {
  fill: { w: 1280, h: 800 },
  desktop: { w: 1440, h: 900 },
  tablet: { w: 768, h: 1024 },
  phone: { w: 390, h: 844 },
};

export interface CanvasFrame {
  id: string;
  name: string;
  file: string;
  x: number;
  y: number;
  w: number;
  h: number;
  device?: CanvasDevice;
}

export interface CanvasPage {
  id: string;
  name: string;
  frames: CanvasFrame[];
}

export interface PrototypeCanvas {
  pages: CanvasPage[];
}

// Fields are read leniently: a wrong type is the same as a missing field.
const finite = z.number().refine(Number.isFinite);
const positive = finite.refine((n) => n > 0);

const FrameSchema = z.object({
  id: z.string().min(1).optional().catch(undefined),
  name: z.string().optional().catch(undefined),
  file: z.string().trim().min(1),
  x: finite.optional().catch(undefined),
  y: finite.optional().catch(undefined),
  w: positive.optional().catch(undefined),
  h: positive.optional().catch(undefined),
  device: z.enum(CANVAS_DEVICES).optional().catch(undefined),
});

const PageSchema = z.object({
  id: z.string().min(1).optional().catch(undefined),
  name: z.string().optional().catch(undefined),
  // A frame without a usable `file` is dropped rather than failing the page.
  frames: z.array(z.unknown()).optional().catch(undefined),
});

const CanvasSchema = z.object({ pages: z.array(z.unknown()) });

/** Whether a file name is a prototype canvas: `canvas.json` or `*.canvas.json`. */
export function isCanvasFilename(name: string): boolean {
  const lower = (name ?? "").trim().toLowerCase();
  return lower === "canvas.json" || lower.endsWith(".canvas.json");
}

/**
 * The canvas in `text`, or null when it is not JSON or has no `pages`.
 * Missing fields take defaults; ids that are absent or repeated are replaced
 * by positional ones so every frame can be keyed.
 */
export function parsePrototypeCanvas(text: string): PrototypeCanvas | null {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return null;
  }
  const root = CanvasSchema.safeParse(raw);
  if (!root.success) return null;

  const pageIds = new Set<string>();
  const pages: CanvasPage[] = [];
  root.data.pages.forEach((rawPage, pageIndex) => {
    const page = PageSchema.safeParse(rawPage);
    if (!page.success) return;
    let pageId = page.data.id ?? `page-${pageIndex + 1}`;
    if (pageIds.has(pageId)) pageId = `page-${pageIndex + 1}`;
    pageIds.add(pageId);

    const frameIds = new Set<string>();
    const frames: CanvasFrame[] = [];
    (page.data.frames ?? []).forEach((rawFrame, frameIndex) => {
      const frame = FrameSchema.safeParse(rawFrame);
      if (!frame.success) return;
      const f = frame.data;
      let id = f.id ?? `frame-${frameIndex + 1}`;
      if (frameIds.has(id)) id = `frame-${frameIndex + 1}`;
      frameIds.add(id);
      const size = CANVAS_DEVICE_SIZES[f.device ?? "fill"];
      frames.push({
        id,
        name: f.name?.trim() ? f.name : f.file,
        file: f.file,
        x: f.x ?? 0,
        y: f.y ?? 0,
        w: f.w ?? size.w,
        h: f.h ?? size.h,
        ...(f.device ? { device: f.device } : {}),
      });
    });
    pages.push({ id: pageId, name: page.data.name?.trim() ? page.data.name : pageId, frames });
  });
  return { pages };
}

interface MovableCanvas {
  pages: ReadonlyArray<{
    id?: string;
    frames?: ReadonlyArray<{ id?: string }>;
  }>;
}

/**
 * A copy of `canvas` with one frame at a new position. Nothing else changes,
 * and fields the parser does not know survive, so it is safe to run on the
 * raw JSON of the file being re-saved.
 */
export function moveFrame<T extends MovableCanvas>(
  canvas: T,
  pageId: string,
  frameId: string,
  x: number,
  y: number,
): T {
  return {
    ...canvas,
    pages: canvas.pages.map((page) =>
      page.id !== pageId
        ? page
        : {
            ...page,
            frames: page.frames?.map((frame) =>
              frame.id === frameId ? { ...frame, x, y } : frame,
            ),
          },
    ),
  };
}

export interface CanvasTransform {
  /** Screen offset of the canvas origin, in px. */
  x: number;
  y: number;
  scale: number;
}

export const CANVAS_MIN_SCALE = 0.05;
export const CANVAS_MAX_SCALE = 4;

export function clampScale(scale: number): number {
  return Math.min(CANVAS_MAX_SCALE, Math.max(CANVAS_MIN_SCALE, scale));
}

type Rect = Pick<CanvasFrame, "x" | "y" | "w" | "h">;

/**
 * The transform that shows every frame inside `viewport` with `padding` px
 * around them, centered. Never scales up past 1:1.
 */
export function fitAllTransform(
  frames: ReadonlyArray<Rect>,
  viewport: { width: number; height: number },
  padding = 48,
): CanvasTransform {
  if (frames.length === 0 || viewport.width <= 0 || viewport.height <= 0) {
    return { x: 0, y: 0, scale: 1 };
  }
  const minX = Math.min(...frames.map((f) => f.x));
  const minY = Math.min(...frames.map((f) => f.y));
  const maxX = Math.max(...frames.map((f) => f.x + f.w));
  const maxY = Math.max(...frames.map((f) => f.y + f.h));
  const width = Math.max(maxX - minX, 1);
  const height = Math.max(maxY - minY, 1);
  const scale = clampScale(
    Math.min(
      1,
      Math.max(viewport.width - padding * 2, 1) / width,
      Math.max(viewport.height - padding * 2, 1) / height,
    ),
  );
  return {
    scale,
    x: (viewport.width - width * scale) / 2 - minX * scale,
    y: (viewport.height - height * scale) / 2 - minY * scale,
  };
}

/** Pans so `frame`'s center sits at the viewport's center, keeping the zoom. */
export function centerOnFrame(
  frame: Rect,
  viewport: { width: number; height: number },
  scale: number,
): CanvasTransform {
  return {
    scale,
    x: viewport.width / 2 - (frame.x + frame.w / 2) * scale,
    y: viewport.height / 2 - (frame.y + frame.h / 2) * scale,
  };
}

/** Zooms to `nextScale` keeping the canvas point under `anchor` (viewport px) fixed. */
export function zoomAround(
  transform: CanvasTransform,
  nextScale: number,
  anchor: { x: number; y: number },
): CanvasTransform {
  const scale = clampScale(nextScale);
  const ratio = scale / transform.scale;
  return {
    scale,
    x: anchor.x - (anchor.x - transform.x) * ratio,
    y: anchor.y - (anchor.y - transform.y) * ratio,
  };
}
