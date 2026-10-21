// @vitest-environment jsdom
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { useProjectLibraryViewStore } from "./library-view-store";
import { setCurrentWorkspace } from "../../platform/workspace-storage";

const flush = () => new Promise((resolve) => queueMicrotask(() => resolve(null)));

// Node 25 ships a partial `localStorage` shim under jsdom that lacks
// `clear`/`removeItem`; swap in a real in-memory Storage so persist can
// round-trip values.
beforeAll(() => {
  if (typeof globalThis.localStorage?.clear !== "function") {
    const values = new Map<string, string>();
    const storage: Storage = {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (k) => values.get(k) ?? null,
      key: (i) => Array.from(values.keys())[i] ?? null,
      removeItem: (k) => { values.delete(k); },
      setItem: (k, v) => { values.set(k, v); },
    };
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
    Object.defineProperty(window, "localStorage", { configurable: true, value: storage });
  }
});

beforeEach(() => {
  localStorage.clear();
  useProjectLibraryViewStore.setState({ viewMode: "list" });
  setCurrentWorkspace(null, null);
});

afterEach(() => {
  setCurrentWorkspace(null, null);
});

describe("useProjectLibraryViewStore", () => {
  it("defaults to the list", () => {
    expect(useProjectLibraryViewStore.getState().viewMode).toBe("list");
  });

  it("persists only the layout, under the workspace-namespaced key", async () => {
    setCurrentWorkspace("acme", "ws_a");
    await flush();
    useProjectLibraryViewStore.getState().setViewMode("grid");

    const raw = localStorage.getItem("multica_project_library_view:acme");
    expect(raw).not.toBeNull();
    expect(Object.keys(JSON.parse(raw as string).state)).toEqual(["viewMode"]);
    expect(JSON.parse(raw as string).state.viewMode).toBe("grid");
  });

  it("follows the saved layout of each workspace and resets for one with none", async () => {
    localStorage.setItem(
      "multica_project_library_view:acme",
      JSON.stringify({ state: { viewMode: "grid" }, version: 0 }),
    );

    setCurrentWorkspace("acme", "ws_a");
    await flush();
    await flush();
    expect(useProjectLibraryViewStore.getState().viewMode).toBe("grid");

    setCurrentWorkspace("beta", "ws_b");
    await flush();
    await flush();
    expect(useProjectLibraryViewStore.getState().viewMode).toBe("list");
  });

  it("ignores a saved layout this build does not know", async () => {
    localStorage.setItem(
      "multica_project_library_view:acme",
      JSON.stringify({ state: { viewMode: "kanban" }, version: 0 }),
    );
    setCurrentWorkspace("acme", "ws_a");
    await flush();
    await flush();
    expect(useProjectLibraryViewStore.getState().viewMode).toBe("list");
  });
});
