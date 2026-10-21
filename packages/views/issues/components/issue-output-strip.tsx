"use client";

import { FileText } from "lucide-react";
import type {
  IssueThreadPullRequest,
  IssueThreadPullRequestChecksState,
  IssueThreadSummary,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT, useTimeAgo } from "../../i18n";
import { PullRequestChecksIcon, PullRequestStateIcon } from "./pull-request-list";

type IssuesT = ReturnType<typeof useT<"issues">>["t"];

const CHIP_CLASS =
  "inline-flex h-5 max-w-full shrink-0 items-center gap-1 whitespace-nowrap rounded-full bg-muted/60 px-1.5 text-micro text-muted-foreground tabular-nums transition-colors";
const INTERACTIVE_CHIP_CLASS = "hover:bg-muted hover:text-foreground";

// Only web links are followed: the URL comes from a provider payload.
const WEB_URL = /^https?:\/\//i;

function stateLabel(state: string, t: IssuesT): string {
  switch (state) {
    case "open":
      return t(($) => $.feed.output.state_open);
    case "draft":
      return t(($) => $.detail.pull_request_state_draft);
    case "merged":
      return t(($) => $.detail.pull_request_state_merged);
    case "closed":
      return t(($) => $.detail.pull_request_state_closed);
    default:
      return state;
  }
}

function checksLabel(state: IssueThreadPullRequestChecksState, t: IssuesT): string | null {
  switch (state) {
    case "passed":
      return t(($) => $.detail.pull_request_verdict_passed);
    case "failed":
      return t(($) => $.feed.output.checks_failed);
    case "pending":
      return t(($) => $.feed.output.checks_pending);
    default:
      return null;
  }
}

function PullRequestChip({ pr }: { pr: IssueThreadPullRequest }) {
  const { t } = useT("issues");
  // A merged or closed PR has no CI left to report.
  const checks = pr.state === "merged" || pr.state === "closed" ? "none" : pr.checks_state;
  const checksText = checksLabel(checks, t);
  const label = [t(($) => $.feed.output.pull_request, { number: pr.number, state: stateLabel(pr.state, t) }), checksText]
    .filter(Boolean)
    .join(", ");
  const body = (
    <>
      <PullRequestStateIcon state={pr.state} className="size-3 shrink-0" />
      <span>#{pr.number}</span>
      <PullRequestChecksIcon state={checks} className="size-3 shrink-0" />
    </>
  );

  if (!WEB_URL.test(pr.url)) {
    return (
      <span data-testid="output-pull-request" className={CHIP_CLASS} title={pr.title} aria-label={label}>
        {body}
      </span>
    );
  }
  return (
    <a
      data-testid="output-pull-request"
      href={pr.url}
      target="_blank"
      rel="noreferrer noopener"
      title={pr.title}
      aria-label={label}
      // The strip also sits inside the feed's clickable thread bar.
      onClick={(event) => event.stopPropagation()}
      className={cn(CHIP_CLASS, INTERACTIVE_CHIP_CLASS)}
    >
      {body}
    </a>
  );
}

function DeliverableChip({
  count,
  lastAt,
  onOpen,
}: {
  count: number;
  lastAt: string | null;
  onOpen?: () => void;
}) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const updated = lastAt ? t(($) => $.feed.output.deliverables_updated, { time: timeAgo(lastAt) }) : undefined;
  const body = (
    <>
      <FileText aria-hidden="true" className="size-3 shrink-0" />
      <span>{t(($) => $.feed.output.deliverables, { count })}</span>
      {lastAt ? (
        <>
          <span aria-hidden="true" className="text-faint-foreground">
            ·
          </span>
          <span className="truncate">{timeAgo(lastAt)}</span>
        </>
      ) : null}
    </>
  );

  if (!onOpen) {
    return (
      <span data-testid="output-deliverables" className={CHIP_CLASS} title={updated}>
        {body}
      </span>
    );
  }
  return (
    <button
      type="button"
      data-testid="output-deliverables"
      title={updated}
      onClick={(event) => {
        event.stopPropagation();
        onOpen();
      }}
      className={cn(CHIP_CLASS, INTERACTIVE_CHIP_CLASS)}
    >
      {body}
    </button>
  );
}

/**
 * What an issue has produced: its newest linked PRs with their state and CI,
 * and one chip counting its deliverable files. Renders nothing for an issue
 * without either. A PR chip opens the PR in a new tab; the deliverables chip
 * calls `onOpenDeliverables`, and is plain text without it (the peek is
 * already that issue).
 */
export function IssueOutputStrip({
  summary,
  onOpenDeliverables,
  className,
}: {
  summary: Pick<IssueThreadSummary, "pull_requests" | "deliverable_count" | "last_deliverable_at"> | undefined;
  onOpenDeliverables?: () => void;
  className?: string;
}) {
  const pullRequests = summary?.pull_requests ?? [];
  const deliverableCount = summary?.deliverable_count ?? 0;
  if (pullRequests.length === 0 && deliverableCount <= 0) return null;
  return (
    <div data-testid="issue-output-strip" className={cn("flex flex-wrap items-center gap-1.5", className)}>
      {pullRequests.map((pr) => (
        <PullRequestChip key={pr.id} pr={pr} />
      ))}
      {deliverableCount > 0 ? (
        <DeliverableChip
          count={deliverableCount}
          lastAt={summary?.last_deliverable_at ?? null}
          onOpen={onOpenDeliverables}
        />
      ) : null}
    </div>
  );
}
