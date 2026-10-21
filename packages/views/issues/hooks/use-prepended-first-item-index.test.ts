// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  VIRTUOSO_START_INDEX,
  nextFirstItemIndex,
  type FirstItemIndexState,
} from "./use-prepended-first-item-index";

const start: FirstItemIndexState = { firstKey: undefined, index: VIRTUOSO_START_INDEX };

describe("nextFirstItemIndex", () => {
  it("adopts the first item without moving the index", () => {
    expect(nextFirstItemIndex(start, ["c:3", "c:4"])).toEqual({
      firstKey: "c:3",
      index: VIRTUOSO_START_INDEX,
    });
  });

  it("keeps the same state while the first item and the top of the list are unchanged", () => {
    const state = nextFirstItemIndex(start, ["c:3", "c:4"]);
    expect(nextFirstItemIndex(state, ["c:3", "c:4", "c:5"])).toBe(state);
  });

  it("lowers the index by the number of items prepended above the old first item", () => {
    const state = nextFirstItemIndex(start, ["c:3", "c:4"]);
    const prepended = nextFirstItemIndex(state, ["c:0", "c:1", "c:2", "c:3", "c:4"]);
    expect(prepended).toEqual({ firstKey: "c:0", index: VIRTUOSO_START_INDEX - 3 });

    // A second older page stacks on the first.
    expect(nextFirstItemIndex(prepended, ["a:0", "a:1", "c:0", "c:1", "c:2", "c:3", "c:4"]).index).toBe(
      VIRTUOSO_START_INDEX - 5,
    );
  });

  it("does not move the index when the old first item disappears", () => {
    const state = nextFirstItemIndex(start, ["c:3", "c:4"]);
    // e.g. the first comment was deleted: nothing was prepended.
    expect(nextFirstItemIndex(state, ["c:4"])).toEqual({
      firstKey: "c:4",
      index: VIRTUOSO_START_INDEX,
    });
  });

  it("ignores an empty list", () => {
    const state = nextFirstItemIndex(start, ["c:3"]);
    expect(nextFirstItemIndex(state, [])).toBe(state);
    expect(nextFirstItemIndex(start, [])).toBe(start);
  });

  it("never goes below zero", () => {
    const state: FirstItemIndexState = { firstKey: "c:9", index: 2 };
    expect(nextFirstItemIndex(state, ["a", "b", "c", "d", "c:9"]).index).toBe(0);
  });
});
