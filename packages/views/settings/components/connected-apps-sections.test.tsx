// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { configStore } from "@multica/core/config";
import { COMPOSIO_MCP_APPS_FLAG, OPEN_CONNECTOR_APPS_FLAG } from "@multica/core/feature-flags";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockApi = vi.hoisted(() => ({
  listComposioToolkits: vi.fn(),
  listComposioConnections: vi.fn(),
  listOpenConnectorApps: vi.fn(),
  listOpenConnectorConnections: vi.fn(),
}));
vi.mock("@multica/core/api", async (orig) => ({
  ...(await orig<typeof import("@multica/core/api")>()),
  api: mockApi,
}));
vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/settings",
    searchParams: new URLSearchParams("tab=apps"),
    hash: "",
    getShareableUrl: (p: string) => p,
  }),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("./settings-layout", () => ({
  SettingsTab: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));

import { ApiError } from "@multica/core/api";
import { ConnectedAppsTab } from "./connected-apps-tab";

function renderTab() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
        <ConnectedAppsTab />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

const notConfigured = (code: string) => new ApiError("x", 403, "Forbidden", { code });

beforeEach(() => {
  vi.clearAllMocks();
  configStore.getState().setFeatureFlags({
    [COMPOSIO_MCP_APPS_FLAG]: true,
    [OPEN_CONNECTOR_APPS_FLAG]: true,
  });
  mockApi.listComposioToolkits.mockResolvedValue([
    { slug: "notion", name: "Notion", connectable: true },
  ]);
  mockApi.listComposioConnections.mockResolvedValue([]);
  mockApi.listOpenConnectorApps.mockResolvedValue([
    { service: "github", name: "GitHub", auth_types: ["oauth"] },
  ]);
  mockApi.listOpenConnectorConnections.mockResolvedValue([]);
});

describe("ConnectedAppsTab providers", () => {
  it("renders a section per provider", async () => {
    renderTab();
    expect(await screen.findByText("Notion")).toBeInTheDocument();
    expect(await screen.findByText("GitHub")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Composio" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Open Connector" })).toBeInTheDocument();
  });

  it("keeps open-connector when Composio is not configured", async () => {
    mockApi.listComposioToolkits.mockRejectedValue(notConfigured("composio_not_configured"));
    renderTab();
    expect(await screen.findByText("GitHub")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("heading", { name: "Composio" })).toBeNull());
    expect(screen.queryByText("Notion")).toBeNull();
  });

  it("keeps Composio when open-connector is not configured", async () => {
    mockApi.listOpenConnectorApps.mockRejectedValue(notConfigured("open_connector_not_configured"));
    renderTab();
    expect(await screen.findByText("Notion")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("GitHub")).toBeNull());
  });

  it("does not request open-connector data when its flag is off", async () => {
    configStore.getState().setFeatureFlags({
      [COMPOSIO_MCP_APPS_FLAG]: true,
      [OPEN_CONNECTOR_APPS_FLAG]: false,
    });
    renderTab();
    await screen.findByText("Notion");
    expect(mockApi.listOpenConnectorApps).not.toHaveBeenCalled();
  });
});
