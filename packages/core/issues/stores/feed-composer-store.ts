"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";
import { registerDraftCleanup } from "../../drafts/cleanup-registry";

/**
 * Unsent text of the project feed composer. Keyed `wsId:projectId`: the
 * workspace-aware storage already partitions by workspace slug, but the key
 * carries the workspace id as well so a draft can never be read back under
 * another workspace's project, and each project keeps its own draft.
 */
export type FeedComposerDraftKey = `${string}:${string}`;

export function feedComposerDraftKey(wsId: string, projectId: string): FeedComposerDraftKey {
  return `${wsId}:${projectId}`;
}

interface FeedComposerDraft {
  content: string;
  updatedAt: number;
}

interface FeedComposerStore {
  drafts: Record<string, FeedComposerDraft>;
  getDraft: (key: FeedComposerDraftKey) => string | undefined;
  /** Blank text drops the entry instead of storing an empty draft. */
  setDraft: (key: FeedComposerDraftKey, content: string) => void;
  clearDraft: (key: FeedComposerDraftKey) => void;
}

// Same retention as comment drafts: stale entries are dropped on rehydrate so
// abandoned drafts do not accumulate in storage.
const TTL_MS = 30 * 24 * 60 * 60 * 1000;

export function pruneStaleFeedDrafts(
  drafts: Record<string, FeedComposerDraft>,
  now = Date.now(),
): Record<string, FeedComposerDraft> {
  const cutoff = now - TTL_MS;
  const out: Record<string, FeedComposerDraft> = {};
  for (const [key, draft] of Object.entries(drafts)) {
    if (
      typeof draft?.content === "string" &&
      draft.content.trim().length > 0 &&
      draft.updatedAt >= cutoff
    ) {
      out[key] = draft;
    }
  }
  return out;
}

export const useFeedComposerStore = create<FeedComposerStore>()(
  persist(
    (set, get) => ({
      drafts: {},
      getDraft: (key) => get().drafts[key]?.content,
      setDraft: (key, content) =>
        set((s) => {
          if (!content.trim()) {
            if (!(key in s.drafts)) return s;
            const next = { ...s.drafts };
            delete next[key];
            return { drafts: next };
          }
          if (s.drafts[key]?.content === content) return s;
          return { drafts: { ...s.drafts, [key]: { content, updatedAt: Date.now() } } };
        }),
      clearDraft: (key) =>
        set((s) => {
          if (!(key in s.drafts)) return s;
          const next = { ...s.drafts };
          delete next[key];
          return { drafts: next };
        }),
    }),
    {
      name: "multica_feed_composer_drafts",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      // An empty workspace namespace must mean an empty draft set, not the
      // previous workspace's drafts (zustand's default merge keeps them).
      merge: (persisted, current) => ({
        ...current,
        drafts: (persisted as { drafts?: Record<string, FeedComposerDraft> } | undefined)?.drafts ?? {},
      }),
      onRehydrateStorage: () => (state) => {
        if (state) state.drafts = pruneStaleFeedDrafts(state.drafts);
      },
    },
  ),
);

registerForWorkspaceRehydration(() => useFeedComposerStore.persist.rehydrate());

registerDraftCleanup({
  storageKey: "multica_feed_composer_drafts",
  workspaceScoped: true,
  resetInMemory: () => useFeedComposerStore.setState({ drafts: {} }),
});
