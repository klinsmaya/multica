-- Dropping the constraint drops the index it took over, leaving 584's down
-- direction a no-op via IF EXISTS.
ALTER TABLE platform_admin DROP CONSTRAINT IF EXISTS platform_admin_pkey;
