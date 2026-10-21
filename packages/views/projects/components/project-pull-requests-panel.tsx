"use client";

import { useState } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { deriveChecksStatus, formatPullRequestDiffCount } from "@multica/core/github";
import { useWorkspaceId } from "@multica/core/hooks";
import { projectPullRequestsOptions } from "@multica/core/projects/queries";
import type {
  IssueThreadPullRequestChecksState,
  ProjectPullRequest,
  ProjectPullRequestIssue,
  ProjectPullRequestState,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT, useTimeAgo } from "../../i18n";
import { useOpenFeedIssue } from "../../issues/components/feed-message-card";
import { PullRequestChecksIcon, PullRequestStateIcon } from "../../issues/components/pull-request-list";

// Each group is its own server query (`state=`), fetched when the group is
// expanded and paged on demand. The Open group's first page doubles as the
// panel's source for every group's count, so collapsed groups cost nothing.
const PAGE_SIZE = 20;
const GROUPS: ProjectPullRequestState[] = ["open", "merged", "closed"];
const WEB_URL = /^https?:\/\//i;

export function ProjectPullRequestsPanel({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const [expanded, setExpanded] = useState<Record<ProjectPullRequestState, boolean>>({
    open: true,
    merged: false,
    closed: false,
  });
  const summary = useQuery(
    projectPullRequestsOptions(wsId, projectId, { state: "open", limit: PAGE_SIZE, offset: 0 }),
  );

  let body;
  if (summary.isPending) {
    body = <p className="px-2 text-caption text-muted-foreground">{t(($) => $.pull_requests.loading)}</p>;
  } else if (summary.isError) {
    body = <LoadError onRetry={() => void summary.refetch()} />;
  } else {
    const counts = summary.data.counts;
    if (counts.open + counts.merged + counts.closed === 0) {
      body = <p className="px-2 text-caption text-muted-foreground">{t(($) => $.pull_requests.empty)}</p>;
    } else {
      body = (
        <div className="space-y-1">
          {GROUPS.map((state) => (
            <PullRequestGroup
              key={state}
              projectId={projectId}
              state={state}
              count={counts[state]}
              open={expanded[state]}
              onToggle={() => setExpanded((prev) => ({ ...prev, [state]: !prev[state] }))}
            />
          ))}
        </div>
      );
    }
  }

  return <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-2">{body}</div>;
}

function LoadError({ onRetry }: { onRetry: () => void }) {
  const { t } = useT("projects");
  return (
    <div className="flex items-center gap-2 px-2 text-caption text-muted-foreground">
      <span>{t(($) => $.pull_requests.load_failed)}</span>
      <Button variant="outline" size="xs" onClick={onRetry}>
        {t(($) => $.pull_requests.retry)}
      </Button>
    </div>
  );
}

function PullRequestGroup({
  projectId,
  state,
  count,
  open,
  onToggle,
}: {
  projectId: string;
  state: ProjectPullRequestState;
  count: number;
  open: boolean;
  onToggle: () => void;
}) {
  const { t } = useT("projects");
  const label =
    state === "open"
      ? t(($) => $.pull_requests.group_open)
      : state === "merged"
        ? t(($) => $.pull_requests.group_merged)
        : t(($) => $.pull_requests.group_closed);
  const empty = count === 0;
  return (
    <section data-testid={`project-pull-requests-${state}`}>
      <button
        type="button"
        aria-expanded={open && !empty}
        disabled={empty}
        onClick={onToggle}
        className="flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-caption font-medium hover:bg-accent/50 disabled:text-muted-foreground disabled:hover:bg-transparent"
      >
        <ChevronRight
          aria-hidden="true"
          className={cn(
            "size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
            open && !empty && "rotate-90",
          )}
        />
        <span className="min-w-0 flex-1 truncate">{label}</span>
        <span className="shrink-0 tabular-nums text-muted-foreground">{count}</span>
      </button>
      {open && !empty ? <PullRequestGroupBody projectId={projectId} state={state} count={count} /> : null}
    </section>
  );
}

function PullRequestGroupBody({
  projectId,
  state,
  count,
}: {
  projectId: string;
  state: ProjectPullRequestState;
  count: number;
}) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const [pages, setPages] = useState(1);
  // One query per loaded page: a realtime update refetches them together and
  // every page keeps its own cache entry across collapse and expand.
  const results = useQueries({
    queries: Array.from({ length: pages }, (_, page) =>
      projectPullRequestsOptions(wsId, projectId, {
        state,
        limit: PAGE_SIZE,
        offset: page * PAGE_SIZE,
      }),
    ),
  });
  const first = results[0];
  const last = results[results.length - 1];

  if (!first || first.isPending) {
    return <p className="px-2 py-1.5 text-caption text-muted-foreground">{t(($) => $.pull_requests.loading)}</p>;
  }
  if (first.isError) {
    return <LoadError onRetry={() => void first.refetch()} />;
  }

  // Offset paging can repeat a PR when the list shifts between page loads.
  const seen = new Set<string>();
  const rows: ProjectPullRequest[] = [];
  for (const result of results) {
    for (const pr of result.data?.pull_requests ?? []) {
      if (seen.has(pr.id)) continue;
      seen.add(pr.id);
      rows.push(pr);
    }
  }
  const total = first.data?.total ?? count;
  const hasMore = pages * PAGE_SIZE < total;

  return (
    <div className="space-y-0.5 pb-1">
      {rows.map((pr) => (
        <PullRequestRow key={pr.id} pr={pr} />
      ))}
      {last?.isError ? <LoadError onRetry={() => void last.refetch()} /> : null}
      {hasMore && !last?.isError ? (
        <Button
          variant="ghost"
          size="xs"
          disabled={!last?.isSuccess}
          aria-busy={!last?.isSuccess}
          onClick={() => setPages((n) => n + 1)}
          className="w-full text-muted-foreground"
        >
          {t(($) => $.pull_requests.load_more)}
        </Button>
      ) : null}
    </div>
  );
}

