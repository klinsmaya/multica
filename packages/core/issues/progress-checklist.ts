export interface ProgressChecklistItem {
  text: string;
  done: boolean;
}

export interface ProgressChecklist {
  items: ProgressChecklistItem[];
  done: number;
  total: number;
}

// A GFM task-list line: optional indentation, a `-`/`*`/`+` bullet, then `[ ]`,
// `[x]` or `[X]` and the item text. Nested items are listed flat.
const TASK_LINE = /^[ \t]*[-*+][ \t]+\[([ xX])\](?:[ \t]+(.*))?$/;

/**
 * Reads the task-list items out of an agent's progress report. Lines that are
 * not task items (headings, prose, plain bullets) are ignored, so a report may
 * wrap its checklist in commentary.
 */
export function parseProgressChecklist(content: string): ProgressChecklist {
  const items: ProgressChecklistItem[] = [];
  for (const line of content.split(/\r?\n/)) {
    const match = TASK_LINE.exec(line);
    if (!match) continue;
    items.push({ text: (match[2] ?? "").trim(), done: match[1] !== " " });
  }
  const done = items.filter((item) => item.done).length;
  return { items, done, total: items.length };
}
