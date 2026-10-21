"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { autopilotListOptions } from "@multica/core/autopilots/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useActorName } from "@multica/core/workspace/hooks";
import type { Autopilot } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { ActorAvatar } from "../../common/actor-avatar";
import { formatInTimeZone } from "../../common/format-in-time-zone";
import { AutopilotDialog } from "../../autopilots/components/autopilot-dialog";
import { useT, useTimeAgo } from "../../i18n";
import { AppLink } from "../../navigation";

/**
 * The project's automations: the workspace autopilots bound to this project,
 * newest first. `GET /api/autopilots` only filters by status, so the project
 * filter runs client-side over the shared list query (which the autopilots
 * page and the realtime invalidation already keep fresh).
 */
export function ProjectAutopilotsPanel({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const { t: tAutopilots } = useT("autopilots");
  const wsId = useWorkspaceId();
  const { data, isLoading, error, refetch } = useQuery({
    ...autopilotListOptions(wsId),
    enabled: !!wsId,
  });
  const [createOpen, setCreateOpen] = useState(false);

  const autopilots = useMemo(
    () =>
      (data ?? [])
        .filter((a) => a.project_id === projectId && a.status !== "archived")
        .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at)),
    [data, projectId],
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center justify-end px-3 py-2">
        <Button type="button" variant="outline" size="xs" onClick={() => setCreateOpen(true)}>
          <Plus />
          {t(($) => $.automations.new)}
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
        {error ? (
          <div role="alert" className="flex flex-col items-start gap-2 px-2 py-2">
            <p className="text-caption text-destructive">
              {error instanceof Error ? error.message : String(error)}
            </p>
            <Button type="button" variant="outline" size="xs" onClick={() => refetch()}>
              {tAutopilots(($) => $.page.retry)}
            </Button>
          </div>
        ) : isLoading ? (
          <div className="space-y-1" aria-hidden="true">
            {[0, 1].map((i) => (
              <div key={i} className="space-y-1.5 px-2 py-2">
                <Skeleton className="h-3.5 w-3/4" />
                <Skeleton className="h-3 w-1/2" />
              </div>
            ))}
          </div>
        ) : autopilots.length === 0 ? (
          <p className="px-2 py-2 text-caption text-muted-foreground">
            {t(($) => $.automations.empty)}
          </p>
        ) : (
          <ul className="space-y-0.5">
            {autopilots.map((autopilot) => (
              <li key={autopilot.id}>
                <AutopilotRow autopilot={autopilot} />
              </li>
            ))}
          </ul>
        )}
      </div>
      {createOpen && (
        <AutopilotDialog
          mode="create"
          open={createOpen}
          onOpenChange={setCreateOpen}
          initial={{ project_id: projectId }}
        />
      )}
    </div>
  );
}

function AutopilotRow({ autopilot }: { autopilot: Autopilot }) {
  const { t, i18n } = useT("projects");
  const { t: tAutopilots } = useT("autopilots");
  const timeAgo = useTimeAgo();
  const wsPaths = useWorkspacePaths();
  const { getActorName } = useActorName();

  const triggers = (autopilot.trigger_kinds ?? []).map((kind) =>
    // Server-driven enum: unknown kinds render as their raw value.
    kind === "schedule" || kind === "webhook" || kind === "api"
      ? tAutopilots(($) => $.trigger_kind[kind])
      : kind,
  );
  const paused = autopilot.status === "paused";
  const timing = autopilot.next_run_at
    ? t(($) => $.automations.next_run, {
        time: formatInTimeZone(autopilot.next_run_at, undefined, i18n.language),
      })
    : autopilot.last_run_at
      ? t(($) => $.automations.last_run, { time: timeAgo(autopilot.last_run_at) })
      : null;

  return (
    <AppLink
      href={wsPaths.autopilotDetail(autopilot.id)}
      newTabTitle={autopilot.title}
      className="block rounded-md px-2 py-1.5 transition-colors hover:bg-accent/50 focus-visible:bg-accent/50 focus-visible:outline-none"
    >
      <div className="flex min-w-0 items-center gap-1.5">
        <span className="min-w-0 flex-1 truncate text-body font-medium">{autopilot.title}</span>
        <ActorAvatar actorType={autopilot.assignee_type} actorId={autopilot.assignee_id} size="sm" />
        <span className="max-w-[40%] shrink-0 truncate text-caption text-muted-foreground">
          {getActorName(autopilot.assignee_type, autopilot.assignee_id)}
        </span>
      </div>
      <div className="mt-0.5 flex min-w-0 items-center gap-1 text-caption text-muted-foreground">
        <span className="min-w-0 truncate">
          {[triggers.join(", "), paused ? null : timing].filter(Boolean).join(" · ")}
        </span>
        {paused && (
          <span
            title={
              autopilot.pause_reason === "agent_runtime_required"
                ? tAutopilots(($) => $.status.paused_runtime_required)
                : undefined
            }
            className="shrink-0 text-amber-500"
          >
            {tAutopilots(($) => $.status.paused)}
          </span>
        )}
      </div>
    </AppLink>
  );
}
