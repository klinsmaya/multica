// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { MarketResource, PreflightResult } from "@multica/core/market";
import enMarket from "../locales/en/market.json";

vi.mock("@multica/core/market", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/market")>();
  return { ...actual, preflightMarket: vi.fn() };
});
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } | null }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

import { preflightMarket } from "@multica/core/market";
import { InstallWizard } from "./install-wizard";

const preflight = vi.mocked(preflightMarket);

const resource: MarketResource = {
  resourceId: "multica/workspace-skill",
  name: "Workspace skill",
  publisherSlug: "multica",
  kind: "skill",
  description: "",
  sourceKey: "stub",
  demands: [
    { kind: "model", ref: "claude", scope: "read-only" },
    { kind: "tool", ref: "skill:notes", scope: "read-write" },
    { kind: "connection", ref: "conn-1", scope: "read-write" },
    { kind: "knowledge", ref: "notes", scope: "read-only" },
  ],
};

const blocked: PreflightResult = {
  status: "blocked",
  taskStarted: false,
  actorUserId: "user-1",
  targetScope: { kind: "workspace", id: "ws-1" },
  denials: [
    { kind: "model", ref: "claude", code: "model.denied" },
    { kind: "tool", ref: "skill:notes", code: "tool.denied" },
    { kind: "connection", ref: "conn-1", code: "connection.denied" },
    { kind: "knowledge", ref: "notes", code: "knowledge.denied" },
  ],
};

function renderWizard() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <I18nProvider locale="en" resources={{ en: { market: enMarket } }}>
      <QueryClientProvider client={queryClient}>
        <InstallWizard workspaceId="ws-1" resource={resource} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("InstallWizard", () => {
  it("renders a blocked preflight and does not offer to start a task", async () => {
    preflight.mockResolvedValue(blocked);
    const user = userEvent.setup();
    renderWizard();

    await user.click(screen.getByRole("button", { name: "Check plan" }));

    expect(await screen.findByText("model.denied")).toBeInTheDocument();
    expect(screen.getByText("tool.denied")).toBeInTheDocument();
    expect(screen.getByText("connection.denied")).toBeInTheDocument();
    expect(screen.getByText("knowledge.denied")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /install|start|create task|run task/i })).toBeNull();

    const body = preflight.mock.calls[0]?.[1];
    expect(body).toBeDefined();
    expect(body).not.toHaveProperty("actor");
  });
});
