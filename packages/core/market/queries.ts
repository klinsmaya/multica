import { queryOptions, skipToken } from "@tanstack/react-query";
import { api } from "../api";
import type { MarketBrowseQuery, MarketPreflightInput } from "../types/market";

export const marketKeys = {
  all: (wsId: string) => ["workspaces", wsId, "market"] as const,
  listings: (wsId: string, q: MarketBrowseQuery) =>
    [...marketKeys.all(wsId), "listings", q.q ?? "", q.page ?? 1] as const,
  listing: (wsId: string, listingId: string, version?: string) =>
    [...marketKeys.all(wsId), "listing", listingId, version ?? ""] as const,
  preflight: (wsId: string, input: MarketPreflightInput) =>
    [
      ...marketKeys.all(wsId),
      "preflight",
      input.listingId,
      input.version,
      input.runtimeId ?? "",
      input.model ?? "",
    ] as const,
};

export function marketListingsOptions(wsId: string, query: MarketBrowseQuery) {
  return queryOptions({
    queryKey: marketKeys.listings(wsId, query),
    queryFn: () => api.listMarketListings(query),
    enabled: wsId.length > 0,
  });
}

export function marketListingOptions(wsId: string, listingId: string, version?: string) {
  return queryOptions({
    queryKey: marketKeys.listing(wsId, listingId, version),
    queryFn: () => api.getMarketListing(listingId, version),
    enabled: wsId.length > 0 && listingId.length > 0,
  });
}

export function marketPreflightOptions(wsId: string, input: MarketPreflightInput | null) {
  return queryOptions({
    queryKey: input ? marketKeys.preflight(wsId, input) : [...marketKeys.all(wsId), "preflight"],
    queryFn: input ? () => api.preflightMarketInstall(input) : skipToken,
    enabled: wsId.length > 0,
    staleTime: 0,
    refetchOnWindowFocus: true,
    retry: false,
  });
}
