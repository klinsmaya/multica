CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_share_grants_token_hash_uidx ON artifact_share_grants (token_hash);
