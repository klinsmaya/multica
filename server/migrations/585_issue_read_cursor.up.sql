-- Per-user issue read cursors for the project feed's unread dots.
--
-- One row per (user, issue): last_read_at is the server time at which the user
-- last opened the issue. A card is unread when the issue's newest counted
-- comment is later than the cursor; no row means the user never opened it.
--
-- workspace_id is stored so member removal and workspace teardown can sweep a
-- user's or a workspace's cursors without joining issue. No foreign keys by
-- project rule: issue deletion, member removal and workspace deletion clean
-- the rows up in application code.
--
-- (user_id, issue_id) is attached as the primary key in 587 from the index
-- built CONCURRENTLY in 586 (repo convention, see 573-575).
CREATE TABLE IF NOT EXISTS issue_read_cursor (
    user_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    last_read_at TIMESTAMPTZ NOT NULL
);
