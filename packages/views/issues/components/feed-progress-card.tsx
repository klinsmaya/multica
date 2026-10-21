"use client";

import { memo, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Square, SquareCheck } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { threadSummariesOptions } from "@multica/core/issues/queries";
import { parseProgressChecklist } from "@multica/core/issues/progress-checklist";
import type { Issue, IssueLatestProgress } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

/** Items listed before the rest fold into "+N more". */
const MAX_VISIBLE_ITEMS = 5;

// Items are one-line previews, so inline Markdown collapses to its text.
function plainItemText(text: string): string {
  return text
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/[`*_~]/g, "")
    .trim();
}

/**
 * The agent's latest progress checklist, under an issue's feed message. A
 * finished issue (done or closed category) folds to its count; the chevron
 * opens the list. Renders nothing without a checklist.
 */
export const FeedProgressCard = memo(function FeedProgressCard({
  progress,
  status,
  className,
}: {
  /** Undefined while the rollup loads or when the issue has no row. */
  progress: IssueLatestProgress | null | undefined;
  status: Issue["status"];
  className?: string;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const { categoryOf } = useIssueStatuses(wsId);
  const [expanded, setExpanded] = useState(false);

  const content = progress?.content ?? "";
  const checklist = useMemo(() => parseProgressChecklist(content), [content]);
  if (checklist.total === 0) return null;

  const category = categoryOf(status);
  const finished = category === "done" || category === "closed";
  const showItems = !finished || expanded;
  const visible = checklist.items.slice(0, MAX_VISIBLE_ITEMS);
  const hidden = checklist.total - visible.length;
  const count = t(($) => $.feed.progress.count, {
    done: checklist.done,
    total: checklist.total,
  });
  const bar = (
    <span
      role="progressbar"
      aria-label={t(($) => $.feed.progress.label)}
      aria-valuemin={0}
      aria-valuemax={checklist.total}
      aria-valuenow={checklist.done}
      className="h-1 w-16 shrink-0 overflow-hidden rounded-full bg-muted"
    >
      <span
        className="block h-full rounded-full bg-brand"
        style={{ width: `${(checklist.done / checklist.total) * 100}%` }}
      />
    </span>
  );

  return (
    <div className={cn("ml-12 mt-1 px-2 text-caption text-muted-foreground", className)}>
      {finished ? (
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((open) => !open)}
          className="inline-flex items-center gap-1.5 rounded-sm hover:text-foreground"
        >
          {expanded ? (
            <ChevronDown className="size-3.5" aria-hidden />
          ) : (
            <ChevronRight className="size-3.5" aria-hidden />
          )}
          <span className="tabular-nums">{count}</span>
        </button>
      ) : (
        <div className="flex items-center gap-2">
          <span className="font-medium tabular-nums text-foreground">{count}</span>
          {bar}
        </div>
      )}
      {showItems && (
        <>
          {finished && <div className="mt-1">{bar}</div>}
          <ul className="mt-1 space-y-0.5">
            {visible.map((item, index) => (
              <li
                // Items repeat text and have no id; position is their identity.
                key={index}
                className={cn(
                  "flex items-start gap-1.5",
                  item.done && "text-faint-foreground line-through",
                )}
              >
                {item.done ? (
                  <SquareCheck className="mt-0.5 size-3.5 shrink-0" aria-hidden />
                ) : (
                  <Square className="mt-0.5 size-3.5 shrink-0" aria-hidden />
                )}
                <span className="sr-only">
                  {item.done
                    ? t(($) => $.feed.progress.item_done)
                    : t(($) => $.feed.progress.item_open)}
                </span>
                <span className="min-w-0 truncate">{plainItemText(item.text)}</span>
              </li>
            ))}
          </ul>
          {hidden > 0 && (
            <p className="mt-0.5 pl-5 text-micro">
              {t(($) => $.feed.progress.more, { number: hidden })}
            </p>
          )}
        </>
      )}
    </div>
  );
});

/**
 * The checklist inside an issue's peek, which has no summary of its own: it
 * reads the thread rollup for this one issue (kept live by the comment
 * realtime patch, like the feed's).
 */
export function PeekProgressCard({ issue }: { issue: Pick<Issue, "id" | "status"> }) {
  const wsId = useWorkspaceId();
  const { data } = useQuery(threadSummariesOptions(wsId, [issue.id]));
  return (
    <FeedProgressCard
      progress={data?.get(issue.id)?.latest_progress}
      status={issue.status}
      className="ml-0 mt-3 px-0"
    />
  );
}
