// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../test/i18n";

const mockApi = vi.hoisted(() => ({
  adminMe: vi.fn(),
  adminOpenConnectorHealth: vi.fn(),
  adminOpenConnectorListServices: vi.fn(),
  adminOpenConnectorListUsers: vi.fn(),
  adminOpenConnectorSetServiceEnabled: vi.fn(),
  adminOpenConnectorSetPolicy: vi.fn(),
}));
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock("@multica/core/api", async (orig) => ({
  ...(await orig<typeof import("@multica/core/api")>()),
  api: mockApi,
}));
vi.mock("../navigation", () => ({
  AppLink: ({ children, href }: { children: React.ReactNode; href: string }) => <a href={href}>{children}</a>,
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/admin/connectors",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p: string) => p,
  }),
}));
vi.mock("sonner", () => ({ toast }));

import { AdminConnectorsPage } from "./connectors-page";

const me = (role: string) => ({ user_id: "admin-1", email: "ops@example.com", name: "Ops", role });
const health = {
  configured: true,
  health: {
    base_url: "https://oc.example.com",
    reachable: true,
    latency_ms: 42,
    error: "",
    providers_total: 120,
    oauth_configured: 3,
    checked_at: "2026-10-21T05:00:00Z",
  },
};
const service = (over: Record<string, unknown> = {}) => ({
  service: "github",
  name: "GitHub",
  logo: "",
  auth_types: ["oauth"],
  available: true,
  enabled: true,
  allowed_tools: [],
  allowed_users: [],
  connections: { active: 4, reauth_required: 1, revoked: 2, users: 3 },
  updated_at: "2026-10-20T00:00:00Z",
  ...over,
});
const servicesBody = {
  configured: true,
  catalog_error: "",
  items: [
    service(),
    service({
      service: "openai",
      name: "OpenAI",
      auth_types: ["api_key"],
      enabled: false,
      allowed_tools: ["chat", "embeddings"],
      allowed_users: [{ user_id: "u1", name: "Alice", email: "alice@example.com" }],
      connections: { active: 0, reauth_required: 0, revoked: 0, users: 0 },
    }),
  ],
};
const usersBody = {
  items: [{
    user_id: "u1", name: "Alice", email: "alice@example.com", disabled_at: null,
    services: ["github", "openai"], connections: { active: 2, reauth_required: 1, revoked: 0, users: 0 },
    last_connected_at: "2026-10-19T00:00:00Z",
  }],
  total: 1,
  page: 1,
  page_size: 25,
};

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <AdminConnectorsPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockApi.adminMe.mockResolvedValue(me("operator"));
  mockApi.adminOpenConnectorHealth.mockResolvedValue(health);
  mockApi.adminOpenConnectorListServices.mockResolvedValue(servicesBody);
  mockApi.adminOpenConnectorListUsers.mockResolvedValue(usersBody);
  mockApi.adminOpenConnectorSetServiceEnabled.mockResolvedValue({ service: "github", enabled: false });
  mockApi.adminOpenConnectorSetPolicy.mockResolvedValue({ service: "openai", enabled: false });
});

