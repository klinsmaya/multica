// @vitest-environment node
import { beforeEach, describe, expect, it } from "vitest";
import { useFeedReplyTargetsStore } from "./feed-reply-targets-store";

const a = { issueId: "i-a", commentId: "c-a", identifier: "MUL-1" };
const b = { issueId: "i-b", commentId: "c-b", identifier: "MUL-2" };

describe("feed reply targets store", () => {
  beforeEach(() => useFeedReplyTargetsStore.setState({ targets: {} }));

  it("toggles a target by issue id", () => {
    const { toggle } = useFeedReplyTargetsStore.getState();
    toggle(a);
    expect(useFeedReplyTargetsStore.getState().targets).toEqual({ "i-a": a });
    toggle(a);
    expect(useFeedReplyTargetsStore.getState().targets).toEqual({});
  });

  it("refreshes the comment of an already selected issue and ignores a no-op select", () => {
    const { select } = useFeedReplyTargetsStore.getState();
    select(a);
    const before = useFeedReplyTargetsStore.getState().targets;
    select({ ...a });
    expect(useFeedReplyTargetsStore.getState().targets).toBe(before);
    select({ ...a, commentId: "c-new" });
    expect(useFeedReplyTargetsStore.getState().targets["i-a"]!.commentId).toBe("c-new");
  });

  it("deselects one, retains a subset, and clears all", () => {
    const s = useFeedReplyTargetsStore.getState();
    s.select(a);
    s.select(b);
    s.deselect("i-a");
    expect(Object.keys(useFeedReplyTargetsStore.getState().targets)).toEqual(["i-b"]);
    s.select(a);
    s.retainOnly(["i-a"]);
    expect(Object.keys(useFeedReplyTargetsStore.getState().targets)).toEqual(["i-a"]);
    s.clear();
    expect(useFeedReplyTargetsStore.getState().targets).toEqual({});
  });

  it("is not persisted", () => {
    expect("persist" in useFeedReplyTargetsStore).toBe(false);
  });
});
