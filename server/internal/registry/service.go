// Package registry is the template marketplace: a product-agnostic registry of
// plugin packages in the neutral Claude Code layout.
//
// The registry knows packages and who may see them, nothing else. It has its
// own tables (registry_*), never reads a consumer's business tables, and is
// reached by consumers through its public read API (or, for Multica, the same
// Service in-process through an adapter). Installation, instance state and
// upgrades belong to the consumer.
package registry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	ConsumerMultica = "multica"

	KindPlugin = "plugin"

	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusDisabled  = "disabled"

	ReviewPending  = "pending"
	ReviewApproved = "approved"
	ReviewRejected = "rejected"
	ReviewRevoked  = "revoked"

	apiKeyPrefix      = "rk_"
	maxChangelog      = 8 * 1024
	maxTags           = 16
	maxTagLength      = 40
	maxCategoryLength = 60
	maxTargets        = 200
	maxTenantLength   = 128
)

var (
	namespacePattern   = regexp.MustCompile(`^@[a-z0-9][a-z0-9-]{0,62}$`)
	consumerKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	sourceKinds        = map[string]bool{"official": true, "self": true, "third_party": true, "skills": true}
	// Only these source kinds take operator uploads; the others are filled by
	// syncing their upstream catalog in a later phase.
	uploadSourceKinds = map[string]bool{"official": true, "self": true}
)

type ErrorKind string

const (
	ErrInvalid     ErrorKind = "invalid"
	ErrNotFound    ErrorKind = "not_found"
	ErrConflict    ErrorKind = "conflict"
	ErrForbidden   ErrorKind = "forbidden"
	ErrUnavailable ErrorKind = "unavailable"
)

