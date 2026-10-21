package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Root-paged timeline (`GET /api/issues/{id}/timeline?roots=N`).
//
// The opt-in param is `roots`, not `limit`: installed clients between #2128
// and #1929 send `?limit=` expecting the whole timeline in the legacy wrapper,
// and that contract is kept (see ListTimeline).
//
// The unpaged timeline loads every comment and activity of an issue. Paging it
// by time split reply threads at page boundaries, which is why the earlier
// time-cursor design was removed. This path pages on ROOT comments instead:
//
//   - A page is `roots` top-level comments (1..timelinePageMaxLimit), newest
//     first by (created_at, id). `before` + `before_id` is a composite cursor
//     meaning "roots strictly older than this pair"; the page emits the next
//     one as `next_before` / `next_before_id`, pointing at the oldest root
//     returned.
//   - Every reply, at any depth, of a returned root is returned with it, so a
//     page never contains a partial thread.
//   - Activities are partitioned by TIME: a page owns [oldest returned root,
//     previous page's oldest root). The newest page has no upper bound and the
//     last page no lower bound, so adjacent pages tile the timeline with no gap
//     and no overlap even though replies and activities interleave in time.
//   - `has_more_before` is exact: the roots query reads one row past `roots`.
//
// Bounds. Replies of one page are capped at commentHardCap. When a page's
// threads would exceed it, whole trailing (older) threads are dropped from the
// page and the cursor continues from the last complete one, so the client still
// sees complete threads. Only when the single newest thread alone exceeds the
// cap is it returned with a chronological prefix of its replies (a prefix is
// closed under "parent of") and X-Timeline-Truncated names "comment". The
// activity list keeps the newest timelineHardCap rows of its window and reports
// "activity" the same way. Thread completion therefore needs no ancestor
// backfill: the descendants of the returned roots are read directly, which is
// why completeCommentThreads is not used on this path.
//
// `around=<commentId>` resolves the comment's thread root and returns the page
// that STARTS at that root and continues toward older roots, so the anchor is
// always on the page. There is deliberately no after-cursor: `has_more_after`
// reports whether newer roots exist, but the newer side is reached by the
// regular first page, not by walking forward. A client that mixes the two
// holds a hole between them, so the web client loads an anchor by walking older
// pages from the first page instead. An unknown or deleted anchor falls back to
// the first page without `target_index`. `around` cannot be combined with
// `before` / `before_id`.
//
// Everything without `roots` — including `limit` / `before` / `after` /
// `around` alone —
// keeps the legacy behavior in ListTimeline.

// timelinePageMaxLimit is the largest accepted `roots`.
const timelinePageMaxLimit = 200

type timelinePageRequest struct {
	limit int
	// hasCursor bounds the page to roots strictly older than (cursorAt, cursorID).
	hasCursor bool
	cursorAt  time.Time
	cursorID  pgtype.UUID
	// around is the comment to land on, valid only when hasAround.
	hasAround bool
	around    pgtype.UUID
}

// parseTimelinePageRequest validates the paging params and writes a 400 when
// they are malformed. rawLimit is the raw, non-empty `roots` value.
func parseTimelinePageRequest(w http.ResponseWriter, q url.Values, rawLimit string) (timelinePageRequest, bool) {
	var req timelinePageRequest

	limit, err := strconv.Atoi(rawLimit)
	if err != nil || limit < 1 || limit > timelinePageMaxLimit {
		writeError(w, http.StatusBadRequest, "invalid roots parameter; expected integer between 1 and "+strconv.Itoa(timelinePageMaxLimit))
		return req, false
	}
	req.limit = limit

	rawBefore, rawBeforeID, rawAround := q.Get("before"), q.Get("before_id"), q.Get("around")
	if (rawBefore == "") != (rawBeforeID == "") {
		writeError(w, http.StatusBadRequest, "before and before_id must be set together (composite cursor)")
		return req, false
	}
	if rawAround != "" && rawBefore != "" {
		writeError(w, http.StatusBadRequest, "around cannot be combined with before / before_id")
		return req, false
	}
	if rawBefore != "" {
		at, err := time.Parse(time.RFC3339Nano, rawBefore)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid before parameter; expected RFC3339 format")
			return req, false
		}
		id, ok := parseUUIDOrBadRequest(w, rawBeforeID, "before_id")
		if !ok {
			return req, false
		}
		req.hasCursor, req.cursorAt, req.cursorID = true, at, id
	}
	if rawAround != "" {
		id, ok := parseUUIDOrBadRequest(w, rawAround, "around")
		if !ok {
			return req, false
		}
		req.hasAround, req.around = true, id
	}
	return req, true
}

