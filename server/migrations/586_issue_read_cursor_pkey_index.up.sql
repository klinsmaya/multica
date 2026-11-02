-- Backing index for issue_read_cursor's primary key, attached in 587.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS issue_read_cursor_pkey_idx
    ON issue_read_cursor (user_id, issue_id);
