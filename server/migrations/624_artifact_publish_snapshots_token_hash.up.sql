CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_publish_snapshots_token_hash_uidx ON artifact_publish_snapshots (token_hash);
