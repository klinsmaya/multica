-- Dropping the constraint drops the index it took over, leaving 586's down
-- direction a no-op via IF EXISTS.
ALTER TABLE issue_read_cursor DROP CONSTRAINT IF EXISTS issue_read_cursor_pkey;
