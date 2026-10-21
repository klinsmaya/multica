"use client";

import { memo, useCallback } from "react";
import type { Issue } from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { useActorName } from "@multica/core/workspace/hooks";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { LabelChip } from "../../labels/label-chip";
import { useNavigation } from "../../navigation";
import { useT, useTimeAgo } from "../../i18n";
import {
  PEEK_TARGET_ATTR,
  useIsIssuePeeked,
  useIssuePeekActions,
} from "../surface/peek-context";
import { descriptionPreview } from "./description-preview";
import type { FeedReadState } from "./feed-read-state";

const MAX_LABELS = 3;

/**
 * Opens an issue from the feed. The feed's whole purpose is reading threads
 * next to the stream, so a plain click always peeks (regardless of the
 * page/peek preference); outside a peek host it falls back to the full page.
 */
export function useOpenFeedIssue(issueId: string) {
  const peek = useIssuePeekActions();
  const { push } = useNavigation();
  const paths = useWorkspacePaths();
  return useCallback(() => {
    if (peek) peek.open(issueId);
    else push(paths.issueDetail(issueId));
  }, [peek, push, paths, issueId]);
}

/**
 * Unread marker on the card's left edge. It carries no text: the dot is an
 * image with an accessible name. "Awaiting" is the same dot with a ring, so it
 * stands out among plain unread cards.
 */
function FeedUnreadDot({ state }: { state: Exclude<FeedReadState, "read"> }) {
  const { t } = useT("issues");
  return (
    <span className="pointer-events-none absolute inset-y-0 left-1 flex items-center">
      <span
        role="img"
        aria-label={
          state === "awaiting"
            ? t(($) => $.feed.awaiting_reply)
            : t(($) => $.feed.unread)
        }
        data-read-state={state}
        className={cn(
          "size-1.5 rounded-full bg-brand",
          state === "awaiting" && "ring-[3px] ring-brand/25",
        )}
      />
    </span>
  );
}

export const FeedMessageCard = memo(function FeedMessageCard({
  issue,
  readState = "read",
}: {
  issue: Issue;
  readState?: FeedReadState;
}) {
  const timeAgo = useTimeAgo();
  const { getActorName } = useActorName();
  const open = useOpenFeedIssue(issue.id);
  const peeked = useIsIssuePeeked(issue.id);
  const labels = issue.labels ?? [];
  const preview = issue.description ? descriptionPreview(issue.description) : "";

  return (
    <div
      role="button"
      tabIndex={0}
      {...{ [PEEK_TARGET_ATTR]: issue.id }}
      data-peeked={peeked ? "" : undefined}
      onClick={open}
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        open();
      }}
      className={cn(
        "relative flex cursor-pointer gap-3 rounded-lg border-[0.5px] border-surface-border bg-surface p-3 shadow-[var(--surface-shadow)] transition-colors hover:border-foreground/15 hover:bg-surface-hover",
        peeked && "ring-2 ring-brand/50",
      )}
    >
      {readState !== "read" && <FeedUnreadDot state={readState} />}
      <ActorAvatar
        actorType={issue.creator_type}
        actorId={issue.creator_id}
        size="lg"
        profileLink={false}
        className="shrink-0"
      />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="min-w-0 truncate text-body font-medium">
            {getActorName(issue.creator_type, issue.creator_id)}
          </span>
          <span className="shrink-0 text-caption text-muted-foreground">
            {timeAgo(issue.created_at)}
          </span>
          <span className="ml-auto shrink-0 text-caption text-muted-foreground">
            {issue.identifier}
          </span>
        </div>
        <p className="mt-0.5 text-body font-medium leading-snug break-words">
          {issue.title}
        </p>
        {preview && (
          <p className="mt-1 text-caption text-muted-foreground line-clamp-3 break-words">
            {preview}
          </p>
        )}
        {labels.length > 0 && (
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {labels.slice(0, MAX_LABELS).map((label) => (
              <LabelChip key={label.id} label={label} />
            ))}
            {labels.length > MAX_LABELS && (
              <span className="text-micro text-muted-foreground">
                +{labels.length - MAX_LABELS}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
});
