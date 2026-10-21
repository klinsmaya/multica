"use client";

import { useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Virtuoso } from "react-virtuoso";
import { MessagesSquare } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { threadSummariesOptions } from "@multica/core/issues/queries";
import type { Issue } from "@multica/core/types";
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
      <Virtuoso
        className="flex-1 min-h-0"
        data={messages}
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
      {quickActions}
    </div>
  );
}
