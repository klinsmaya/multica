// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseProgressChecklist } from "./progress-checklist";

describe("parseProgressChecklist", () => {
  it("reads open and done items in order and counts them", () => {
    expect(parseProgressChecklist("- [x] read the code\n- [ ] write the fix\n- [ ] ship")).toEqual({
      items: [
        { text: "read the code", done: true },
        { text: "write the fix", done: false },
        { text: "ship", done: false },
      ],
      done: 1,
      total: 3,
    });
  });

  it("tolerates * and + bullets, uppercase X and nested indentation", () => {
    const parsed = parseProgressChecklist("* [X] a\n  - [ ] nested\n\t+ [x] tabbed");
    expect(parsed.items).toEqual([
      { text: "a", done: true },
      { text: "nested", done: false },
      { text: "tabbed", done: true },
    ]);
    expect(parsed.done).toBe(2);
  });

  it("ignores prose, headings, plain bullets and malformed boxes", () => {
    const parsed = parseProgressChecklist(
      "## Plan\nWorking on it.\n- plain bullet\n- [] no space\n- [y] wrong mark\n-[ ] no gap\n- [ ] real",
    );
    expect(parsed.items).toEqual([{ text: "real", done: false }]);
  });

  it("handles CRLF and items without text", () => {
    expect(parseProgressChecklist("- [x] one\r\n- [ ]\r\n- [ ]   ").items).toEqual([
      { text: "one", done: true },
      { text: "", done: false },
      { text: "", done: false },
    ]);
  });

  it("returns an empty checklist for empty or task-free content", () => {
    expect(parseProgressChecklist("")).toEqual({ items: [], done: 0, total: 0 });
    expect(parseProgressChecklist("nothing here")).toEqual({ items: [], done: 0, total: 0 });
  });
});