type Error struct {
	Kind    ErrorKind
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

func errf(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

func unavailable(message string, err error) *Error {
	return &Error{Kind: ErrUnavailable, Message: message, Err: err}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func uuidString(id pgtype.UUID) string { return util.UUIDToString(id) }

func parseID(value, what string) (pgtype.UUID, error) {
	id, err := util.ParseUUID(strings.TrimSpace(value))
	if err != nil {
		return pgtype.UUID{}, errf(ErrNotFound, "%s not found", what)
	}
	return id, nil
}

// Service owns the registry. Operator writes take the caller's
// transaction-bound queries so the write and its audit row commit together.
type Service struct {
	Queries *db.Queries
}

func NewService(queries *db.Queries) *Service {
	return &Service{Queries: queries}
}

// Caller is who is reading: a keyed consumer (optionally on behalf of one of
// its tenants), or nobody, which sees only public listings.
type Caller struct {
	ConsumerID   pgtype.UUID
	TenantID     string
	TenantGroups []string
}

func (c Caller) tenant() pgtype.Text {
	if c.TenantID == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: c.TenantID, Valid: true}
}

func (c Caller) groups() []string {
	if c.TenantGroups == nil {
		return []string{}
	}
	return c.TenantGroups
}

// ---------------------------------------------------------------------------
// Consumers
// ---------------------------------------------------------------------------

// CreateConsumer registers a product and returns its API key. Only the key's
// hash is stored, so this is the one time it is visible.
func (s *Service) CreateConsumer(ctx context.Context, q *db.Queries, userID pgtype.UUID, key, name, webhookURL string) (db.RegistryConsumer, string, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	name = strings.TrimSpace(name)
	webhookURL = strings.TrimSpace(webhookURL)
	if !consumerKeyPattern.MatchString(key) {
		return db.RegistryConsumer{}, "", errf(ErrInvalid, "key must be 2-63 lowercase letters, digits or dashes")
	}
	if name == "" || len(name) > 160 {
		return db.RegistryConsumer{}, "", errf(ErrInvalid, "name is required and must be at most 160 characters")
	}
	if webhookURL != "" && !strings.HasPrefix(webhookURL, "https://") {
		return db.RegistryConsumer{}, "", errf(ErrInvalid, "webhook_url must be https")
	}
	apiKey, hash, err := newAPIKey()
	if err != nil {
		return db.RegistryConsumer{}, "", err
	}
	consumer, err := q.CreateRegistryConsumer(ctx, db.CreateRegistryConsumerParams{
		Key:        key,
		Name:       name,
		ApiKeyHash: pgtype.Text{String: hash, Valid: true},
		WebhookUrl: webhookURL,
		CreatedBy:  userID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return db.RegistryConsumer{}, "", errf(ErrConflict, "consumer %s already exists", key)
		}
		return db.RegistryConsumer{}, "", unavailable("create registry consumer", err)
	}
	return consumer, apiKey, nil
}

func (s *Service) externalConsumer(ctx context.Context, q *db.Queries, consumerID string) (db.RegistryConsumer, error) {
	id, err := parseID(consumerID, "consumer")
	if err != nil {
		return db.RegistryConsumer{}, err
	}
	consumer, err := q.GetRegistryConsumer(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.RegistryConsumer{}, errf(ErrNotFound, "consumer not found")
	}
	if err != nil {
		return db.RegistryConsumer{}, unavailable("load registry consumer", err)
	}
	if consumer.Builtin {
		return db.RegistryConsumer{}, errf(ErrConflict, "the built-in %s consumer reads in-process; it has no API key and cannot be disabled", consumer.Key)
	}
	return consumer, nil
}

func (s *Service) RotateConsumerKey(ctx context.Context, q *db.Queries, consumerID string) (db.RegistryConsumer, string, error) {
	consumer, err := s.externalConsumer(ctx, q, consumerID)
	if err != nil {
		return db.RegistryConsumer{}, "", err
	}
	apiKey, hash, err := newAPIKey()
	if err != nil {
		return db.RegistryConsumer{}, "", err
	}
	updated, err := q.RotateRegistryConsumerAPIKey(ctx, db.RotateRegistryConsumerAPIKeyParams{
		ID:         consumer.ID,
		ApiKeyHash: pgtype.Text{String: hash, Valid: true},
	})
	if err != nil {
		return db.RegistryConsumer{}, "", unavailable("rotate registry consumer key", err)
	}
	return updated, apiKey, nil
}

func (s *Service) SetConsumerEnabled(ctx context.Context, q *db.Queries, consumerID string, enabled bool) (db.RegistryConsumer, error) {
	consumer, err := s.externalConsumer(ctx, q, consumerID)
	if err != nil {
		return db.RegistryConsumer{}, err
	}
	updated, err := q.SetRegistryConsumerEnabled(ctx, db.SetRegistryConsumerEnabledParams{ID: consumer.ID, Enabled: enabled})
	if err != nil {
		return db.RegistryConsumer{}, unavailable("update registry consumer", err)
	}
	return updated, nil
}

func newAPIKey() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", unavailable("generate API key", err)
	}
	key := apiKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return key, HashAPIKey(key), nil
}

// HashAPIKey is how a presented API key is looked up.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Authenticate resolves an API key to an enabled external consumer. Every
// failure reads the same to the caller.
func (s *Service) Authenticate(ctx context.Context, apiKey string) (db.RegistryConsumer, error) {
	if !strings.HasPrefix(apiKey, apiKeyPrefix) {
		return db.RegistryConsumer{}, errf(ErrForbidden, "invalid API key")
	}
	consumer, err := s.Queries.GetRegistryConsumerByAPIKeyHash(ctx, pgtype.Text{String: HashAPIKey(apiKey), Valid: true})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!consumer.Enabled || consumer.Builtin)) {
		return db.RegistryConsumer{}, errf(ErrForbidden, "invalid API key")
	}
	if err != nil {
		return db.RegistryConsumer{}, unavailable("load registry consumer", err)
	}
	_ = s.Queries.TouchRegistryConsumer(ctx, consumer.ID)
	return consumer, nil
}

// ConsumerByKey loads a consumer by its key; adapters use it to find their
// own consumer row.
func (s *Service) ConsumerByKey(ctx context.Context, key string) (db.RegistryConsumer, error) {
	consumer, err := s.Queries.GetRegistryConsumerByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.RegistryConsumer{}, errf(ErrNotFound, "consumer %s is not registered", key)
	}
	if err != nil {
		return db.RegistryConsumer{}, unavailable("load registry consumer", err)
	}
	return consumer, nil
}

