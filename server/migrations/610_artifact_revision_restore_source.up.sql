-- A08: history restore inserts a new revision that points at an older one.
-- The source revision row is not updated. No foreign key.

ALTER TABLE artifact_revisions
    ADD COLUMN IF NOT EXISTS restored_from_revision_id UUID;