function checksStateOf(pr: ProjectPullRequest): IssueThreadPullRequestChecksState {
  // A merged or closed PR has no CI left to report.
  if (pr.state === "merged" || pr.state === "closed") return "none";
  const checks = deriveChecksStatus(pr);
  switch (checks.kind) {
    case "failed":
      return "failed";
    case "pending":
      return "pending";
    case "passed":
      return "passed";
    default:
      return "none";
  }
}

function PullRequestRow({ pr }: { pr: ProjectPullRequest }) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const checks = checksStateOf(pr);
  const checksLabel =
    checks === "failed"
      ? t(($) => $.feed.output.checks_failed)
      : checks === "pending"
        ? t(($) => $.feed.output.checks_pending)
        : checks === "passed"
          ? t(($) => $.detail.pull_request_verdict_passed)
          : null;
  const additions = pr.additions ?? 0;
  const deletions = pr.deletions ?? 0;
  const mainClass = "flex min-w-0 items-start gap-2 rounded-md px-2 pt-1.5";
  const main = (
    <>
      <PullRequestStateIcon state={pr.state} className="mt-px size-3.5 shrink-0" />
      <div className="min-w-0 flex-1">
        <p className="truncate text-caption font-medium" title={pr.title}>
          {pr.title}
        </p>
        <p className="mt-0.5 flex h-lh min-w-0 flex-wrap items-center gap-x-1.5 overflow-hidden whitespace-nowrap text-micro text-muted-foreground tabular-nums">
          <span className="min-w-0 truncate">
            {pr.repo_name}#{pr.number}
          </span>
          {additions + deletions > 0 ? (
            <span className="inline-flex items-center gap-1.5">
              <span aria-hidden="true" className="text-faint-foreground">
                ·
              </span>
              <span>
                <span className="text-emerald-600 dark:text-emerald-400">+{formatPullRequestDiffCount(additions)}</span>{" "}
                <span className="text-rose-600 dark:text-rose-400">−{formatPullRequestDiffCount(deletions)}</span>
              </span>
            </span>
          ) : null}
          <span className="inline-flex items-center gap-1.5">
            <span aria-hidden="true" className="text-faint-foreground">
              ·
            </span>
            <span>{timeAgo(pr.pr_updated_at)}</span>
          </span>
        </p>
      </div>
      {checksLabel ? (
        <span role="img" aria-label={checksLabel} title={checksLabel} className="mt-px shrink-0">
          <PullRequestChecksIcon state={checks} className="size-3.5" />
        </span>
      ) : null}
    </>
  );

  // The chips are buttons, so they sit beside the link rather than inside it.
  return (
    <div
      data-testid="project-pull-request-row"
      className="rounded-md pb-1.5 transition-colors hover:bg-accent/50"
    >
      {WEB_URL.test(pr.html_url) ? (
        <a href={pr.html_url} target="_blank" rel="noreferrer noopener" className={mainClass}>
          {main}
        </a>
      ) : (
        <div className={mainClass}>{main}</div>
      )}
      {pr.issues.length > 0 ? (
        <div className="mt-1 flex flex-wrap gap-1 pl-[1.625rem] pr-2">
          {pr.issues.map((issue) => (
            <IssueChip key={issue.id} issue={issue} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function IssueChip({ issue }: { issue: ProjectPullRequestIssue }) {
  const open = useOpenFeedIssue(issue.id);
  return (
    <Button
      variant="outline"
      size="xs"
      title={issue.title || undefined}
      onClick={open}
      className="max-w-full tabular-nums"
    >
      <span className="truncate">{issue.identifier || issue.title}</span>
    </Button>
  );
}
