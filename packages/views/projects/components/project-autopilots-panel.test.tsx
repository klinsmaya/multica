import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { Autopilot } from "@multica/core/types";
import { NavigationProvider } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import { ProjectAutopilotsPanel } from "./project-autopilots-panel";

vi.mock("@multica/core/api", () => ({ api: { listAutopilots: vi.fn() } }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ autopilotDetail: (id: string) => `/acme/autopilots/${id}` }),
}));
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: (_type: string, id: string) => `Actor ${id}` }),
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => <span data-testid="actor-avatar">{actorId}</span>,
}));
vi.mock("../../autopilots/components/autopilot-dialog", () => ({
  AutopilotDialog: ({ mode, initial }: { mode: string; initial?: { project_id?: string | null } }) => (
    <div role="dialog" data-mode={mode}>
      {`dialog project ${initial?.project_id}`}
    </div>
  ),
}));

function makeAutopilot(overrides: Partial<Autopilot>): Autopilot {
  return {
    id: "ap",
    workspace_id: "ws",
    title: "Autopilot",
    description: null,
    project_id: "proj-1",
    assignee_type: "agent",
    assignee_id: "agent-1",
    status: "active",
    execution_mode: "run_only",
    issue_title_template: null,
    created_by_type: "member",
    created_by_id: "user-1",
    last_run_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    trigger_kinds: ["schedule"],
    ...overrides,
  };
}

function renderPanel(push = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithI18n(
    <QueryClientProvider client={client}>
      <NavigationProvider
        value={{
          pathname: "/acme/projects/proj-1",
          searchParams: new URLSearchParams(),
          hash: "",
          push,
          replace: vi.fn(),
          back: vi.fn(),
          getShareableUrl: (p) => p,
        }}
      >
        <ProjectAutopilotsPanel projectId="proj-1" />
      </NavigationProvider>
    </QueryClientProvider>,
  );
  return push;
}

describe("ProjectAutopilotsPanel", () => {
  beforeEach(() => {
    vi.mocked(api.listAutopilots).mockReset();
  });

  it("lists only this project's autopilots, newest first", async () => {
    vi.mocked(api.listAutopilots).mockResolvedValue({
      total: 4,
      autopilots: [
        makeAutopilot({ id: "old", title: "Older job", created_at: "2026-01-01T00:00:00Z" }),
        makeAutopilot({ id: "other", title: "Other project job", project_id: "proj-2" }),
        makeAutopilot({ id: "none", title: "Unbound job", project_id: null }),
        makeAutopilot({ id: "new", title: "Newer job", created_at: "2026-03-01T00:00:00Z" }),
      ],
    });
    renderPanel();

    const links = await screen.findAllByRole("link");
    expect(links.map((l) => l.querySelector("span")?.textContent)).toEqual(["Newer job", "Older job"]);
    expect(screen.queryByText("Other project job")).toBeNull();
    expect(screen.queryByText("Unbound job")).toBeNull();
  });

  it("summarizes assignee, trigger, next run and paused state", async () => {
    vi.mocked(api.listAutopilots).mockResolvedValue({
      total: 2,
      autopilots: [
        makeAutopilot({
          id: "a",
          title: "Nightly sweep",
          next_run_at: "2126-07-14T01:00:00Z",
          trigger_kinds: ["schedule", "webhook"],
        }),
        makeAutopilot({ id: "b", title: "Paused sweep", status: "paused", created_at: "2025-01-01T00:00:00Z" }),
      ],
    });
    renderPanel();

    await screen.findByText("Nightly sweep");
    expect(screen.getAllByText("Actor agent-1")).toHaveLength(2);
    expect(screen.getByText(/^Schedule, Webhook · Next /)).toBeInTheDocument();
    expect(screen.getByText("Paused")).toBeInTheDocument();
  });

  it("navigates to the autopilot detail page when a row is clicked", async () => {
    vi.mocked(api.listAutopilots).mockResolvedValue({
      total: 1,
      autopilots: [makeAutopilot({ id: "ap-9", title: "Nightly sweep" })],
    });
    const push = renderPanel();

    fireEvent.click(await screen.findByRole("link", { name: /Nightly sweep/ }));

    expect(push).toHaveBeenCalledWith("/acme/autopilots/ap-9");
  });

  it("opens the create dialog pre-filled with this project", async () => {
    vi.mocked(api.listAutopilots).mockResolvedValue({ total: 0, autopilots: [] });
    renderPanel();

    await screen.findByText("No automations in this project yet");
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "New" }));

    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("data-mode", "create");
    expect(dialog).toHaveTextContent("dialog project proj-1");
  });

  it("shows the empty state when no autopilot belongs to the project", async () => {
    vi.mocked(api.listAutopilots).mockResolvedValue({
      total: 1,
      autopilots: [makeAutopilot({ project_id: "proj-2", title: "Elsewhere" })],
    });
    renderPanel();

    expect(await screen.findByText("No automations in this project yet")).toBeInTheDocument();
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByRole("button", { name: "New" })).toBeInTheDocument();
  });

  it("shows the load error with a retry", async () => {
    vi.mocked(api.listAutopilots).mockRejectedValueOnce(new Error("Autopilots unavailable"));
    renderPanel();

    expect(await screen.findByRole("alert")).toHaveTextContent("Autopilots unavailable");
    vi.mocked(api.listAutopilots).mockResolvedValue({ total: 0, autopilots: [] });
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("No automations in this project yet")).toBeInTheDocument();
  });
});
