-- Template marketplace registry (internal/registry). Product-agnostic: these
-- queries never join a Multica business table, except the few at the end
-- that belong to the Multica adapter (internal/service/plugin_registry.go).
--
-- Version rows carry the archive bytes; every query that does not need them
-- names its columns so lists never load an archive.

-- name: ListRegistryConsumers :many
SELECT * FROM registry_consumer ORDER BY builtin DESC, key ASC;

-- name: GetRegistryConsumer :one
SELECT * FROM registry_consumer WHERE id = $1;

-- name: GetRegistryConsumerByKey :one
SELECT * FROM registry_consumer WHERE key = $1;

-- name: GetRegistryConsumerByAPIKeyHash :one
SELECT * FROM registry_consumer WHERE api_key_hash = $1;

-- name: CreateRegistryConsumer :one
INSERT INTO registry_consumer (key, name, api_key_hash, webhook_url, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: SetRegistryConsumerEnabled :one
UPDATE registry_consumer SET enabled = $2 WHERE id = $1 RETURNING *;

-- name: RotateRegistryConsumerAPIKey :one
UPDATE registry_consumer SET api_key_hash = $2 WHERE id = $1 RETURNING *;

-- name: TouchRegistryConsumer :exec
UPDATE registry_consumer SET last_used_at = now() WHERE id = $1;

-- name: ListRegistrySources :many
SELECT s.*,
       (SELECT count(*) FROM registry_package p WHERE p.source_id = s.id) AS package_count
FROM registry_source s
ORDER BY s.namespace ASC;

-- name: GetRegistrySource :one
SELECT * FROM registry_source WHERE id = $1;

-- name: GetRegistrySourceByNamespace :one
SELECT * FROM registry_source WHERE namespace = $1;

-- name: CreateRegistrySource :one
INSERT INTO registry_source (namespace, kind, display_name, url, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: SetRegistrySourceEnabled :one
UPDATE registry_source
SET enabled = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: LockRegistryPackageName :exec
-- Serializes uploads of one (source, name): the package row an upload would
-- lock may not exist yet.
SELECT pg_advisory_xact_lock(hashtextextended('registry:' || $1::text, 0));

-- name: GetRegistryPackageBySourceName :one
SELECT * FROM registry_package WHERE source_id = $1 AND name = $2;

-- name: CreateRegistryPackage :one
INSERT INTO registry_package (source_id, name, kind, display_name, description, category, tags, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateRegistryPackageDisplay :one
-- Display fields follow the newest upload; the name never moves.
UPDATE registry_package
SET display_name = $2,
    description = $3,
    category = $4,
    tags = $5,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateRegistryListing :one
INSERT INTO registry_listing (package_id) VALUES ($1) RETURNING *;

-- name: GetRegistryListingByPackage :one
SELECT * FROM registry_listing WHERE package_id = $1;

-- name: SetRegistryListingStatus :one
UPDATE registry_listing
SET status = $2,
    public = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: TouchRegistryListing :exec
UPDATE registry_listing SET updated_at = now() WHERE id = $1;

-- name: GetRegistryListing :one
SELECT l.id, l.package_id, l.status, l.public, l.enforced, l.created_at, l.updated_at,
       p.source_id, p.name, p.kind, p.display_name, p.description, p.category, p.tags,
       s.namespace, s.kind AS source_kind, s.enabled AS source_enabled
FROM registry_listing l
JOIN registry_package p ON p.id = l.package_id
JOIN registry_source s ON s.id = p.source_id
WHERE l.id = $1;

-- name: AdminListRegistryListings :many
SELECT l.id, l.package_id, l.status, l.public, l.enforced, l.updated_at,
       p.source_id, p.name, p.kind, p.display_name, p.description, p.category, p.tags,
       s.namespace,
       COALESCE((SELECT v.version FROM registry_package_version v
                 WHERE v.package_id = p.id AND v.review_status = 'approved'
                 ORDER BY v.created_at DESC, v.id DESC LIMIT 1), '')::text AS latest_version,
       (SELECT count(*) FROM registry_package_version v
        WHERE v.package_id = p.id AND v.review_status = 'pending') AS pending_count
FROM registry_listing l
JOIN registry_package p ON p.id = l.package_id
JOIN registry_source s ON s.id = p.source_id
WHERE (sqlc.narg('source_id')::uuid IS NULL OR p.source_id = sqlc.narg('source_id'))
  AND (sqlc.narg('status')::text IS NULL OR l.status = sqlc.narg('status'))
  AND (sqlc.narg('q')::text IS NULL
       OR p.name ILIKE '%' || sqlc.narg('q')::text || '%'
       OR p.display_name ILIKE '%' || sqlc.narg('q')::text || '%')
  AND (NOT @pending_only::boolean OR EXISTS (
       SELECT 1 FROM registry_package_version v
       WHERE v.package_id = p.id AND v.review_status = 'pending'))
ORDER BY l.updated_at DESC, l.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: AdminCountRegistryListings :one
SELECT count(*)
FROM registry_listing l
JOIN registry_package p ON p.id = l.package_id
WHERE (sqlc.narg('source_id')::uuid IS NULL OR p.source_id = sqlc.narg('source_id'))
  AND (sqlc.narg('status')::text IS NULL OR l.status = sqlc.narg('status'))
  AND (sqlc.narg('q')::text IS NULL
       OR p.name ILIKE '%' || sqlc.narg('q')::text || '%'
       OR p.display_name ILIKE '%' || sqlc.narg('q')::text || '%')
  AND (NOT @pending_only::boolean OR EXISTS (
       SELECT 1 FROM registry_package_version v
       WHERE v.package_id = p.id AND v.review_status = 'pending'));

-- name: CreateRegistryPackageVersion :one
INSERT INTO registry_package_version (
    package_id, version, manifest, archive, sha256, size_bytes, source_ref, changelog, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, package_id, version, manifest, sha256, size_bytes, source_ref, changelog,
          review_status, review_reason, reviewed_by, reviewed_at, created_by, created_at;

-- name: CreateRegistryPackageFile :exec
INSERT INTO registry_package_file (version_id, path, content, size_bytes)
VALUES ($1, $2, $3, $4);

-- name: ListRegistryPackageFiles :many
SELECT path, content FROM registry_package_file
WHERE version_id = $1
ORDER BY path ASC;

-- name: ListRegistryPackageVersions :many
SELECT id, package_id, version, manifest, sha256, size_bytes, source_ref, changelog,
       review_status, review_reason, reviewed_by, reviewed_at, created_by, created_at
FROM registry_package_version
WHERE package_id = $1
ORDER BY created_at DESC, id DESC;

-- name: GetRegistryPackageVersion :one
SELECT id, package_id, version, manifest, sha256, size_bytes, source_ref, changelog,
       review_status, review_reason, reviewed_by, reviewed_at, created_by, created_at
FROM registry_package_version
WHERE id = $1;

-- name: GetRegistryPackageVersionArchive :one
SELECT archive, sha256 FROM registry_package_version WHERE id = $1;

-- name: ReviewRegistryPackageVersion :one
UPDATE registry_package_version
SET review_status = $2,
    review_reason = $3,
    reviewed_by = $4,
    reviewed_at = now()
WHERE id = $1
RETURNING id, package_id, version, manifest, sha256, size_bytes, source_ref, changelog,
          review_status, review_reason, reviewed_by, reviewed_at, created_by, created_at;

-- name: ListRegistryTargeting :many
SELECT t.id, t.listing_id, t.consumer_id, t.tenant_id, t.tenant_group, t.created_at,
       c.key AS consumer_key, c.name AS consumer_name
FROM registry_targeting t
JOIN registry_consumer c ON c.id = t.consumer_id
WHERE t.listing_id = $1
ORDER BY c.key ASC, t.tenant_id ASC NULLS FIRST, t.tenant_group ASC NULLS FIRST;

-- name: DeleteRegistryTargeting :exec
DELETE FROM registry_targeting WHERE listing_id = $1;

-- name: CreateRegistryTargeting :exec
INSERT INTO registry_targeting (listing_id, consumer_id, tenant_id, tenant_group, created_by)
VALUES ($1, $2, $3, $4, $5);

-- name: ListVisibleRegistryListings :many
-- What one caller may see, each with its newest approved version. A keyed
-- consumer sees published listings targeted at it (all tenants, its tenant,
-- or one of its tenant groups); an anonymous caller (no consumer) sees only
-- public listings. Optionally narrowed to one namespace.
SELECT l.id, l.public, l.enforced, p.name, p.kind, p.display_name, p.description,
       p.category, p.tags, s.namespace, s.kind AS source_kind,
       v.id AS version_id, v.version, v.manifest, v.sha256, v.size_bytes,
       v.changelog, v.created_at AS version_created_at
FROM registry_listing l
JOIN registry_package p ON p.id = l.package_id
JOIN registry_source s ON s.id = p.source_id
JOIN LATERAL (
    SELECT pv.id, pv.version, pv.manifest, pv.sha256, pv.size_bytes, pv.changelog, pv.created_at
    FROM registry_package_version pv
    WHERE pv.package_id = p.id AND pv.review_status = 'approved'
    ORDER BY pv.created_at DESC, pv.id DESC
    LIMIT 1
) v ON true
WHERE l.status = 'published'
  AND s.enabled
  AND (sqlc.narg('namespace')::text IS NULL OR s.namespace = sqlc.narg('namespace'))
  AND (
      (sqlc.narg('consumer_id')::uuid IS NULL AND l.public)
      OR EXISTS (
          SELECT 1 FROM registry_targeting t
          WHERE t.listing_id = l.id
            AND t.consumer_id = sqlc.narg('consumer_id')
            AND (t.tenant_id IS NULL OR t.tenant_id = sqlc.narg('tenant_id'))
            AND (t.tenant_group IS NULL OR t.tenant_group = ANY(@tenant_groups::text[]))
      )
  )
ORDER BY s.namespace ASC, p.name ASC;

-- name: GetVisibleRegistryVersion :one
-- One version a caller may download or install: approved, its listing visible
-- under the same rules as ListVisibleRegistryListings.
SELECT v.id, v.package_id, v.version, v.manifest, v.sha256, v.size_bytes, v.changelog,
       v.review_status, v.created_at, l.id AS listing_id, p.name, s.namespace
FROM registry_package_version v
JOIN registry_package p ON p.id = v.package_id
JOIN registry_listing l ON l.package_id = p.id
JOIN registry_source s ON s.id = p.source_id
WHERE v.id = @version_id
  AND l.status = 'published'
  AND s.enabled
  AND (
      (sqlc.narg('consumer_id')::uuid IS NULL AND l.public)
      OR EXISTS (
          SELECT 1 FROM registry_targeting t
          WHERE t.listing_id = l.id
            AND t.consumer_id = sqlc.narg('consumer_id')
            AND (t.tenant_id IS NULL OR t.tenant_id = sqlc.narg('tenant_id'))
            AND (t.tenant_group IS NULL OR t.tenant_group = ANY(@tenant_groups::text[]))
      )
  );

-- name: GetRegistryVersionByName :one
SELECT v.id
FROM registry_package_version v
JOIN registry_package p ON p.id = v.package_id
JOIN registry_source s ON s.id = p.source_id
WHERE s.namespace = $1 AND p.name = $2 AND v.version = $3;

-- name: UpsertRegistryInstallReport :exec
INSERT INTO registry_install_report (consumer_id, tenant_id, listing_id, version_id, state, error)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (consumer_id, tenant_id, listing_id) DO UPDATE
SET version_id = EXCLUDED.version_id,
    state = EXCLUDED.state,
    error = EXCLUDED.error,
    reported_at = now();

-- name: DeleteRegistryInstallReport :exec
DELETE FROM registry_install_report
WHERE consumer_id = $1 AND tenant_id = $2 AND listing_id = $3;

-- name: CountRegistryInstallReportsByVersion :many
-- Installed version distribution of one listing, per consumer.
SELECT r.version_id, c.key AS consumer_key, count(*) AS installations
FROM registry_install_report r
JOIN registry_consumer c ON c.id = r.consumer_id
WHERE r.listing_id = $1
GROUP BY r.version_id, c.key;

-- Multica adapter (internal/service/plugin_registry.go) -----------------------

-- name: CreateRegistryPluginPackage :one
-- The workspace copy of a registry listing. Same table as a workspace's own
-- publishes, so install, surfaces and skills read it the same way.
INSERT INTO plugin_package (workspace_id, plugin_key, name, created_by, registry_listing_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateRegistryPluginPackageVersion :one
INSERT INTO plugin_package_version (
    package_id, workspace_id, version, manifest, digest, size_bytes, published_by, registry_version_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetWorkspacePluginPackageVersionByRegistryVersion :one
SELECT * FROM plugin_package_version
WHERE workspace_id = $1 AND registry_version_id = $2;

-- name: ListWorkspaceRegistryInstallations :many
-- Which registry version each registry-sourced installation runs.
SELECT i.id AS installation_id, i.plugin_key, pp.registry_listing_id, pv.registry_version_id,
       pv.version
FROM plugin_installation i
JOIN plugin_package_version pv ON pv.id = i.package_version_id
JOIN plugin_package pp ON pp.id = pv.package_id
WHERE i.workspace_id = $1 AND pp.registry_listing_id IS NOT NULL;

-- name: GetInstallationRegistrySource :one
SELECT pp.registry_listing_id, pv.registry_version_id
FROM plugin_package_version pv
JOIN plugin_package pp ON pp.id = pv.package_id
WHERE pv.id = $1;