// ---------------------------------------------------------------------------
// Sources
// ---------------------------------------------------------------------------

type SourceInput struct {
	Namespace   string
	Kind        string
	DisplayName string
	URL         string
}

// NormalizeNamespace accepts "self" or "@self" and returns "@self".
func NormalizeNamespace(namespace string) string {
	namespace = strings.ToLower(strings.TrimSpace(namespace))
	if namespace != "" && !strings.HasPrefix(namespace, "@") {
		namespace = "@" + namespace
	}
	return namespace
}

func (s *Service) CreateSource(ctx context.Context, q *db.Queries, userID pgtype.UUID, in SourceInput) (db.RegistrySource, error) {
	namespace := NormalizeNamespace(in.Namespace)
	if !namespacePattern.MatchString(namespace) {
		return db.RegistrySource{}, errf(ErrInvalid, "namespace must look like @name: lowercase letters, digits and dashes")
	}
	if !sourceKinds[in.Kind] {
		return db.RegistrySource{}, errf(ErrInvalid, "kind must be one of official, self, third_party, skills")
	}
	displayName := strings.TrimSpace(in.DisplayName)
	if displayName == "" || len(displayName) > 160 {
		return db.RegistrySource{}, errf(ErrInvalid, "display_name is required and must be at most 160 characters")
	}
	sourceURL := strings.TrimSpace(in.URL)
	if !uploadSourceKinds[in.Kind] && sourceURL == "" {
		return db.RegistrySource{}, errf(ErrInvalid, "a %s source needs the URL of its catalog", in.Kind)
	}
	if sourceURL != "" && !strings.HasPrefix(sourceURL, "https://") {
		return db.RegistrySource{}, errf(ErrInvalid, "source url must be https")
	}
	source, err := q.CreateRegistrySource(ctx, db.CreateRegistrySourceParams{
		Namespace:   namespace,
		Kind:        in.Kind,
		DisplayName: displayName,
		Url:         sourceURL,
		CreatedBy:   userID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return db.RegistrySource{}, errf(ErrConflict, "namespace %s is already registered", namespace)
		}
		return db.RegistrySource{}, unavailable("create registry source", err)
	}
	return source, nil
}

func (s *Service) SetSourceEnabled(ctx context.Context, q *db.Queries, sourceID string, enabled bool) (db.RegistrySource, error) {
	id, err := parseID(sourceID, "source")
	if err != nil {
		return db.RegistrySource{}, err
	}
	source, err := q.SetRegistrySourceEnabled(ctx, db.SetRegistrySourceEnabledParams{ID: id, Enabled: enabled})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.RegistrySource{}, errf(ErrNotFound, "source not found")
	}
	if err != nil {
		return db.RegistrySource{}, unavailable("update registry source", err)
	}
	return source, nil
}

// ---------------------------------------------------------------------------
// Packages, versions and listings
// ---------------------------------------------------------------------------

type UploadInput struct {
	SourceID  string
	Archive   []byte
	Changelog string
	Category  string
	Tags      []string
}

