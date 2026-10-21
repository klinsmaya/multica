// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Agent } from "@multica/core/types";
import { configStore } from "@multica/core/config";
import { OPEN_CONNECTOR_APPS_FLAG } from "@multica/core/feature-flags";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";

const mockApi = vi.hoisted(() => ({
  listOpenConnectorConnections: vi.fn(),
  updateAgent: vi.fn(),
}));

vi.mock("@multica/core/api", async (orig) => ({
  ...(await orig<typeof import("@multica/core/api")>()),
  api: mockApi,
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ settings: () => "/ws/settings" }),
}));
vi.mock("../../../navigation", () => ({
  AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

import { AgentMcpTab } from "./agent-mcp-tab";

const conn = (id: string, service: string, status = "active") => ({
  id,
  service,
  label: "",
  auth_type: "oauth",
  status,
  is_default: true,
  connected_at: "",
  last_used_at: null,
});

const baseAgent = {
  id: "agent-1",
  workspace_id: "ws-1",
  owner_id: "user-1",
  permission_mode: "private",
  invocation_targets: [],
  connector_service_allowlist: ["github"],
} as unknown as Agent;

function renderTab(agent: Agent = baseAgent) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, agents: enAgents } }}>
        <AgentMcpTab agent={agent} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  configStore.getState().setFeatureFlags({ [OPEN_CONNECTOR_APPS_FLAG]: true });
  mockApi.listOpenConnectorConnections.mockResolvedValue([
    conn("1", "github"),
    conn("2", "github"),
    conn("3", "slack"),
    conn("4", "notion", "reauth_required"),
  ]);
  mockApi.updateAgent.mockResolvedValue(baseAgent);
});

describe("AgentMcpTab open-connector section", () => {
  it("lists distinct services of active connections with allowlist state", async () => {
    renderTab();
    const github = await screen.findByRole("checkbox", { name: /Allow github/ });
    expect(github).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /Allow slack/ })).not.toBeChecked();
    expect(screen.getAllByRole("checkbox")).toHaveLength(2);
  });

  it("writes the full augmented allowlist and removes one service", async () => {
    const user = userEvent.setup();
    renderTab();
    await user.click(await screen.findByRole("checkbox", { name: /Allow slack/ }));
    await waitFor(() =>
      expect(mockApi.updateAgent).toHaveBeenCalledWith("agent-1", {
        connector_service_allowlist: ["github", "slack"],
      }),
    );
    await user.click(screen.getByRole("checkbox", { name: /Allow github/ }));
    await waitFor(() =>
      expect(mockApi.updateAgent).toHaveBeenLastCalledWith("agent-1", {
        connector_service_allowlist: [],
      }),
    );
  });

  it("warns that a shared agent exposes the owner's apps", async () => {
    renderTab({ ...baseAgent, permission_mode: "public_to" } as Agent);
    expect(await screen.findByRole("alert")).toHaveTextContent(/shared/i);
  });

  it("shows a hidden state when the allowlist is redacted and skips the section when the flag is off", async () => {
    const { unmount } = renderTab({
      ...baseAgent,
      connector_service_allowlist: undefined,
      connector_service_allowlist_redacted: true,
    } as Agent);
    expect(await screen.findByText(/hidden from your view/)).toBeInTheDocument();
    unmount();
    mockApi.listOpenConnectorConnections.mockClear();
    configStore.getState().setFeatureFlags({ [OPEN_CONNECTOR_APPS_FLAG]: false });
    renderTab();
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(mockApi.listOpenConnectorConnections).not.toHaveBeenCalled();
  });
});
