DROP TABLE IF EXISTS artifact_links;
DROP TABLE IF EXISTS artifact_revisions;
DROP TABLE IF EXISTS artifact_uploads;
DROP TABLE IF EXISTS artifact_operations;
DROP TABLE IF EXISTS artifact_blobs;
DROP TABLE IF EXISTS artifacts;

DROP FUNCTION IF EXISTS artifact_revisions_immutable();
DROP FUNCTION IF EXISTS artifact_uploads_immutable_binding();
DROP FUNCTION IF EXISTS artifact_blobs_immutable_bytes();
DROP FUNCTION IF EXISTS artifact_operations_immutable_identity();
