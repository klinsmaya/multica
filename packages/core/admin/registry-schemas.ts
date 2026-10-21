import { z } from "zod";
import { adminPageSchema } from "./schemas";

// Response contracts for /api/admin/registry/*, the template marketplace's
// operator API. Same defaults-everywhere contract as the rest of /api/admin.

export const REGISTRY_SOURCE_KINDS = ["official", "self", "third_party", "skills"] as const;
export type RegistrySourceKind = (typeof REGISTRY_SOURCE_KINDS)[number];

export const REGISTRY_LISTING_STATUSES = ["draft", "published", "disabled"] as const;
export type RegistryListingStatus = (typeof REGISTRY_LISTING_STATUSES)[number];

export const REGISTRY_REVIEW_STATUSES = ["pending", "approved", "rejected", "revoked"] as const;
export type RegistryReviewStatus = (typeof REGISTRY_REVIEW_STATUSES)[number];

export const REGISTRY_REVIEW_DECISIONS = ["approved", "rejected", "revoked"] as const;
export type RegistryReviewDecision = (typeof REGISTRY_REVIEW_DECISIONS)[number];

export const RegistryConsumerSchema = z
  .object({
    id: z.string(),
    key: z.string().default(""),
    name: z.string().default(""),
    builtin: z.boolean().default(false),
    enabled: z.boolean().default(true),
    has_api_key: z.boolean().default(false),
    webhook_url: z.string().default(""),
    last_used_at: z.string().default(""),
    created_at: z.string().default(""),
  })
  .loose();
export type RegistryConsumer = z.infer<typeof RegistryConsumerSchema>;
export const EMPTY_REGISTRY_CONSUMER: RegistryConsumer = {
  id: "",
  key: "",
  name: "",
  builtin: false,
  enabled: true,
  has_api_key: false,
  webhook_url: "",
  last_used_at: "",
  created_at: "",
};

/** The only response that carries an API key; it is shown once. */
export const RegistryConsumerKeySchema = z
  .object({
    consumer: RegistryConsumerSchema.default(EMPTY_REGISTRY_CONSUMER),
    api_key: z.string().default(""),
  })
  .loose();
export type RegistryConsumerKey = z.infer<typeof RegistryConsumerKeySchema>;
export const EMPTY_REGISTRY_CONSUMER_KEY: RegistryConsumerKey = {
  consumer: EMPTY_REGISTRY_CONSUMER,
  api_key: "",
};

export const RegistrySourceSchema = z
  .object({
    id: z.string(),
    namespace: z.string().default(""),
    kind: z.enum(REGISTRY_SOURCE_KINDS).catch("self" as RegistrySourceKind).default("self"),
    display_name: z.string().default(""),
    url: z.string().default(""),
    enabled: z.boolean().default(true),
    package_count: z.number().default(0),
    last_synced_at: z.string().default(""),
    created_at: z.string().default(""),
  })
  .loose();
export type RegistrySource = z.infer<typeof RegistrySourceSchema>;
export const EMPTY_REGISTRY_SOURCE: RegistrySource = {
  id: "",
  namespace: "",
  kind: "self",
  display_name: "",
  url: "",
  enabled: true,
  package_count: 0,
  last_synced_at: "",
  created_at: "",
};

const ListingStatusSchema = z
  .enum(REGISTRY_LISTING_STATUSES)
  .catch("draft" as RegistryListingStatus)
  .default("draft");
const ReviewStatusSchema = z
  .enum(REGISTRY_REVIEW_STATUSES)
  .catch("pending" as RegistryReviewStatus)
  .default("pending");

export const RegistryListingSchema = z
  .object({
    id: z.string(),
    source_id: z.string().default(""),
    namespace: z.string().default(""),
    name: z.string().default(""),
    kind: z.string().default("plugin"),
    display_name: z.string().default(""),
    description: z.string().default(""),
    category: z.string().default(""),
    tags: z.array(z.string()).default([]),
    status: ListingStatusSchema,
    public: z.boolean().default(false),
    enforced: z.boolean().default(false),
    latest_version: z.string().default(""),
    pending_count: z.number().default(0),
    updated_at: z.string().default(""),
  })
  .loose();
export type RegistryListing = z.infer<typeof RegistryListingSchema>;

export const RegistryVersionInstallCountSchema = z
  .object({
    consumer: z.string().default(""),
    count: z.number().default(0),
  })
  .loose();
export type RegistryVersionInstallCount = z.infer<typeof RegistryVersionInstallCountSchema>;

