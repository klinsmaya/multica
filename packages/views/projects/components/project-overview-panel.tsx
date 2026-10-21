"use client";

import { memo, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Paperclip } from "lucide-react";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import {
  childIssueProgressForParentsOptions,
  issueTableRowPageOptions,
  threadSummariesOptions,
} from "@multica/core/issues/queries";
import type {
  Issue,
  IssueStatusCategory,
  IssueTableRowsRequest,
  IssueThreadSummary,
} from "@multica/core/types";
import { useT, useTimeAgo } from "../../i18n";
import { useOpenFeedIssue } from "../../issues/components/feed-message-card";
import { ProgressRing } from "../../issues/components/progress-ring";
import { StatusIcon } from "../../issues/components/status-icon";
import { useIssueSurfaceActivity } from "../../issues/surface/activity";
import {
  groupProjectIssues,
  projectIssueActivityAt,
  PROJECT_ISSUE_GROUP_ORDER,
  type ProjectIssueGroupKey,
} from "./group-project-issues";

// The server's largest Table page. The overview reads one page of the
// project's most recently active top-level issues.
const OVERVIEW_PAGE_LIMIT = 100;

const EMPTY_SUMMARIES: ReadonlyMap<string, IssueThreadSummary> = new Map();
const EMPTY_PROGRESS: ReadonlyMap<string, { done: number; total: number }> = new Map();

function overviewRowsRequest(projectId: string): IssueTableRowsRequest {
  return {
    query: {
      scope: { kind: "project", project_id: projectId },
      filters: { include_sub_issues: false },
      sort: { field: "last_activity", direction: "desc" },
    },
    group: { kind: "none" },
    group_key: null,
    hierarchy: { enabled: false },
    parent_id: null,
    page: { limit: OVERVIEW_PAGE_LIMIT, cursor: null },
  };
}

/**
 * The project sidebar's overview: the project's top-level issues in four
 * groups (needs you, running, idle, resolved), each row showing the newest
 * reply and what the issue has produced.
 *
 * Issues come from a fixed project-scoped Table query rather than the issue
 * surface's own lists, which follow the surface's filters, grouping and sort.
 * The panel sits beside the surface, outside its peek host, so a row click
 * opens the issue page.
 */