// resolveCommentThreadRoot walks parent_id up from commentID to its thread
// root, one tenant-scoped lookup per level and bounded by commentThreadMaxDepth.
// ok is false when the comment, or a link of its chain, does not exist in this
// issue (deleted anchor, cross-tenant id, depth bound exceeded).
func (h *Handler) resolveCommentThreadRoot(r *http.Request, issue db.Issue, commentID pgtype.UUID) (root db.Comment, ok bool, err error) {
	current := commentID
	for depth := 0; depth <= commentThreadMaxDepth; depth++ {
		rows, err := h.Queries.ListCommentsByIDsForIssue(r.Context(), db.ListCommentsByIDsForIssueParams{
			Ids:         []pgtype.UUID{current},
			IssueID:     issue.ID,
			WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			return db.Comment{}, false, err
		}
		if len(rows) == 0 {
			return db.Comment{}, false, nil
		}
		if !rows[0].ParentID.Valid {
			return rows[0], true, nil
		}
		current = rows[0].ParentID
	}
	return db.Comment{}, false, nil
}

// listTimelinePage serves the root-paged timeline. See the header comment.
func (h *Handler) listTimelinePage(w http.ResponseWriter, r *http.Request, issue db.Issue, req timelinePageRequest) {
	ctx := r.Context()

	// Resolve `around` into a cursor just after the anchor's root. Stored
	// timestamps have microsecond precision, so one microsecond past the root
	// makes the strict "<" include it (and any root sharing its instant).
	foundAround := false
	if req.hasAround {
		root, ok, err := h.resolveCommentThreadRoot(r, issue, req.around)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve comment thread")
			return
		}
		if ok {
			foundAround = true
			req.hasCursor = true
			req.cursorAt = root.CreatedAt.Time.Add(time.Microsecond)
			// The all-zero id is irrelevant: every root created before the
			// bumped instant already sorts below the cursor on created_at.
			req.cursorID = pgtype.UUID{Valid: true}
		}
	}
	cursorAt := pgtype.Timestamptz{Time: req.cursorAt, Valid: req.hasCursor}

	roots, err := h.Queries.ListCommentRootsForIssueBefore(ctx, db.ListCommentRootsForIssueBeforeParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		HasCursor:   req.hasCursor,
		BeforeAt:    cursorAt,
		BeforeID:    req.cursorID,
		RowLimit:    int32(req.limit) + 1,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	hasMoreBefore := len(roots) > req.limit
	if hasMoreBefore {
		roots = roots[:req.limit]
	}

	var comments []db.Comment
	replyTruncated := false
	if len(roots) > 0 {
		rootIDs := make([]pgtype.UUID, len(roots))
		for i, c := range roots {
			rootIDs[i] = c.ID
		}
		replyCap := commentHardCap
		rows, err := h.Queries.ListCommentRepliesForRoots(ctx, db.ListCommentRepliesForRootsParams{
			RootIds:     rootIDs,
			IssueID:     issue.ID,
			WorkspaceID: issue.WorkspaceID,
			RowLimit:    int32(replyCap) + 1,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list comment replies")
			return
		}
		if len(rows) > replyCap {
			// Rows are grouped by root, newest root first, so the probe row's
			// root is the thread the cap cut through. Drop it and every older
			// root; the cursor then continues from the last complete thread.
			cutRoot := rows[replyCap].RootID
			firstRow := 0
			for i, row := range rows {
				if row.RootID == cutRoot {
					firstRow = i
					break
				}
			}
			if firstRow == 0 {
				// The newest thread alone exceeds the cap: keep a chronological
				// prefix of it rather than returning nothing.
				roots = roots[:1]
				rows = rows[:replyCap]
				replyTruncated = true
			} else {
				keep := len(roots)
				for i, c := range roots {
					if c.ID == cutRoot {
						keep = i
						break
					}
				}
				roots = roots[:keep]
				rows = rows[:firstRow]
			}
			hasMoreBefore = true
		}
		comments = make([]db.Comment, 0, len(roots)+len(rows))
		comments = append(comments, roots...)
		for _, row := range rows {
			comments = append(comments, row.Comment)
		}
	}

	// Activities: [oldest returned root, cursor). The last page (no older
	// roots) also owns everything before its oldest root, and the first page
	// has no upper bound, so the pages tile the whole timeline.
	var from pgtype.Timestamptz
	if hasMoreBefore && len(roots) > 0 {
		from = roots[len(roots)-1].CreatedAt
	}
	activities, err := h.Queries.ListActivitiesForIssueBetween(ctx, db.ListActivitiesForIssueBetweenParams{
		IssueID:  issue.ID,
		HasFrom:  from.Valid,
		FromAt:   from,
		HasTo:    cursorAt.Valid,
		ToAt:     cursorAt,
		RowLimit: timelineProbeLimit(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}
	activities, activitiesTruncated := takeNewest(activities)

	if kinds := truncatedKinds(replyTruncated, activitiesTruncated); kinds != "" {
		w.Header().Set(HeaderTimelineTruncated, kinds)
	}

	entries := h.mergeTimeline(r, comments, activities, false)
	h.hydrateTimelineMemberActors(ctx, entries)
	if entries == nil {
		entries = []TimelineEntry{}
	}

	resp := timelinePaginatedResponse{
		Entries:       entries,
		HasMoreBefore: hasMoreBefore,
	}
	if len(roots) > 0 {
		oldest := roots[len(roots)-1]
		resp.NextBefore = timestampToNanoPtr(oldest.CreatedAt)
		id := uuidToString(oldest.ID)
		resp.NextBeforeID = &id

		if foundAround {
			newest := roots[0]
			after, err := h.Queries.ExistsCommentRootAfterForIssue(ctx, db.ExistsCommentRootAfterForIssueParams{
				IssueID:     issue.ID,
				WorkspaceID: issue.WorkspaceID,
				AfterAt:     newest.CreatedAt,
				AfterID:     newest.ID,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list comments")
				return
			}
			resp.HasMoreAfter = after
		}
	}
	if foundAround {
		anchor := uuidToString(req.around)
		for i, e := range entries {
			if e.ID == anchor {
				idx := i
				resp.TargetIndex = &idx
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
