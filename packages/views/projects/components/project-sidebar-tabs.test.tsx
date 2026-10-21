import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";
import { ProjectSidebarTabs } from "./project-sidebar-tabs";

vi.mock("./project-overview-panel", () => ({
  ProjectOverviewPanel: ({ projectId }: { projectId: string }) => (
    <div data-testid="overview-panel">{projectId}</div>
  ),
}));
vi.mock("./project-autopilots-panel", () => ({
  ProjectAutopilotsPanel: () => <div data-testid="autopilots-panel" />,
}));
vi.mock("./project-library-panel", () => ({
  ProjectLibraryPanel: () => <div data-testid="library-panel" />,
}));
vi.mock("./project-pull-requests-panel", () => ({
  ProjectPullRequestsPanel: () => <div data-testid="pull-requests-panel" />,
}));

describe("ProjectSidebarTabs", () => {
  it("shows the properties column alone outside the feed view", () => {
    renderWithI18n(
      <ProjectSidebarTabs projectId="p1" properties={<p>Properties column</p>} panels={false} />,
    );
    expect(screen.getByText("Properties column")).toBeInTheDocument();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(screen.queryByTestId("overview-panel")).not.toBeInTheDocument();
  });

  it("offers the project panels in the feed view and mounts one only when selected", () => {
    renderWithI18n(
      <ProjectSidebarTabs projectId="p1" properties={<p>Properties column</p>} panels />,
    );
    expect(screen.getByRole("tablist", { name: "Project panels" })).toBeInTheDocument();
    expect(screen.getAllByRole("tab")).toHaveLength(5);
    expect(screen.getByText("Properties column")).toBeInTheDocument();
    expect(screen.queryByTestId("overview-panel")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    expect(screen.getByTestId("overview-panel")).toHaveTextContent("p1");
    expect(screen.queryByTestId("library-panel")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Library" }));
    expect(screen.getByTestId("library-panel")).toBeInTheDocument();
    expect(screen.queryByTestId("overview-panel")).not.toBeInTheDocument();
  });
});
