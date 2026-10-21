// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockApi = vi.hoisted(() => ({
  listOpenConnectorApps: vi.fn(),
  listOpenConnectorConnections: vi.fn(),
  openConnectorConnectInit: vi.fn(),
  openConnectorConnectApiKey: vi.fn(),
  updateOpenConnectorConnection: vi.fn(),
  deleteOpenConnectorConnection: vi.fn(),
}));
const searchParamsRef = vi.hoisted(() => ({ current: new URLSearchParams("tab=apps") }));
const mockReplace = vi.hoisted(() => vi.fn());
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock("@multica/core/api", async (orig) => ({
  ...(await orig<typeof import("@multica/core/api")>()),
  api: mockApi,
}));
vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: mockReplace,
    back: vi.fn(),
    pathname: "/acme/settings",
    searchParams: searchParamsRef.current,
    hash: "",
    getShareableUrl: (p: string) => p,
  }),
}));
vi.mock("sonner", () => ({ toast }));

import { ApiError } from "@multica/core/api";
import { OpenConnectorTab } from "./open-connector-tab";

const app = (service: string, name: string, auth_types: string[]) => ({ service, name, auth_types });
const conn = (id: string, service: string, over: Record<string, unknown> = {}) => ({
  id,
  service,
  label: "",
  auth_type: "oauth",
  status: "active",
  is_default: false,
  connected_at: "2026-01-01T00:00:00Z",
  last_used_at: null,
  ...over,
});

function renderTab(props: { ownsConnectedCallback?: boolean } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
        <OpenConnectorTab {...props} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  searchParamsRef.current = new URLSearchParams("tab=apps");
  mockApi.listOpenConnectorApps.mockResolvedValue([
    app("github", "GitHub", ["oauth", "api_key"]),
    app("notion", "Notion", ["oauth"]),
    app("openai", "OpenAI", ["api_key"]),
  ]);
  mockApi.listOpenConnectorConnections.mockResolvedValue([
    conn("c1", "github", { label: "work", is_default: true }),
    conn("c2", "github", { label: "personal" }),
    conn("c3", "notion", { status: "reauth_required" }),
    conn("c4", "openai", { status: "weird", auth_type: "api_key" }),
  ]);
});

describe("OpenConnectorTab", () => {
  it("groups connections by service with default and status markers", async () => {
    renderTab();
    expect(await screen.findByText("work")).toBeInTheDocument();
    expect(screen.getByText("personal")).toBeInTheDocument();
    expect(screen.getAllByText("Default")).toHaveLength(1);
    expect(screen.getByText("Needs reauthorization")).toBeInTheDocument();
    expect(screen.getByText("Unknown")).toBeInTheDocument();
    // Unlabeled connections show the service name.
    expect(screen.getAllByText("Notion").length).toBeGreaterThan(0);
  });

  it("offers buttons per supported auth type", async () => {
    renderTab();
    await screen.findByText("work");
    expect(screen.getAllByRole("button", { name: "Connect" })).toHaveLength(2);
    expect(screen.getAllByRole("button", { name: "Connect with API key" })).toHaveLength(2);
  });

  it("sets another connection as default", async () => {
    const user = userEvent.setup();
    mockApi.updateOpenConnectorConnection.mockResolvedValue(conn("c2", "github"));
    renderTab();
    await screen.findByText("personal");
    const row = screen.getByText("personal").closest("li")!;
    await user.click(within(row).getByRole("button", { name: "Set as default" }));
    await waitFor(() =>
      expect(mockApi.updateOpenConnectorConnection).toHaveBeenCalledWith("c2", { is_default: true }),
    );
  });

  it("submits an API key without echoing it and clears it on close", async () => {
    const user = userEvent.setup();
    mockApi.openConnectorConnectApiKey.mockResolvedValue(conn("c9", "openai"));
    renderTab();
    await screen.findByText("work");
    const buttons = screen.getAllByRole("button", { name: "Connect with API key" });
    await user.click(buttons[buttons.length - 1]!);
    const dialog = await screen.findByRole("dialog");
    const keyInput = within(dialog).getByLabelText("API key");
    expect(keyInput).toHaveAttribute("type", "password");
    await user.type(keyInput, "sk-secret");
    await user.type(within(dialog).getByLabelText("Name (optional)"), "ci");
    await user.click(within(dialog).getByRole("button", { name: "Connect" }));
    await waitFor(() =>
      expect(mockApi.openConnectorConnectApiKey).toHaveBeenCalledWith({
        service: "openai",
        api_key: "sk-secret",
        label: "ci",
      }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.body.textContent).not.toContain("sk-secret");
  });

  it("keeps the dialog and input open and shows the busy message on 409", async () => {
    const user = userEvent.setup();
    mockApi.openConnectorConnectApiKey.mockRejectedValue(
      new ApiError("busy", 409, "Conflict", { code: "open_connector_connect_busy" }),
    );
    renderTab();
    await screen.findByText("work");
    const buttons = screen.getAllByRole("button", { name: "Connect with API key" });
    await user.click(buttons[0]!);
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("API key"), "k");
    await user.click(within(dialog).getByRole("button", { name: "Connect" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Someone else is connecting this app. Try again shortly.",
    );
    expect(within(dialog).getByLabelText("API key")).toHaveValue("k");
  });

  it("shows the busy message when OAuth init conflicts", async () => {
    const user = userEvent.setup();
    mockApi.openConnectorConnectInit.mockRejectedValue(
      new ApiError("busy", 409, "Conflict", { code: "open_connector_connect_busy" }),
    );
    renderTab();
    await screen.findByText("work");
    await user.click(screen.getAllByRole("button", { name: "Connect" })[0]!);
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Someone else is connecting this app. Try again shortly."),
    );
  });

  it("only disconnects after the user confirms", async () => {
    const user = userEvent.setup();
    let resolveDelete!: () => void;
    mockApi.deleteOpenConnectorConnection.mockReturnValue(
      new Promise<void>((r) => {
        resolveDelete = r;
      }),
    );
    renderTab();
    await screen.findByText("personal");
    const row = screen.getByText("personal").closest("li")!;
    await user.click(within(row).getByRole("button", { name: "Disconnect personal" }));
    expect(mockApi.deleteOpenConnectorConnection).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Disconnect" }));
    await waitFor(() => expect(mockApi.deleteOpenConnectorConnection).toHaveBeenCalledWith("c2"));
    // Refresh happens only after the server answered.
    expect(mockApi.listOpenConnectorConnections).toHaveBeenCalledTimes(1);
    resolveDelete();
    await waitFor(() => expect(mockApi.listOpenConnectorConnections).toHaveBeenCalledTimes(2));
  });

  it("toasts and strips the failure return param", async () => {
    searchParamsRef.current = new URLSearchParams("tab=integrations&error=open_connector_connect_failed");
    renderTab();
    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));
    expect(mockReplace).toHaveBeenCalledWith("/acme/settings?tab=apps");
  });

  it("leaves the connected param to Composio when it owns the callback", async () => {
    searchParamsRef.current = new URLSearchParams("tab=integrations&connected=github");
    renderTab({ ownsConnectedCallback: false });
    await screen.findByText("work");
    expect(toast.success).not.toHaveBeenCalled();
    expect(mockReplace).not.toHaveBeenCalled();
  });
});
