/** An open-connector service as returned by GET /api/integrations/open-connector/apps.
 * Mirrors `AppView` in `server/internal/integrations/openconnector/service.go`.
 * `auth_types` holds "oauth" and/or "api_key"; unknown values must be ignored
 * by the UI. New backend fields MUST stay optional. */
export interface OpenConnectorApp {
  service: string;
  name: string;
  logo?: string;
  auth_types: string[];
}

export type OpenConnectorConnectionStatus = "active" | "reauth_required" | "unknown";
export type OpenConnectorAuthType = "oauth" | "api_key" | "unknown";

/** A user's open-connector connection (GET .../open-connector/connections).
 * Mirrors `ConnectionView` server-side. `auth_type` and `status` fall back to
 * "unknown" for values this client does not recognise. */
export interface OpenConnectorConnection {
  id: string;
  service: string;
  label: string;
  auth_type: OpenConnectorAuthType;
  status: OpenConnectorConnectionStatus;
  is_default: boolean;
  connected_at: string;
  last_used_at: string | null;
}

export interface OpenConnectorConnectInitResponse {
  redirect_url: string;
}

export interface OpenConnectorConnectApiKeyRequest {
  service: string;
  api_key: string;
  extra?: Record<string, string>;
  label?: string;
}

export interface UpdateOpenConnectorConnectionRequest {
  is_default?: boolean;
  label?: string;
}
