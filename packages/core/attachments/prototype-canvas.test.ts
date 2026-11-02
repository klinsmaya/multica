// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  centerOnFrame,
  fitAllTransform,
  isCanvasFilename,
  moveFrame,
  parsePrototypeCanvas,
  zoomAround,
} from "./prototype-canvas";

const FULL = {
  pages: [
    {
      id: "p1",
      name: "Flow",
      frames: [
        { id: "a", name: "Home", file: "home.html", x: 10, y: 20, w: 300, h: 200, device: "phone", note: "keep" },
        { id: "b", name: "Detail", file: "detail.html", x: 400, y: 20, w: 300, h: 200 },
      ],
    },
    { id: "p2", name: "Other", frames: [{ id: "a", file: "other.html", x: 1, y: 2, w: 3, h: 4 }] },
  ],
};

describe("parsePrototypeCanvas", () => {
  it("fills defaults for missing fields", () => {
    const canvas = parsePrototypeCanvas(
      JSON.stringify({ pages: [{ frames: [{ file: "home.html" }, { file: "m.html", device: "phone" }] }] }),
    );
    expect(canvas).toEqual({
      pages: [
        {
          id: "page-1",
          name: "page-1",
          frames: [
            { id: "frame-1", name: "home.html", file: "home.html", x: 0, y: 0, w: 1280, h: 800 },
            { id: "frame-2", name: "m.html", file: "m.html", x: 0, y: 0, w: 390, h: 844, device: "phone" },
          ],
        },
      ],
    });
  });

  it("treats wrong types and unknown devices as missing", () => {
    const canvas = parsePrototypeCanvas(
      JSON.stringify({
        pages: [{ id: "p", frames: [{ file: "a.html", x: "1", w: -5, device: "watch" }] }],
      }),
    );
    expect(canvas?.pages[0]?.frames[0]).toEqual({
      id: "frame-1",
      name: "a.html",
      file: "a.html",
      x: 0,
      y: 0,
      w: 1280,
      h: 800,
    });
  });

  it("drops frames without a file and keeps the page", () => {
    const canvas = parsePrototypeCanvas(
      JSON.stringify({ pages: [{ id: "p", frames: [{ id: "x" }, { file: "ok.html" }] }] }),
    );
    expect(canvas?.pages[0]?.frames.map((f) => f.file)).toEqual(["ok.html"]);
  });

  it("replaces repeated ids with positional ones", () => {
    const canvas = parsePrototypeCanvas(
      JSON.stringify({ pages: [{ id: "p", frames: [{ id: "a", file: "1.html" }, { id: "a", file: "2.html" }] }] }),
    );
    expect(canvas?.pages[0]?.frames.map((f) => f.id)).toEqual(["a", "frame-2"]);
  });

  it("returns null for invalid JSON or a missing pages array", () => {
    expect(parsePrototypeCanvas("{nope")).toBeNull();
    expect(parsePrototypeCanvas("[]")).toBeNull();
    expect(parsePrototypeCanvas("{}")).toBeNull();
    expect(parsePrototypeCanvas(JSON.stringify({ pages: "x" }))).toBeNull();
  });

  it("accepts an empty pages array", () => {
    expect(parsePrototypeCanvas('{"pages":[]}')).toEqual({ pages: [] });
  });
});

describe("moveFrame", () => {
  it("changes only the moved frame's x and y", () => {
    const moved = moveFrame(FULL, "p1", "a", 99, -5);
    expect(moved.pages[0]!.frames[0]).toEqual({ ...FULL.pages[0]!.frames[0], x: 99, y: -5 });
    expect(moved.pages[0]!.frames[1]).toEqual(FULL.pages[0]!.frames[1]);
    // Same frame id on another page is a different frame.
    expect(moved.pages[1]).toEqual(FULL.pages[1]);
  });

  it("does not mutate its input", () => {
    const before = JSON.stringify(FULL);
    moveFrame(FULL, "p1", "a", 1, 1);
    expect(JSON.stringify(FULL)).toBe(before);
  });

  it("leaves the canvas as it was when nothing matches", () => {
    expect(moveFrame(FULL, "nope", "a", 1, 1)).toEqual(FULL);
    expect(moveFrame(FULL, "p1", "nope", 1, 1)).toEqual(FULL);
  });
});

describe("isCanvasFilename", () => {
  it("matches canvas.json and *.canvas.json, case-insensitively", () => {
    expect(isCanvasFilename("canvas.json")).toBe(true);
    expect(isCanvasFilename("Canvas.JSON")).toBe(true);
    expect(isCanvasFilename("onboarding.canvas.json")).toBe(true);
    expect(isCanvasFilename("my-canvas.json")).toBe(false);
    expect(isCanvasFilename("canvas.json.bak")).toBe(false);
    expect(isCanvasFilename("data.json")).toBe(false);
    expect(isCanvasFilename("")).toBe(false);
  });
});

describe("fitAllTransform", () => {
  const viewport = { width: 1000, height: 600 };

  it("centers and scales the frames' bounds into the viewport", () => {
    // Bounds 2000 × 1000 at (100, 50); padding 50 leaves 900 × 500.
    const t = fitAllTransform(
      [
        { x: 100, y: 50, w: 1000, h: 1000 },
        { x: 1100, y: 50, w: 1000, h: 500 },
      ],
      viewport,
      50,
    );
    expect(t.scale).toBeCloseTo(0.45);
    // The bounds' width at that scale is 900, centered: 50px each side.
    expect(100 * t.scale + t.x).toBeCloseTo(50);
    expect(2100 * t.scale + t.x).toBeCloseTo(950);
    // Height 1000 * 0.45 = 450, centered in 600.
    expect(50 * t.scale + t.y).toBeCloseTo(75);
  });

  it("does not scale up past 1:1", () => {
    const t = fitAllTransform([{ x: 0, y: 0, w: 100, h: 100 }], viewport);
    expect(t.scale).toBe(1);
    expect(t.x).toBeCloseTo(450);
    expect(t.y).toBeCloseTo(250);
  });

  it("is the identity for no frames or no viewport", () => {
    expect(fitAllTransform([], viewport)).toEqual({ x: 0, y: 0, scale: 1 });
    expect(fitAllTransform([{ x: 0, y: 0, w: 1, h: 1 }], { width: 0, height: 0 })).toEqual({
      x: 0,
      y: 0,
      scale: 1,
    });
  });
});

describe("centerOnFrame and zoomAround", () => {
  it("puts the frame's center at the viewport's center", () => {
    const t = centerOnFrame({ x: 100, y: 100, w: 200, h: 100 }, { width: 1000, height: 600 }, 0.5);
    expect(200 * t.scale + t.x).toBeCloseTo(500);
    expect(150 * t.scale + t.y).toBeCloseTo(300);
  });

  it("keeps the point under the cursor fixed while zooming", () => {
    const before = { x: 30, y: 40, scale: 1 };
    const anchor = { x: 200, y: 100 };
    const after = zoomAround(before, 2, anchor);
    const canvasPoint = { x: (anchor.x - before.x) / before.scale, y: (anchor.y - before.y) / before.scale };
    expect(canvasPoint.x * after.scale + after.x).toBeCloseTo(anchor.x);
    expect(canvasPoint.y * after.scale + after.y).toBeCloseTo(anchor.y);
  });

  it("clamps the scale", () => {
    expect(zoomAround({ x: 0, y: 0, scale: 1 }, 1000, { x: 0, y: 0 }).scale).toBe(4);
    expect(zoomAround({ x: 0, y: 0, scale: 1 }, 0, { x: 0, y: 0 }).scale).toBe(0.05);
  });
});
