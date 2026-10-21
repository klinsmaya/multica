// @vitest-environment node
import { describe, expect, it } from "vitest";
import { createStore } from "zustand/vanilla";
import {
  mergeViewStatePersisted,
  viewStorePersistOptions,
  viewStoreSlice,
  type IssueViewState,
} from "./view-store";

function createViewStore() {
  return createStore<IssueViewState>()((set) => viewStoreSlice(set));
}

describe("feedSort", () => {
  it("defaults to creation order and switches", () => {
    const store = createViewStore();
    expect(store.getState().feedSort).toBe("created");
    store.getState().setFeedSort("activity");
    expect(store.getState().feedSort).toBe("activity");
  });

  it("is part of the persisted and saved-view display state", () => {
    const store = createViewStore();
    store.getState().setFeedSort("activity");
    const persisted = viewStorePersistOptions("feed-sort").partialize(store.getState());
    expect(persisted.feedSort).toBe("activity");
  });

  it("round-trips through the persisted snapshot", () => {
    const merged = mergeViewStatePersisted(
      { viewMode: "feed", feedSort: "activity" },
      createViewStore().getState(),
    );
    expect(merged.feedSort).toBe("activity");
  });

  it("falls back to creation order for snapshots and views saved before it existed", () => {
    const merged = mergeViewStatePersisted(
      { viewMode: "feed" },
      createViewStore().getState(),
    );
    expect(merged.feedSort).toBe("created");
  });

  it("falls back to creation order for a value this client does not know", () => {
    const merged = mergeViewStatePersisted(
      { feedSort: "popularity" },
      createViewStore().getState(),
    );
    expect(merged.feedSort).toBe("created");
  });
});
