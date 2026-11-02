"use client";

import { memo } from "react";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import type { IssueThreadChild, IssueThreadSummary } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT, useTimeAgo } from "../../i18n";
import { AppLink } from "../../navigation";
import { useIsIssuePeeked } from "../surface/peek-context";
import { useIssueSurfaceActivity } from "../surface/activity";
import { useStatusLabel } from "../utils/status-label";
import { useOpenFeedIssue } from "./feed-message-card";
import { StatusIcon } from "./status-icon";

function ThreadCard({ child, isWorking }: { child: IssueThreadChild; isWorking: boolean }) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const wsId = useWorkspaceId();
  const labelOf = useStatusLabel(wsId);
  const { categoryOf, colorOf, iconOf } = useIssueStatuses(wsId);
  const open = useOpenFeedIssue(child.id);
  const peeked = useIsIssuePeeked(child.id);

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
        "flex min-h-8 cursor-pointer items-center gap-2 rounded-md border-[0.5px] border-surface-border bg-surface px-2.5 py-1.5 text-caption transition-colors hover:border-foreground/15 hover:bg-surface-hover",
        peeked && "ring-2 ring-brand/50",
      )}
    >
      <span role="img" aria-label={labelOf(child.status)} className="inline-flex shrink-0">
        <StatusIcon
          status={child.status}
          category={categoryOf(child.status)}
          color={colorOf(child.status)}
          icon={iconOf(child.status)}
          className="size-3.5"
        />
      </span>
      <span className="shrink-0 text-micro text-muted-foreground">{child.identifier}</span>
      <span className="min-w-0 flex-1 truncate font-medium text-foreground">{child.title}</span>
      {isWorking && (
        <span
          role="img"
          aria-label={t(($) => $.agent_activity.status_running)}
          className="size-2 shrink-0 animate-pulse rounded-full bg-brand"
        />
      )}
      {child.assignee_type && child.assignee_id && (
        <ActorAvatar
          actorType={child.assignee_type}
          actorId={child.assignee_id}
          size="xs"
          profileLink={false}
          className="shrink-0"
        />
      )}
      {child.reply_count > 0 && (
        <span className="shrink-0 text-muted-foreground">
          {t(($) => $.feed.reply_count, { count: child.reply_count })}
        </span>
      )}
      {child.last_reply_at && (
        <span className="hidden shrink-0 text-micro text-muted-foreground sm:inline">
          {timeAgo(child.last_reply_at)}
        </span>
      )}
    </div>
  );
}

function ThreadCardList({
  parentId,
  cards,
  total,
}: {
  parentId: string;
  cards: IssueThreadChild[];
  total: number;
}) {
  const { t } = useT("issues");
  const paths = useWorkspacePaths();
  // The task snapshot is workspace-wide, so child issues are covered.
  const { activityByIssueId } = useIssueSurfaceActivity();
  const hidden = total - cards.length;

  return (
    <div className="ml-12 mt-1 flex flex-col gap-1">
      {cards.map((child) => (
        <ThreadCard
          key={child.id}
          child={child}
          isWorking={activityByIssueId.get(child.id)?.isWorking ?? false}
        />
      ))}
      {hidden > 0 && (
        <AppLink
          href={paths.issueDetail(parentId)}
          className="self-start rounded-md px-2.5 py-1 text-caption text-muted-foreground transition-colors hover:bg-accent/60 hover:text-foreground"
        >
          {t(($) => $.feed.children.more, { count: hidden })}
        </AppLink>
      )}
    </div>
  );
}

/**
 * The child issues a message was handed to, as compact cards under it (and its
 * decision card). Each opens its issue in the peek; when more children exist
 * than the summary lists, a last row opens the parent. Renders nothing for an
 * issue without children, without subscribing to agent activity.
 */
export const FeedThreadCards = memo(function FeedThreadCards({
  issueId,
  summary,
}: {
  issueId: string;
  /** Undefined while the rollup loads or when the issue has no row. */
  summary: IssueThreadSummary | undefined;
}) {
  const children = summary?.children ?? [];
  if (children.length === 0) return null;
  return (
    <ThreadCardList parentId={issueId} cards={children} total={summary?.child_total ?? 0} />
  );
});
