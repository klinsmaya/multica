import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { adminKeys } from "./queries";
import type {
  AdminActionRequest,
  SetConnectorPolicyRequest,
  SetPlatformAdminRequest,
} from "./schemas";

// Admin writes are rare, consequential and audited, so none of them patch the
// cache optimistically: every one awaits the server, then invalidates the
// whole admin tree (lists, detail, overview counts and the audit log all move).
function useInvalidateAdmin() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: adminKeys.all });
}

export function useAdminDisableUser() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ userId, ...body }: AdminActionRequest & { userId: string }) =>
      api.adminDisableUser(userId, body),
    onSuccess: invalidate,
  });
}

export function useAdminEnableUser() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ userId, ...body }: AdminActionRequest & { userId: string }) =>
      api.adminEnableUser(userId, body),
    onSuccess: invalidate,
  });
}

export function useAdminRevokeUserToken() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({
      userId,
      tokenId,
      ...body
    }: AdminActionRequest & { userId: string; tokenId: string }) =>
      api.adminRevokeUserToken(userId, tokenId, body),
    onSuccess: invalidate,
  });
}

export function useAdminSetPlatformAdmin() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: (body: SetPlatformAdminRequest) => api.adminSetPlatformAdmin(body),
    onSuccess: invalidate,
  });
}

export function useAdminRemovePlatformAdmin() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ userId, ...body }: AdminActionRequest & { userId: string }) =>
      api.adminRemovePlatformAdmin(userId, body),
    onSuccess: invalidate,
  });
}

export function useAdminSetConnectorServiceEnabled() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({
      service,
      enabled,
      ...body
    }: AdminActionRequest & { service: string; enabled: boolean }) =>
      api.adminOpenConnectorSetServiceEnabled(service, enabled, body),
    onSuccess: invalidate,
  });
}

export function useAdminSetConnectorPolicy() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ service, ...body }: SetConnectorPolicyRequest & { service: string }) =>
      api.adminOpenConnectorSetPolicy(service, body),
    onSuccess: invalidate,
  });
}
