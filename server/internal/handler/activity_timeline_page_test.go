package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// Tests for the root-paged timeline (`?roots=`). Timestamps are inserted at
// microsecond precision (what PostgreSQL stores) and compared as instants, so a
// cursor that lost sub-second precision shows up as a duplicated or skipped row
// instead of passing by accident. Roots are 1ms apart on purpose: every page
// boundary falls inside one wall-clock second.

type pagedThreads struct {
	rootIDs   []string    // oldest first
	rootTimes []time.Time // parallel to rootIDs
	replyIDs  [][]string  // replyIDs[i] are the replies of rootIDs[i], oldest first
	allIDs    map[string]bool
	base      time.Time
}

// seedPagedThreads inserts n roots with repliesPer replies each. Root i is at
// base + i ms. Replies come well after every root (base + 1s + i ms + j µs), so
// replies of old roots are newer than later roots, as in a real conversation.
func seedPagedThreads(t *testing.T, issueID string, n, repliesPer int) pagedThreads {
	t.Helper()
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	out := pagedThreads{base: base, allIDs: map[string]bool{}}
	for i := 0; i < n; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond)
		id := dbfx.Comment(t, issueID, fmt.Sprintf("root %d", i), testutil.Cols{
			"created_at": at,
			"updated_at": at,
		})
		out.rootIDs = append(out.rootIDs, id)
		out.rootTimes = append(out.rootTimes, at)
		out.allIDs[id] = true
		var replies []string
		for j := 0; j < repliesPer; j++ {
			rat := base.Add(time.Second + time.Duration(i)*time.Millisecond + time.Duration(j+1)*time.Microsecond)
			rid := dbfx.Comment(t, issueID, fmt.Sprintf("reply %d.%d", i, j), testutil.Cols{
				"parent_id":  id,
				"created_at": rat,
				"updated_at": rat,
			})
			replies = append(replies, rid)
			out.allIDs[rid] = true
		}
		out.replyIDs = append(out.replyIDs, replies)
	}
	return out
}

func seedPagedActivity(t *testing.T, issueID string, at time.Time) string {
	t.Helper()
	return dbfx.Insert(t, "activity_log", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"issue_id":     issueID,
		"actor_type":   "member",
		"actor_id":     testUserID,
		"action":       "status_changed",
		"details":      testutil.Raw(`'{"from":"todo","to":"in_progress"}'::jsonb`),
		"created_at":   at,
	})
}

func timelinePageRequestFor(issueID, rawQuery string) *http.Request {
	target := "/api/issues/" + issueID + "/timeline"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	return withURLParam(newRequest("GET", target, nil), "id", issueID)
}

func getTimelinePage(t *testing.T, issueID, rawQuery string) timelinePaginatedResponse {
	t.Helper()
	var out timelinePaginatedResponse
	testutil.Call(t, testHandler.ListTimeline, timelinePageRequestFor(issueID, rawQuery)).
		Want(http.StatusOK).JSON(&out)
	return out
}

// pageQuery builds the query string for the page after prev.
func pageQuery(limit int, prev *timelinePaginatedResponse) string {
	q := fmt.Sprintf("roots=%d", limit)
	if prev != nil {
		q += "&before=" + url.QueryEscape(*prev.NextBefore) + "&before_id=" + url.QueryEscape(*prev.NextBeforeID)
	}
	return q
}

func splitPage(resp timelinePaginatedResponse) (roots []TimelineEntry, replies []TimelineEntry, activities []TimelineEntry) {
	for _, e := range resp.Entries {
		switch {
		case e.Type == "activity":
			activities = append(activities, e)
		case e.ParentID == nil:
			roots = append(roots, e)
		default:
			replies = append(replies, e)
		}
	}
	return
}

// assertRootSet checks which roots a page holds. Entry order inside a page is
// not asserted: entries carry second-precision timestamps, so roots created
// milliseconds apart tie and fall back to id order (the legacy merge order).
func assertRootSet(t *testing.T, got []TimelineEntry, want []string) {
	t.Helper()
	wantSet := map[string]bool{}
	for _, id := range want {
		wantSet[id] = true
	}
	if len(got) != len(want) {
		t.Errorf("page holds %d roots, want %d", len(got), len(want))
	}
	for _, r := range got {
		if !wantSet[r.ID] {
			t.Errorf("unexpected root %s on the page", r.ID)
		}
	}
}

