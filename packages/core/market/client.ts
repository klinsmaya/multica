import { getApi } from "../api";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_MARKET_CATALOG,
  EMPTY_MARKET_RESOURCE,
  EMPTY_PREFLIGHT,
  MarketCatalogSchema,
  MarketResourceSchema,
  PreflightRequestSchema,
  PreflightResultSchema,
  type MarketCatalog,
  type MarketResource,
  type PreflightRequest,
  type PreflightResult,
} from "./schema";

function workspacePath(workspaceId: string, suffix: string): string {
  return `/api/workspaces/${encodeURIComponent(workspaceId)}/market/${suffix}`;
}

// Catalog ids are publisher/name. The API route is a wildcard, so the slash
// stays a path separator and each segment is encoded on its own.
function catalogResourcePath(workspaceId: string, resourceId: string): string {
  const id = resourceId.split("/").map((part) => encodeURIComponent(part)).join("/");
  return workspacePath(workspaceId, `catalog/${id}`);
}

export async function listMarketCatalog(workspaceId: string): Promise<MarketCatalog> {
  const raw = await getApi().requestJSON(workspacePath(workspaceId, "catalog"));
  return parseWithFallback(raw, MarketCatalogSchema, EMPTY_MARKET_CATALOG, {
    endpoint: "GET /api/workspaces/:id/market/catalog",
  });
}

export async function getMarketResource(
  workspaceId: string,
  resourceId: string,
): Promise<MarketResource> {
  const raw = await getApi().requestJSON(catalogResourcePath(workspaceId, resourceId));
  return parseWithFallback(raw, MarketResourceSchema, EMPTY_MARKET_RESOURCE, {
    endpoint: "GET /api/workspaces/:id/market/catalog/:resourceId",
  });
}

export async function preflightMarket(
  workspaceId: string,
  body: PreflightRequest,
): Promise<PreflightResult> {
  const payload = PreflightRequestSchema.parse(body);
  const raw = await getApi().requestJSON(workspacePath(workspaceId, "preflight"), {
    method: "POST",
    body: JSON.stringify(payload),
  });
  return parseWithFallback(raw, PreflightResultSchema, EMPTY_PREFLIGHT, {
    endpoint: "POST /api/workspaces/:id/market/preflight",
  });
}
