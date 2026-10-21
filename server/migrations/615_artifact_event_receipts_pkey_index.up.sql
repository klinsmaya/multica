CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_event_receipts_pkey_uidx
    ON artifact_event_receipts (event_id);