func TestListTimeline_RootPaged_FirstPageCarriesWholeThreads(t *testing.T) {
	issueID := dbfx.Issue(t, "Root paged first page")
	threads := seedPagedThreads(t, issueID, 120, 2)

	resp := getTimelinePage(t, issueID, "roots=50")
	roots, replies, activities := splitPage(resp)

	if len(roots) != 50 || len(replies) != 100 || len(activities) != 0 {
		t.Fatalf("roots/replies/activities = %d/%d/%d, want 50/100/0", len(roots), len(replies), len(activities))
	}
	if !resp.HasMoreBefore {
		t.Errorf("has_more_before = false, want true (70 roots remain)")
	}
	if resp.HasMoreAfter {
		t.Errorf("has_more_after = true, want false on the first page")
	}

	// The page is exactly the newest 50 roots, and every reply sits under a
	// returned root.
	wantRoots := map[string]bool{}
	for _, id := range threads.rootIDs[70:] {
		wantRoots[id] = true
	}
	for _, r := range roots {
		if !wantRoots[r.ID] {
			t.Errorf("root %s is not among the newest 50", r.ID)
		}
	}
	for _, r := range replies {
		if r.ParentID == nil || !wantRoots[*r.ParentID] {
			t.Errorf("reply %s has parent %v outside the page roots", r.ID, r.ParentID)
		}
	}

	// The cursor points at the oldest returned root with full precision.
	oldestID, oldestAt := threads.rootIDs[70], threads.rootTimes[70]
	if resp.NextBeforeID == nil || *resp.NextBeforeID != oldestID {
		t.Fatalf("next_before_id = %v, want %s", resp.NextBeforeID, oldestID)
	}
	if resp.NextBefore == nil {
		t.Fatalf("next_before is null")
	}
	gotAt, err := time.Parse(time.RFC3339Nano, *resp.NextBefore)
	if err != nil {
		t.Fatalf("next_before %q is not RFC3339Nano: %v", *resp.NextBefore, err)
	}
	if !gotAt.Equal(oldestAt) {
		t.Errorf("next_before = %s, want %s (sub-second precision must survive)", gotAt.Format(time.RFC3339Nano), oldestAt.Format(time.RFC3339Nano))
	}
	if resp.NextCursor != nil || resp.PrevCursor != nil {
		t.Errorf("legacy cursors must stay null, got next=%v prev=%v", resp.NextCursor, resp.PrevCursor)
	}

	// Newest first, like the legacy wrapper.
	if !sort.SliceIsSorted(resp.Entries, func(i, j int) bool {
		return resp.Entries[i].CreatedAt > resp.Entries[j].CreatedAt
	}) {
		t.Errorf("entries are not ordered newest first")
	}
}

func TestListTimeline_RootPaged_WalkReturnsEveryEntryExactlyOnce(t *testing.T) {
	issueID := dbfx.Issue(t, "Root paged walk")
	threads := seedPagedThreads(t, issueID, 120, 2)

	// Activities tile by time: before every root, exactly on the first page's
	// oldest root (inclusive lower bound of page 1), exactly on page 2's oldest
	// root, in the middle of a page, and after everything.
	wantActivities := map[string]bool{}
	for _, at := range []time.Time{
		threads.base.Add(-time.Minute),
		threads.rootTimes[0],
		threads.rootTimes[20],
		threads.rootTimes[45],
		threads.rootTimes[70],
		threads.rootTimes[119].Add(time.Microsecond),
		threads.base.Add(10 * time.Second),
	} {
		wantActivities[seedPagedActivity(t, issueID, at)] = true
	}

	seen := map[string]int{}
	var prev *timelinePaginatedResponse
	var pageRoots []int
	for page := 0; page < 10; page++ {
		resp := getTimelinePage(t, issueID, pageQuery(50, prev))
		roots, replies, _ := splitPage(resp)
		pageRoots = append(pageRoots, len(roots))
		for _, e := range resp.Entries {
			seen[e.ID]++
		}
		// Whole threads on every page.
		onPage := map[string]bool{}
		for _, r := range roots {
			onPage[r.ID] = true
		}
		for _, r := range replies {
			if r.ParentID == nil || !onPage[*r.ParentID] {
				t.Fatalf("page %d: reply %s is separated from its root", page, r.ID)
			}
		}
		if !resp.HasMoreBefore {
			break
		}
		if resp.NextBefore == nil || resp.NextBeforeID == nil {
			t.Fatalf("page %d: has_more_before without a cursor", page)
		}
		prev = &resp
	}

	if fmt.Sprint(pageRoots) != "[50 50 20]" {
		t.Errorf("roots per page = %v, want [50 50 20]", pageRoots)
	}
	for id := range threads.allIDs {
		if seen[id] != 1 {
			t.Errorf("comment %s appeared %d times, want exactly 1", id, seen[id])
		}
	}
	for id := range wantActivities {
		if seen[id] != 1 {
			t.Errorf("activity %s appeared %d times, want exactly 1", id, seen[id])
		}
	}
	if want := len(threads.allIDs) + len(wantActivities); len(seen) != want {
		t.Errorf("distinct entries = %d, want %d", len(seen), want)
	}
}

