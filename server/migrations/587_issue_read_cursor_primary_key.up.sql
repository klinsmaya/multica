-- Attach the CONCURRENTLY-built unique index as the table's primary key.
ALTER TABLE issue_read_cursor
    ADD CONSTRAINT issue_read_cursor_pkey PRIMARY KEY USING INDEX issue_read_cursor_pkey_idx;
