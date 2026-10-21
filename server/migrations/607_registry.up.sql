-- Template marketplace, phase 1: a product-agnostic package registry.
--
-- The registry only knows packages and who may see them. It stores packages
-- in the neutral Claude Code plugin layout (.claude-plugin/plugin.json,
-- skills/, agents/, .mcp.json), decides visibility per (consumer, tenant),
-- and records the install state consumers report back. It does not reference
-- any Multica business table: Multica is its first consumer and reaches it
-- through an adapter, like any other product would.
--
-- Relationships are validated in application code; no foreign keys by project
-- rule. Primary keys and unique indexes are built CONCURRENTLY in 591-615.

-- A product that reads the registry. External consumers authenticate with an
-- API key stored only as its sha256. The built-in multica consumer has no key:
-- Multica reads the registry in-process.
CREATE TABLE IF NOT EXISTS registry_consumer (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    key TEXT NOT NULL CHECK (key ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    builtin BOOLEAN NOT NULL DEFAULT false,
    api_key_hash TEXT,
    webhook_url TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_used_at TIMESTAMPTZ,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO registry_consumer (key, name, builtin)
SELECT 'multica', 'Multica', true
WHERE NOT EXISTS (SELECT 1 FROM registry_consumer WHERE key = 'multica');

CREATE TABLE IF NOT EXISTS registry_source (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    namespace TEXT NOT NULL CHECK (namespace ~ '^@[a-z0-9][a-z0-9-]{0,62}$'),
    kind TEXT NOT NULL CHECK (kind IN ('official', 'self', 'third_party', 'skills')),
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 160),
    url TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_synced_at TIMESTAMPTZ,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One package identity inside a source. `name` is plugin.json's name and never
-- changes; display fields follow the newest uploaded version.
CREATE TABLE IF NOT EXISTS registry_package (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    source_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    kind TEXT NOT NULL CHECK (kind IN ('plugin', 'skill', 'mcp_server')),
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 160),
    description TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    tags TEXT[] NOT NULL DEFAULT '{}',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One immutable version. Only the review columns change after insert.
-- `archive` is the canonical zip consumers download; its sha256 is what
-- marketplace.json pins. `manifest` is the parsed plugin.json.
CREATE TABLE IF NOT EXISTS registry_package_version (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    package_id UUID NOT NULL,
    version TEXT NOT NULL CHECK (char_length(version) BETWEEN 1 AND 64),
    manifest JSONB NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
    archive BYTEA NOT NULL,
    sha256 TEXT NOT NULL CHECK (char_length(sha256) = 64),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    source_ref TEXT NOT NULL DEFAULT '',
    changelog TEXT NOT NULL DEFAULT '',
    review_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (review_status IN ('pending', 'approved', 'rejected', 'revoked')),
    review_reason TEXT NOT NULL DEFAULT '',
    reviewed_by UUID,
    reviewed_at TIMESTAMPTZ,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The files of a version, so a consumer adapter can read one file without
-- unpacking the archive.
CREATE TABLE IF NOT EXISTS registry_package_file (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    version_id UUID NOT NULL,
    path TEXT NOT NULL CHECK (char_length(path) BETWEEN 1 AND 1024),
    content BYTEA NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0)
);

-- The catalog entry for a package: whether it is released and how.
CREATE TABLE IF NOT EXISTS registry_listing (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    package_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'disabled')),
    -- Readable without an API key, e.g. by `claude plugin marketplace add`.
    public BOOLEAN NOT NULL DEFAULT false,
    enforced BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Who sees a listing. No row means no keyed consumer does. tenant_id NULL is
-- every tenant of the consumer; tenant_group is a consumer-defined group (for
-- Multica, a plan). Tenant ids are opaque to the registry.
CREATE TABLE IF NOT EXISTS registry_targeting (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL,
    consumer_id UUID NOT NULL,
    tenant_id TEXT,
    tenant_group TEXT,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- What consumers report about their installations; the operator page's
-- version distribution reads this.
CREATE TABLE IF NOT EXISTS registry_install_report (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    consumer_id UUID NOT NULL,
    tenant_id TEXT NOT NULL,
    listing_id UUID NOT NULL,
    version_id UUID NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('current', 'outdated', 'pending_consent', 'blocked', 'pinned')),
    error TEXT NOT NULL DEFAULT '',
    reported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Multica adapter: a workspace copy of a registry version remembers where it
-- came from, so an installation's listing and version are derived by join.
-- NULL for packages the workspace published itself.
ALTER TABLE plugin_package ADD COLUMN IF NOT EXISTS registry_listing_id UUID;
ALTER TABLE plugin_package_version ADD COLUMN IF NOT EXISTS registry_version_id UUID;
