import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { adminKeys } from "./queries";
import type {
  RegistryCreateConsumerRequest,
  RegistryCreateSourceRequest,
  RegistryListingStatusRequest,
  RegistryReviewRequest,
  RegistryTargetingRequest,
  RegistryToggleRequest,
  RegistryUploadRequest,
} from "./registry-schemas";

// Registry writes are audited admin actions: await the server, then
// invalidate the whole admin tree (listing counts and the audit log move).
function useInvalidateAdmin() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: adminKeys.all });
}

export function useAdminRegistryUpload() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: (request: RegistryUploadRequest) => api.adminRegistryUploadVersion(request),
    onSuccess: invalidate,
  });
}

export function useAdminRegistrySetListingStatus() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ listingId, ...body }: RegistryListingStatusRequest & { listingId: string }) =>
      api.adminRegistrySetListingStatus(listingId, body),
    onSuccess: invalidate,
  });
}

export function useAdminRegistrySetTargeting() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ listingId, ...body }: RegistryTargetingRequest & { listingId: string }) =>
      api.adminRegistrySetTargeting(listingId, body),
    onSuccess: invalidate,
  });
}

export function useAdminRegistryReviewVersion() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ versionId, ...body }: RegistryReviewRequest & { versionId: string }) =>
      api.adminRegistryReviewVersion(versionId, body),
    onSuccess: invalidate,
  });
}

export function useAdminRegistryCreateSource() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: (body: RegistryCreateSourceRequest) => api.adminRegistryCreateSource(body),
    onSuccess: invalidate,
  });
}

export function useAdminRegistrySetSourceEnabled() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ sourceId, ...body }: RegistryToggleRequest & { sourceId: string }) =>
      api.adminRegistrySetSourceEnabled(sourceId, body),
    onSuccess: invalidate,
  });
}

export function useAdminRegistryCreateConsumer() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: (body: RegistryCreateConsumerRequest) => api.adminRegistryCreateConsumer(body),
    onSuccess: invalidate,
  });
}

export function useAdminRegistryRotateConsumerKey() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ consumerId, reason }: { consumerId: string; reason: string }) =>
      api.adminRegistryRotateConsumerKey(consumerId, { reason }),
    onSuccess: invalidate,
  });
}

export function useAdminRegistrySetConsumerEnabled() {
  const invalidate = useInvalidateAdmin();
  return useMutation({
    mutationFn: ({ consumerId, ...body }: RegistryToggleRequest & { consumerId: string }) =>
      api.adminRegistrySetConsumerEnabled(consumerId, body),
    onSuccess: invalidate,
  });
}
