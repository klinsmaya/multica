// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  buildPreflightRequest,
  EMPTY_MARKET_CATALOG,
  EMPTY_PREFLIGHT,
  MarketCatalogSchema,
  PreflightResultSchema,
  workspacePreflightIdentity,
} from "./schema";

const identity = workspacePreflightIdentity({
  workspaceId: "ws-1",
  userId: "user-1",
  now: new Date("2026-10-18T00:00:00Z"),
});

describe("market schemas", () => {
  it("falls back when a catalog body is malformed", () => {
    const parsed = parseWithFallback(
      { resources: [{ name: 1 }] },
      MarketCatalogSchema,
      EMPTY_MARKET_CATALOG,
      { endpoint: "GET /api/workspaces/:id/market/catalog" },
    );
    expect(parsed).toEqual(EMPTY_MARKET_CATALOG);
  });

  it("omits actor from the preflight body", () => {
    const body = buildPreflightRequest({
      identity,
      demands: [{ kind: "model", ref: "claude", scope: "read-only" }],
      actor: { userId: "forged", source: "client" },
    });
    expect(body).not.toHaveProperty("actor");
    expect(body.consumerRef.consumerType).toBe("multica");
    expect(body.targetScope).toEqual({ kind: "workspace", id: "ws-1" });
    expect(body.expiry).toBe("2026-10-18T01:00:00Z");
  });

  it("parses ready and blocked preflight results", () => {
    const ready = parseWithFallback(
      {
        status: "ready",
        taskStarted: false,
        actorUserId: "user-1",
        targetScope: { kind: "workspace", id: "ws-1" },
        denials: [],
      },
      PreflightResultSchema,
      EMPTY_PREFLIGHT,
      { endpoint: "POST /api/workspaces/:id/market/preflight" },
    );
    expect(ready.status).toBe("ready");
    expect(ready.taskStarted).toBe(false);
    expect(ready.denials).toEqual([]);

    const blocked = parseWithFallback(
      {
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
      },
      PreflightResultSchema,
      EMPTY_PREFLIGHT,
      { endpoint: "POST /api/workspaces/:id/market/preflight" },
    );
    expect(blocked.status).toBe("blocked");
    expect(blocked.denials.map((denial) => denial.code)).toEqual([
      "model.denied",
      "tool.denied",
      "connection.denied",
      "knowledge.denied",
    ]);

    const started = parseWithFallback(
      {
        status: "ready",
        taskStarted: true,
        actorUserId: "user-1",
        targetScope: { kind: "workspace", id: "ws-1" },
        denials: [],
      },
      PreflightResultSchema,
      EMPTY_PREFLIGHT,
      { endpoint: "POST /api/workspaces/:id/market/preflight" },
    );
    expect(started).toEqual(EMPTY_PREFLIGHT);
  });
});
