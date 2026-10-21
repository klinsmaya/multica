import http from "node:http";
import type { AddressInfo } from "node:net";
import { test, expect } from "@playwright/test";
import { TestApiClient } from "./fixtures";

// open-connector integration, end to end over HTTP: apps list -> OAuth connect
// -> callback redirect -> connection listed -> disconnect.
//
// The Multica backend is started outside Playwright (`make up`), so this spec
// can only run when that backend was booted against the mock below:
//
//   OPEN_CONNECTOR_BASE_URL=http://127.0.0.1:18931   (E2E_OPEN_CONNECTOR_PORT)
//   OPEN_CONNECTOR_ADMIN_TOKEN=e2e-admin-token
//   FF_OPEN_CONNECTOR_APPS=true
//   MULTICA_PUBLIC_URL / FRONTEND_ORIGIN pointing at the running API / web
//
// Run it with E2E_OPEN_CONNECTOR=1. Response shapes mirror a real open-connector
// (see the SDK testdata in server/pkg/openconnector/testdata).

const ENABLED = process.env.E2E_OPEN_CONNECTOR === "1";
const MOCK_PORT = Number(process.env.E2E_OPEN_CONNECTOR_PORT ?? 18931);
const ADMIN_TOKEN = process.env.OPEN_CONNECTOR_ADMIN_TOKEN ?? "e2e-admin-token";
const API_BASE =
  process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;

const CONNECTION_ID = "3602ae65-8fd2-4372-afb8-7edf5785f7a6";
const ALIAS = "e4500f74-2f16-4c77-9eb1-4a660c4b5a77";
const REQUEST_ID = "c9f25f65-05ee-4b35-98e3-7b3c23745954";
const TOKEN_ID = "c785d826-5267-488b-b05a-6fc0d8c2bf4b";

interface MockState {
  returnUri: string;
  connected: boolean;
  deletedConnections: string[];
  revokedTokens: string[];
}

function wrap(data: unknown) {
  return { success: true, message: "OK", data, meta: {} };
}

const GITHUB_OPTIONS = [
  { id: "read:user", label: "Account profile", description: "", required: true, defaultSelected: true, risk: "standard" },
  { id: "repo", label: "Repositories", description: "", required: false, defaultSelected: true, risk: "sensitive" },
  { id: "delete_repo", label: "Delete repositories", description: "", required: false, defaultSelected: false, risk: "destructive" },
];

function startMock(state: MockState): Promise<http.Server> {
  const server = http.createServer((req, res) => {
    const url = new URL(req.url ?? "/", `http://127.0.0.1:${MOCK_PORT}`);
    const path = url.pathname;
    const json = (status: number, body: unknown) => {
      res.writeHead(status, { "content-type": "application/json" });
      res.end(JSON.stringify(body));
    };

    // The browser-facing consent page needs no auth; everything else does.
    if (req.method === "GET" && path === "/authorize") {
      state.connected = true;
      const target = new URL(state.returnUri);
      target.searchParams.set("status", "success");
      target.searchParams.set("service", "github");
      res.writeHead(302, { location: target.toString() });
      res.end();
      return;
    }
    if (req.headers.authorization !== `Bearer ${ADMIN_TOKEN}`) {
      json(401, { error: { code: "unauthorized", message: "A valid local bearer token is required." } });
      return;
    }

    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const raw = Buffer.concat(chunks).toString();
      const body = raw ? JSON.parse(raw) : {};
      const connection = {
        id: CONNECTION_ID,
        providerAccountId: "octocat",
        service: "github",
        status: "active",
        alias: ALIAS,
        authType: "oauth2",
        displayName: "octocat",
        accountLabel: "octocat",
        isDefault: false,
        scopes: ["read:user", "repo"],
        comment: null,
      };

      if (req.method === "GET" && path === "/v1/providers") {
        json(200, wrap([{
          service: "github", displayName: "GitHub", iconUrl: null, homepageUrl: "https://github.com",
          categories: [{ id: "Developer", displayName: "Developer" }], scenario: "code", authTypes: ["oauth2", "api_key"],
        }]));
      } else if (req.method === "GET" && path === "/api/oauth/configs") {
        json(200, [{
          service: "github", configured: true, customClientAvailable: false, clientId: "fake-client-id",
          expectedRedirectUri: `http://127.0.0.1:${MOCK_PORT}/oauth/callback`, redirectUri: null, missingFields: [],
          auth: { type: "oauth2", scopes: ["read:user", "repo"], authorizationOptions: GITHUB_OPTIONS },
          requestedScopes: [], effectiveScopes: [],
        }]);
      } else if (req.method === "GET" && path === "/v1/providers/github/setup") {
        json(200, wrap({
          service: "github",
          auth: [{ type: "oauth2", clientFields: [], scopes: [], authorizationOptions: GITHUB_OPTIONS }],
          oauthClient: { configured: true, customClientAvailable: false, expectedRedirectUri: `http://127.0.0.1:${MOCK_PORT}/oauth/callback`, missingFields: [] },
        }));
      } else if (req.method === "POST" && path === "/v1/connections/github/connect") {
        state.returnUri = body.returnUri;
        json(200, wrap({
          authorizationUrl: `http://127.0.0.1:${MOCK_PORT}/authorize?request=${REQUEST_ID}`,
          stateHandle: "320ca403-9990-44e6-8386-4e33784ce480",
          connectionRequestId: REQUEST_ID,
          status: "initiated",
          expiresAt: new Date(Date.now() + 10 * 60_000).toISOString(),
        }));
      } else if (req.method === "GET" && path === `/v1/connection-requests/${REQUEST_ID}`) {
        json(200, wrap({
          connectionRequestId: REQUEST_ID, service: "github",
          status: state.connected ? "connected" : "initiated",
          appId: state.connected ? CONNECTION_ID : null,
          errorCode: null, errorMessage: null,
          expiresAt: new Date(Date.now() + 10 * 60_000).toISOString(),
          createdAt: Date.now(), updatedAt: Date.now(),
        }));
      } else if (req.method === "GET" && path === `/v1/connections/by-id/${CONNECTION_ID}`) {
        json(200, wrap(connection));
      } else if (req.method === "DELETE" && path === "/api/connections/github") {
        state.deletedConnections.push(body.connectionName);
        json(200, { service: "github", connectionName: body.connectionName, configured: false, revoked: "skipped" });
      } else if (req.method === "POST" && path === "/api/runtime-tokens") {
        json(200, {
          token: "oct_e2e",
          record: {
            id: TOKEN_ID, name: body.name ?? "", allowedActions: [], blockedActions: [], allowedProxies: [],
            allowedConnections: [], allowedTriggers: [], createdAt: new Date().toISOString(),
          },
        });
      } else if (req.method === "DELETE" && path.startsWith("/api/runtime-tokens/")) {
        state.revokedTokens.push(path.split("/").pop() ?? "");
        json(200, { id: path.split("/").pop(), revoked: true });
      } else {
        json(404, { error: { code: "not_found", message: `${req.method} ${path}` } });
      }
    });
  });
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(MOCK_PORT, "127.0.0.1", () => resolve(server));
  });
}