func TestListTimeline_RootPaged_ExactBoundaryHasNoMore(t *testing.T) {
	issueID := dbfx.Issue(t, "Root paged exact boundary")
	seedPagedThreads(t, issueID, 5, 1)

	resp := getTimelinePage(t, issueID, "roots=5")
	if resp.HasMoreBefore {
		t.Errorf("has_more_before = true with exactly `roots` roots, want false")
	}
	roots, replies, _ := splitPage(resp)
	if len(roots) != 5 || len(replies) != 5 {
		t.Errorf("roots/replies = %d/%d, want 5/5", len(roots), len(replies))
	}
}

func TestListTimeline_RootPaged_IssueWithoutCommentsReturnsActivities(t *testing.T) {
	issueID := dbfx.Issue(t, "Root paged activity only")
	id := seedPagedActivity(t, issueID, time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute))

	resp := getTimelinePage(t, issueID, "roots=50")
	if resp.HasMoreBefore || resp.NextBefore != nil || resp.NextBeforeID != nil {
		t.Errorf("empty comment set must not offer a cursor: %+v", resp)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].ID != id {
		t.Errorf("entries = %+v, want the single activity %s", resp.Entries, id)
	}
}

func TestListTimeline_RootPaged_AroundLandsOnTheAnchorsPage(t *testing.T) {
	issueID := dbfx.Issue(t, "Root paged around")
	threads := seedPagedThreads(t, issueID, 30, 2)

	// A reply to the 11th root (index 10): the page must start at that root.
	anchor := threads.replyIDs[10][1]
	resp := getTimelinePage(t, issueID, "roots=5&around="+anchor)

	roots, replies, _ := splitPage(resp)
	if len(roots) != 5 {
		t.Fatalf("roots = %d, want 5", len(roots))
	}
	assertRootSet(t, roots, threads.rootIDs[6:11])
	if len(replies) != 10 {
		t.Errorf("replies = %d, want 10 (two per root)", len(replies))
	}
	if resp.TargetIndex == nil {
		t.Fatalf("target_index missing")
	}
	if got := resp.Entries[*resp.TargetIndex].ID; got != anchor {
		t.Errorf("target_index points at %s, want %s", got, anchor)
	}
	if !resp.HasMoreBefore {
		t.Errorf("has_more_before = false, want true (roots 0..5 are older)")
	}
	if !resp.HasMoreAfter {
		t.Errorf("has_more_after = false, want true (roots 11..29 are newer)")
	}
	if resp.NextBeforeID == nil || *resp.NextBeforeID != threads.rootIDs[6] {
		t.Errorf("next_before_id = %v, want %s", resp.NextBeforeID, threads.rootIDs[6])
	}

	// The anchor can be a root itself, and the newest root has nothing after.
	newest := threads.rootIDs[29]
	resp = getTimelinePage(t, issueID, "roots=5&around="+newest)
	if resp.HasMoreAfter {
		t.Errorf("has_more_after = true around the newest root, want false")
	}
	if resp.TargetIndex == nil || resp.Entries[*resp.TargetIndex].ID != newest {
		t.Errorf("target_index does not point at the newest root")
	}

	// The cursor continues from the around page like any other page.
	resp = getTimelinePage(t, issueID, "roots=5&around="+anchor)
	next := getTimelinePage(t, issueID, pageQuery(5, &resp))
	nextRoots, _, _ := splitPage(next)
	assertRootSet(t, nextRoots, threads.rootIDs[1:6])
}

