/**
 * Progressive documents — a long document an agent writes section by section.
 *
 * Convention: the agent first uploads an outline in which every unwritten
 * section is one line, `<!-- pending: <section title> -->`, then re-uploads
 * the same file name after each section is written. Each upload is a new
 * version of the deliverable (see `deliverables.ts`); the markers still in the
 * latest version are the sections yet to come.
 *
 * Pure — no React, no DOM, no platform APIs.
 */

export interface PendingSection {
  title: string;
  /** Index of the marker's block in `splitParagraphs(markdown)`. */
  index: number;
}

export interface ParagraphDiff {
  /** Indexes into `splitParagraphs(next)` of the blocks `prev` does not have. */
  changedIndexes: number[];
}

export interface WritingProgress {
  /** Sections the outline announced. */
  total: number;
  /** Sections no longer pending in the latest version. */
  done: number;
}

const PENDING_LINE = /^\s*<!--\s*pending:\s*(.*?)\s*-->\s*$/i;
const FENCE_OPEN = /^\s{0,3}(`{3,}|~{3,})/;

/** Title of a `<!-- pending: ... -->` line, or null for any other line. */
export function pendingTitle(line: string): string | null {
  const match = PENDING_LINE.exec(line);
  return match ? match[1]! : null;
}

function normalizeNewlines(markdown: string): string {
  return markdown.replace(/\r\n?/g, "\n");
}

/**
 * Blocks of a document: runs of lines separated by blank lines. A fenced code
 * block is one block however many blank lines it holds, and a pending marker
 * is always a block of its own, so a marker glued to the text above it still
 * reads as a placeholder. Blocks keep their inner lines verbatim.
 */
export function splitParagraphs(markdown: string): string[] {
  const blocks: string[] = [];
  let current: string[] = [];
  let fence: { char: string; length: number } | null = null;

  const flush = () => {
    if (current.length > 0) blocks.push(current.join("\n"));
    current = [];
  };

  for (const line of normalizeNewlines(markdown).split("\n")) {
    if (fence) {
      current.push(line);
      const close = FENCE_OPEN.exec(line);
      if (
        close &&
        close[1]![0] === fence.char &&
        close[1]!.length >= fence.length &&
        line.trim() === close[1]
      ) {
        fence = null;
      }
      continue;
    }
    const open = FENCE_OPEN.exec(line);
    if (open) {
      current.push(line);
      fence = { char: open[1]![0]!, length: open[1]!.length };
      continue;
    }
    if (pendingTitle(line) !== null) {
      flush();
      blocks.push(line.trim());
      continue;
    }
    if (line.trim() === "") {
      flush();
      continue;
    }
    current.push(line);
  }
  flush();
  return blocks;
}

/** The sections still pending in `markdown`, in document order. */
export function parsePendingSections(markdown: string): PendingSection[] {
  const sections: PendingSection[] = [];
  splitParagraphs(markdown).forEach((block, index) => {
    const title = pendingTitle(block);
    if (title !== null) sections.push({ title, index });
  });
  return sections;
}

const comparable = (block: string) => block.replace(/\s+/g, " ").trim();

/**
 * Which blocks of `next` were not in `prev`. A block counts as unchanged when
 * `prev` has an equal block left to pair it with, so a document that did not
 * change yields nothing and a moved block is not flagged. Pending markers are
 * never "changed": they announce work, they are not work.
 */
export function diffParagraphs(prev: string, next: string): ParagraphDiff {
  const available = new Map<string, number>();
  for (const block of splitParagraphs(prev)) {
    const key = comparable(block);
    available.set(key, (available.get(key) ?? 0) + 1);
  }
  const changedIndexes: number[] = [];
  splitParagraphs(next).forEach((block, index) => {
    const key = comparable(block);
    const left = available.get(key) ?? 0;
    if (left > 0) {
      available.set(key, left - 1);
      return;
    }
    if (pendingTitle(block) === null) changedIndexes.push(index);
  });
  return { changedIndexes };
}

/**
 * How far the writing has got: `total` is what the outline announced, `done`
 * how many of those markers the latest version no longer carries.
 */
export function writingProgress(outline: string, latest: string): WritingProgress {
  const total = parsePendingSections(outline).length;
  const left = parsePendingSections(latest).length;
  return { total, done: Math.min(total, Math.max(0, total - left)) };
}
