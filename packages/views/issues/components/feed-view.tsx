"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { ArrowDown, MessagesSquare } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { threadSummariesOptions } from "@multica/core/issues/queries";
import type { Issue } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { useIssuePeekActions } from "../surface/peek-context";
import { FeedMessageCard } from "./feed-message-card";
import { FeedQuickActions } from "./feed-quick-actions";
import { FeedThreadBar } from "./feed-thread-bar";

/**
 * Top-level issues of a scope as a chat-like stream: oldest at the top, newest
 * at the bottom, opened scrolled to the newest. Sort and grouping preferences
 * do not apply — a feed is chronological by definition.
 */
export function FeedView({
  issues,
  projectId,
}: {
  issues: Issue[];
  projectId: string | null;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const peek = useIssuePeekActions();

  const messages = useMemo(
    () =>
      issues
        .filter((issue) => issue.parent_issue_id == null)
        .sort(
          (a, b) =>
            new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
        ),
    [issues],
  );
  const messageIds = useMemo(() => messages.map((issue) => issue.id), [messages]);

  // A failed or unsupported summaries request leaves `data` undefined; the
  // cards render without thread details instead of surfacing an error.
  const { data: summaries } = useQuery(threadSummariesOptions(wsId, messageIds));

  // Whether the reader is parked at the newest message lives in a ref: it only
  // decides what to do when the list grows, so it must not re-render the feed.
  const listRef = useRef<VirtuosoHandle>(null);
  const atBottomRef = useRef(true);
  const lastSeenRef = useRef({ count: messages.length, lastId: messages.at(-1)?.id });
  const [hasNewMessages, setHasNewMessages] = useState(false);

  useEffect(() => {
    const prev = lastSeenRef.current;
    const lastId = messages.at(-1)?.id;
    lastSeenRef.current = { count: messages.length, lastId };
    // The first population is covered by `initialTopMostItemIndex`.
    if (prev.count === 0 || messages.length <= prev.count || lastId === prev.lastId) return;
    if (atBottomRef.current) {
      listRef.current?.scrollToIndex({ index: "LAST", align: "end", behavior: "smooth" });
    } else {
      setHasNewMessages(true);
    }
  }, [messages]);

  const handleAtBottomStateChange = useCallback((atBottom: boolean) => {
    atBottomRef.current = atBottom;
    if (atBottom) setHasNewMessages(false);
  }, []);

  const scrollToNewest = useCallback(() => {
    listRef.current?.scrollToIndex({ index: "LAST", align: "end", behavior: "smooth" });
    setHasNewMessages(false);
  }, []);

  useEffect(() => {
    peek?.publishColumns([messageIds]);
  }, [peek, messageIds]);
  useEffect(() => () => peek?.publishColumns(null), [peek]);

  // The bottom composer marks its input with `data-feed-composer-input`.
  const focusComposer = () => {
    document.querySelector<HTMLElement>("[data-feed-composer-input]")?.focus();
  };
  const quickActions = (
    <FeedQuickActions
      issues={issues}
      projectId={projectId}
      onNewIssue={focusComposer}
    />
  );

  if (messages.length === 0) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-3 text-muted-foreground">
          <MessagesSquare className="h-10 w-10 text-faint-foreground" />
          <p className="text-body">{t(($) => $.feed.empty)}</p>
        </div>
        {quickActions}
      </div>
    );
  }

  return (
    <div className="flex flex-1 min-h-0 flex-col">
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
              <FeedMessageCard issue={issue} />
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
      {quickActions}
    </div>
  );
}