func TestListTimeline_RootPaged_AroundUnknownCommentFallsBackToFirstPage(t *testing.T) {
	issueID := dbfx.Issue(t, "Root paged around unknown")
	threads := seedPagedThreads(t, issueID, 8, 0)

	resp := getTimelinePage(t, issueID, "roots=3&around=00000000-0000-4000-8000-000000000001")
	roots, _, _ := splitPage(resp)
	assertRootSet(t, roots, threads.rootIDs[5:8])
	if resp.TargetIndex != nil {
		t.Errorf("target_index = %d, want none for an unknown anchor", *resp.TargetIndex)
	}
}

func TestListTimeline_RootPaged_RejectsMalformedParams(t *testing.T) {
	issueID := dbfx.Issue(t, "Root paged validation")
	cursorAt := url.QueryEscape(time.Now().UTC().Format(time.RFC3339Nano))
	const someID = "00000000-0000-4000-8000-000000000001"

	for name, query := range map[string]string{
		"zero roots":           "roots=0",
		"negative roots":       "roots=-3",
		"roots over the max":   "roots=201",
		"non-numeric roots":    "roots=abc",
		"fractional roots":     "roots=1.5",
		"before without id":    "roots=10&before=" + cursorAt,
		"before_id without at": "roots=10&before_id=" + someID,
		"malformed before":     "roots=10&before=yesterday&before_id=" + someID,
		"malformed before_id":  "roots=10&before=" + cursorAt + "&before_id=nope",
		"malformed around":     "roots=10&around=nope",
		"around with a cursor": "roots=10&around=" + someID + "&before=" + cursorAt + "&before_id=" + someID,
	} {
		t.Run(name, func(t *testing.T) {
			testutil.Call(t, testHandler.ListTimeline, timelinePageRequestFor(issueID, query)).
				Want(http.StatusBadRequest)
		})
	}

	// The documented bounds are accepted.
	getTimelinePage(t, issueID, "roots=1")
	getTimelinePage(t, issueID, "roots=200")
}

func TestListTimeline_WithoutLimitKeepsUnpagedBehavior(t *testing.T) {
	issueID := dbfx.Issue(t, "Root paged leaves unpaged alone")
	threads := seedPagedThreads(t, issueID, 60, 1)
	activityID := seedPagedActivity(t, issueID, threads.base.Add(-time.Minute))

	// No params: the flat ascending array with everything in it.
	var flat []TimelineEntry
	testutil.Call(t, testHandler.ListTimeline, timelinePageRequestFor(issueID, "")).
		Want(http.StatusOK).JSON(&flat)
	if len(flat) != 120+1 {
		t.Fatalf("flat entries = %d, want 121", len(flat))
	}
	if flat[0].ID != activityID {
		t.Errorf("flat timeline is not oldest first: first entry %s", flat[0].ID)
	}

	// limit / before / after / around WITHOUT roots stay on the legacy wrapper: the
	// whole timeline, null cursors, nothing paged.
	for _, query := range []string{
		"limit=50",
		"before=" + url.QueryEscape(time.Now().UTC().Format(time.RFC3339Nano)),
		"after=" + url.QueryEscape(threads.base.Format(time.RFC3339Nano)),
		"around=" + threads.rootIDs[3],
	} {
		resp := getTimelinePage(t, issueID, query)
		if len(resp.Entries) != 121 {
			t.Errorf("%s: entries = %d, want 121", query, len(resp.Entries))
		}
		if resp.HasMoreBefore || resp.HasMoreAfter || resp.NextBefore != nil || resp.NextBeforeID != nil {
			t.Errorf("%s: legacy wrapper grew paging state: %+v", query, resp)
		}
	}

	// An empty roots is "absent", not an error.
	var flatAgain []TimelineEntry
	testutil.Call(t, testHandler.ListTimeline, timelinePageRequestFor(issueID, "roots=")).
		Want(http.StatusOK).JSON(&flatAgain)
	if len(flatAgain) != 121 {
		t.Errorf("roots= entries = %d, want 121", len(flatAgain))
	}
}

