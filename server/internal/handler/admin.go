package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Platform admin console (/api/admin/*). Everything here is platform-level:
// it is gated by the platform_admin table, never by workspace membership, and
// every write lands in admin_audit_log inside the same transaction.

const (
	PlatformRoleSuperAdmin = "super_admin"
	PlatformRoleOperator   = "operator"
	PlatformRoleViewer     = "viewer"

	adminReasonMaxLen    = 500
	adminPageSizeMax     = 100
	adminPageSizeDefault = 25
)

var platformRoleRank = map[string]int{
	PlatformRoleViewer:     1,
	PlatformRoleOperator:   2,
	PlatformRoleSuperAdmin: 3,
}

type platformAdminCtxKey struct{}

// PlatformAdminFromContext returns the role RequirePlatformAdmin resolved.
func PlatformAdminFromContext(ctx context.Context) (db.PlatformAdmin, bool) {
	pa, ok := ctx.Value(platformAdminCtxKey{}).(db.PlatformAdmin)
	return pa, ok
}

// RequirePlatformAdmin gates a route on a platform role of at least minRole.
// It must sit behind middleware.Auth. A user listed in PLATFORM_ADMIN_EMAILS
// without a row is granted super_admin on first use, which is how the first
// administrator gets in before anyone can use the console to add others.
func (h *Handler) RequirePlatformAdmin(minRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isMachineCredentialActor(r) {
				writeError(w, http.StatusForbidden, "this endpoint is only available to human actors")
				return
			}
			userID, ok := requireUserID(w, r)
			if !ok {
				return
			}
			pa, err := h.resolvePlatformAdmin(r.Context(), userID)
			if err != nil {
				if isNotFound(err) {
					writeError(w, http.StatusForbidden, "platform admin access required")
					return
				}
				slog.Warn("platform admin lookup failed", append(logger.RequestAttrs(r), "error", err)...)
				writeError(w, http.StatusInternalServerError, "failed to resolve platform admin")
				return
			}
			if platformRoleRank[pa.Role] < platformRoleRank[minRole] {
				writeError(w, http.StatusForbidden, "insufficient platform role")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), platformAdminCtxKey{}, pa)))
		})
	}
}

func (h *Handler) resolvePlatformAdmin(ctx context.Context, userID string) (db.PlatformAdmin, error) {
	uid, err := parseUUIDString(userID)
	if err != nil {
		return db.PlatformAdmin{}, err
	}
	pa, err := h.Queries.GetPlatformAdmin(ctx, uid)
	if err == nil || !isNotFound(err) {
		return pa, err
	}
	user, lookupErr := h.Queries.GetUser(ctx, uid)
	if lookupErr != nil || !h.isBootstrapPlatformAdminEmail(user.Email) {
		return db.PlatformAdmin{}, err
	}
	return h.Queries.UpsertPlatformAdmin(ctx, db.UpsertPlatformAdminParams{
		UserID: uid,
		Role:   PlatformRoleSuperAdmin,
	})
}

func (h *Handler) isBootstrapPlatformAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, candidate := range h.cfg.PlatformAdminEmails {
		if email == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

// adminAction is one audited write. The business write and the audit row
// commit together, so an action can never happen unrecorded.
type adminAction struct {
	Action     string
	TargetType string
	TargetID   string
	Reason     string
	Payload    map[string]any
}

type adminReasonRequest struct {
	Reason string `json:"reason"`
}

func readAdminReason(w http.ResponseWriter, r *http.Request, dest any) (string, bool) {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(dest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
	var reason string
	switch v := dest.(type) {
	case *adminReasonRequest:
		reason = v.Reason
	case interface{ reasonValue() string }:
		reason = v.reasonValue()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return "", false
	}
	if len(reason) > adminReasonMaxLen {
		writeError(w, http.StatusBadRequest, "reason is too long")
		return "", false
	}
	return reason, true
}

// runAdminAction executes write inside a transaction and records the audit
// row in the same transaction. write returns the HTTP status and body to
// send; a non-2xx status rolls everything back.
func (h *Handler) runAdminAction(w http.ResponseWriter, r *http.Request, action adminAction, write func(q *db.Queries) (int, any, error)) {
	actor, _ := PlatformAdminFromContext(r.Context())
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	status, body, err := write(qtx)
	if err != nil {
		slog.Warn("admin action failed", append(logger.RequestAttrs(r), "action", action.Action, "error", err)...)
		writeError(w, http.StatusInternalServerError, "admin action failed")
		return
	}
	if status < 200 || status >= 300 {
		writeJSON(w, status, body)
		return
	}
	payload, _ := json.Marshal(action.Payload)
	if action.Payload == nil {
		payload = []byte("{}")
	}
	if _, err := qtx.InsertAdminAuditLog(r.Context(), db.InsertAdminAuditLogParams{
		ActorUserID: actor.UserID,
		Action:      action.Action,
		TargetType:  action.TargetType,
		TargetID:    action.TargetID,
		Reason:      action.Reason,
		Payload:     payload,
		Ip:          h.clientIPForRateLimit(r),
		UserAgent:   truncateString(r.UserAgent(), 512),
	}); err != nil {
		slog.Warn("admin audit insert failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to record audit log")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit")
		return
	}
	writeJSON(w, status, body)
}

func adminPaging(r *http.Request) (limit, offset int32, page int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if size < 1 {
		size = adminPageSizeDefault
	}
	if size > adminPageSizeMax {
		size = adminPageSizeMax
	}
	return int32(size), int32((page - 1) * size), page
}

func optionalText(s string) pgtype.Text {
	s = strings.TrimSpace(s)
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func optionalUUID(w http.ResponseWriter, s, field string) (pgtype.UUID, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return pgtype.UUID{}, true
	}
	return parseUUIDOrBadRequest(w, s, field)
}

type AdminPage[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// --- me ---

type AdminMeResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Role   string `json:"role"`
}

func (h *Handler) AdminMe(w http.ResponseWriter, r *http.Request) {
	pa, _ := PlatformAdminFromContext(r.Context())
	user, err := h.Queries.GetUser(r.Context(), pa.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	writeJSON(w, http.StatusOK, AdminMeResponse{
		UserID: uuidToString(pa.UserID),
		Email:  user.Email,
		Name:   user.Name,
		Role:   pa.Role,
	})
}

// --- overview ---

type AdminOverviewResponse struct {
	UsersTotal      int64            `json:"users_total"`
	UsersNew7d      int64            `json:"users_new_7d"`
	UsersDisabled   int64            `json:"users_disabled"`
	WorkspacesTotal int64            `json:"workspaces_total"`
	RuntimesTotal   int64            `json:"runtimes_total"`
	RuntimesOnline  int64            `json:"runtimes_online"`
	Tasks24h        map[string]int64 `json:"tasks_24h"`
}

func (h *Handler) AdminOverview(w http.ResponseWriter, r *http.Request) {
	counts, err := h.Queries.AdminOverviewCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}
	tasks, err := h.Queries.AdminTaskCounts24h(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load task counts")
		return
	}
	byStatus := map[string]int64{}
	for _, t := range tasks {
		byStatus[t.Status] = t.Total
	}
	writeJSON(w, http.StatusOK, AdminOverviewResponse{
		UsersTotal:      counts.UsersTotal,
		UsersNew7d:      counts.UsersNew7d,
		UsersDisabled:   counts.UsersDisabled,
		WorkspacesTotal: counts.WorkspacesTotal,
		RuntimesTotal:   counts.RuntimesTotal,
		RuntimesOnline:  counts.RuntimesOnline,
		Tasks24h:        byStatus,
	})
}

// --- users ---

type AdminUserResponse struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Email          string  `json:"email"`
	AvatarURL      *string `json:"avatar_url"`
	CreatedAt      string  `json:"created_at"`
	DisabledAt     *string `json:"disabled_at"`
	DisabledReason string  `json:"disabled_reason"`
	WorkspaceCount int64   `json:"workspace_count"`
}

type AdminUserWorkspaceResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Role     string `json:"role"`
	JoinedAt string `json:"joined_at"`
}

// AdminUserTokenResponse deliberately carries no hash: the prefix is the only
// token material an operator ever sees.
type AdminUserTokenResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	TokenPrefix string  `json:"token_prefix"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at"`
	ExpiresAt   *string `json:"expires_at"`
}

type AdminUserDetailResponse struct {
	AdminUserResponse
	Workspaces []AdminUserWorkspaceResponse `json:"workspaces"`
	Tokens     []AdminUserTokenResponse     `json:"tokens"`
}

func adminUserFromRow(id pgtype.UUID, name, email string, avatar pgtype.Text, createdAt, disabledAt pgtype.Timestamptz, reason string, wsCount int64) AdminUserResponse {
	return AdminUserResponse{
		ID:             uuidToString(id),
		Name:           name,
		Email:          email,
		AvatarURL:      textToPtr(avatar),
		CreatedAt:      timestampToString(createdAt),
		DisabledAt:     timestampToPtr(disabledAt),
		DisabledReason: reason,
		WorkspaceCount: wsCount,
	}
}

func (h *Handler) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset, page := adminPaging(r)
	q := optionalText(r.URL.Query().Get("q"))
	disabledOnly := r.URL.Query().Get("disabled") == "true"
	rows, err := h.Queries.AdminListUsers(r.Context(), db.AdminListUsersParams{
		Q: q, DisabledOnly: disabledOnly, PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	total, err := h.Queries.AdminCountUsers(r.Context(), db.AdminCountUsersParams{Q: q, DisabledOnly: disabledOnly})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count users")
		return
	}
	items := make([]AdminUserResponse, 0, len(rows))
	for _, u := range rows {
		items = append(items, adminUserFromRow(u.ID, u.Name, u.Email, u.AvatarUrl, u.CreatedAt, u.DisabledAt, u.DisabledReason, u.WorkspaceCount))
	}
	writeJSON(w, http.StatusOK, AdminPage[AdminUserResponse]{Items: items, Total: total, Page: page, PageSize: int(limit)})
}

func (h *Handler) AdminGetUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "user id")
	if !ok {
		return
	}
	u, err := h.Queries.AdminGetUser(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	workspaces, err := h.Queries.AdminListUserWorkspaces(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workspaces")
		return
	}
	tokens, err := h.Queries.ListPersonalAccessTokensByUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load tokens")
		return
	}
	resp := AdminUserDetailResponse{
		AdminUserResponse: adminUserFromRow(u.ID, u.Name, u.Email, u.AvatarUrl, u.CreatedAt, u.DisabledAt, u.DisabledReason, u.WorkspaceCount),
		Workspaces:        make([]AdminUserWorkspaceResponse, 0, len(workspaces)),
		Tokens:            make([]AdminUserTokenResponse, 0, len(tokens)),
	}
	for _, ws := range workspaces {
		resp.Workspaces = append(resp.Workspaces, AdminUserWorkspaceResponse{
			ID: uuidToString(ws.ID), Name: ws.Name, Slug: ws.Slug, Role: ws.Role, JoinedAt: timestampToString(ws.JoinedAt),
		})
	}
	for _, t := range tokens {
		resp.Tokens = append(resp.Tokens, AdminUserTokenResponse{
			ID: uuidToString(t.ID), Name: t.Name, TokenPrefix: t.TokenPrefix,
			CreatedAt: timestampToString(t.CreatedAt), LastUsedAt: timestampToPtr(t.LastUsedAt), ExpiresAt: timestampToPtr(t.ExpiresAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) AdminDisableUser(w http.ResponseWriter, r *http.Request) {
	h.adminSetUserDisabled(w, r, true)
}

func (h *Handler) AdminEnableUser(w http.ResponseWriter, r *http.Request) {
	h.adminSetUserDisabled(w, r, false)
}

func (h *Handler) adminSetUserDisabled(w http.ResponseWriter, r *http.Request, disable bool) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "user id")
	if !ok {
		return
	}
	var req adminReasonRequest
	reason, ok := readAdminReason(w, r, &req)
	if !ok {
		return
	}
	actor, _ := PlatformAdminFromContext(r.Context())
	if disable && actor.UserID == id {
		writeError(w, http.StatusBadRequest, "you cannot disable your own account")
		return
	}
	user, err := h.Queries.GetUser(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	if disable {
		if _, err := h.Queries.GetPlatformAdmin(r.Context(), id); err == nil {
			writeError(w, http.StatusConflict, "remove the platform admin role before disabling this account")
			return
		} else if !isNotFound(err) {
			writeError(w, http.StatusInternalServerError, "failed to check platform admin")
			return
		}
	}
	action := "user.enable"
	if disable {
		action = "user.disable"
	}
	h.runAdminAction(w, r, adminAction{
		Action: action, TargetType: "user", TargetID: uuidToString(id), Reason: reason,
		Payload: map[string]any{"email": user.Email},
	}, func(q *db.Queries) (int, any, error) {
		var n int64
		var err error
		if disable {
			n, err = q.AdminDisableUser(r.Context(), db.AdminDisableUserParams{ID: id, DisabledReason: reason})
		} else {
			n, err = q.AdminEnableUser(r.Context(), id)
		}
		if err != nil {
			return 0, nil, err
		}
		if n == 0 {
			if disable {
				return http.StatusConflict, map[string]string{"error": "user is already disabled"}, nil
			}
			return http.StatusConflict, map[string]string{"error": "user is not disabled"}, nil
		}
		// Applied after the DB write succeeds; the transaction commits right
		// after the audit row, and a commit failure leaves this replica
		// stricter than the database until the next refresh, never looser.
		if disable {
			auth.MarkUserDisabled(uuidToString(id), user.Email)
		} else {
			auth.MarkUserEnabled(uuidToString(id), user.Email)
		}
		u, err := q.AdminGetUser(r.Context(), id)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, adminUserFromRow(u.ID, u.Name, u.Email, u.AvatarUrl, u.CreatedAt, u.DisabledAt, u.DisabledReason, u.WorkspaceCount), nil
	})
}

func (h *Handler) AdminRevokeUserToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "user id")
	if !ok {
		return
	}
	tokenID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "tokenId"), "token id")
	if !ok {
		return
	}
	var req adminReasonRequest
	reason, ok := readAdminReason(w, r, &req)
	if !ok {
		return
	}
	h.runAdminAction(w, r, adminAction{
		Action: "user.token.revoke", TargetType: "personal_access_token", TargetID: uuidToString(tokenID), Reason: reason,
		Payload: map[string]any{"user_id": uuidToString(userID)},
	}, func(q *db.Queries) (int, any, error) {
		hash, err := q.RevokePersonalAccessToken(r.Context(), db.RevokePersonalAccessTokenParams{ID: tokenID, UserID: userID})
		if err != nil {
			if isNotFound(err) {
				return http.StatusNotFound, map[string]string{"error": "token not found"}, nil
			}
			return 0, nil, err
		}
		h.PATCache.Invalidate(r.Context(), hash)
		return http.StatusOK, map[string]string{"status": "revoked"}, nil
	})
}

// --- workspaces ---

type AdminWorkspaceResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	CreatedAt   string `json:"created_at"`
	MemberCount int64  `json:"member_count"`
	AgentCount  int64  `json:"agent_count"`
}

type AdminWorkspaceMemberResponse struct {
	UserID     string  `json:"user_id"`
	Name       string  `json:"name"`
	Email      string  `json:"email"`
	Role       string  `json:"role"`
	JoinedAt   string  `json:"joined_at"`
	DisabledAt *string `json:"disabled_at"`
}

type AdminWorkspaceDetailResponse struct {
	AdminWorkspaceResponse
	Members []AdminWorkspaceMemberResponse `json:"members"`
}

func (h *Handler) AdminListWorkspaces(w http.ResponseWriter, r *http.Request) {
	limit, offset, page := adminPaging(r)
	q := optionalText(r.URL.Query().Get("q"))
	rows, err := h.Queries.AdminListWorkspaces(r.Context(), db.AdminListWorkspacesParams{Q: q, PageLimit: limit, PageOffset: offset})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces")
		return
	}
	total, err := h.Queries.AdminCountWorkspaces(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count workspaces")
		return
	}
	items := make([]AdminWorkspaceResponse, 0, len(rows))
	for _, ws := range rows {
		items = append(items, AdminWorkspaceResponse{
			ID: uuidToString(ws.ID), Name: ws.Name, Slug: ws.Slug, CreatedAt: timestampToString(ws.CreatedAt),
			MemberCount: ws.MemberCount, AgentCount: ws.AgentCount,
		})
	}
	writeJSON(w, http.StatusOK, AdminPage[AdminWorkspaceResponse]{Items: items, Total: total, Page: page, PageSize: int(limit)})
}

func (h *Handler) AdminGetWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	ws, err := h.Queries.AdminGetWorkspace(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load workspace")
		return
	}
	members, err := h.Queries.AdminListWorkspaceMembers(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load members")
		return
	}
	resp := AdminWorkspaceDetailResponse{
		AdminWorkspaceResponse: AdminWorkspaceResponse{
			ID: uuidToString(ws.ID), Name: ws.Name, Slug: ws.Slug, CreatedAt: timestampToString(ws.CreatedAt),
			MemberCount: ws.MemberCount, AgentCount: ws.AgentCount,
		},
		Members: make([]AdminWorkspaceMemberResponse, 0, len(members)),
	}
	for _, m := range members {
		resp.Members = append(resp.Members, AdminWorkspaceMemberResponse{
			UserID: uuidToString(m.UserID), Name: m.Name, Email: m.Email, Role: m.Role,
			JoinedAt: timestampToString(m.JoinedAt), DisabledAt: timestampToPtr(m.DisabledAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- audit ---

type AdminAuditEntryResponse struct {
	ID          string          `json:"id"`
	ActorUserID string          `json:"actor_user_id"`
	ActorEmail  string          `json:"actor_email"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	Reason      string          `json:"reason"`
	Payload     json.RawMessage `json:"payload"`
	IP          string          `json:"ip"`
	CreatedAt   string          `json:"created_at"`
}

func (h *Handler) AdminListAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset, page := adminPaging(r)
	actor, ok := optionalUUID(w, r.URL.Query().Get("actor"), "actor")
	if !ok {
		return
	}
	action := optionalText(r.URL.Query().Get("action"))
	targetID := optionalText(r.URL.Query().Get("target_id"))
	rows, err := h.Queries.ListAdminAuditLog(r.Context(), db.ListAdminAuditLogParams{
		ActorUserID: actor, Action: action, TargetID: targetID, PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list audit log")
		return
	}
	total, err := h.Queries.CountAdminAuditLog(r.Context(), db.CountAdminAuditLogParams{ActorUserID: actor, Action: action, TargetID: targetID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count audit log")
		return
	}
	items := make([]AdminAuditEntryResponse, 0, len(rows))
	for _, e := range rows {
		payload := json.RawMessage(e.Payload)
		if len(payload) == 0 {
			payload = json.RawMessage("{}")
		}
		items = append(items, AdminAuditEntryResponse{
			ID: uuidToString(e.ID), ActorUserID: uuidToString(e.ActorUserID), ActorEmail: e.ActorEmail,
			Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID, Reason: e.Reason,
			Payload: payload, IP: e.Ip, CreatedAt: timestampToString(e.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, AdminPage[AdminAuditEntryResponse]{Items: items, Total: total, Page: page, PageSize: int(limit)})
}

// --- platform admins ---

type PlatformAdminResponse struct {
	UserID    string  `json:"user_id"`
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	Role      string  `json:"role"`
	GrantedBy *string `json:"granted_by"`
	CreatedAt string  `json:"created_at"`
}

func (h *Handler) AdminListPlatformAdmins(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Queries.ListPlatformAdmins(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list platform admins")
		return
	}
	items := make([]PlatformAdminResponse, 0, len(rows))
	for _, pa := range rows {
		var grantedBy *string
		if pa.GrantedBy.Valid {
			s := uuidToString(pa.GrantedBy)
			grantedBy = &s
		}
		items = append(items, PlatformAdminResponse{
			UserID: uuidToString(pa.UserID), Name: pa.Name, Email: pa.Email, Role: pa.Role,
			GrantedBy: grantedBy, CreatedAt: timestampToString(pa.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, items)
}

type setPlatformAdminRequest struct {
	Email  string `json:"email"`
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

func (r *setPlatformAdminRequest) reasonValue() string { return r.Reason }

// AdminSetPlatformAdmin grants or changes a platform role by email, so an
// operator never has to look up a user id to add a colleague.
func (h *Handler) AdminSetPlatformAdmin(w http.ResponseWriter, r *http.Request) {
	var req setPlatformAdminRequest
	reason, ok := readAdminReason(w, r, &req)
	if !ok {
		return
	}
	if _, valid := platformRoleRank[req.Role]; !valid {
		writeError(w, http.StatusBadRequest, "role must be one of super_admin, operator, viewer")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	user, err := h.Queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "no user with that email; they must sign in once first")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	if user.DisabledAt.Valid {
		writeError(w, http.StatusConflict, "enable the account before granting a platform role")
		return
	}
	actor, _ := PlatformAdminFromContext(r.Context())
	if actor.UserID == user.ID && req.Role != PlatformRoleSuperAdmin {
		writeError(w, http.StatusBadRequest, "you cannot lower your own role")
		return
	}
	h.runAdminAction(w, r, adminAction{
		Action: "platform_admin.set", TargetType: "user", TargetID: uuidToString(user.ID), Reason: reason,
		Payload: map[string]any{"email": user.Email, "role": req.Role},
	}, func(q *db.Queries) (int, any, error) {
		pa, err := q.UpsertPlatformAdmin(r.Context(), db.UpsertPlatformAdminParams{
			UserID: user.ID, Role: req.Role, GrantedBy: actor.UserID,
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, PlatformAdminResponse{
			UserID: uuidToString(pa.UserID), Name: user.Name, Email: user.Email, Role: pa.Role,
			CreatedAt: timestampToString(pa.CreatedAt),
		}, nil
	})
}

func (h *Handler) AdminRemovePlatformAdmin(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "userId"), "user id")
	if !ok {
		return
	}
	var req adminReasonRequest
	reason, ok := readAdminReason(w, r, &req)
	if !ok {
		return
	}
	actor, _ := PlatformAdminFromContext(r.Context())
	if actor.UserID == userID {
		writeError(w, http.StatusBadRequest, "you cannot remove your own platform role")
		return
	}
	h.runAdminAction(w, r, adminAction{
		Action: "platform_admin.remove", TargetType: "user", TargetID: uuidToString(userID), Reason: reason,
	}, func(q *db.Queries) (int, any, error) {
		n, err := q.DeletePlatformAdmin(r.Context(), userID)
		if err != nil {
			return 0, nil, err
		}
		if n == 0 {
			return http.StatusNotFound, map[string]string{"error": "platform admin not found"}, nil
		}
		return http.StatusOK, map[string]string{"status": "removed"}, nil
	})
}
