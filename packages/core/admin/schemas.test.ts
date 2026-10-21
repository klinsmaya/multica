// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  AdminConnectorHealthResponseSchema,
  AdminConnectorPolicySchema,
  AdminConnectorServicesResponseSchema,
  AdminConnectorUserPageSchema,
  EMPTY_ADMIN_CONNECTOR_HEALTH,
  EMPTY_ADMIN_CONNECTOR_POLICY,
  EMPTY_ADMIN_CONNECTOR_SERVICES,
  emptyAdminPage,
  type AdminConnectorUser,
} from "./schemas";

const ep = { endpoint: "test" };

describe("AdminConnectorHealthResponseSchema", () => {
  it("parses a probe result and an unconfigured deployment", () => {
    const ok = AdminConnectorHealthResponseSchema.parse({
      configured: true,
      health: { base_url: "https://oc", reachable: true, latency_ms: 12, providers_total: 3, oauth_configured: 1, checked_at: "2026-01-01T00:00:00Z" },
    });
    expect(ok.health).toMatchObject({ reachable: true, latency_ms: 12, error: "" });
    const off = AdminConnectorHealthResponseSchema.parse({ configured: false });
    expect(off).toEqual({ configured: false, health: null });
  });

  it("falls back to not configured on malformed bodies", () => {
    for (const bad of [null, "x", [], { configured: "yes" }]) {
      expect(parseWithFallback(bad, AdminConnectorHealthResponseSchema, EMPTY_ADMIN_CONNECTOR_HEALTH, ep)).toEqual(
        EMPTY_ADMIN_CONNECTOR_HEALTH,
      );
    }
  });
});

describe("AdminConnectorServicesResponseSchema", () => {
  it("defaults every optional field of a service row", () => {
    const out = AdminConnectorServicesResponseSchema.parse({ configured: true, items: [{ service: "github" }] });
    expect(out.items[0]).toEqual({
      service: "github",
      name: "",
      logo: "",
      auth_types: [],
      available: false,
      enabled: false,
      allowed_tools: [],
      allowed_users: [],
      connections: { active: 0, reauth_required: 0, revoked: 0, users: 0 },
      updated_at: null,
    });
    expect(out.catalog_error).toBe("");
  });

  it("keeps allowed users and counts when present", () => {
    const out = AdminConnectorServicesResponseSchema.parse({
      items: [{
        service: "openai", enabled: true, allowed_tools: ["chat"], allowed_users: [{ user_id: "u1", email: "a@x" }],
        connections: { active: 2, users: 1 },
      }],
    });
    expect(out.items[0]?.allowed_users[0]).toEqual({ user_id: "u1", name: "", email: "a@x" });
    expect(out.items[0]?.connections).toEqual({ active: 2, reauth_required: 0, revoked: 0, users: 1 });
  });

  it("falls back to an empty catalog for malformed bodies", () => {
    for (const bad of [null, [], { items: [{ name: "no service" }] }, { items: "x" }]) {
      expect(parseWithFallback(bad, AdminConnectorServicesResponseSchema, EMPTY_ADMIN_CONNECTOR_SERVICES, ep)).toEqual(
        EMPTY_ADMIN_CONNECTOR_SERVICES,
      );
    }
  });
});

describe("AdminConnectorPolicySchema and user page", () => {
  it("parses a policy write response", () => {
    const out = AdminConnectorPolicySchema.parse({ service: "github", enabled: true, allowed_tools: ["create_issue"] });
    expect(out).toMatchObject({ service: "github", enabled: true, allowed_tools: ["create_issue"], allowed_users: [] });
    expect(parseWithFallback(42, AdminConnectorPolicySchema, EMPTY_ADMIN_CONNECTOR_POLICY, ep)).toEqual(
      EMPTY_ADMIN_CONNECTOR_POLICY,
    );
  });

  it("parses a page of connector users and rejects rows without an id", () => {
    const out = AdminConnectorUserPageSchema.parse({
      items: [{ user_id: "u1", email: "a@x", services: ["github"], connections: { active: 1 } }],
      total: 1,
    });
    expect(out.items[0]).toMatchObject({ user_id: "u1", services: ["github"], disabled_at: null, last_connected_at: null });
    expect(out.items[0]?.connections.active).toBe(1);
    expect(
      parseWithFallback({ items: [{ email: "no id" }] }, AdminConnectorUserPageSchema, emptyAdminPage<AdminConnectorUser>(), ep),
    ).toEqual(emptyAdminPage());
  });
});
