"use client";

import { memo, useEffect } from "react";
import { useFeedReplyTargetsStore } from "@multica/core/issues/stores";
import type { IssueThreadSummary } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { useOpenFeedIssue } from "./feed-message-card";

/**
 * Agent questions waiting on the reader, under the issue's message card. Several
 * open questions fold into one card: the newest excerpt plus a "+N" count. The
 * button opens the issue's peek on the newest question's comment; the checkbox
 * adds the thread to the composer's reply targets without opening anything.
 */
export const FeedDecisionCard = memo(function FeedDecisionCard({
  issueId,
  identifier,
  summary,
}: {
  issueId: string;
  /** Shown on the composer chip once the thread is selected. */
  identifier: string;
  /** Undefined while the rollup loads or when the issue has no row. */
  summary: IssueThreadSummary | undefined;
}) {
  const { t } = useT("issues");
  // Newest first, as the server returns it.
  const awaitingMe = summary?.awaiting_me ?? [];
  const newest = awaitingMe[0];
  const open = useOpenFeedIssue(issueId, newest?.comment_id);
  const selected = useFeedReplyTargetsStore((s) => issueId in s.targets);
  const newestCommentId = newest?.comment_id;

  // The selection follows the thread: a newer question moves the target to its
  // comment, and a question answered elsewhere drops it from the composer.
  useEffect(() => {
    if (!selected) return;
    const { select, deselect } = useFeedReplyTargetsStore.getState();
    if (newestCommentId) select({ issueId, commentId: newestCommentId, identifier });
    else if (summary) deselect(issueId);
  }, [selected, issueId, identifier, newestCommentId, summary]);

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
      <div className="flex shrink-0 items-center gap-3">
        <label className="flex cursor-pointer items-center gap-1.5 text-caption text-muted-foreground">
          <Checkbox
            checked={selected}
            onCheckedChange={() =>
              useFeedReplyTargetsStore
                .getState()
                .toggle({ issueId, commentId: newest.comment_id, identifier })
            }
          />
          {t(($) => $.feed.decision.reply_here)}
        </label>
        <Button type="button" variant="outline" size="sm" onClick={open}>
          {t(($) => $.feed.decision.open_thread)}
        </Button>
      </div>
    </div>
  );
});