type UploadResult struct {
	ListingID string `json:"listing_id"`
	PackageID string `json:"package_id"`
	VersionID string `json:"version_id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
}

// UploadVersion stores an uploaded package as a new pending version, creating
// the package and its (draft) listing on the first upload of a name.
func (s *Service) UploadVersion(ctx context.Context, q *db.Queries, userID pgtype.UUID, in UploadInput) (UploadResult, error) {
	sourceID, err := util.ParseUUID(strings.TrimSpace(in.SourceID))
	if err != nil {
		return UploadResult{}, errf(ErrInvalid, "source_id is required")
	}
	source, err := q.GetRegistrySource(ctx, sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return UploadResult{}, errf(ErrNotFound, "source not found")
	}
	if err != nil {
		return UploadResult{}, unavailable("load registry source", err)
	}
	if !uploadSourceKinds[source.Kind] {
		return UploadResult{}, errf(ErrInvalid, "%s sources are filled from their upstream catalog, not by upload", source.Kind)
	}
	pkg, err := ParseArchive(in.Archive)
	if err != nil {
		return UploadResult{}, errf(ErrInvalid, "package is invalid: %v", err)
	}
	changelog := strings.TrimSpace(in.Changelog)
	if changelog == "" {
		if raw, ok := pkg.File("CHANGELOG.md"); ok && len(raw) <= maxChangelog {
			changelog = strings.TrimSpace(string(raw))
		}
	}
	if len(changelog) > maxChangelog {
		return UploadResult{}, errf(ErrInvalid, "changelog must be at most %d bytes", maxChangelog)
	}
	category, tags, err := normalizeLabels(in.Category, in.Tags)
	if err != nil {
		return UploadResult{}, err
	}
	if len(tags) == 0 {
		tags, _ = normalizeTags(pkg.Manifest.Keywords)
	}
	manifest := pkg.Manifest
	displayName := manifest.DisplayName
	if displayName == "" {
		displayName = manifest.Name
	}

	if err := q.LockRegistryPackageName(ctx, uuidString(source.ID)+":"+manifest.Name); err != nil {
		return UploadResult{}, unavailable("lock registry package", err)
	}
	row, err := q.GetRegistryPackageBySourceName(ctx, db.GetRegistryPackageBySourceNameParams{SourceID: source.ID, Name: manifest.Name})
	var listing db.RegistryListing
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		row, err = q.CreateRegistryPackage(ctx, db.CreateRegistryPackageParams{
			SourceID:    source.ID,
			Name:        manifest.Name,
			Kind:        KindPlugin,
			DisplayName: displayName,
			Description: manifest.Description,
			Category:    category,
			Tags:        tags,
			CreatedBy:   userID,
		})
		if err != nil {
			return UploadResult{}, unavailable("create registry package", err)
		}
		listing, err = q.CreateRegistryListing(ctx, row.ID)
		if err != nil {
			return UploadResult{}, unavailable("create registry listing", err)
		}
	case err != nil:
		return UploadResult{}, unavailable("load registry package", err)
	default:
		// Labels left blank on a later upload keep what the package has.
		if category == "" {
			category = row.Category
		}
		if len(in.Tags) == 0 && len(manifest.Keywords) == 0 {
			tags = row.Tags
		}
		row, err = q.UpdateRegistryPackageDisplay(ctx, db.UpdateRegistryPackageDisplayParams{
			ID:          row.ID,
			DisplayName: displayName,
			Description: manifest.Description,
			Category:    category,
			Tags:        tags,
		})
		if err != nil {
			return UploadResult{}, unavailable("update registry package", err)
		}
		listing, err = q.GetRegistryListingByPackage(ctx, row.ID)
		if err != nil {
			return UploadResult{}, unavailable("load registry listing", err)
		}
	}

	version, err := q.CreateRegistryPackageVersion(ctx, db.CreateRegistryPackageVersionParams{
		PackageID: row.ID,
		Version:   manifest.Version,
		Manifest:  pkg.RawManifest,
		Archive:   pkg.Archive,
		Sha256:    pkg.SHA256,
		SizeBytes: int64(len(pkg.Archive)),
		SourceRef: "upload",
		Changelog: changelog,
		CreatedBy: userID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return UploadResult{}, errf(ErrConflict, "version %s of %s is already in the registry; versions are immutable, so upload a new version instead", manifest.Version, manifest.Name)
		}
		return UploadResult{}, unavailable("store registry version", err)
	}
	for _, file := range pkg.Files {
		if err := q.CreateRegistryPackageFile(ctx, db.CreateRegistryPackageFileParams{
			VersionID: version.ID,
			Path:      file.Path,
			Content:   file.Content,
			SizeBytes: int64(len(file.Content)),
		}); err != nil {
			return UploadResult{}, unavailable("store registry file", err)
		}
	}
	if err := q.TouchRegistryListing(ctx, listing.ID); err != nil {
		return UploadResult{}, unavailable("update registry listing", err)
	}
	return UploadResult{
		ListingID: uuidString(listing.ID),
		PackageID: uuidString(row.ID),
		VersionID: uuidString(version.ID),
		Name:      manifest.Name,
		Version:   manifest.Version,
	}, nil
}

func normalizeLabels(category string, tags []string) (string, []string, error) {
	category = strings.ToLower(strings.TrimSpace(category))
	if len(category) > maxCategoryLength {
		return "", nil, errf(ErrInvalid, "category must be at most %d characters", maxCategoryLength)
	}
	normalized, err := normalizeTags(tags)
	return category, normalized, err
}

func normalizeTags(tags []string) ([]string, error) {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || seen[tag] {
			continue
		}
		if len(tag) > maxTagLength {
			return nil, errf(ErrInvalid, "tag %q is longer than %d characters", tag, maxTagLength)
		}
		seen[tag] = true
		normalized = append(normalized, tag)
	}
	if len(normalized) > maxTags {
		return nil, errf(ErrInvalid, "at most %d tags", maxTags)
	}
	return normalized, nil
}

// SetListingStatus releases, withdraws or drafts a listing, and sets whether it
// is readable without an API key. Disabling hides it from every consumer at
// once; installations already made keep running their version.
func (s *Service) SetListingStatus(ctx context.Context, q *db.Queries, listingID, status string, public bool) (db.RegistryListing, error) {
	if status != StatusDraft && status != StatusPublished && status != StatusDisabled {
		return db.RegistryListing{}, errf(ErrInvalid, "status must be draft, published or disabled")
	}
	id, err := parseID(listingID, "listing")
	if err != nil {
		return db.RegistryListing{}, err
	}
	listing, err := q.SetRegistryListingStatus(ctx, db.SetRegistryListingStatusParams{ID: id, Status: status, Public: public})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.RegistryListing{}, errf(ErrNotFound, "listing not found")
	}
	if err != nil {
		return db.RegistryListing{}, unavailable("update registry listing", err)
	}
	return listing, nil
}

// reviewTransitions lists, per decision, the states it may leave. A revoked
// version is final, because consumers were told to stop using it.
var reviewTransitions = map[string]map[string]bool{
	ReviewApproved: {ReviewPending: true, ReviewRejected: true},
	ReviewRejected: {ReviewPending: true},
	ReviewRevoked:  {ReviewApproved: true},
}

// ReviewVersion records an operator decision. Only approved versions are
// offered; revoking withdraws one from new installs and downloads.
func (s *Service) ReviewVersion(ctx context.Context, q *db.Queries, userID pgtype.UUID, versionID, decision, reason string) (db.ReviewRegistryPackageVersionRow, error) {
	allowedFrom, ok := reviewTransitions[decision]
	if !ok {
		return db.ReviewRegistryPackageVersionRow{}, errf(ErrInvalid, "decision must be approved, rejected or revoked")
	}
	id, err := parseID(versionID, "version")
	if err != nil {
		return db.ReviewRegistryPackageVersionRow{}, err
	}
	current, err := q.GetRegistryPackageVersion(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ReviewRegistryPackageVersionRow{}, errf(ErrNotFound, "version not found")
	}
	if err != nil {
		return db.ReviewRegistryPackageVersionRow{}, unavailable("load registry version", err)
	}
	if !allowedFrom[current.ReviewStatus] {
		return db.ReviewRegistryPackageVersionRow{}, errf(ErrConflict, "a %s version cannot be %s", current.ReviewStatus, decision)
	}
	reviewed, err := q.ReviewRegistryPackageVersion(ctx, db.ReviewRegistryPackageVersionParams{
		ID:           id,
		ReviewStatus: decision,
		ReviewReason: reason,
		ReviewedBy:   userID,
	})
	if err != nil {
		return db.ReviewRegistryPackageVersionRow{}, unavailable("review registry version", err)
	}
	return reviewed, nil
}

// Target is one visibility rule: a consumer, and optionally one tenant or one
// tenant group of it. Neither means every tenant of the consumer.
type Target struct {
	Consumer    string `json:"consumer"`
	TenantID    string `json:"tenant_id,omitempty"`
	TenantGroup string `json:"tenant_group,omitempty"`
}

// ReplaceTargeting swaps a listing's whole visibility set. Tenant ids are
// opaque here; a consumer adapter validates its own before calling.
func (s *Service) ReplaceTargeting(ctx context.Context, q *db.Queries, userID pgtype.UUID, listingID string, targets []Target) error {
	id, err := parseID(listingID, "listing")
	if err != nil {
		return err
	}
	if _, err := q.GetRegistryListing(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return errf(ErrNotFound, "listing not found")
	} else if err != nil {
		return unavailable("load registry listing", err)
	}
	if len(targets) > maxTargets {
		return errf(ErrInvalid, "at most %d targeting rules", maxTargets)
	}
	consumers := map[string]db.RegistryConsumer{}
	seen := map[Target]bool{}
	if err := q.DeleteRegistryTargeting(ctx, id); err != nil {
		return unavailable("clear registry targeting", err)
	}
	for _, target := range targets {
		target.Consumer = strings.TrimSpace(target.Consumer)
		target.TenantID = strings.TrimSpace(target.TenantID)
		target.TenantGroup = strings.TrimSpace(target.TenantGroup)
		if target.TenantID != "" && target.TenantGroup != "" {
			return errf(ErrInvalid, "a rule names a tenant or a tenant group, not both")
		}
		if len(target.TenantID) > maxTenantLength || len(target.TenantGroup) > maxTenantLength {
			return errf(ErrInvalid, "tenant ids and groups are at most %d characters", maxTenantLength)
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		consumer, ok := consumers[target.Consumer]
		if !ok {
			consumer, err = q.GetRegistryConsumerByKey(ctx, target.Consumer)
			if errors.Is(err, pgx.ErrNoRows) {
				return errf(ErrInvalid, "unknown consumer %q", target.Consumer)
			}
			if err != nil {
				return unavailable("load registry consumer", err)
			}
			consumers[target.Consumer] = consumer
		}
		if err := q.CreateRegistryTargeting(ctx, db.CreateRegistryTargetingParams{
			ListingID:   id,
			ConsumerID:  consumer.ID,
			TenantID:    optionalText(target.TenantID),
			TenantGroup: optionalText(target.TenantGroup),
			CreatedBy:   userID,
		}); err != nil {
			return unavailable("store registry targeting", err)
		}
	}
	return nil
}

func optionalText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// Visible lists what the caller may see, optionally in one namespace.
func (s *Service) Visible(ctx context.Context, caller Caller, namespace string) ([]db.ListVisibleRegistryListingsRow, error) {
	params := db.ListVisibleRegistryListingsParams{
		ConsumerID:   caller.ConsumerID,
		TenantID:     caller.tenant(),
		TenantGroups: caller.groups(),
	}
	if namespace != "" {
		params.Namespace = pgtype.Text{String: NormalizeNamespace(namespace), Valid: true}
	}
	rows, err := s.Queries.ListVisibleRegistryListings(ctx, params)
	if err != nil {
		return nil, unavailable("list registry catalog", err)
	}
	return rows, nil
}

// VisibleVersion loads an approved version the caller may download or
// install. Not visible and not found are the same answer.
func (s *Service) VisibleVersion(ctx context.Context, caller Caller, versionID string) (db.GetVisibleRegistryVersionRow, error) {
	id, err := parseID(versionID, "version")
	if err != nil {
		return db.GetVisibleRegistryVersionRow{}, err
	}
	row, err := s.Queries.GetVisibleRegistryVersion(ctx, db.GetVisibleRegistryVersionParams{
		VersionID:    id,
		ConsumerID:   caller.ConsumerID,
		TenantID:     caller.tenant(),
		TenantGroups: caller.groups(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GetVisibleRegistryVersionRow{}, errf(ErrNotFound, "version not found")
	}
	if err != nil {
		return db.GetVisibleRegistryVersionRow{}, unavailable("load registry version", err)
	}
	if row.ReviewStatus != ReviewApproved {
		return db.GetVisibleRegistryVersionRow{}, errf(ErrConflict, "version %s is %s and is not offered", row.Version, row.ReviewStatus)
	}
	return row, nil
}

// VersionIDByName resolves @namespace/name@version to a version id.
func (s *Service) VersionIDByName(ctx context.Context, namespace, name, version string) (string, error) {
	id, err := s.Queries.GetRegistryVersionByName(ctx, db.GetRegistryVersionByNameParams{
		Namespace: NormalizeNamespace(namespace),
		Name:      name,
		Version:   version,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errf(ErrNotFound, "version not found")
	}
	if err != nil {
		return "", unavailable("load registry version", err)
	}
	return uuidString(id), nil
}

// Archive returns the canonical archive of a version the caller may see.
func (s *Service) Archive(ctx context.Context, caller Caller, versionID string) ([]byte, string, error) {
	version, err := s.VisibleVersion(ctx, caller, versionID)
	if err != nil {
		return nil, "", err
	}
	row, err := s.Queries.GetRegistryPackageVersionArchive(ctx, version.ID)
	if err != nil {
		return nil, "", unavailable("read registry archive", err)
	}
	return row.Archive, row.Sha256, nil
}

// LoadPackage reads a visible version's stored files back into a validated
// package, for consumer adapters.
func (s *Service) LoadPackage(ctx context.Context, caller Caller, versionID string) (db.GetVisibleRegistryVersionRow, Package, error) {
	version, err := s.VisibleVersion(ctx, caller, versionID)
	if err != nil {
		return db.GetVisibleRegistryVersionRow{}, Package{}, err
	}
	rows, err := s.Queries.ListRegistryPackageFiles(ctx, version.ID)
	if err != nil {
		return db.GetVisibleRegistryVersionRow{}, Package{}, unavailable("read registry files", err)
	}
	files := make([]File, 0, len(rows))
	for _, row := range rows {
		files = append(files, File{Path: row.Path, Content: row.Content})
	}
	pkg, err := ParseFiles(files)
	if err != nil {
		return db.GetVisibleRegistryVersionRow{}, Package{}, errf(ErrInvalid, "stored package is invalid: %v", err)
	}
	return version, pkg, nil
}

// ---------------------------------------------------------------------------
// Install reports
// ---------------------------------------------------------------------------

var installStates = map[string]bool{"current": true, "outdated": true, "pending_consent": true, "blocked": true, "pinned": true}

type InstallReport struct {
	TenantID  string `json:"tenant_id"`
	VersionID string `json:"version_id"`
	// State "removed" deletes the tenant's report for that listing.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// ReportInstall records what a consumer's tenant runs, for the operator's
// version distribution. The registry does not act on it.
func (s *Service) ReportInstall(ctx context.Context, consumerID pgtype.UUID, report InstallReport) error {
	tenant := strings.TrimSpace(report.TenantID)
	if tenant == "" || len(tenant) > maxTenantLength {
		return errf(ErrInvalid, "tenant_id is required")
	}
	versionID, err := parseID(report.VersionID, "version")
	if err != nil {
		return err
	}
	version, err := s.Queries.GetRegistryPackageVersion(ctx, versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errf(ErrNotFound, "version not found")
	}
	if err != nil {
		return unavailable("load registry version", err)
	}
	listing, err := s.Queries.GetRegistryListingByPackage(ctx, version.PackageID)
	if err != nil {
		return unavailable("load registry listing", err)
	}
	if report.State == "removed" {
		if err := s.Queries.DeleteRegistryInstallReport(ctx, db.DeleteRegistryInstallReportParams{
			ConsumerID: consumerID, TenantID: tenant, ListingID: listing.ID,
		}); err != nil {
			return unavailable("delete install report", err)
		}
		return nil
	}
	if !installStates[report.State] {
		return errf(ErrInvalid, "state must be current, outdated, pending_consent, blocked, pinned or removed")
	}
	if len(report.Error) > 2000 {
		report.Error = report.Error[:2000]
	}
	if err := s.Queries.UpsertRegistryInstallReport(ctx, db.UpsertRegistryInstallReportParams{
		ConsumerID: consumerID,
		TenantID:   tenant,
		ListingID:  listing.ID,
		VersionID:  version.ID,
		State:      report.State,
		Error:      report.Error,
	}); err != nil {
		return unavailable("store install report", err)
	}
	return nil
}
