"use client";

import { useState } from "react";

/**
 * react-virtuoso keeps the viewport still when items are prepended only if
 * `firstItemIndex` drops by the number of prepended items. It must stay
 * non-negative, so it starts high.
 */
export const VIRTUOSO_START_INDEX = 1_000_000;

export interface FirstItemIndexState {
  firstKey: string | undefined;
  index: number;
}

const INITIAL_STATE: FirstItemIndexState = {
  firstKey: undefined,
  index: VIRTUOSO_START_INDEX,
};

/**
 * Pure step of the prepend tracking. When the previous first item is still in
 * the list but no longer first, everything before it was prepended: lower the
 * index by that many items. Anything else (same first item, items appended,
 * first item gone because it was edited away or regrouped, empty list) leaves
 * the index alone — guessing there would move the viewport instead of keeping
 * it.
 */
export function nextFirstItemIndex(
  previous: FirstItemIndexState,
  keys: readonly string[],
): FirstItemIndexState {
  const firstKey = keys[0];
  if (firstKey === undefined || previous.firstKey === firstKey) return previous;
  if (previous.firstKey === undefined) return { firstKey, index: previous.index };
  const at = keys.indexOf(previous.firstKey);
  return {
    firstKey,
    index: at > 0 ? Math.max(0, previous.index - at) : previous.index,
  };
}

/**
 * `firstItemIndex` for a Virtuoso list that grows at the top. `resetKey`
 * identifies the list (the issue); a new key starts over, matching the Virtuoso
 * remount that keys on the same value.
 *
 * Derived during render (the "adjust state while rendering" pattern) so the
 * value is already right for the render that carries the prepended data.
 */
export function usePrependedFirstItemIndex(
  keys: readonly string[],
  resetKey: string,
): number {
  const [state, setState] = useState(() => ({
    resetKey,
    ...INITIAL_STATE,
  }));
  const current = state.resetKey === resetKey ? state : { resetKey, ...INITIAL_STATE };
  const next = nextFirstItemIndex(current, keys);
  if (next !== state) setState({ resetKey, ...next });
  return next.index;
}
