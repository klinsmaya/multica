-- A04: the upload records the client declaration separately from the bytes,
-- and a stale base keeps the verified candidate as a conflict draft.
--
-- declared_media_type and filename are part of the immutable binding so a
-- later request cannot retarget what verify already checked. The temporary
-- defaults backfill any A03 rows that were inserted already committed; new
-- inserts must set both columns.

ALTER TABLE artifact_uploads
    ADD COLUMN IF NOT EXISTS declared_media_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    ADD COLUMN IF NOT EXISTS filename TEXT NOT NULL DEFAULT 'unknown';

ALTER TABLE artifact_uploads
    ALTER COLUMN declared_media_type DROP DEFAULT,
    ALTER COLUMN filename DROP DEFAULT;

ALTER TABLE artifact_uploads DROP CONSTRAINT IF EXISTS artifact_uploads_declared_media_type_check;
ALTER TABLE artifact_uploads
    ADD CONSTRAINT artifact_uploads_declared_media_type_check
    CHECK (char_length(declared_media_type) BETWEEN 1 AND 255);

ALTER TABLE artifact_uploads DROP CONSTRAINT IF EXISTS artifact_uploads_filename_check;
ALTER TABLE artifact_uploads
    ADD CONSTRAINT artifact_uploads_filename_check
    CHECK (char_length(filename) BETWEEN 1 AND 1024);

ALTER TABLE artifact_uploads DROP CONSTRAINT IF EXISTS artifact_uploads_state_check;
ALTER TABLE artifact_uploads
    ADD CONSTRAINT artifact_uploads_state_check
    CHECK (state IN (
        'created', 'uploading', 'uploaded', 'verifying', 'scanning',
        'ready', 'rejected', 'expired', 'cancelled', 'committed', 'conflict_draft'
    ));

CREATE OR REPLACE FUNCTION artifact_uploads_immutable_binding()
RETURNS TRIGGER
LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.actor_id IS DISTINCT FROM OLD.actor_id
       OR NEW.artifact_id IS DISTINCT FROM OLD.artifact_id
       OR NEW.base_revision_id IS DISTINCT FROM OLD.base_revision_id
       OR NEW.expected_size_bytes IS DISTINCT FROM OLD.expected_size_bytes
       OR NEW.expected_sha256 IS DISTINCT FROM OLD.expected_sha256
       OR NEW.staging_key IS DISTINCT FROM OLD.staging_key
       OR NEW.declared_media_type IS DISTINCT FROM OLD.declared_media_type
       OR NEW.filename IS DISTINCT FROM OLD.filename
    THEN
        RAISE EXCEPTION 'artifact upload binding is immutable';
    END IF;
    RETURN NEW;
END
$function$;
