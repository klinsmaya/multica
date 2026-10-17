-- One open lease per artifact. Expired and committed rows stay, with their drafts.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_edit_sessions_one_open_uidx
    ON artifact_edit_sessions (artifact_id)
    WHERE state = 'open';
