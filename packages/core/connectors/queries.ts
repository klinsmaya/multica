import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/** Account-level query keys for open-connector data (connections belong to a
 * user, not a workspace). */
export const openConnectorKeys = {
  all: ["open-connector"] as const,
  apps: () => [...openConnectorKeys.all, "apps"] as const,
  connections: () => [...openConnectorKeys.all, "connections"] as const,
};

/** The connectable services. Changes rarely, so a long staleTime. */
export const openConnectorAppsOptions = () =>
  queryOptions({
    queryKey: openConnectorKeys.apps(),
    queryFn: () => api.listOpenConnectorApps(),
    staleTime: 5 * 60 * 1000,
  });

/** The current user's non-revoked connections. */
export const openConnectorConnectionsOptions = () =>
  queryOptions({
    queryKey: openConnectorKeys.connections(),
    queryFn: () => api.listOpenConnectorConnections(),
  });
