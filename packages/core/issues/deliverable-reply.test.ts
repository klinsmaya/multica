// @vitest-environment node
import { describe, expect, it } from "vitest";
import { composeDeliverableReply } from "./deliverable-reply";

const ref = { attachmentId: "11111111-1111-1111-1111-111111111111", filename: "report.pdf" };
const card = "!file[report.pdf](/api/attachments/11111111-1111-1111-1111-111111111111/download)";

describe("composeDeliverableReply", () => {
  it("puts the file card on its own line before the reply", () => {
    expect(composeDeliverableReply("Looks good", ref)).toBe(`${card}\n\nLooks good`);
  });

  it("keeps /note as the first token", () => {
    expect(composeDeliverableReply("/note\nPrivate", ref)).toBe(`/note\n\n${card}\n\nPrivate`);
    expect(composeDeliverableReply("/NOTE", ref)).toBe(`/NOTE\n\n${card}`);
    expect(composeDeliverableReply("/notes are text", ref)).toBe(`${card}\n\n/notes are text`);
  });

  it("returns the content unchanged without a reference", () => {
    expect(composeDeliverableReply("  Looks good ", null)).toBe("  Looks good ");
    expect(composeDeliverableReply("Looks good", undefined)).toBe("Looks good");
  });

  it("escapes the filename so it cannot break the label", () => {
    expect(composeDeliverableReply("x", { ...ref, filename: "a[1](v2)\\b.png" })).toContain(
      "!file[a\\[1\\]\\(v2\\)\\\\b.png](/api/attachments/",
    );
  });
});
