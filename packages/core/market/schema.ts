import { z } from "zod";

export const DemandSchema = z.object({
  kind: z.string(),
  ref: z.string(),
  scope: z.string(),
});

export type Demand = z.infer<typeof DemandSchema>;

export const MarketResourceSchema = z.object({
  resourceId: z.string(),
  name: z.string(),
  publisherSlug: z.string(),
  kind: z.string(),
  description: z.string().optional().default(""),
  sourceKey: z.string(),
  demands: z.array(DemandSchema),
});

export type MarketResource = z.infer<typeof MarketResourceSchema>;

export const MarketCatalogSchema = z.object({
  resources: z.array(MarketResourceSchema),
});

export type MarketCatalog = z.infer<typeof MarketCatalogSchema>;

export const EMPTY_MARKET_CATALOG: MarketCatalog = { resources: [] };

export const EMPTY_MARKET_RESOURCE: MarketResource = {
  resourceId: "",
  name: "",
  publisherSlug: "",
  kind: "",
  description: "",
  sourceKey: "",
  demands: [],
};

const ConsumerRefSchema = z.object({
  consumerType: z.string(),
  consumerId: z.string(),
  tenantId: z.string(),
  subjectId: z.string(),
});

const TargetScopeSchema = z.object({
  kind: z.string(),
  id: z.string(),
});

export const PreflightIdentitySchema = z.object({
  mappingId: z.string(),
  revision: z.number(),
  consumerRef: ConsumerRefSchema,
  targetScope: TargetScopeSchema,
  verification: z.enum(["verified", "unverified", "rejected"]),
  expiry: z.string(),
});

export type PreflightIdentity = z.infer<typeof PreflightIdentitySchema>;

export const PreflightRequestSchema = PreflightIdentitySchema.extend({
  demands: z.array(DemandSchema),
}).strict();

export type PreflightRequest = z.infer<typeof PreflightRequestSchema>;

export const DenialSchema = z.object({
  kind: z.string(),
  ref: z.string(),
  code: z.string(),
});

export type Denial = z.infer<typeof DenialSchema>;

export const PreflightResultSchema = z.object({
  status: z.enum(["ready", "blocked"]),
  taskStarted: z.literal(false),
  actorUserId: z.string(),
  targetScope: TargetScopeSchema,
  denials: z.array(DenialSchema),
});

export type PreflightResult = z.infer<typeof PreflightResultSchema>;

export const EMPTY_PREFLIGHT: PreflightResult = {
  status: "blocked",
  taskStarted: false,
  actorUserId: "",
  targetScope: { kind: "workspace", id: "" },
  denials: [],
};

export function rfc3339Z(date: Date): string {
  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

export function workspacePreflightIdentity(input: {
  workspaceId: string;
  userId: string;
  now?: Date;
}): PreflightIdentity {
  const now = input.now ?? new Date();
  return {
    mappingId: input.workspaceId,
    revision: 1,
    consumerRef: {
      consumerType: "multica",
      consumerId: "multica",
      tenantId: input.workspaceId,
      subjectId: input.userId,
    },
    targetScope: { kind: "workspace", id: input.workspaceId },
    verification: "verified",
    expiry: rfc3339Z(new Date(now.getTime() + 60 * 60 * 1000)),
  };
}

export function buildPreflightRequest(input: {
  identity: PreflightIdentity;
  demands: Demand[];
  actor?: { userId?: string; source?: string };
}): PreflightRequest {
  return {
    mappingId: input.identity.mappingId,
    revision: input.identity.revision,
    consumerRef: { ...input.identity.consumerRef },
    targetScope: { ...input.identity.targetScope },
    verification: input.identity.verification,
    expiry: input.identity.expiry,
    demands: input.demands.map((demand) => ({
      kind: demand.kind,
      ref: demand.ref,
      scope: demand.scope,
    })),
  };
}
