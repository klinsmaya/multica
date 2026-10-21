package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/registry"
	"github.com/multica-ai/multica/server/internal/util"
)

// Template marketplace wiring. The registry (internal/registry) owns its
// tables and HTTP surface; Multica hosts it, supplies admin auth and audit,
// and consumes it through service.PluginRegistry.

func (h *Handler) marketplaceV1Enabled(ctx context.Context) bool {
	return h.pluginsV1Enabled(ctx) && featureflags.MarketplaceV1Enabled(ctx, h.FeatureFlags)
}

func (h *Handler) requireMarketplaceV1(w http.ResponseWriter, r *http.Request) bool {
	if h.marketplaceV1Enabled(r.Context()) {
		return true
	}
	writeFeatureDisabled(w, "marketplace_disabled", "The template marketplace is not enabled")
	return false
}

// RequireMarketplaceV1 hides a route group entirely while the flag is off.
func (h *Handler) RequireMarketplaceV1(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.marketplaceV1Enabled(r.Context()) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RegistryPublic serves /registry/v1.
func (h *Handler) RegistryPublic() *registry.Public {
	return &registry.Public{Service: h.RegistryService, BaseURL: h.cfg.PublicURL}
}

// RegistryAdmin serves /api/admin/registry, under platform admin auth.
func (h *Handler) RegistryAdmin() *registry.Admin {
	return &registry.Admin{Service: h.RegistryService, Host: registryAdminHost{h}, Tenants: h.resolveRegistryTenant}
}

type registryAdminHost struct{ h *Handler }

func (host registryAdminHost) RunAdminAction(w http.ResponseWriter, r *http.Request, action registry.AdminAction, write registry.AdminWrite) {
	host.h.runAdminAction(w, r, adminAction(action), write)
}

func (host registryAdminHost) AdminActorID(r *http.Request) pgtype.UUID {
	actor, _ := PlatformAdminFromContext(r.Context())
	return actor.UserID
}

// resolveRegistryTenant checks a Multica tenant (a workspace) exists, for
// targeting. Other consumers' tenants are opaque to Multica.
func (h *Handler) resolveRegistryTenant(ctx context.Context, consumerKey, tenantID string) (string, error) {
	if consumerKey != registry.ConsumerMultica {
		return "", nil
	}
	id, err := util.ParseUUID(tenantID)
	if err != nil {
		return "", &registry.Error{Kind: registry.ErrInvalid, Message: "multica tenants are workspace ids"}
	}
	workspace, err := h.Queries.GetWorkspace(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &registry.Error{Kind: registry.ErrInvalid, Message: "workspace " + tenantID + " does not exist"}
	}
	if err != nil {
		return "", err
	}
	return workspace.Name, nil
}

// ListWorkspaceMarketplace — GET /api/workspaces/{id}/plugins/marketplace
//
// The templates this workspace may install, with what it already runs.
func (h *Handler) ListWorkspaceMarketplace(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace_id")
	if !ok {
		return
	}
	entries, err := h.PluginRegistry.Catalog(r.Context(), workspaceID)
	if err != nil {
		writePluginError(w, err, "failed to list marketplace templates")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listings": entries})
}

// The admin routes are mounted per role group: reads for viewers, catalog
// work for operators, sources and consumers for super admins.
func (h *Handler) MountRegistryAdminRead(r chi.Router) {
	admin := h.RegistryAdmin()
	r.Get("/registry/listings", admin.ListListings)
	r.Get("/registry/listings/{id}", admin.GetListing)
	r.Get("/registry/sources", admin.ListSources)
	r.Get("/registry/consumers", admin.ListConsumers)
}

func (h *Handler) MountRegistryAdminOperate(r chi.Router) {
	admin := h.RegistryAdmin()
	r.Post("/registry/listings", admin.UploadVersion)
	r.Put("/registry/listings/{id}/status", admin.SetListingStatus)
	r.Put("/registry/listings/{id}/targeting", admin.SetTargeting)
	r.Post("/registry/versions/{id}/review", admin.ReviewVersion)
}

func (h *Handler) MountRegistryAdminSuper(r chi.Router) {
	admin := h.RegistryAdmin()
	r.Post("/registry/sources", admin.CreateSource)
	r.Put("/registry/sources/{id}/enabled", admin.SetSourceEnabled)
	r.Post("/registry/consumers", admin.CreateConsumer)
	r.Post("/registry/consumers/{id}/key", admin.RotateConsumerKey)
	r.Put("/registry/consumers/{id}/enabled", admin.SetConsumerEnabled)
}
