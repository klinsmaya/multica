"use client";

import { CheckCheck, X } from "lucide-react";
import type { FeedReplyTarget } from "@multica/core/issues/stores";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { useOpenFeedIssue } from "./feed-message-card";

function ForwardedLink({ target }: { target: FeedReplyTarget }) {
  const open = useOpenFeedIssue(target.issueId);
  return (
    <Button type="button" variant="link" size="xs" className="h-auto p-0" onClick={open}>
      {target.identifier}
    </Button>
  );
}

/**
 * Receipt after one message went to several waiting threads: the issues it
 * reached, each opening its peek. Purely transient; the feed owns when it goes.
 */
export function FeedForwardSummary({
  targets,
  onDismiss,
}: {
  targets: FeedReplyTarget[];
  onDismiss: () => void;
}) {
  const { t } = useT("issues");
  return (
    <div className="shrink-0">
      <div className="mx-auto w-full max-w-3xl px-4 pb-2">
        <div
          data-testid="feed-forward-summary"
          className="flex items-center gap-2 rounded-lg border-[0.5px] border-surface-border bg-surface px-3 py-2 text-body"
        >
          <CheckCheck className="size-4 shrink-0 text-muted-foreground" />
          <span className="font-medium">
            {t(($) => $.feed.forward.title, { count: targets.length })}
          </span>
          <ul className="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1">
            {targets.map((target) => (
              <li key={target.issueId}>
                <ForwardedLink target={target} />
              </li>
            ))}
          </ul>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            className="shrink-0"
            aria-label={t(($) => $.feed.forward.dismiss)}
            onClick={onDismiss}
          >
            <X />
          </Button>
        </div>
      </div>
    </div>
  );
}
