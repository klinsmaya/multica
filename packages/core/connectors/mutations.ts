import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type {
  OpenConnectorConnectApiKeyRequest,
  UpdateOpenConnectorConnectionRequest,
} from "../types";
import { openConnectorKeys } from "./queries";

// None of these are optimistic: connect and disconnect have server-side
// effects that may fail or conflict, and set-default changes sibling rows.
// Each refetches the connection list once the server has answered.

export function useConnectOpenConnectorApiKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: OpenConnectorConnectApiKeyRequest) =>
      api.openConnectorConnectApiKey(req),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: openConnectorKeys.connections() }),
  });
}

export function useUpdateOpenConnectorConnection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string } & UpdateOpenConnectorConnectionRequest) =>
      api.updateOpenConnectorConnection(id, data),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: openConnectorKeys.connections() }),
  });
}

export function useDeleteOpenConnectorConnection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteOpenConnectorConnection(id),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: openConnectorKeys.connections() }),
  });
}
