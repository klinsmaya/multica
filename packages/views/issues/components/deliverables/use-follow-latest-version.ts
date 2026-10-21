"use client";

import { useEffect, useRef } from "react";
import type { DeliverableFile } from "@multica/core/attachments/deliverables";

/**
 * Moves the reader to a file's new latest version as it lands, but only when
 * they were on the previous latest, and only once `ready` says the new
 * version's body is in hand — so the swap never shows a loading state. The
 * same follow rule as `useProgressiveDocument`, for files that have no diff
 * to mark.
 */
export function useFollowLatestVersion({
  file,
  viewedId,
  ready,
  onFollow,
}: {
  /** Undefined switches following off. */
  file: DeliverableFile | undefined;
  /** The upload on screen. */
  viewedId: string;
  /** The latest version's body has loaded (or failed to). */
  ready: boolean;
  onFollow: (id: string) => void;
}): void {
  const lastLatest = useRef<{ key: string; id: string } | null>(null);
  const followTo = useRef<string | null>(null);

  useEffect(() => {
    if (!file) {
      lastLatest.current = null;
      followTo.current = null;
      return;
    }
    const latest = file.latest;
    const before = lastLatest.current;
    if (before && before.key === file.key && before.id !== latest.id && viewedId === before.id) {
      followTo.current = latest.id;
    }
    lastLatest.current = { key: file.key, id: latest.id };
    if (followTo.current === latest.id && ready) {
      followTo.current = null;
      onFollow(latest.id);
    }
  }, [file, viewedId, ready, onFollow]);
}
