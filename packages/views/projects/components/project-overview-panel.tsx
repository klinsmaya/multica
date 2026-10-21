"use client";

/** Placeholder until the panel lands; see the project sidebar tabs. */
export function ProjectOverviewPanel({ projectId }: { projectId: string }) {
  return <div data-project-panel="project-overview-panel" data-project-id={projectId} className="flex-1" />;
}
