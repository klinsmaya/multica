// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  diffParagraphs,
  parsePendingSections,
  splitParagraphs,
  writingProgress,
} from "./progressive-document";

const OUTLINE = [
  "# Report",
  "",
  "<!-- pending: Background -->",
  "",
  "<!-- pending: Findings -->",
  "",
  "<!-- pending: Next steps -->",
].join("\n");

describe("parsePendingSections", () => {
  it("lists the markers with their titles in document order", () => {
    expect(parsePendingSections(OUTLINE)).toEqual([
      { title: "Background", index: 1 },
      { title: "Findings", index: 2 },
      { title: "Next steps", index: 3 },
    ]);
  });

  it("tolerates surrounding whitespace and CRLF line endings", () => {
    const crlf = "# Report\r\n\r\n   <!--   pending:   Background   -->   \r\n\r\ntext\r\n";
    expect(parsePendingSections(crlf)).toEqual([{ title: "Background", index: 1 }]);
  });

  it("finds a marker glued to the text above it", () => {
    expect(parsePendingSections("intro\n<!-- pending: A -->\noutro")).toEqual([
      { title: "A", index: 1 },
    ]);
  });

  it("ignores marker text inside a fenced code block and plain comments", () => {
    const doc = "```md\n<!-- pending: Not real -->\n```\n\n<!-- just a note -->";
    expect(parsePendingSections(doc)).toEqual([]);
  });

  it("finds nothing in a plain document", () => {
    expect(parsePendingSections("# Title\n\nBody")).toEqual([]);
  });
});

describe("splitParagraphs", () => {
  it("splits on blank lines and normalizes CRLF", () => {
    expect(splitParagraphs("a\r\nb\r\n\r\n\r\nc\r\n")).toEqual(["a\nb", "c"]);
  });

  it("keeps a fenced code block whole, blank lines included", () => {
    const doc = "before\n\n```ts\nconst a = 1;\n\nconst b = 2;\n```\n\nafter";
    expect(splitParagraphs(doc)).toEqual([
      "before",
      "```ts\nconst a = 1;\n\nconst b = 2;\n```",
      "after",
    ]);
  });

  it("keeps an unterminated fence to the end of the document", () => {
    expect(splitParagraphs("a\n\n```\nx\n\ny")).toEqual(["a", "```\nx\n\ny"]);
  });

  it("returns no blocks for an empty document", () => {
    expect(splitParagraphs("  \n\n")).toEqual([]);
  });
});

describe("diffParagraphs", () => {
  it("marks only the blocks that were not in the previous version", () => {
    const prev = "# Report\n\n<!-- pending: Background -->\n\n<!-- pending: Findings -->";
    const next = "# Report\n\nBackground written here.\n\n<!-- pending: Findings -->";
    expect(diffParagraphs(prev, next)).toEqual({ changedIndexes: [1] });
  });

  it("yields nothing when the document did not change", () => {
    expect(diffParagraphs(OUTLINE, OUTLINE)).toEqual({ changedIndexes: [] });
    expect(diffParagraphs(OUTLINE, OUTLINE.replace(/\n/g, "\r\n"))).toEqual({
      changedIndexes: [],
    });
  });

  it("does not flag a block that only moved", () => {
    expect(diffParagraphs("a\n\nb", "b\n\na")).toEqual({ changedIndexes: [] });
  });

  it("flags every copy beyond the ones the previous version had", () => {
    expect(diffParagraphs("a", "a\n\na")).toEqual({ changedIndexes: [1] });
  });

  it("flags an edited block and ignores new pending markers", () => {
    expect(diffParagraphs("a\n\nb", "a\n\nb2\n\n<!-- pending: C -->")).toEqual({
      changedIndexes: [1],
    });
  });
});

describe("writingProgress", () => {
  it("counts the sections written since the outline", () => {
    const latest = OUTLINE.replace("<!-- pending: Background -->", "Written.");
    expect(writingProgress(OUTLINE, latest)).toEqual({ total: 3, done: 1 });
  });

  it("reports nothing done for the outline itself and all done at the end", () => {
    expect(writingProgress(OUTLINE, OUTLINE)).toEqual({ total: 3, done: 0 });
    expect(writingProgress(OUTLINE, "# Report\n\nAll of it.")).toEqual({ total: 3, done: 3 });
  });

  it("has a total of 0 for a document without markers", () => {
    expect(writingProgress("# Plain", "# Plain\n\nMore")).toEqual({ total: 0, done: 0 });
  });

  it("never reports more done than total or a negative count", () => {
    expect(writingProgress("<!-- pending: A -->", OUTLINE)).toEqual({ total: 1, done: 0 });
  });
});
