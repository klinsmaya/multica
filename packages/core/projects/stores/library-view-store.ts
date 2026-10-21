"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

/** How the project library lays its files out: one row each, or a tile grid. */
export type ProjectLibraryViewMode = "list" | "grid";

export interface ProjectLibraryViewState {
  viewMode: ProjectLibraryViewMode;
  setViewMode: (mode: ProjectLibraryViewMode) => void;
}

const DEFAULTS = { viewMode: "list" as ProjectLibraryViewMode };

const isViewMode = (value: unknown): value is ProjectLibraryViewMode =>
  value === "list" || value === "grid";

export const useProjectLibraryViewStore = create<ProjectLibraryViewState>()(
  persist(
    (set) => ({
      ...DEFAULTS,
      setViewMode: (viewMode) => set({ viewMode }),
    }),
    {
      name: "multica_project_library_view",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      partialize: (state) => ({ viewMode: state.viewMode }),
      // An empty workspace namespace, or a value this build does not know,
      // must land on the default instead of keeping the previous workspace's.
      merge: (persisted, current) => {
        const viewMode = (persisted as Partial<ProjectLibraryViewState> | undefined)?.viewMode;
        return { ...current, viewMode: isViewMode(viewMode) ? viewMode : DEFAULTS.viewMode };
      },
    },
  ),
);

registerForWorkspaceRehydration(() => useProjectLibraryViewStore.persist.rehydrate());
