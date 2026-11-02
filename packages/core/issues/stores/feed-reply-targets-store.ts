"use client";

import { create } from "zustand";

/** A waiting thread the feed composer will answer: the issue and its newest open question. */
export interface FeedReplyTarget {
  issueId: string;
  commentId: string;
  /** Shown on the composer chip, e.g. MUL-12. */
  identifier: string;
}

interface FeedReplyTargetsStore {
  /** Keyed by issue id. Ephemeral: never persisted. */
  targets: Record<string, FeedReplyTarget>;
  /** Adds the target, or refreshes it when the issue is already selected. */
  select: (target: FeedReplyTarget) => void;
  deselect: (issueId: string) => void;
  toggle: (target: FeedReplyTarget) => void;
  /** Keeps only the given issues; used to retain the targets a send failed on. */
  retainOnly: (issueIds: readonly string[]) => void;
  clear: () => void;
}

export const useFeedReplyTargetsStore = create<FeedReplyTargetsStore>()((set) => ({
  targets: {},
  select: (target) =>
    set((s) => {
      const current = s.targets[target.issueId];
      if (
        current &&
        current.commentId === target.commentId &&
        current.identifier === target.identifier
      ) {
        return s;
      }
      return { targets: { ...s.targets, [target.issueId]: target } };
    }),
  deselect: (issueId) =>
    set((s) => {
      if (!(issueId in s.targets)) return s;
      const next = { ...s.targets };
      delete next[issueId];
      return { targets: next };
    }),
  toggle: (target) =>
    set((s) => {
      if (target.issueId in s.targets) {
        const next = { ...s.targets };
        delete next[target.issueId];
        return { targets: next };
      }
      return { targets: { ...s.targets, [target.issueId]: target } };
    }),
  retainOnly: (issueIds) =>
    set((s) => {
      const keep = new Set(issueIds);
      const next: Record<string, FeedReplyTarget> = {};
      for (const [id, target] of Object.entries(s.targets)) {
        if (keep.has(id)) next[id] = target;
      }
      return { targets: next };
    }),
  clear: () => set((s) => (Object.keys(s.targets).length ? { targets: {} } : s)),
}));
