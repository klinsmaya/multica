-- One object key names one blob row, so a later upload cannot retarget
-- bytes another revision already committed.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_blobs_storage_key_uidx
    ON artifact_blobs (storage_key);
