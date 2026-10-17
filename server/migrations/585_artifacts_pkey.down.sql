-- Dropping the constraint drops the index it took over.
ALTER TABLE artifacts DROP CONSTRAINT IF EXISTS artifacts_pkey;
