// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseMentions } from "../issues/comment-trigger-outcomes";
import { composeAnnotatedReply, hasReplyIntent, locateReplyAnnotation, normalizeReplyAnnotations, type ReplyAnnotation } from "./reply-annotation";

const annotation: ReplyAnnotation = {
  id: "a", sourceCommentId: "source", sourceActorName: "Emacs", quote: "first\nsecond",
  start: 0, prefix: "", suffix: "", note: "Please revise.",
};

describe("annotated reply serialization", () => {
  it("puts annotations before the overall reply with a section divider", () => {
    expect(composeAnnotatedReply("Overall reply", [annotation, { ...annotation, id: "b", quote: "other", note: "" }]))
      .toBe("> first\n> second\n\nPlease revise.\n\n---\n\nOverall reply");
  });

  it("only adds a divider when both sections have content", () => {
    expect(composeAnnotatedReply("Overall reply", [])).toBe("Overall reply");
    expect(composeAnnotatedReply("Overall reply", [{ ...annotation, note: " " }])).toBe("Overall reply");
    expect(composeAnnotatedReply("  ", [annotation])).toBe("> first\n> second\n\nPlease revise.");
  });

  it("keeps /note as a command while placing the remaining body below annotations", () => {
    expect(composeAnnotatedReply(" /NOTE\nPrivate", [annotation])).toBe("/NOTE\n\n> first\n> second\n\nPlease revise.\n\n---\n\nPrivate");
    expect(composeAnnotatedReply("/note", [annotation])).toBe("/note\n\n> first\n> second\n\nPlease revise.");
    expect(composeAnnotatedReply("/notes are ordinary text", [annotation])).toBe("> first\n> second\n\nPlease revise.\n\n---\n\n/notes are ordinary text");
  });

  it("preserves user-authored note Markdown and links without list indentation", () => {
    expect(composeAnnotatedReply("", [{ ...annotation, note: "See [details](https://example.com).\n\n- First change\n- Second change" }]))
      .toBe("> first\n> second\n\nSee [details](https://example.com).\n\n- First change\n- Second change");
  });

  it("keeps quoted raw mentions and markup inert, while preserving deliberate mentions", () => {
    const mention = "[@Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)";
    const content = composeAnnotatedReply("", [{ ...annotation, quote: `\`\`\`\n${mention}\n<script>x</script>\n&colon;\n\`\`\``, sourceActorName: mention, note: mention }]);
    expect(parseMentions(content)).toHaveLength(1);
    expect(content.match(/mention:\/\//g)).toHaveLength(1);
    expect(content).toContain("&#60;script&#62;");
    expect(content).toContain("&amp;colon;");
    expect(content).toContain(mention);
  });

  it("retains /note behavior and does not send quotes alone", () => {
    expect(composeAnnotatedReply("/note Private", [annotation])).toBe("/note\n\n> first\n> second\n\nPlease revise.\n\n---\n\nPrivate");
    expect(hasReplyIntent("  ", [{ ...annotation, note: " " }])).toBe(false);
    expect(hasReplyIntent("", [annotation])).toBe(true);
    expect(hasReplyIntent("Overall", [{ ...annotation, note: "" }])).toBe(true);
  });

  it("normalizes old and corrupt drafts without inventing annotations", () => {
    expect(normalizeReplyAnnotations(undefined)).toEqual([]);
    expect(normalizeReplyAnnotations([null, {}, { ...annotation, note: " " }, { ...annotation, quote: "x".repeat(4001) }, annotation])).toEqual([annotation]);
  });
});

describe("source links on quoted replies", () => {
  const href = "https://app.example/acme/issues/MUL-1#comment-source";
  const link = () => href;

  it("is byte-identical without a resolver or when it returns null", () => {
    const plain = composeAnnotatedReply("Overall", [annotation]);
    expect(composeAnnotatedReply("Overall", [annotation], undefined)).toBe(plain);
    expect(composeAnnotatedReply("Overall", [annotation], () => null)).toBe(plain);
  });

  it("ends the blockquote with a link to the source comment", () => {
    expect(composeAnnotatedReply("Overall", [annotation], link)).toBe(
      `> first\n> second\n>\n> — [Emacs](${href})\n\nPlease revise.\n\n---\n\nOverall`,
    );
  });

  it("never links description sources, even when the resolver returns an href", () => {
    const description = { ...annotation, sourceCommentId: "description:issue-1" };
    expect(composeAnnotatedReply("", [description], link)).toBe(composeAnnotatedReply("", [description]));
  });

  it("links per annotation and keeps /note as the first token", () => {
    const description = { ...annotation, id: "b", sourceCommentId: "description:issue-1", quote: "other" };
    expect(composeAnnotatedReply("/note Private", [annotation, description], (a) => `${href}-${a.id}`)).toBe(
      `/note\n\n> first\n> second\n>\n> — [Emacs](${href}-a)\n\nPlease revise.\n\n&nbsp;\n\n> other\n\nPlease revise.\n\n---\n\nPrivate`,
    );
  });

  it("keeps the actor name and href inert", () => {
    const mention = "[@Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)";
    const content = composeAnnotatedReply("", [{ ...annotation, sourceActorName: `${mention}\nA` }], () => "https://app.example/a b)c");
    expect(parseMentions(content)).toHaveLength(0);
    expect(content).not.toContain("mention://");
    expect(content).toContain("(https://app.example/a%20b%29c)");
  });

  it("refuses hrefs that are neither http(s) nor an in-app path", () => {
    const plain = composeAnnotatedReply("", [annotation]);
    for (const bad of ["mention://agent/x", "javascript:alert(1)", "//evil.example/x", " "]) {
      expect(composeAnnotatedReply("", [annotation], () => bad)).toBe(plain);
    }
    expect(composeAnnotatedReply("", [annotation], () => "/acme/issues/MUL-1#comment-source"))
      .toContain("(/acme/issues/MUL-1#comment-source)");
  });
});

describe("attachment sources", () => {
  const file = {
    ...annotation, sourceCommentId: "attachment:att-1", sourceActorName: "report.md",
    source: { type: "attachment" as const, id: "att-1", filename: "report.md" },
  };
  const href = "https://app.example/acme/attachments/att-1/preview?name=report.md";

  it("attributes the quote to the file, linked when the resolver can", () => {
    expect(composeAnnotatedReply("", [file], (a) => a.source ? href : null)).toBe(
      `> first\n> second\n>\n> — [report&#46;md](${href})\n\nPlease revise.`,
    );
  });

  it("falls back to the plain file name without a link", () => {
    const plain = "> first\n> second\n>\n> — report&#46;md\n\nPlease revise.";
    expect(composeAnnotatedReply("", [file])).toBe(plain);
    expect(composeAnnotatedReply("", [file], () => null)).toBe(plain);
    expect(composeAnnotatedReply("", [file], () => "mention://agent/x")).toBe(plain);
  });

  it("lets the resolver branch on the source", () => {
    const seen: (string | undefined)[] = [];
    composeAnnotatedReply("", [annotation, file], (a) => { seen.push(a.source?.id); return null; });
    expect(seen).toEqual([undefined, "att-1"]);
  });

  it("keeps the file name inert", () => {
    const named = { ...file, source: { ...file.source, filename: "[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)\nb.md" } };
    const content = composeAnnotatedReply("", [named]);
    expect(parseMentions(content)).toHaveLength(0);
    expect(content).not.toContain("mention://");
  });

  it("preserves a valid source and drops an unreadable one without losing the annotation", () => {
    expect(normalizeReplyAnnotations([file])).toEqual([file]);
    const { source: _source, ...bare } = file;
    for (const bad of [{ type: "attachment", id: "x" }, { type: "url", id: "x", filename: "f" }, "att-1", null]) {
      expect(normalizeReplyAnnotations([{ ...file, source: bad }])).toEqual([bare]);
    }
    expect(normalizeReplyAnnotations([annotation])).toEqual([annotation]);
  });
});

describe("quote anchors", () => {
  const anchor = { ...annotation, quote: "same", prefix: "left ", suffix: " right", start: 5 };
  it("relocates a unique quote with unchanged context", () => {
    expect(locateReplyAnnotation("left same right", anchor)).toBe(5);
    expect(locateReplyAnnotation("new paragraph left same right", anchor)).toBe(19);
  });
  it("does not guess when source text or context changed or the match is ambiguous", () => {
    expect(locateReplyAnnotation("left different right", anchor)).toBeNull();
    expect(locateReplyAnnotation("other same right", anchor)).toBeNull();
    expect(locateReplyAnnotation("left same right / left same right", anchor)).toBeNull();
  });
});