test.describe("open-connector integration", () => {
  test.skip(!ENABLED, "set E2E_OPEN_CONNECTOR=1 with a backend booted against the mock (see header comment)");

  let server: http.Server;
  const state: MockState = { returnUri: "", connected: false, deletedConnections: [], revokedTokens: [] };

  test.beforeAll(async () => {
    server = await startMock(state);
    expect((server.address() as AddressInfo).port).toBe(MOCK_PORT);
  });

  test.afterAll(async () => {
    await new Promise<void>((resolve) => server?.close(() => resolve()));
  });

  test("connects an OAuth app and disconnects it", async ({ page, context, baseURL }) => {
    const runId = Date.now().toString(36);
    const api = new TestApiClient();
    await api.login(`e2e-oc-${runId}@multica.ai`, "E2E OpenConnector");
    const workspace = await api.ensureWorkspace(`E2E OC WS ${runId}`, `e2e-oc-${runId}`);
    await api.markUserOnboarded();
    const token = api.getToken();
    if (!token) throw new Error("login did not return a token");

    const headers = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
    const call = (path: string, init: RequestInit = {}) =>
      fetch(`${API_BASE}/api/integrations/open-connector${path}`, { ...init, headers });

    try {
      // 1. The catalog offers github.
      const appsRes = await call("/apps");
      expect(appsRes.status).toBe(200);
      const apps = (await appsRes.json()) as Array<{ service: string; auth_types: string[] }>;
      expect(apps.find((a) => a.service === "github")?.auth_types).toContain("oauth");

      // 2. Nothing is connected yet.
      expect(await (await call("/connections")).json()).toEqual([]);

      // 3. Start the OAuth flow.
      const initRes = await call("/connect/init", { method: "POST", body: JSON.stringify({ service: "github" }) });
      expect(initRes.status).toBe(200);
      const { redirect_url } = (await initRes.json()) as { redirect_url: string };
      expect(redirect_url).toContain(`127.0.0.1:${MOCK_PORT}/authorize`);

      // 4. Walk the browser through the mock consent page and the callback.
      const host = new URL(baseURL ?? "http://localhost:3000").hostname;
      await context.addCookies([
        { name: "multica_logged_in", value: "1", domain: host, path: "/" },
        { name: "last_workspace_slug", value: workspace.slug, domain: host, path: "/" },
      ]);
      await page.addInitScript((t) => {
        localStorage.setItem("multica_token", t);
      }, token);
      await page.goto(redirect_url);
      await page.waitForURL(/\/settings\?.*connected=github/);
      const landed = new URL(page.url());
      expect(landed.pathname).toBe(`/${workspace.slug}/settings`);
      expect(landed.searchParams.get("tab")).toBe("integrations");
      expect(landed.searchParams.get("connected")).toBe("github");

      // 5. The connection is recorded and is the default.
      const conns = (await (await call("/connections")).json()) as Array<{
        id: string; service: string; status: string; is_default: boolean;
      }>;
      expect(conns).toHaveLength(1);
      expect(conns[0]).toMatchObject({ service: "github", status: "active", is_default: true });

      // 6. Disconnecting removes it upstream and locally.
      const delRes = await call(`/connections/${conns[0].id}`, { method: "DELETE" });
      expect(delRes.status).toBeLessThan(300);
      expect(state.deletedConnections).toContain(ALIAS);
      expect(await (await call("/connections")).json()).toEqual([]);
    } finally {
      await api.cleanup();
    }
  });
});
