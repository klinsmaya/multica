"use client";

import { memo, useCallback, useEffect, useState } from "react";
import { Clock3, Loader2 } from "@multica/ui/icons";
import { useQuery } from "@tanstack/react-query";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { isTaskMessageTaskId, taskMessagesOptions } from "@multica/core/chat/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import type { AgentTask, TaskMessagePayload } from "@multica/core/types";
import { useActorName } from "@multica/core/workspace/hooks";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { redactSecrets } from "../../common/task-transcript/redact";
import {
  traceEventSummary,
  traceToolArgSummary,
} from "../../common/task-transcript/trace-event-presenter";
import { useT } from "../../i18n";
import { selectIssueTasks, type IssueTaskGroups } from "../surface/activity";

const ELAPSED_TICK_MS = 30_000;
const EMPTY_GROUPS: IssueTaskGroups = { running: [], queued: [] };

function taskStartedAt(task: AgentTask): string {
  return task.started_at ?? task.dispatched_at ?? task.created_at;
}

/**
 * One-line text for the newest thing the run did: a tool call, a message or a
 * thought. Tool results are skipped so a returned call keeps its summary until
 * new progress arrives, the way the inline run block does. Scans from the end,
 * so the cost does not grow with the transcript.
 */
export function latestRunActivity(messages: readonly TaskMessagePayload[]): string {
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i]!;
    let summary = "";
    if (message.type === "tool_use") {
      summary = traceToolArgSummary(message.input) || message.tool || "";
    } else if (message.type === "text" || message.type === "thinking") {
      // Redact before clipping so a credential cut by the clip cannot leak.
      summary = traceEventSummary({
        type: message.type,
        content: redactSecrets(message.content ?? ""),
      });
    }
    if (summary) return redactSecrets(summary);
  }
  return "";
}

function FeedRunLine({ task, queued }: { task: AgentTask; queued: boolean }) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  const [now, setNow] = useState(Date.now);

  useEffect(() => {
    if (queued) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), ELAPSED_TICK_MS);
    return () => clearInterval(timer);
  }, [queued]);

  // The transcript is one cached query per run. The workspace-wide realtime
  // handler keeps it current once it is held, so a row adds no subscription
  // of its own; the fetch happens only while the run is actually running.
  const selectActivity = useCallback(
    (messages: TaskMessagePayload[]) => latestRunActivity(messages),
    [],
  );
  const { data: activity = "" } = useQuery({
    ...taskMessagesOptions(task.id),
    enabled: !queued && isTaskMessageTaskId(task.id),
    select: selectActivity,
  });

  const name = getActorName("agent", task.agent_id);
  const startedMs = Date.parse(taskStartedAt(task));
  const minutes = Number.isFinite(startedMs)
    ? Math.max(0, Math.floor((now - startedMs) / 60_000))
    : 0;
  const label = queued
    ? t(($) => $.feed.run.queued)
    : minutes >= 1
      ? t(($) => $.feed.run.running, { minutes })
      : t(($) => $.feed.run.starting);

  return (
    <div
      data-testid="feed-run-status"
      className="flex min-w-0 basis-full items-center gap-1.5 text-micro"
    >
      {queued ? (
        <Clock3 aria-hidden className="size-3 shrink-0 text-muted-foreground" />
      ) : (
        <Loader2
          aria-hidden
          className="size-3 shrink-0 text-info motion-safe:animate-spin motion-reduce:animate-none"
        />
      )}
      <ActorAvatar
        actorType="agent"
        actorId={task.agent_id}
        size="xs"
        profileLink={false}
      />
      <span className="max-w-[30%] shrink-0 truncate font-medium text-foreground" title={name}>
        {name}
      </span>
      <span className={cn("shrink-0 tabular-nums", queued ? "text-muted-foreground" : "text-info")}>
        {label}
      </span>
      {!queued && activity && (
        <span className="min-w-0 flex-1 truncate text-muted-foreground" title={activity}>
          {activity}
        </span>
      )}
    </div>
  );
}

/**
 * Live line under a feed thread bar while the issue's own agent task is running
 * (or waiting to run). Renders nothing, and takes no space, otherwise.
 */
export const FeedRunStatus = memo(function FeedRunStatus({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const select = useCallback(
    (snapshot: AgentTask[]) => selectIssueTasks(snapshot, issueId),
    [issueId],
  );
  // Same per-issue slice the bar's activity indicator reads: a snapshot
  // refresh re-renders only the rows whose own tasks changed.
  const { data: groups = EMPTY_GROUPS } = useQuery({
    ...agentTaskSnapshotOptions(wsId),
    select,
  });

  const running = groups.running.reduce<AgentTask | undefined>(
    (latest, task) =>
      !latest || Date.parse(taskStartedAt(task)) > Date.parse(taskStartedAt(latest)) ? task : latest,
    undefined,
  );
  if (running) return <FeedRunLine key={running.id} task={running} queued={false} />;
  const queued = groups.queued[0];
  if (queued) return <FeedRunLine key={queued.id} task={queued} queued />;
  return null;
});