// With a shrunk reply cap, a page keeps whole threads: the thread the cap cuts
// through and every older one move to the next page instead of arriving partial.
func TestListTimeline_RootPaged_ReplyCapDefersWholeThreads(t *testing.T) {
	shrinkListCapsForTest(t) // reply cap = commentHardCap = 20
	issueID := dbfx.Issue(t, "Root paged reply cap")
	threads := seedPagedThreads(t, issueID, 5, 8)

	seen := map[string]int{}
	var prev *timelinePaginatedResponse
	var pageRoots []int
	for page := 0; page < 10; page++ {
		resp := getTimelinePage(t, issueID, pageQuery(5, prev))
		roots, replies, _ := splitPage(resp)
		pageRoots = append(pageRoots, len(roots))
		onPage := map[string]bool{}
		for _, r := range roots {
			onPage[r.ID] = true
		}
		perRoot := map[string]int{}
		for _, r := range replies {
			if r.ParentID == nil || !onPage[*r.ParentID] {
				t.Fatalf("page %d: reply %s is separated from its root", page, r.ID)
			}
			perRoot[*r.ParentID]++
		}
		for _, r := range roots {
			if perRoot[r.ID] != 8 {
				t.Fatalf("page %d: root %s carries %d replies, want all 8", page, r.ID, perRoot[r.ID])
			}
		}
		for _, e := range resp.Entries {
			seen[e.ID]++
		}
		if !resp.HasMoreBefore {
			break
		}
		prev = &resp
	}
	// 2 threads (16 replies) fit under the cap of 20; a third would not.
	if fmt.Sprint(pageRoots) != "[2 2 1]" {
		t.Errorf("roots per page = %v, want [2 2 1]", pageRoots)
	}
	for id := range threads.allIDs {
		if seen[id] != 1 {
			t.Errorf("comment %s appeared %d times, want exactly 1", id, seen[id])
		}
	}
}

// A single thread bigger than the reply cap cannot be deferred, so it is
// returned as a chronological prefix and the truncation is reported.
func TestListTimeline_RootPaged_OversizedThreadIsReportedTruncated(t *testing.T) {
	shrinkListCapsForTest(t) // reply cap = 20
	issueID := dbfx.Issue(t, "Root paged oversized thread")
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)

	olderRoot := dbfx.Comment(t, issueID, "older root", testutil.Cols{"created_at": base, "updated_at": base})
	olderReply := dbfx.Comment(t, issueID, "older reply", testutil.Cols{
		"parent_id": olderRoot, "created_at": base.Add(time.Second), "updated_at": base.Add(time.Second),
	})
	bigAt := base.Add(time.Minute)
	bigRoot := dbfx.Comment(t, issueID, "big root", testutil.Cols{"created_at": bigAt, "updated_at": bigAt})
	var bigReplies []string
	for j := 0; j < 25; j++ {
		at := bigAt.Add(time.Duration(j+1) * time.Second)
		bigReplies = append(bigReplies, dbfx.Comment(t, issueID, fmt.Sprintf("big reply %d", j), testutil.Cols{
			"parent_id": bigRoot, "created_at": at, "updated_at": at,
		}))
	}

	w := testutil.Call(t, testHandler.ListTimeline, timelinePageRequestFor(issueID, "roots=5")).Want(http.StatusOK)
	var first timelinePaginatedResponse
	w.JSON(&first)
	if got := w.Header().Get(HeaderTimelineTruncated); got != "comment" {
		t.Errorf("%s = %q, want \"comment\"", HeaderTimelineTruncated, got)
	}
	roots, replies, _ := splitPage(first)
	if len(roots) != 1 || roots[0].ID != bigRoot || len(replies) != 20 {
		t.Fatalf("first page roots/replies = %d/%d, want the big root with 20 replies", len(roots), len(replies))
	}
	got := map[string]bool{}
	for _, r := range replies {
		got[r.ID] = true
	}
	for _, id := range bigReplies[:20] {
		if !got[id] {
			t.Errorf("chronological prefix is missing reply %s", id)
		}
	}
	if !first.HasMoreBefore {
		t.Fatalf("has_more_before = false, want true: the older thread is still ahead")
	}

	second := getTimelinePage(t, issueID, pageQuery(5, &first))
	roots, replies, _ = splitPage(second)
	if len(roots) != 1 || roots[0].ID != olderRoot || len(replies) != 1 || replies[0].ID != olderReply {
		t.Errorf("second page = roots %v replies %v, want the older thread", roots, replies)
	}
	if second.HasMoreBefore {
		t.Errorf("second page has_more_before = true, want false")
	}
}
