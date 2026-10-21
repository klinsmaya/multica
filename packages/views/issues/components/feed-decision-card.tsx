"use client";

import { memo } from "react";
import type { IssueThreadSummary } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { useOpenFeedIssue } from "./feed-message-card";

/**
 * Agent questions waiting on the reader, under the issue's message card. Several
 * open questions fold into one card: the newest excerpt plus a "+N" count. The
 * button opens the issue's peek on the newest question's comment.
 */
export const FeedDecisionCard = memo(function FeedDecisionCard({
  issueId,
  summary,
}: {
  issueId: string;
  /** Undefined while the rollup loads or when the issue has no row. */
  summary: IssueThreadSummary | undefined;
}) {
  const { t } = useT("issues");
  // Newest first, as the server returns it.
  const awaitingMe = summary?.awaiting_me ?? [];
  const newest = awaitingMe[0];
  const open = useOpenFeedIssue(issueId, newest?.comment_id);
  if (!newest) return null;

  const more = awaitingMe.length - 1;

  return (
    <div className="ml-12 mt-1 flex items-start gap-2.5 rounded-lg border-[0.5px] border-brand/28 bg-brand/7 px-3 py-2">
      <ActorAvatar
        actorType="agent"
        actorId={newest.actor_id}
        size="md"
        profileLink={false}
        className="mt-0.5 shrink-0"
      />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5 text-micro font-medium text-brand">
          <span>{t(($) => $.feed.decision.title)}</span>
          {more > 0 && <span>+{more}</span>}
        </div>
        <p className="mt-0.5 text-caption text-foreground line-clamp-2 break-words">
          {newest.excerpt}
        </p>
      </div>
      <Button type="button" variant="outline" size="sm" className="shrink-0" onClick={open}>
        {t(($) => $.feed.decision.open_thread)}
      </Button>
    </div>
  );
});