export const RegistryMCPServerSchema = z
  .object({
    name: z.string().default(""),
    type: z.string().default("http"),
    url: z.string().default(""),
    description: z.string().default(""),
  })
  .loose();
export type RegistryMCPServer = z.infer<typeof RegistryMCPServerSchema>;

export const RegistryVersionSchema = z
  .object({
    id: z.string(),
    version: z.string().default(""),
    sha256: z.string().default(""),
    size_bytes: z.number().default(0),
    changelog: z.string().default(""),
    review_status: ReviewStatusSchema,
    review_reason: z.string().default(""),
    reviewed_at: z.string().default(""),
    created_at: z.string().default(""),
    installations: z.array(RegistryVersionInstallCountSchema).default([]),
    skills: z.array(z.string()).default([]),
    agents: z.array(z.string()).default([]),
    mcp_servers: z.array(RegistryMCPServerSchema).default([]),
    metadata: z.record(z.string(), z.unknown()).default({}),
  })
  .loose();
export type RegistryVersion = z.infer<typeof RegistryVersionSchema>;

export const RegistryTargetSchema = z
  .object({
    consumer: z.string().default(""),
    consumer_name: z.string().default(""),
    tenant_id: z.string().default(""),
    tenant_group: z.string().default(""),
    tenant_label: z.string().default(""),
  })
  .loose();
export type RegistryTarget = z.infer<typeof RegistryTargetSchema>;

export const RegistryListingDetailSchema = RegistryListingSchema.extend({
  source_kind: z.string().default("self"),
  source_enabled: z.boolean().default(true),
  versions: z.array(RegistryVersionSchema).default([]),
  targeting: z.array(RegistryTargetSchema).default([]),
  installations_total: z.number().default(0),
});
export type RegistryListingDetail = z.infer<typeof RegistryListingDetailSchema>;
export const EMPTY_REGISTRY_LISTING_DETAIL: RegistryListingDetail = {
  id: "",
  source_id: "",
  namespace: "",
  name: "",
  kind: "plugin",
  display_name: "",
  description: "",
  category: "",
  tags: [],
  status: "draft",
  public: false,
  enforced: false,
  latest_version: "",
  pending_count: 0,
  updated_at: "",
  source_kind: "self",
  source_enabled: true,
  versions: [],
  targeting: [],
  installations_total: 0,
};

export const RegistryUploadResultSchema = z
  .object({
    listing_id: z.string().default(""),
    package_id: z.string().default(""),
    version_id: z.string().default(""),
    name: z.string().default(""),
    version: z.string().default(""),
  })
  .loose();
export type RegistryUploadResult = z.infer<typeof RegistryUploadResultSchema>;
export const EMPTY_REGISTRY_UPLOAD_RESULT: RegistryUploadResult = {
  listing_id: "",
  package_id: "",
  version_id: "",
  name: "",
  version: "",
};

const itemsSchema = <T extends z.ZodType>(item: T) =>
  z.object({ items: z.array(item).default([]) }).loose();

export const RegistryConsumerListSchema = itemsSchema(RegistryConsumerSchema);
export const RegistrySourceListSchema = itemsSchema(RegistrySourceSchema);
export const RegistryListingPageSchema = adminPageSchema(RegistryListingSchema);

export interface RegistryListingListParams {
  page?: number;
  page_size?: number;
  source_id?: string;
  status?: RegistryListingStatus;
  q?: string;
  pending_only?: boolean;
}

export interface RegistryUploadRequest {
  source_id: string;
  bundle: File;
  reason: string;
  changelog?: string;
  category?: string;
  tags?: string[];
}

export interface RegistryListingStatusRequest {
  status: RegistryListingStatus;
  public: boolean;
  reason: string;
}

export interface RegistryTargetInput {
  consumer: string;
  tenant_id?: string;
  tenant_group?: string;
}

export interface RegistryTargetingRequest {
  targets: RegistryTargetInput[];
  reason: string;
}

export interface RegistryReviewRequest {
  decision: RegistryReviewDecision;
  reason: string;
}

export interface RegistryCreateSourceRequest {
  namespace: string;
  kind: RegistrySourceKind;
  display_name: string;
  url?: string;
  reason: string;
}

export interface RegistryCreateConsumerRequest {
  key: string;
  name: string;
  webhook_url?: string;
  reason: string;
}

export interface RegistryToggleRequest {
  enabled: boolean;
  reason: string;
}
