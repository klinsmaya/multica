"use client";

import { memo } from "react";
import { MessageSquare } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import type { Issue, IssueThreadSummary } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT, useTimeAgo } from "../../i18n";
import { useStatusLabel } from "../utils/status-label";
import { useIsIssuePeeked } from "../surface/peek-context";
import { useOpenFeedIssue } from "./feed-message-card";
import { IssueAgentActivityIndicator } from "./issue-agent-activity-indicator";
import { StatusIcon } from "./status-icon";

const MAX_PARTICIPANTS = 3;

function FeedStatusChip({ status }: { status: Issue["status"] }) {
  const wsId = useWorkspaceId();
  const labelOf = useStatusLabel(wsId);
  const { categoryOf, colorOf, iconOf } = useIssueStatuses(wsId);
  return (
    <span className="inline-flex max-w-[160px] items-center gap-1 rounded-full bg-muted/60 px-1.5 py-0.5 text-micro text-muted-foreground">
      <StatusIcon
        status={status}
        category={categoryOf(status)}
        color={colorOf(status)}
        icon={iconOf(status)}
        className="size-3"
      />
      <span className="truncate">{labelOf(status)}</span>
    </span>
  );
}

/**
 * The row under a feed message: reply count, who took part, when it last moved
 * and where the issue stands. `summary` is undefined both while the rollup is
 * loading and when the server has no row for the issue (no counted comments,
 * or a backend without the endpoint) — both read as an issue with no replies.
 */
export const FeedThreadBar = memo(function FeedThreadBar({
  issue,
  summary,
}: {
  issue: Issue;
  summary: IssueThreadSummary | undefined;
}) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const open = useOpenFeedIssue(issue.id);
  const peeked = useIsIssuePeeked(issue.id);

  const replyCount = summary?.reply_count ?? 0;
  const openThreadCount = summary?.open_thread_count ?? 0;
  const participants = summary?.participants ?? [];
  const shownParticipants = participants.slice(0, MAX_PARTICIPANTS);
  const hiddenParticipants = participants.length - shownParticipants.length;

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={open}
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        open();
      }}
      className={cn(
        "ml-12 mt-1 flex min-h-8 cursor-pointer flex-wrap items-center gap-x-3 gap-y-1 rounded-md px-2 py-1 text-caption text-muted-foreground transition-colors hover:bg-accent/60",
        peeked && "bg-accent/60",
      )}
    >
      <span className="inline-flex items-center gap-1.5 font-medium text-foreground">
        <MessageSquare className="size-3.5 text-muted-foreground" />
        {replyCount > 0
          ? t(($) => $.feed.reply_count, { count: replyCount })
          : t(($) => $.feed.open_thread)}
      </span>
      {shownParticipants.length > 0 && (
        <span className="inline-flex items-center">
          {shownParticipants.map((participant) => (
            <ActorAvatar
              key={`${participant.type}:${participant.id}`}
              actorType={participant.type}
              actorId={participant.id}
              size="xs"
              profileLink={false}
              className="-ml-1 first:ml-0 ring-2 ring-background"
            />
          ))}
          {hiddenParticipants > 0 && (
            <span className="ml-1 text-micro">+{hiddenParticipants}</span>
          )}
        </span>
      )}
      {summary?.last_reply_at && (
        <span>{t(($) => $.feed.last_reply, { time: timeAgo(summary.last_reply_at) })}</span>
      )}
      <span className="ml-auto inline-flex items-center gap-2">
        {openThreadCount > 0 && (
          <span className="rounded-full bg-brand/10 px-1.5 py-0.5 text-micro font-medium text-brand">
            {t(($) => $.feed.open_threads, { count: openThreadCount })}
          </span>
        )}
        <IssueAgentActivityIndicator issueId={issue.id} />
        <FeedStatusChip status={issue.status} />
      </span>
    </div>
  );
});
