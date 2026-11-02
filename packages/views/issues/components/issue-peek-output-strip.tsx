"use client";

import { useQuery } from "@tanstack/react-query";
import { threadSummariesOptions } from "@multica/core/issues/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { PAGE_GUTTER } from "../../layout/page-header";
import { IssueOutputStrip } from "./issue-output-strip";
import { cn } from "@multica/ui/lib/utils";

/**
 * The peek's output strip: the issue's PRs and deliverable count under the
 * header. Reads the one-issue thread-summary rollup (the same cache the feed
 * patches from comments) and renders nothing while it loads or when the issue
 * has produced nothing.
 */
export function IssuePeekOutputStrip({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const { data } = useQuery(threadSummariesOptions(wsId, [issueId]));
  const summary = data?.get(issueId);
  if (!summary || (summary.pull_requests.length === 0 && summary.deliverable_count <= 0)) {
    return null;
  }
  return (
    <div className={cn("shrink-0 border-b py-2", PAGE_GUTTER)}>
      <IssueOutputStrip summary={summary} />
    </div>
  );
}
