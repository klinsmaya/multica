-- name: ListProjectPullRequestPage :many
-- One page of the distinct PRs (GitHub and self-hosted VCS together) linked to
-- any issue of a project, newest update first. A PR linked to several issues of
-- the project is one row, so limit/offset page per PR rather than per link.
-- state_filter is the set of stored states to keep; empty keeps every state.
WITH linked AS (
    SELECT DISTINCT 'github'::text AS kind, pr.id, pr.state, pr.pr_updated_at
    FROM issue_pull_request ipr
    JOIN github_pull_request pr ON pr.id = ipr.pull_request_id
    JOIN issue i ON i.id = ipr.issue_id
    WHERE i.project_id = sqlc.arg('project_id')
      AND i.workspace_id = sqlc.arg('workspace_id')
      AND pr.workspace_id = sqlc.arg('workspace_id')
    UNION ALL
    SELECT DISTINCT pr.provider AS kind, pr.id, pr.state, pr.pr_updated_at
    FROM issue_vcs_pull_request ipr
    JOIN vcs_pull_request pr ON pr.id = ipr.pull_request_id
    JOIN issue i ON i.id = ipr.issue_id
    WHERE i.project_id = sqlc.arg('project_id')
      AND i.workspace_id = sqlc.arg('workspace_id')
      AND pr.workspace_id = sqlc.arg('workspace_id')
)
SELECT kind, id
FROM linked
WHERE cardinality(sqlc.arg('state_filter')::text[]) = 0
   OR state = ANY(sqlc.arg('state_filter')::text[])
ORDER BY pr_updated_at DESC, id DESC
LIMIT sqlc.arg('page_limit') OFFSET sqlc.arg('page_offset');

-- name: CountProjectPullRequestsByState :many
-- Distinct linked PR count per stored state, over GitHub and VCS PRs. Ignores
-- any state filter so the tab can show every group's count from one request.
WITH linked AS (
    SELECT DISTINCT pr.id, pr.state
    FROM issue_pull_request ipr
    JOIN github_pull_request pr ON pr.id = ipr.pull_request_id
    JOIN issue i ON i.id = ipr.issue_id
    WHERE i.project_id = sqlc.arg('project_id')
      AND i.workspace_id = sqlc.arg('workspace_id')
      AND pr.workspace_id = sqlc.arg('workspace_id')
    UNION ALL
    SELECT DISTINCT pr.id, pr.state
    FROM issue_vcs_pull_request ipr
    JOIN vcs_pull_request pr ON pr.id = ipr.pull_request_id
    JOIN issue i ON i.id = ipr.issue_id
    WHERE i.project_id = sqlc.arg('project_id')
      AND i.workspace_id = sqlc.arg('workspace_id')
      AND pr.workspace_id = sqlc.arg('workspace_id')
)
SELECT state, COUNT(*)::bigint AS total
FROM linked
GROUP BY state;

-- name: ListProjectPullRequests :many
-- GitHub PR rows for ids picked by ListProjectPullRequestPage, with the same
-- columns and snapshot / check-run aggregation as ListPullRequestsByIssue (the
-- handler reuses its row serializer). There is no per-issue link here, so
-- linked_by_type is a constant.
WITH target_prs AS (
    SELECT pr.id, pr.snapshot_head_sha
    FROM github_pull_request pr
    WHERE pr.id = ANY(sqlc.arg('pr_ids')::uuid[])
      AND pr.workspace_id = sqlc.arg('workspace_id')
),
checks AS (
    SELECT
        cr.pr_id,
        COUNT(*)::bigint AS total,
        SUM(CASE WHEN cr.status = 'completed' AND cr.conclusion IN
                ('failure','cancelled','timed_out','action_required','startup_failure','stale','error')
            THEN 1 ELSE 0 END)::bigint AS failed,
        SUM(CASE WHEN cr.status = 'completed' AND cr.conclusion IN
                ('success','neutral','skipped')
            THEN 1 ELSE 0 END)::bigint AS passed,
        SUM(CASE WHEN cr.status <> 'completed' OR cr.conclusion IS NULL
            THEN 1 ELSE 0 END)::bigint AS running,
        COALESCE(
            array_agg(cr.name) FILTER (WHERE cr.status = 'completed' AND cr.conclusion IN
                ('failure','cancelled','timed_out','action_required','startup_failure','stale','error')),
            '{}'
        )::text[] AS failed_names
    FROM github_pull_request_check_run cr
    JOIN target_prs tp ON tp.id = cr.pr_id
    WHERE cr.head_sha = tp.snapshot_head_sha AND tp.snapshot_head_sha <> ''
    GROUP BY cr.pr_id
)
SELECT
    pr.id, pr.workspace_id, pr.installation_id, pr.repo_owner, pr.repo_name,
    pr.pr_number, pr.title, pr.state, pr.html_url, pr.branch, pr.author_login,
    pr.author_avatar_url, pr.merged_at, pr.closed_at, pr.pr_created_at,
    pr.pr_updated_at, pr.head_sha, pr.mergeable_state,
    pr.additions, pr.deletions, pr.changed_files,
    pr.api_mergeable, pr.api_merge_state_status, pr.checks_rollup_state,
    pr.snapshot_head_sha, pr.snapshot_fetched_at,
    pr.created_at, pr.updated_at,
    'system'::text AS linked_by_type,
    COALESCE(c.total, 0)::bigint   AS checks_total,
    COALESCE(c.passed, 0)::bigint  AS checks_passed,
    COALESCE(c.failed, 0)::bigint  AS checks_failed,
    COALESCE(c.running, 0)::bigint AS checks_running,
    COALESCE(c.failed_names, '{}')::text[] AS failed_check_names
