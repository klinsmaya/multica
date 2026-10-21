// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../api/client";

function stubFetchJson(body: unknown, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

// The marketplace admin pages must render against an older or partially
// failing backend: malformed responses degrade to empty shapes, unknown enums
// fall back to the safest value, and nothing throws into React.
describe("registry admin responses", () => {
  it("degrades a malformed listing page to an empty page", async () => {
    stubFetchJson({ items: "nope", total: "many" });
    const client = new ApiClient("https://api.example.test");
    await expect(client.adminRegistryListListings()).resolves.toEqual({
      items: [],
      total: 0,
      page: 1,
      page_size: 25,
    });
  });

  it("fills defaults and folds unknown statuses in a listing detail", async () => {
    stubFetchJson({
      id: "listing-1",
      status: "archived",
      versions: [{ id: "v1", review_status: "quarantined" }],
      targeting: [{ consumer: "multica" }],
    });
    const client = new ApiClient("https://api.example.test");
    const detail = await client.adminRegistryGetListing("listing-1");
    expect(detail.status).toBe("draft");
    expect(detail.tags).toEqual([]);
    expect(detail.versions[0]?.review_status).toBe("pending");
    expect(detail.versions[0]?.installations).toEqual([]);
    expect(detail.targeting[0]?.tenant_id).toBe("");
    expect(detail.installations_total).toBe(0);
  });

  it("returns no sources or consumers when the lists are malformed", async () => {
    stubFetchJson({ items: [{ id: 1 }] });
    const client = new ApiClient("https://api.example.test");
    await expect(client.adminRegistryListSources()).resolves.toEqual([]);
    stubFetchJson({ consumers: [] });
    await expect(client.adminRegistryListConsumers()).resolves.toEqual([]);
  });

  it("never invents an API key from a malformed consumer response", async () => {
    stubFetchJson({ consumer: { id: "c1", key: "acme" } });
    const client = new ApiClient("https://api.example.test");
    const created = await client.adminRegistryCreateConsumer({ key: "acme", name: "Acme", reason: "x" });
    expect(created.api_key).toBe("");
    expect(created.consumer.key).toBe("acme");
    expect(created.consumer.has_api_key).toBe(false);
  });

  it("reads an unknown marketplace install state as not installed", async () => {
    stubFetchJson({
      listings: [{ listing_id: "l1", version_id: "v1", state: "pinned" }],
    });
    const client = new ApiClient("https://api.example.test");
    const page = await client.listPluginMarketplace("ws-1");
    expect(page.listings).toHaveLength(1);
    expect(page.listings[0]?.state).toBe("not_installed");
    expect(page.listings[0]?.tags).toEqual([]);
  });
});
