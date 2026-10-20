package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const DisabledUserError = "account disabled"

var ErrDisabledUser = errors.New(DisabledUserError)

// DisabledUserRefreshInterval bounds how long another API replica keeps
// accepting a user after an admin suspends them: every replica reloads the
// set from "user".disabled_at on this cadence. The replica that handled the
// suspension applies it immediately through MarkUserDisabled.
const DisabledUserRefreshInterval = 30 * time.Second

// DisabledUserEntry is one suspended account as the refresh query reports it.
type DisabledUserEntry struct {
	ID    string
	Email string
}

// DisabledUserSource is the read side of account suspension; implemented by
// the sqlc Queries through a small adapter so this package stays free of db.
type DisabledUserSource interface {
	ListDisabledUsers(ctx context.Context) ([]DisabledUserEntry, error)
}

// The set is process-wide because every credential path (JWT, PAT, daemon,
// WebSocket, session renewal) checks it without access to a handler. It is
// read on every authenticated request, so it is an in-memory set, never a
// query.
var disabledUsers = struct {
	mu     sync.RWMutex
	ids    map[string]struct{}
	emails map[string]struct{}
}{ids: map[string]struct{}{}, emails: map[string]struct{}{}}

func normalizeDisabledKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func IsDisabledUser(userID, email string) bool {
	return IsDisabledUserID(userID) || IsDisabledUserEmail(email)
}

func IsDisabledUserID(userID string) bool {
	userID = normalizeDisabledKey(userID)
	if userID == "" {
		return false
	}
	disabledUsers.mu.RLock()
	defer disabledUsers.mu.RUnlock()
	_, ok := disabledUsers.ids[userID]
	return ok
}

func IsDisabledUserEmail(email string) bool {
	email = normalizeDisabledKey(email)
	if email == "" {
		return false
	}
	disabledUsers.mu.RLock()
	defer disabledUsers.mu.RUnlock()
	_, ok := disabledUsers.emails[email]
	return ok
}

// MarkUserDisabled applies a suspension to this process at once; other
// replicas pick it up on their next refresh.
func MarkUserDisabled(userID, email string) {
	disabledUsers.mu.Lock()
	defer disabledUsers.mu.Unlock()
	if id := normalizeDisabledKey(userID); id != "" {
		disabledUsers.ids[id] = struct{}{}
	}
	if e := normalizeDisabledKey(email); e != "" {
		disabledUsers.emails[e] = struct{}{}
	}
}

// MarkUserEnabled is the local counterpart of MarkUserDisabled.
func MarkUserEnabled(userID, email string) {
	disabledUsers.mu.Lock()
	defer disabledUsers.mu.Unlock()
	delete(disabledUsers.ids, normalizeDisabledKey(userID))
	delete(disabledUsers.emails, normalizeDisabledKey(email))
}

// ReplaceDisabledUsers swaps the whole set for what the database reports.
func ReplaceDisabledUsers(entries []DisabledUserEntry) {
	ids := make(map[string]struct{}, len(entries))
	emails := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if id := normalizeDisabledKey(e.ID); id != "" {
			ids[id] = struct{}{}
		}
		if email := normalizeDisabledKey(e.Email); email != "" {
			emails[email] = struct{}{}
		}
	}
	disabledUsers.mu.Lock()
	disabledUsers.ids, disabledUsers.emails = ids, emails
	disabledUsers.mu.Unlock()
}

// RefreshDisabledUsers loads the set once. A failed refresh keeps the
// previous set rather than clearing it, so a database blip never lifts a
// suspension.
func RefreshDisabledUsers(ctx context.Context, src DisabledUserSource) error {
	entries, err := src.ListDisabledUsers(ctx)
	if err != nil {
		return err
	}
	ReplaceDisabledUsers(entries)
	return nil
}

// RunDisabledUserRefresh refreshes until ctx is done. Call it once per process
// after the first synchronous RefreshDisabledUsers at startup.
func RunDisabledUserRefresh(ctx context.Context, src DisabledUserSource, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := RefreshDisabledUsers(ctx, src); err != nil && ctx.Err() == nil {
				slog.Warn("disabled user refresh failed", "error", err)
			}
		}
	}
}