export function ProjectOverviewPanel({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const currentUserId = useAuthStore((s) => s.user?.id);
  const { categoryOf, colorOf, iconOf } = useIssueStatuses(wsId);
  const { activityByIssueId } = useIssueSurfaceActivity();
  const [collapsed, setCollapsed] = useState<Record<ProjectIssueGroupKey, boolean>>({
    needs_you: false,
    running: false,
    idle: false,
    resolved: true,
  });

  const request = useMemo(() => overviewRowsRequest(projectId), [projectId]);
  const {
    data: page,
    isPending,
    isError,
  } = useQuery(issueTableRowPageOptions(wsId, request));
  const issues = useMemo(
    () => (page?.rows ?? []).map((row) => row.issue),
    [page],
  );
  const issueIds = useMemo(() => issues.map((issue) => issue.id), [issues]);
  const { data: summaries = EMPTY_SUMMARIES } = useQuery(
    threadSummariesOptions(wsId, issueIds),
  );
  const { data: childProgress = EMPTY_PROGRESS } = useQuery(
    childIssueProgressForParentsOptions(wsId, issueIds),
  );

  const groups = useMemo(
    () =>
      groupProjectIssues({
        issues,
        summaries,
        currentUserId,
        runningIssueIds: new Set(activityByIssueId.keys()),
        categoryOf,
      }),
    [issues, summaries, currentUserId, activityByIssueId, categoryOf],
  );

  const labels: Record<ProjectIssueGroupKey, { title: string; empty: string }> = {
    needs_you: {
      title: t(($) => $.overview.group_needs_you),
      empty: t(($) => $.overview.empty_needs_you),
    },
    running: {
      title: t(($) => $.overview.group_running),
      empty: t(($) => $.overview.empty_running),
    },
    idle: {
      title: t(($) => $.overview.group_idle),
      empty: t(($) => $.overview.empty_idle),
    },
    resolved: {
      title: t(($) => $.overview.group_resolved),
      empty: t(($) => $.overview.empty_resolved),
    },
  };

  if (isPending) {
    return (
      <div className="flex min-h-0 flex-1 flex-col gap-2 p-3">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
      </div>
    );
  }
  if (isError) {
    return (
      <div className="flex min-h-0 flex-1 flex-col p-4">
        <p className="text-caption text-muted-foreground">{t(($) => $.overview.load_failed)}</p>
      </div>
    );
  }
  if (issues.length === 0) {
    return (
      <div className="flex min-h-0 flex-1 flex-col p-4">
        <p className="text-caption text-muted-foreground">{t(($) => $.overview.empty_project)}</p>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-2">
      {PROJECT_ISSUE_GROUP_ORDER.map((key) => {
        const rows = groups[key];
        const open = !collapsed[key];
        return (
          <section key={key} className="mb-2">
            <button
              type="button"
              aria-expanded={open}
              onClick={() => setCollapsed((prev) => ({ ...prev, [key]: !prev[key] }))}
              className="flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors hover:bg-accent/70"
            >
              <ChevronRight
                className={cn(
                  "!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
                  open && "rotate-90",
                )}
                aria-hidden="true"
              />
              <span className="truncate">{labels[key].title}</span>
              <span className="ml-1 tabular-nums text-muted-foreground">{rows.length}</span>
            </button>
            {open &&
              (rows.length === 0 ? (
                <p className="px-2 py-1 pl-6 text-caption text-muted-foreground">
                  {labels[key].empty}
                </p>
              ) : (
                <ul className="mt-0.5 flex flex-col">
                  {rows.map((issue) => (
                    <li key={issue.id}>
                      <OverviewRow
                        issue={issue}
                        summary={summaries.get(issue.id)}
                        progress={childProgress.get(issue.id)}
                        category={categoryOf(issue.status)}
                        color={colorOf(issue.status)}
                        icon={iconOf(issue.status)}
                      />
                    </li>
                  ))}
                </ul>
              ))}
          </section>
        );
      })}
      {page?.next_cursor != null && (
        <p className="px-2 py-1 text-micro text-muted-foreground">
          {t(($) => $.overview.truncated, { limit: OVERVIEW_PAGE_LIMIT })}
        </p>
      )}
    </div>
  );
}

const OverviewRow = memo(function OverviewRow({
  issue,
  summary,
  progress,
  category,
  color,
  icon,
}: {
  issue: Issue;
  summary: IssueThreadSummary | undefined;
  progress: { done: number; total: number } | undefined;
  category: IssueStatusCategory;
  color: string | null | undefined;
  icon: string | null | undefined;
}) {
  const { t } = useT("projects");
  const timeAgo = useTimeAgo();
  const open = useOpenFeedIssue(issue.id);
  const excerpt = summary?.last_reply_excerpt ?? null;
  const deliverables = summary?.deliverable_count ?? 0;
  const showProgress = progress != null && progress.total > 0;
  const hasDetail = excerpt != null || showProgress || deliverables > 0;

  return (
    <button
      type="button"
      onClick={open}
      className="flex w-full min-w-0 flex-col gap-0.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-accent/70"
    >
      <span className="flex min-w-0 items-center gap-2">
        <StatusIcon
          status={issue.status}
          category={category}
          color={color}
          icon={icon}
          className="size-3.5 shrink-0"
        />
        <span className="min-w-0 flex-1 truncate text-caption">{issue.title}</span>
        <span className="shrink-0 text-micro text-muted-foreground">
          {timeAgo(projectIssueActivityAt(issue, summary))}
        </span>
      </span>
      {hasDetail && (
        <span className="flex min-w-0 items-center gap-2 pl-[22px] text-micro text-muted-foreground">
          <span className="min-w-0 flex-1 truncate">{excerpt}</span>
          {showProgress && (
            <span
              className="inline-flex shrink-0 items-center gap-1 tabular-nums"
              title={t(($) => $.overview.sub_issue_progress, {
                done: progress.done,
                total: progress.total,
              })}
            >
              <ProgressRing done={progress.done} total={progress.total} size={12} />
              {progress.done}/{progress.total}
            </span>
          )}
          {deliverables > 0 && (
            <span
              className="inline-flex shrink-0 items-center gap-0.5 tabular-nums"
              title={t(($) => $.overview.deliverables, { count: deliverables })}
            >
              <Paperclip className="size-3" aria-hidden="true" />
              {deliverables}
            </span>
          )}
        </span>
      )}
    </button>
  );
});
