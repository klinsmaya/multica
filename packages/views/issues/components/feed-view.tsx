"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { ArrowDown, MessagesSquare } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { threadSummariesOptions } from "@multica/core/issues/queries";
import { useViewStore } from "@multica/core/issues/stores/view-store-context";
import type { Issue } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { useIssuePeekActions } from "../surface/peek-context";
import { FeedComposer, type FeedComposerHandle } from "./feed-composer";
import { FeedMessageCard } from "./feed-message-card";
import { getFeedReadState } from "./feed-read-state";
import { FeedQuickActions } from "./feed-quick-actions";
import { FeedThreadBar } from "./feed-thread-bar";

const SCROLL_TO_NEWEST = { index: "LAST", align: "end", behavior: "smooth" } as const;

/**
 * Top-level issues of a scope as a chat-like stream: oldest at the top, newest
 * at the bottom, opened scrolled to the newest. The feed's own sort picks
 * creation time or latest thread activity; the generic sort and grouping
 * preferences do not apply.
 */
export function FeedView({
  issues,
  projectId,
}: {
  issues: Issue[];
  /** Project new messages are created in; null scopes have no composer. */
  projectId: string | null;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const peek = useIssuePeekActions();

  const feedSort = useViewStore((s) => s.feedSort);

  const messages = useMemo(() => {
    const sortTime = (issue: Issue) =>
      new Date(
        feedSort === "activity"
          ? (issue.last_activity_at ?? issue.created_at)
          : issue.created_at,
      ).getTime();
    return issues
      .filter((issue) => issue.parent_issue_id == null)
      .sort((a, b) => sortTime(a) - sortTime(b));
  }, [issues, feedSort]);
  const messageIds = useMemo(() => messages.map((issue) => issue.id), [messages]);

  // A failed or unsupported summaries request leaves `data` undefined; the
  // cards render without thread details instead of surfacing an error.
  const { data: summaries } = useQuery(threadSummariesOptions(wsId, messageIds));
  const currentUserId = useAuthStore((s) => s.user?.id);

  // Whether the reader is parked at the newest message lives in a ref: it only
  // decides what to do when the list grows, so it must not re-render the feed.
  const listRef = useRef<VirtuosoHandle>(null);
  const atBottomRef = useRef(true);
  const lastSeenRef = useRef({ count: messages.length, lastId: messages.at(-1)?.id });
  const [hasNewMessages, setHasNewMessages] = useState(false);

  // A message the composer just sent is scrolled to once it shows up in the
  // list; the create response can land before the surface re-renders with it.
  const scrollToIssueId = useRef<string | null>(null);

  useEffect(() => {
    const prev = lastSeenRef.current;
    const lastId = messages.at(-1)?.id;
    lastSeenRef.current = { count: messages.length, lastId };

    const sentId = scrollToIssueId.current;
    if (sentId && messageIds.includes(sentId)) {
      scrollToIssueId.current = null;
      listRef.current?.scrollToIndex(SCROLL_TO_NEWEST);
      setHasNewMessages(false);
      return;
    }
    // The first population is covered by `initialTopMostItemIndex`.
    if (prev.count === 0 || messages.length <= prev.count || lastId === prev.lastId) return;
    if (atBottomRef.current) {
      listRef.current?.scrollToIndex(SCROLL_TO_NEWEST);
    } else {
      setHasNewMessages(true);
    }
  }, [messages, messageIds]);

  const handleAtBottomStateChange = useCallback((atBottom: boolean) => {
    atBottomRef.current = atBottom;
    if (atBottom) setHasNewMessages(false);
  }, []);

  const scrollToNewest = useCallback(() => {
    listRef.current?.scrollToIndex(SCROLL_TO_NEWEST);
    setHasNewMessages(false);
  }, []);

  const handleSent = useCallback(
    (issue: Issue) => {
      scrollToIssueId.current = issue.id;
      if (messageIds.includes(issue.id)) {
        scrollToIssueId.current = null;
        listRef.current?.scrollToIndex(SCROLL_TO_NEWEST);
        setHasNewMessages(false);
      }
    },
    [messageIds],
  );

  useEffect(() => {
    peek?.publishColumns([messageIds]);
  }, [peek, messageIds]);
  useEffect(() => () => peek?.publishColumns(null), [peek]);

  const composerRef = useRef<FeedComposerHandle>(null);
  const focusComposer = useCallback(() => composerRef.current?.focus(), []);

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      {messages.length === 0 ? (
        <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-3 text-muted-foreground">
          <MessagesSquare className="h-10 w-10 text-faint-foreground" />
          <p className="text-body">{t(($) => $.feed.empty)}</p>
        </div>
      ) : (
        <div className="relative flex flex-1 min-h-0 flex-col">
          <Virtuoso
            ref={listRef}
            className="flex-1 min-h-0"
            data={messages}
            atBottomStateChange={handleAtBottomStateChange}
            computeItemKey={(_, issue) => issue.id}
            initialTopMostItemIndex={{ index: messages.length - 1, align: "end" }}
            followOutput={false}
            itemContent={(_, issue) => (
              <div className="mx-auto w-full max-w-3xl px-4 py-2">
                <FeedMessageCard
                  issue={issue}
                  readState={getFeedReadState(summaries?.get(issue.id), currentUserId)}
                />
                <FeedThreadBar issue={issue} summary={summaries?.get(issue.id)} />
              </div>
            )}
          />
          {hasNewMessages && (
            <Button
              type="button"
              size="sm"
              variant="brand"
              className="absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-full shadow-md"
              onClick={scrollToNewest}
            >
              <ArrowDown />
              {t(($) => $.feed.new_messages)}
            </Button>
          )}
        </div>
      )}
      <FeedQuickActions issues={issues} projectId={projectId} onNewIssue={focusComposer} />
      {projectId && <FeedComposer ref={composerRef} projectId={projectId} onSent={handleSent} />}
    </div>
  );
}
