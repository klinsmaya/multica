"use client";

import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { ListChecks, Loader2, PenLine, Play } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCreateIssue } from "@multica/core/issues/mutations";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { projectDetailOptions } from "@multica/core/projects/queries";
import type { Issue } from "@multica/core/types";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { useIssuePeekActions } from "../surface/peek-context";

function activityTime(issue: Issue): number {
  return new Date(issue.last_activity_at ?? issue.updated_at).getTime();
}

/**
 * The fixed shortcut row above the feed composer: resume the most recently
 * active unfinished issue, ask the project's agent lead for a plan, or start
 * a new issue.
 */
export function FeedQuickActions({
  issues,
  projectId,
  onNewIssue,
}: {
  issues: Issue[];
  projectId: string | null;
  onNewIssue: () => void;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const peek = useIssuePeekActions();
  const { push } = useNavigation();
  const paths = useWorkspacePaths();
  const { categoryOf } = useIssueStatuses(wsId);
  const createIssue = useCreateIssue();

  const { data: project } = useQuery({
    ...projectDetailOptions(wsId, projectId ?? ""),
    enabled: Boolean(projectId),
  });

  const latestOpen = useMemo(() => {
    let latest: Issue | null = null;
    for (const issue of issues) {
      if (issue.parent_issue_id != null) continue;
      const category = categoryOf(issue.status);
      if (category === "done" || category === "closed") continue;
      if (!latest || activityTime(issue) > activityTime(latest)) latest = issue;
    }
    return latest;
  }, [issues, categoryOf]);

  const openIssue = useCallback(
    (issueId: string) => {
      if (peek) peek.open(issueId);
      else push(paths.issueDetail(issueId));
    },
    [peek, push, paths],
  );

  const leadId = project?.lead_type === "agent" ? project.lead_id : null;

  const makePlan = async () => {
    if (!projectId || !leadId) return;
    try {
      const created = await createIssue.mutateAsync({
        title: t(($) => $.feed.quick.plan_title),
        project_id: projectId,
        assignee_type: "agent",
        assignee_id: leadId,
      });
      openIssue(created.id);
    } catch {
      toast.error(t(($) => $.table.quick_create_failed));
    }
  };

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-wrap gap-2 px-4 pt-2">
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={!latestOpen}
        onClick={() => latestOpen && openIssue(latestOpen.id)}
      >
        <Play />
        {t(($) => $.feed.quick.continue)}
      </Button>
      {leadId && (
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={createIssue.isPending}
          aria-busy={createIssue.isPending}
          onClick={makePlan}
        >
          {createIssue.isPending ? <Loader2 className="animate-spin" /> : <ListChecks />}
          {t(($) => $.feed.quick.plan)}
        </Button>
      )}
      <Button type="button" variant="outline" size="sm" onClick={onNewIssue}>
        <PenLine />
        {t(($) => $.feed.quick.new)}
      </Button>
    </div>
  );
}
