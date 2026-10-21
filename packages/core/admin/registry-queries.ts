import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { adminKeys } from "./queries";
import type { RegistryListingListParams } from "./registry-schemas";

export const adminRegistryKeys = {
  all: [...adminKeys.all, "registry"] as const,
  listings: (params: RegistryListingListParams) =>
    [...adminRegistryKeys.all, "listings", params] as const,
  listing: (id: string) => [...adminRegistryKeys.all, "listing", id] as const,
  sources: () => [...adminRegistryKeys.all, "sources"] as const,
  consumers: () => [...adminRegistryKeys.all, "consumers"] as const,
};

export function adminRegistryListingsOptions(params: RegistryListingListParams) {
  return queryOptions({
    queryKey: adminRegistryKeys.listings(params),
    queryFn: () => api.adminRegistryListListings(params),
  });
}

export function adminRegistryListingOptions(id: string) {
  return queryOptions({
    queryKey: adminRegistryKeys.listing(id),
    queryFn: () => api.adminRegistryGetListing(id),
    enabled: id.length > 0,
  });
}

export function adminRegistrySourcesOptions() {
  return queryOptions({
    queryKey: adminRegistryKeys.sources(),
    queryFn: () => api.adminRegistryListSources(),
  });
}

export function adminRegistryConsumersOptions() {
  return queryOptions({
    queryKey: adminRegistryKeys.consumers(),
    queryFn: () => api.adminRegistryListConsumers(),
  });
}
