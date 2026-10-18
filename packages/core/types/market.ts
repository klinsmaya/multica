import { PREFLIGHT_BLOCK_REASONS } from "./market-block-reasons";

export { PREFLIGHT_BLOCK_REASONS };

/** Where a listing came from. A stub listing is a sample and never installable. */
export type MarketSource = "live" | "stub" | "unknown";

export interface MarketListingSummary {
  id: string;
  name: string;
  summary: string;
  publisher: string;
  icon_url: string | null;
  latest_version: string;
}

export interface MarketListingPage {
  source: MarketSource;
  items: MarketListingSummary[];
  page: number;
  page_size: number;
  total: number;
}

export interface MarketRequirements {
  model: { provider: string; models: string[] };
  tools: { name: string; provisionable: boolean }[];
  connections: { provider: string; toolkit: string }[];
  knowledge: { source: string; name: string; provisionable: boolean }[];
  unsupported: { kind: string; name: string }[];
}

export interface MarketListing {
  source: MarketSource;
  listing: MarketListingSummary;
  version: string;
  description: string;
  agent: { name: string; description: string };
  requirements: MarketRequirements;
}

export interface MarketBrowseQuery {
  q?: string;
  page?: number;
}

/** The whole preflight request. There is no actor field to fill. */
export interface MarketPreflightInput {
  listingId: string;
  version: string;
  runtimeId?: string;
  model?: string;
}

/** "unknown" is a reason a newer server sent that this client cannot name. It still blocks. */
export type PreflightBlockReason = (typeof PREFLIGHT_BLOCK_REASONS)[number] | "unknown";

export interface PreflightBinding {
  action: "use_existing" | "provision";
  id: string | null;
}

export interface SatisfiedCheck {
  name: string;
  status: "satisfied";
  binding: PreflightBinding;
}

export interface BlockedCheck {
  name: string;
  status: "blocked";
  reason: PreflightBlockReason;
}

export type PreflightCheck = SatisfiedCheck | BlockedCheck;

export interface RuntimeOption {
  runtime_id: string;
  label: string;
  usable: boolean;
  reason: PreflightBlockReason | null;
  models: string[];
}

interface ModelCheckBase {
  provider: string;
  options: RuntimeOption[];
}

export interface SatisfiedModelCheck extends ModelCheckBase {
  status: "satisfied";
  runtime_id: string;
  model: string | null;
}

export interface BlockedModelCheck extends ModelCheckBase {
  status: "blocked";
  reason: PreflightBlockReason;
}

export interface ChooseModelCheck extends ModelCheckBase {
  status: "choose";
}

export type ModelCheck = SatisfiedModelCheck | BlockedModelCheck | ChooseModelCheck;

interface PreflightMeta {
  listing_id: string;
  version: string;
  source: MarketSource;
  target_scope: "workspace";
  evaluated_for: { user_id: string; role: string };
}

/** Ready carries only satisfied checks. The type cannot hold a ready report with a block in it. */
export interface ReadyPreflight extends PreflightMeta {
  status: "ready";
  source: "live";
  model: SatisfiedModelCheck;
  tools: SatisfiedCheck[];
  connections: SatisfiedCheck[];
  knowledge: SatisfiedCheck[];
  unsupported: [];
}

export interface OpenPreflight extends PreflightMeta {
  status: "blocked" | "choose";
  model: ModelCheck;
  tools: PreflightCheck[];
  connections: PreflightCheck[];
  knowledge: PreflightCheck[];
  unsupported: BlockedCheck[];
}

/** Client-only. The response did not parse. Renders as not ready. */
export interface UnreadablePreflight {
  status: "unreadable";
}

export type MarketPreflight = ReadyPreflight | OpenPreflight | UnreadablePreflight;
