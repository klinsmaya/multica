// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { PREFLIGHT_BLOCK_REASONS } from "../types/market-block-reasons";
import type { MarketOperationResult, MarketPreflight } from "../types/market";
import { ApiClient } from "./client";
import { parseWithFallback } from "./schema";
import {
  EMPTY_MARKET_LISTING_PAGE,
  MarketListingPageSchema,
  MarketListingSchema,
  MarketOperationSchema,
  MarketPreflightSchema,
  UNREADABLE_MARKET_OPERATION,
  UNREADABLE_PREFLIGHT,
} from "./schemas";

afterEach(() => {
  vi.unstubAllGlobals();
});

const satisfied = (name: string) => ({
  name,
  status: "satisfied",
  binding: { action: "use_existing", id: "id-" + name },
});

function readyReport(overrides: Record<string, unknown> = {}) {
  return {
    listing_id: "acme.reviewer",
    version: "1.0.0",
    source: "live",
    target_scope: "workspace",
    evaluated_for: { user_id: "u-1", role: "member" },
    status: "ready",
    model: {
      provider: "claude",
      status: "satisfied",
      runtime_id: "rt-1",
      model: null,
      options: [{ runtime_id: "rt-1", label: "Laptop", usable: true, reason: null, models: [] }],
    },
    tools: [satisfied("github")],
    connections: [],
    knowledge: [satisfied("code-review")],
    unsupported: [],
    ...overrides,
  };
}

const parsePreflight = (raw: unknown) =>
  parseWithFallback<MarketPreflight>(raw, MarketPreflightSchema, UNREADABLE_PREFLIGHT, { endpoint: "test" });

describe("MarketPreflightSchema", () => {
  it("parses a ready report", () => {
    expect(parsePreflight(readyReport()).status).toBe("ready");
  });

  it.each([
    ["a blocked tool", { tools: [{ name: "github", status: "blocked", reason: "mcp_server_missing" }] }],
    ["a blocked connection", { connections: [{ name: "github", status: "blocked", reason: "connection_not_connected" }] }],
    ["a model still to choose", { model: { provider: "claude", status: "choose", options: [] } }],
    ["an unsupported need", { unsupported: [{ name: "squad", status: "blocked", reason: "requirement_unsupported" }] }],
    ["a missing check list", { knowledge: undefined }],
    ["a stub source", { source: "stub" }],
    ["an unknown source", { source: "nocobase" }],
  ])("reads a ready report with %s as unreadable", (_, overrides) => {
    expect(parsePreflight(readyReport(overrides))).toEqual(UNREADABLE_PREFLIGHT);
  });

  it("keeps a reason this client cannot name as a blocking unknown", () => {
    const report = parsePreflight(
      readyReport({
        status: "blocked",
        tools: [{ name: "github", status: "blocked", reason: "reason_from_a_newer_server" }],
      }),
    );
    expect(report.status).toBe("blocked");
    if (report.status !== "blocked") return;
    expect(report.tools[0]).toEqual({ name: "github", status: "blocked", reason: "unknown" });
  });

  it("reads an unknown report status or check status as unreadable", () => {
    expect(parsePreflight(readyReport({ status: "installing" }))).toEqual(UNREADABLE_PREFLIGHT);
    expect(
      parsePreflight(readyReport({ status: "blocked", tools: [{ name: "github", status: "pending" }] })),
    ).toEqual(UNREADABLE_PREFLIGHT);
  });

  it("names every generated reason", () => {
    for (const reason of PREFLIGHT_BLOCK_REASONS) {
      const report = parsePreflight(
        readyReport({ status: "blocked", model: { provider: "claude", status: "blocked", reason, options: [] } }),
      );
      expect(report.status === "blocked" && report.model.status === "blocked" && report.model.reason).toBe(reason);
    }
  });
});

describe("Market listing schemas", () => {
  it("never lets an adapter name through as the source", () => {
    const page = parseWithFallback(
      { source: "nocobase", items: [], page: 1, page_size: 24, total: 0 },
      MarketListingPageSchema,
      EMPTY_MARKET_LISTING_PAGE,
      { endpoint: "test" },
    );
    expect(page.source).toBe("unknown");
  });

  it("falls back to an empty page when the body is not a page", () => {
    expect(
      parseWithFallback({ items: [{ name: "no id" }] }, MarketListingPageSchema, EMPTY_MARKET_LISTING_PAGE, {
        endpoint: "test",
      }),
    ).toEqual(EMPTY_MARKET_LISTING_PAGE);
  });

  it("reads a listing without a version as unreadable", () => {
    expect(
      parseWithFallback({ source: "live", listing: { id: "x", latest_version: "1" } }, MarketListingSchema, null, {
        endpoint: "test",
      }),
    ).toBeNull();
  });
});

describe("ApiClient market preflight", () => {
  function stubFetch(body: unknown) {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
    );
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  it("sends only version, runtime_id, and model", async () => {
    const fetchMock = stubFetch(readyReport());
    const input = { listingId: "acme:reviewer", version: "1.0.0", runtimeId: "rt-1", actor: "someone-else" };
    await new ApiClient("https://api.example.test").preflightMarketInstall(input);

    const [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(String(url)).toBe("https://api.example.test/api/market/listings/acme%3Areviewer/preflight");
    expect(JSON.parse(String(init?.body))).toEqual({ version: "1.0.0", runtime_id: "rt-1" });
  });

  it("returns unreadable for a contradictory ready answer", async () => {
    stubFetch(readyReport({ knowledge: [{ name: "code-review", status: "blocked", reason: "skill_missing" }] }));
    const report = await new ApiClient("https://api.example.test").preflightMarketInstall({
      listingId: "acme.reviewer",
      version: "1.0.0",
    });
    expect(report).toEqual(UNREADABLE_PREFLIGHT);
  });
});

const readyOperation = {
  id: "00000000-0000-4000-8000-000000000001",
  status: "succeeded",
  readiness: "ready",
  idempotency_key: "idempotency-key-1",
  plan_hash: "sha256:abc",
  steps: [
    { key: "agent", kind: "agent", phase: "applied", output_id: "00000000-0000-4000-8000-000000000002" },
    { key: "task", kind: "task", phase: "refused", reason: "task_create_not_uniquely_retryable" },
  ],
};

describe("MarketOperationSchema", () => {
  const parseOperation = (raw: unknown) =>
    parseWithFallback<MarketOperationResult>(raw, MarketOperationSchema, UNREADABLE_MARKET_OPERATION, { endpoint: "test" });

  it("parses a ready operation that refused the task", () => {
    const operation = parseOperation(readyOperation);
    expect(operation.status).toBe("succeeded");
    if (operation.status === "unreadable") return;
    expect(operation.readiness).toBe("ready");
    expect(operation.steps[1]?.reason).toBe("task_create_not_uniquely_retryable");
  });

  it("reads a malformed operation as unreadable", () => {
    expect(parseOperation({ id: "op", status: "started", steps: [] })).toEqual(UNREADABLE_MARKET_OPERATION);
    expect(parseOperation({ status: "succeeded" })).toEqual(UNREADABLE_MARKET_OPERATION);
  });
});