FROM github_pull_request pr
JOIN target_prs tp ON tp.id = pr.id
LEFT JOIN checks c ON c.pr_id = pr.id;

-- name: ListProjectVCSPullRequests :many
-- Self-hosted VCS PR rows for ids picked by ListProjectPullRequestPage, shaped
-- like ListVCSPullRequestsByIssue (commit statuses of the current head sha
-- folded into passed / failed / pending counts).
WITH checks AS (
    SELECT
        pr.id AS pr_id,
        COUNT(*)::bigint AS total,
        SUM(CASE WHEN cs.state = 'failed'  THEN 1 ELSE 0 END)::bigint AS failed,
        SUM(CASE WHEN cs.state = 'passed'  THEN 1 ELSE 0 END)::bigint AS passed,
        SUM(CASE WHEN cs.state = 'pending' THEN 1 ELSE 0 END)::bigint AS pending
    FROM vcs_pull_request pr
    JOIN vcs_commit_status cs
        ON cs.connection_id = pr.connection_id
       AND cs.sha = pr.head_sha
       AND pr.head_sha <> ''
    WHERE pr.id = ANY(sqlc.arg('pr_ids')::uuid[])
      AND pr.workspace_id = sqlc.arg('workspace_id')
    GROUP BY pr.id
)
SELECT
    pr.*,
    'system'::text AS linked_by_type,
    COALESCE(c.total, 0)::bigint   AS checks_total,
    COALESCE(c.passed, 0)::bigint  AS checks_passed,
    COALESCE(c.failed, 0)::bigint  AS checks_failed,
    COALESCE(c.pending, 0)::bigint AS checks_pending
FROM vcs_pull_request pr
LEFT JOIN checks c ON c.pr_id = pr.id
WHERE pr.id = ANY(sqlc.arg('pr_ids')::uuid[])
  AND pr.workspace_id = sqlc.arg('workspace_id');

-- name: ListProjectPullRequestIssues :many
-- The project's issues linked to each of the given PRs, GitHub and VCS ids
-- passed separately; kind is "github" or the VCS provider.
SELECT 'github'::text AS kind, ipr.pull_request_id AS pr_id, i.id AS issue_id, i.number, i.title
FROM issue_pull_request ipr
JOIN issue i ON i.id = ipr.issue_id
WHERE ipr.pull_request_id = ANY(sqlc.arg('github_pr_ids')::uuid[])
  AND i.project_id = sqlc.arg('project_id')
  AND i.workspace_id = sqlc.arg('workspace_id')
UNION ALL
SELECT pr.provider AS kind, ipr.pull_request_id AS pr_id, i.id AS issue_id, i.number, i.title
FROM issue_vcs_pull_request ipr
JOIN vcs_pull_request pr ON pr.id = ipr.pull_request_id
JOIN issue i ON i.id = ipr.issue_id
WHERE ipr.pull_request_id = ANY(sqlc.arg('vcs_pr_ids')::uuid[])
  AND i.project_id = sqlc.arg('project_id')
  AND i.workspace_id = sqlc.arg('workspace_id')
ORDER BY number;
