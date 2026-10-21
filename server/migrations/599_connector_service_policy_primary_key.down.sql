-- Dropping the constraint drops the index it took over, leaving 598's down
-- direction a no-op via IF EXISTS.
ALTER TABLE connector_service_policy DROP CONSTRAINT IF EXISTS connector_service_policy_pkey;
