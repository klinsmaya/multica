// @vitest-environment node
import { describe, expect, it } from "vitest";
import { commentExcerpt } from "./comment-excerpt";

describe("commentExcerpt", () => {
  it("collapses mentions to their label and whitespace to single spaces", () => {
    expect(
      commentExcerpt(
        "Ping [@Alice](mention://member/6f1c0a52-0000-4000-8000-000000000001)\n\nsee  [MUL-7](mention://issue/6f1c0a52-0000-4000-8000-000000000002) please",
        80,
      ),
    ).toBe("Ping @Alice see MUL-7 please");
  });

  it("handles @all and agent mentions", () => {
    expect(commentExcerpt("[@all](mention://all/all) [@Go](mention://agent/ab-12)", 80)).toBe(
      "@all @Go",
    );
  });

  it("cuts to the limit with an ellipsis, counting code points", () => {
    expect(commentExcerpt("abcdef", 3)).toBe("abc…");
    expect(commentExcerpt("abc", 3)).toBe("abc");
    expect(commentExcerpt("😀😀😀😀", 2)).toBe("😀😀…");
  });

  it("returns an empty string for blank content", () => {
    expect(commentExcerpt("  \n ", 80)).toBe("");
  });
});
