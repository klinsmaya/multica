// @vitest-environment node
import { describe, expect, it } from "vitest";
import { composeFeedForwardComment } from "./feed-forward";

describe("composeFeedForwardComment", () => {
  it("puts the marker in a blockquote above the message", () => {
    expect(composeFeedForwardComment("  Ship it  ", "来自项目对话 · Ada")).toBe(
      "> 来自项目对话 · Ada\n\nShip it",
    );
  });

  it("keeps a leading /note as the first token", () => {
    expect(composeFeedForwardComment("/note FYI", "From project chat · Ada")).toBe(
      "/note\n\n> From project chat · Ada\n\nFYI",
    );
  });

  it("neutralises markup in the sender name", () => {
    const out = composeFeedForwardComment("hi", "From chat · [@x](mention://agent/1)");
    expect(out).not.toContain("mention://");
    expect(out).not.toContain("[@x]");
  });
});
