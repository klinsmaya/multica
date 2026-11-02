// @vitest-environment node
import { beforeEach, describe, expect, it } from "vitest";
import {
  feedComposerDraftKey,
  pruneStaleFeedDrafts,
  useFeedComposerStore,
} from "./feed-composer-store";

const DAY_MS = 24 * 60 * 60 * 1000;

describe("feed composer store", () => {
  beforeEach(() => {
    useFeedComposerStore.setState({ drafts: {} });
  });

  it("keys drafts by workspace and project", () => {
    expect(feedComposerDraftKey("ws-1", "p-1")).toBe("ws-1:p-1");
  });

  it("keeps a separate draft per project", () => {
    const { setDraft, getDraft } = useFeedComposerStore.getState();
    setDraft(feedComposerDraftKey("ws-1", "p-1"), "alpha");
    setDraft(feedComposerDraftKey("ws-1", "p-2"), "beta");

    expect(getDraft(feedComposerDraftKey("ws-1", "p-1"))).toBe("alpha");
    expect(getDraft(feedComposerDraftKey("ws-1", "p-2"))).toBe("beta");
    expect(getDraft(feedComposerDraftKey("ws-2", "p-1"))).toBeUndefined();
  });

  it("drops the entry when the text becomes blank", () => {
    const key = feedComposerDraftKey("ws-1", "p-1");
    const { setDraft, getDraft } = useFeedComposerStore.getState();
    setDraft(key, "hello");
    setDraft(key, "  \n ");

    expect(getDraft(key)).toBeUndefined();
    expect(key in useFeedComposerStore.getState().drafts).toBe(false);
  });

  it("clears one draft without touching the others", () => {
    const a = feedComposerDraftKey("ws-1", "p-1");
    const b = feedComposerDraftKey("ws-1", "p-2");
    const { setDraft, clearDraft, getDraft } = useFeedComposerStore.getState();
    setDraft(a, "one");
    setDraft(b, "two");
    clearDraft(a);

    expect(getDraft(a)).toBeUndefined();
    expect(getDraft(b)).toBe("two");
  });

  it("does not replace the drafts object for an identical write", () => {
    const key = feedComposerDraftKey("ws-1", "p-1");
    useFeedComposerStore.getState().setDraft(key, "same");
    const before = useFeedComposerStore.getState().drafts;
    useFeedComposerStore.getState().setDraft(key, "same");

    expect(useFeedComposerStore.getState().drafts).toBe(before);
  });

  it("prunes drafts older than 30 days and blank ones", () => {
    const now = Date.now();
    const pruned = pruneStaleFeedDrafts(
      {
        fresh: { content: "keep", updatedAt: now - DAY_MS },
        stale: { content: "old", updatedAt: now - 31 * DAY_MS },
        blank: { content: "  ", updatedAt: now },
      },
      now,
    );

    expect(Object.keys(pruned)).toEqual(["fresh"]);
  });
});
