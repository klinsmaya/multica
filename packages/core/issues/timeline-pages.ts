import type { InfiniteData } from "@tanstack/react-query";
import type {
  TimelineEntry,
  TimelinePage,
  TimelinePageCursor,
} from "@multica/core/types";
import { sortTimelineEntriesAsc } from "./timeline-sort";

/**
 * The issue timeline cache is an infinite query whose pages are root-paged:
 * `pages[0]` is the NEWEST page (the first fetch) and every later page is older.
 * Older pages are therefore the query's "next" pages — `fetchNextPage` appends,
 * so `pages[0]` stays the newest page across refetches, and a refetch re-walks
 * the cursors from newest to oldest. A page always holds whole threads: a root
 * and every reply of it.
 *
 * Everything that reads or patches that cache goes through this module, so the
 * pages structure stays an implementation detail of the cache.
 */
export type TimelinePagesCache = InfiniteData<
  TimelinePage,
  TimelinePageCursor | null
>;

/** What the helpers need of a cache: its loaded pages, newest first. */
export interface TimelinePagesLike {
  pages: TimelinePage[];
  pageParams?: unknown[];
}

/** Root comments per timeline page. */
export const TIMELINE_PAGE_SIZE = 50;

/** Cursor of the next OLDER page, or undefined when this page is the last. */
export function nextTimelinePageCursor(
  page: TimelinePage,
): TimelinePageCursor | undefined {
  if (!page.has_more_before || !page.next_before || !page.next_before_id) {
    return undefined;
  }
  return { before: page.next_before, beforeId: page.next_before_id };
}

/** A cache holding one page with no older page, for seeding and tests. */
export function singleTimelinePage(
  entries: TimelineEntry[],
): TimelinePagesCache {
  return {
    pages: [
      {
        entries,
        has_more_before: false,
        has_more_after: false,
        next_before: null,
        next_before_id: null,
      },
    ],
    pageParams: [null],
  };
}

/**
 * Every loaded entry as one flat list, oldest first, each id once. An entry can
 * sit on two pages for a moment (a realtime patch and a refetch racing); the
 * newest page wins.
 */
export function flattenTimelinePages(
  data: TimelinePagesLike | undefined,
): TimelineEntry[] {
  if (!data) return [];
  const byId = new Map<string, TimelineEntry>();
  for (const page of data.pages) {
    for (const entry of page.entries) {
      if (!byId.has(entry.id)) byId.set(entry.id, entry);
    }
  }
  return sortTimelineEntriesAsc([...byId.values()]);
}

/** Whether an older page can still be fetched. */
export function hasOlderTimelinePage(
  data: TimelinePagesLike | undefined,
): boolean {
  const last = data?.pages[data.pages.length - 1];
  return !!last && nextTimelinePageCursor(last) !== undefined;
}

/**
 * Applies a flat-list edit to the paged cache. `edit` receives every loaded
 * entry oldest first and returns the new list; entries it keeps stay on the page
 * they were on, so a filter, map or tombstone applies across all pages at once.
 * An entry it adds lands on the page of its parent when the parent is loaded and
 * on the newest page otherwise (a new root or an activity is always the newest
 * thing there is).
 *
 * Returns `data` itself when `edit` hands back the list it was given, and the
 * original page object for every page whose entries did not change, so memoized
 * consumers keep their references. A cache that has not loaded yet is left
 * alone: seeding it would present a partial timeline as complete.
 */
export function updateTimelinePages<T extends TimelinePagesLike>(
  data: T | undefined,
  edit: (entries: TimelineEntry[]) => TimelineEntry[],
): T | undefined {
  if (!data || data.pages.length === 0) return data;

  const flat = flattenTimelinePages(data);
  const next = edit(flat);
  if (next === flat) return data;

  const pageOf = new Map<string, number>();
  data.pages.forEach((page, index) => {
    for (const entry of page.entries) {
      if (!pageOf.has(entry.id)) pageOf.set(entry.id, index);
    }
  });

  const buckets: TimelineEntry[][] = data.pages.map(() => []);
  for (const entry of next) {
    const index =
      pageOf.get(entry.id) ??
      (entry.parent_id ? pageOf.get(entry.parent_id) : undefined) ??
      0;
    buckets[index]!.push(entry);
  }

  let changed = false;
  const pages = data.pages.map((page, index) => {
    const bucket = buckets[index]!;
    if (sameEntries(page.entries, bucket)) return page;
    changed = true;
    return { ...page, entries: bucket };
  });
  return changed ? { ...data, pages } : data;
}

/** Same entries (by reference), ignoring order. */
function sameEntries(a: TimelineEntry[], b: TimelineEntry[]): boolean {
  if (a.length !== b.length) return false;
  const set = new Set(a);
  return b.every((entry) => set.has(entry));
}