describe("AdminConnectorsPage", () => {
  it("shows health, the service catalog with policy and counts, and connections by user", async () => {
    renderPage();
    expect(await screen.findByText("Reachable")).toBeInTheDocument();
    expect(screen.getByText("https://oc.example.com")).toBeInTheDocument();
    expect(screen.getByText("42 ms")).toBeInTheDocument();

    const github = (await screen.findByText("GitHub")).closest("tr")!;
    expect(within(github).getByText("4 active · 3 users")).toBeInTheDocument();
    expect(within(github).getByText("Everyone")).toBeInTheDocument();
    expect(within(github).getByText("All")).toBeInTheDocument();
    expect(within(github).getByRole("switch", { name: "Publish GitHub" })).toBeChecked();

    const openai = screen.getByText("OpenAI").closest("tr")!;
    expect(within(openai).getByText("1 user")).toBeInTheDocument();
    expect(within(openai).getByText("2 tools")).toBeInTheDocument();
    expect(within(openai).getByRole("switch", { name: "Publish OpenAI" })).not.toBeChecked();

    const alice = (await screen.findByText("alice@example.com", { selector: "span" })).closest("tr")!;
    expect(within(alice).getByText("github, openai")).toBeInTheDocument();
    expect(mockApi.adminOpenConnectorListUsers).toHaveBeenCalledWith(
      expect.objectContaining({ page: 1, page_size: 25 }),
    );
  });

  it("explains an unconfigured deployment and a stale catalog entry", async () => {
    mockApi.adminOpenConnectorHealth.mockResolvedValue({ configured: false, health: null });
    mockApi.adminOpenConnectorListServices.mockResolvedValue({
      configured: false,
      catalog_error: "",
      items: [service({ service: "slack", name: "slack", available: false, enabled: true, auth_types: [] })],
    });
    renderPage();
    expect(await screen.findByText("Not configured")).toBeInTheDocument();
    const slack = (await screen.findByText("Not offered by the instance")).closest("tr")!;
    // Still published: the operator can unpublish it, but never re-publish.
    expect(within(slack).getByRole("switch", { name: "Publish slack" })).not.toHaveAttribute("aria-disabled");
  });

  it("hides writes from a viewer", async () => {
    mockApi.adminMe.mockResolvedValue(me("viewer"));
    renderPage();
    const github = (await screen.findByText("GitHub")).closest("tr")!;
    // Base UI renders the switch as a span, so disabled state is aria-disabled.
    await waitFor(() =>
      expect(within(github).getByRole("switch", { name: "Publish GitHub" })).toHaveAttribute(
        "aria-disabled",
        "true",
      ),
    );
    expect(screen.queryByRole("button", { name: "Policy" })).not.toBeInTheDocument();
  });

  it("unpublishes a service only with a reason and reports success", async () => {
    const user = userEvent.setup();
    renderPage();
    const github = (await screen.findByText("GitHub")).closest("tr")!;
    await user.click(within(github).getByRole("switch", { name: "Publish GitHub" }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Unpublish GitHub?")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Unpublish" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("A reason is required.");
    expect(mockApi.adminOpenConnectorSetServiceEnabled).not.toHaveBeenCalled();

    await user.type(within(dialog).getByLabelText("Reason"), "incident 42");
    await user.click(within(dialog).getByRole("button", { name: "Unpublish" }));
    await waitFor(() =>
      expect(mockApi.adminOpenConnectorSetServiceEnabled).toHaveBeenCalledWith("github", false, {
        reason: "incident 42",
      }),
    );
    expect(toast.success).toHaveBeenCalledWith("Service unpublished");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("edits a service policy from its current allowlists", async () => {
    const user = userEvent.setup();
    renderPage();
    const openai = (await screen.findByText("OpenAI")).closest("tr")!;
    await user.click(within(openai).getByRole("button", { name: "Policy" }));

    const dialog = await screen.findByRole("dialog");
    const tools = within(dialog).getByLabelText("Allowed tools") as HTMLTextAreaElement;
    const users = within(dialog).getByLabelText("Allowed users") as HTMLTextAreaElement;
    expect(tools.value).toBe("chat\nembeddings");
    expect(users.value).toBe("alice@example.com");

    await user.clear(tools);
    await user.type(tools, "chat, openai.models{enter}");
    await user.type(users, "{enter}bob@example.com");
    await user.type(within(dialog).getByLabelText("Reason"), "pilot");
    await user.click(within(dialog).getByRole("button", { name: "Save policy" }));

    await waitFor(() =>
      expect(mockApi.adminOpenConnectorSetPolicy).toHaveBeenCalledWith("openai", {
        allowed_tools: ["chat", "openai.models"],
        allowed_user_emails: ["alice@example.com", "bob@example.com"],
        reason: "pilot",
      }),
    );
    expect(toast.success).toHaveBeenCalledWith("Policy saved");
  });
});
