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
    THEN
        RAISE EXCEPTION 'artifact upload binding is immutable';
    END IF;
    RETURN NEW;
END
$function$;

UPDATE artifact_uploads SET state = 'rejected' WHERE state = 'conflict_draft';

ALTER TABLE artifact_uploads DROP CONSTRAINT IF EXISTS artifact_uploads_state_check;
ALTER TABLE artifact_uploads
    ADD CONSTRAINT artifact_uploads_state_check
    CHECK (state IN (
        'created', 'uploading', 'uploaded', 'verifying', 'scanning',
        'ready', 'rejected', 'expired', 'cancelled', 'committed'
    ));

ALTER TABLE artifact_uploads DROP CONSTRAINT IF EXISTS artifact_uploads_declared_media_type_check;
ALTER TABLE artifact_uploads DROP CONSTRAINT IF EXISTS artifact_uploads_filename_check;

ALTER TABLE artifact_uploads DROP COLUMN IF EXISTS declared_media_type;
ALTER TABLE artifact_uploads DROP COLUMN IF EXISTS filename;
