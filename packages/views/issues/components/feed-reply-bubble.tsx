"use client";

import { memo, useLayoutEffect, useRef, useState } from "react";
import type { IssueThreadSummary } from "@multica/core/types";
import { useActorName } from "@multica/core/workspace/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { ReadonlyContent } from "../../editor";
import { useT, useTimeAgo } from "../../i18n";
import { useOpenFeedIssue } from "./feed-message-card";

type LastReply = NonNullable<IssueThreadSummary["last_reply"]>;

/**
 * The reply's Markdown, clamped to about six lines. The toggle only appears
 * when the clamped content actually overflows; it is measured when the content
 * or the clamp changes, so later layout shifts are not tracked.
 */
function ReplyBody({ reply }: { reply: LastReply }) {
  const { t } = useT("issues");
  const ref = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);

  useLayoutEffect(() => {
    const el = ref.current;
    // An expanded body never overflows, so measuring then would hide the
    // collapse toggle; only the clamped state is measured.
    if (!el || expanded) return;
    setOverflowing(el.scrollHeight > el.clientHeight);
  }, [reply.content, expanded]);

  return (
    <>
      <div ref={ref} className={cn("min-w-0 break-words", !expanded && "line-clamp-6")}>
        <ReadonlyContent content={reply.content} />
      </div>
      {(overflowing || expanded) && (
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="mt-0.5 -ml-1.5 text-muted-foreground"
          aria-expanded={expanded}
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? t(($) => $.feed.reply.collapse) : t(($) => $.feed.reply.expand)}
        </Button>
      )}
    </>
  );
}

/**
 * The newest reply of an issue, as a chat bubble under its message card. It
 * renders the stored Markdown with the same renderer comments use; "view
 * thread" opens the issue's peek at that comment.
 */
export const FeedReplyBubble = memo(function FeedReplyBubble({
  issueId,
  identifier,
  summary,
}: {
  issueId: string;
  /** Tells the repeated "view thread" actions apart for assistive tech. */
  identifier: string;
  /** Undefined while the rollup loads or when the issue has no row. */
  summary: IssueThreadSummary | undefined;
}) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const { getActorName } = useActorName();
  const reply = summary?.last_reply ?? null;
  const open = useOpenFeedIssue(issueId, reply?.comment_id);

  if (!reply || !reply.content.trim()) return null;

  return (
    <div className="group ml-12 mt-1 flex items-start gap-2.5 rounded-lg bg-muted/40 px-3 py-2">
      <ActorAvatar
        actorType={reply.author_type}
        actorId={reply.author_id}
        size="md"
        profileLink={false}
        className="mt-0.5 shrink-0"
      />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="min-w-0 truncate text-caption font-medium">
            {getActorName(reply.author_type, reply.author_id)}
          </span>
          <span className="shrink-0 text-micro text-muted-foreground">
            {timeAgo(reply.created_at)}
          </span>
          <Button
            type="button"
            variant="ghost"
            size="xs"
            className="ml-auto shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100"
            onClick={open}
          >
            {t(($) => $.feed.reply.open_thread)}
            <span className="sr-only"> {identifier}</span>
          </Button>
        </div>
        <ReplyBody key={reply.comment_id} reply={reply} />
      </div>
    </div>
  );
});
