"use client";

import { useState, type ReactNode } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { useT } from "../../i18n";
import { ProjectOverviewPanel } from "./project-overview-panel";
import { ProjectAutopilotsPanel } from "./project-autopilots-panel";
import { ProjectLibraryPanel } from "./project-library-panel";
import { ProjectPullRequestsPanel } from "./project-pull-requests-panel";

export type ProjectSidebarTab = "properties" | "overview" | "autopilots" | "library" | "pull_requests";

/**
 * The project detail's right-hand sidebar: the editable properties column,
 * then one panel per project-wide view (overview, automations, library, PRs).
 * The selected tab is per-mount UI state; the sidebar is mounted once at a
 * time (desktop panel or mobile sheet).
 */
export function ProjectSidebarTabs({
  projectId,
  properties,
}: {
  projectId: string;
  /** The properties column rendered by the detail page. */
  properties: ReactNode;
}) {
  const { t } = useT("projects");
  const [tab, setTab] = useState<ProjectSidebarTab>("properties");
  const panelClass = "flex min-h-0 flex-1 flex-col data-hidden:hidden";
  return (
    <Tabs
      value={tab}
      onValueChange={(value) => setTab(value as ProjectSidebarTab)}
      className="flex h-full min-h-0 flex-col gap-0"
    >
      <div className="shrink-0 border-b px-2 py-1">
        <TabsList variant="line" className="w-full" aria-label={t(($) => $.sidebar.tabs_label)}>
          <TabsTrigger value="properties">{t(($) => $.sidebar.tab_properties)}</TabsTrigger>
          <TabsTrigger value="overview">{t(($) => $.sidebar.tab_overview)}</TabsTrigger>
          <TabsTrigger value="autopilots">{t(($) => $.sidebar.tab_autopilots)}</TabsTrigger>
          <TabsTrigger value="library">{t(($) => $.sidebar.tab_library)}</TabsTrigger>
          <TabsTrigger value="pull_requests">{t(($) => $.sidebar.tab_pull_requests)}</TabsTrigger>
        </TabsList>
      </div>
      <TabsContent value="properties" className="min-h-0 flex-1 overflow-y-auto p-4 data-hidden:hidden">
        {properties}
      </TabsContent>
      <TabsContent value="overview" className={panelClass}>
        {tab === "overview" && <ProjectOverviewPanel projectId={projectId} />}
      </TabsContent>
      <TabsContent value="autopilots" className={panelClass}>
        {tab === "autopilots" && <ProjectAutopilotsPanel projectId={projectId} />}
      </TabsContent>
      <TabsContent value="library" className={panelClass}>
        {tab === "library" && <ProjectLibraryPanel projectId={projectId} />}
      </TabsContent>
      <TabsContent value="pull_requests" className={panelClass}>
        {tab === "pull_requests" && <ProjectPullRequestsPanel projectId={projectId} />}
      </TabsContent>
    </Tabs>
  );
}
