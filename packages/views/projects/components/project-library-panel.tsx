"use client";

/** Placeholder until the panel lands; see the project sidebar tabs. */
export function ProjectLibraryPanel({ projectId }: { projectId: string }) {
  return <div data-project-panel="project-library-panel" data-project-id={projectId} className="flex-1" />;
}
