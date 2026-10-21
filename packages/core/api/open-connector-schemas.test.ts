// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  EMPTY_OPEN_CONNECTOR_APP_LIST,
  EMPTY_OPEN_CONNECTOR_CONNECTION,
  EMPTY_OPEN_CONNECTOR_CONNECTION_LIST,
  EMPTY_OPEN_CONNECTOR_CONNECT_INIT_RESPONSE,
  OpenConnectorAppListSchema,
  OpenConnectorConnectInitResponseSchema,
  OpenConnectorConnectionListSchema,
  OpenConnectorConnectionSchema,
} from "./schemas";
import { parseWithFallback } from "./schema";

const ep = { endpoint: "test" };

describe("OpenConnectorAppListSchema", () => {
  it("parses a well-formed catalog and keeps unknown auth types as strings", () => {
    const out = parseWithFallback(
      [{ service: "github", name: "GitHub", logo: "https://x/y.png", auth_types: ["oauth", "saml"], extra: 1 }],
      OpenConnectorAppListSchema,
      EMPTY_OPEN_CONNECTOR_APP_LIST,
      ep,
    );
    expect(out[0]).toMatchObject({ service: "github", auth_types: ["oauth", "saml"] });
  });

  it("defaults missing optional fields", () => {
    const out = OpenConnectorAppListSchema.parse([{ service: "notion" }]);
    expect(out[0]).toMatchObject({ service: "notion", name: "", auth_types: [] });
  });

  it("falls back to an empty list for non-array or malformed elements", () => {
    for (const bad of [null, "x", { apps: [] }, [42], [{ name: "no service" }]]) {
      expect(
        parseWithFallback(bad, OpenConnectorAppListSchema, EMPTY_OPEN_CONNECTOR_APP_LIST, ep),
      ).toEqual([]);
    }
  });
});

describe("OpenConnectorConnectionSchema", () => {
  const good = {
    id: "c1",
    service: "github",
    label: "work",
    auth_type: "api_key",
    status: "reauth_required",
    is_default: true,
    connected_at: "2026-01-01T00:00:00Z",
    last_used_at: null,
  };

  it("parses a well-formed connection", () => {
    expect(OpenConnectorConnectionSchema.parse(good)).toMatchObject(good);
  });

  it("maps unknown auth_type and status to unknown", () => {
    const out = OpenConnectorConnectionSchema.parse({ ...good, auth_type: "kerberos", status: "suspended" });
    expect(out.auth_type).toBe("unknown");
    expect(out.status).toBe("unknown");
  });

  it("defaults missing or wrongly typed fields without treating them as active", () => {
    const out = OpenConnectorConnectionSchema.parse({ id: "c1", service: "s", status: 5, is_default: "yes", last_used_at: 3 });
    expect(out).toMatchObject({
      label: "",
      auth_type: "unknown",
      status: "unknown",
      is_default: false,
      connected_at: "",
      last_used_at: null,
    });
  });

  it("falls back for non-object or id-less responses", () => {
    for (const bad of [null, "x", 7, { service: "s" }]) {
      expect(
        parseWithFallback(bad, OpenConnectorConnectionSchema, EMPTY_OPEN_CONNECTOR_CONNECTION, ep),
      ).toEqual(EMPTY_OPEN_CONNECTOR_CONNECTION);
    }
  });

  it("list falls back to empty on malformed payloads", () => {
    for (const bad of [null, {}, [null]]) {
      expect(
        parseWithFallback(bad, OpenConnectorConnectionListSchema, EMPTY_OPEN_CONNECTOR_CONNECTION_LIST, ep),
      ).toEqual([]);
    }
  });
});

describe("OpenConnectorConnectInitResponseSchema", () => {
  it("parses redirect_url", () => {
    expect(OpenConnectorConnectInitResponseSchema.parse({ redirect_url: "https://a/b" }).redirect_url).toBe("https://a/b");
  });

  it("falls back to an empty redirect_url on malformed responses", () => {
    for (const bad of [null, "x", {}, { redirect_url: 5 }]) {
      expect(
        parseWithFallback(bad, OpenConnectorConnectInitResponseSchema, EMPTY_OPEN_CONNECTOR_CONNECT_INIT_RESPONSE, ep),
      ).toEqual({ redirect_url: "" });
    }
  });
});
