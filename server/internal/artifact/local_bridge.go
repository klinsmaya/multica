package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/util"
)

const (
	// CandidateKind is a local hint. It is not an artifact row and not a revision.
	CandidateKind = "artifact_candidate"
	// LocalBridgeMaxBytes is the largest file the bridge will read from an
	// authorized root. A larger file stays a hint.
	LocalBridgeMaxBytes int64 = 32 << 20

	SignalFileLink  = "file_link"
	SignalWriteFile = "write_file"
	SignalMediaHint = "media_hint"

	HintBridgeOff  = "bridge_off"
	HintMissing    = "missing"
	HintEscape     = "escape"
	HintUnreadable = "unreadable"
	HintOwnership  = "ownership"
	HintFormat     = "format"
	HintSize       = "size"
	HintSource     = "source"
)

var (
	// ErrBridgeDisabled means the local bridge is off. Cloud artifact reads
	// do not use this error.
	ErrBridgeDisabled = errors.New("local bridge is off")
	// ErrUnauthorized means the bridge request has no bearer token.
	ErrUnauthorized = errors.New("missing token")
	// ErrForbiddenHost means the Host header is not an install allowlist name.
	ErrForbiddenHost = errors.New("host is not allowed")
	// ErrCrossOrigin means Origin is missing on a write or is not allowlisted.
	ErrCrossOrigin = errors.New("origin is not allowed")
	// ErrHintOnly means the path failed a local check and was not submitted.
	ErrHintOnly = errors.New("local candidate hint")
	// ErrLocalDeleteKept means a local removal did not delete the cloud artifact.
	ErrLocalDeleteKept  = errors.New("local delete does not delete the cloud artifact")
	errOwnerUnavailable = errors.New("file owner is unavailable")
)

// LocalBridge is the optional local file channel. The zero value is off.
// Authorized roots are chosen at install and mapped to one workspace and user.
// Closing the bridge does not remove cloud artifacts.
type LocalBridge struct {
	Enabled      bool
	Bindings     []RootBinding
	AllowedHosts []string
	open         func(string) ([]byte, error)
	owner        func(string) (bool, error)
}

// RootBinding is one workspace and user pair and the directories that pair may read.
type RootBinding struct {
	WorkspaceID string
	UserID      string
	Roots       []string
}

// Signal is an existing task or daemon observation. Path is untrusted.
type Signal struct {
	Source    string `json:"source"`
	Path      string `json:"path"`
	MediaHint string `json:"media_hint,omitempty"`
}

// Candidate is not a saved artifact. Data stays on the server that read the
// file and is not part of the JSON sent to any user.
type Candidate struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	Name        string `json:"name,omitempty"`
	Hint        string `json:"hint,omitempty"`
	Submittable bool   `json:"submittable"`
	Saved       bool   `json:"saved"`
	Size        int64  `json:"size_bytes,omitempty"`
	MediaType   string `json:"media_type,omitempty"`
	data        []byte
}

// HintError is a refused local path. The error text is a reason code, not a path.
type HintError struct {
	Reason string
}

func (e HintError) Error() string { return ErrHintOnly.Error() }

func (e HintError) Is(target error) bool { return target == ErrHintOnly }

// LocalBridgeFromEnv is off unless MULTICA_ARTIFACT_BRIDGE=on.
// Roots use workspaceID:userID=/absolute/dir separated by semicolons.
// Hosts are a comma list. An empty host list allows loopback only.
func LocalBridgeFromEnv() LocalBridge {
	if os.Getenv("MULTICA_ARTIFACT_BRIDGE") != "on" {
		return LocalBridge{}
	}
	bridge := LocalBridge{Enabled: true}
	for _, part := range strings.Split(os.Getenv("MULTICA_ARTIFACT_BRIDGE_ROOTS"), ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		wsUser, path, ok := strings.Cut(part, "=")
		if !ok || !filepath.IsAbs(path) {
			continue
		}
		workspace, user, ok := strings.Cut(wsUser, ":")
		if !ok || workspace == "" || user == "" {
			continue
		}
		bridge.Bindings = append(bridge.Bindings, RootBinding{
			WorkspaceID: workspace,
			UserID:      user,
			Roots:       []string{path},
		})
	}
	if hosts := strings.TrimSpace(os.Getenv("MULTICA_ARTIFACT_BRIDGE_HOSTS")); hosts != "" {
		for _, host := range strings.Split(hosts, ",") {
			host = strings.TrimSpace(host)
			if host != "" {
				bridge.AllowedHosts = append(bridge.AllowedHosts, host)
			}
		}
	}
	return bridge
}

// Discover classifies file links, write-file events, and media hints.
// A failed check returns a hint and does not read the file.
func (b LocalBridge) Discover(workspace, user string, signals []Signal) []Candidate {
	out := make([]Candidate, 0, len(signals))
	for _, signal := range signals {
		out = append(out, b.one(workspace, user, signal))
	}
	return out
}

// PublishCandidates drops file bytes. Another user also loses the file name.
// Absolute paths are not a field on Candidate.
func PublishCandidates(owner, viewer string, items []Candidate) []Candidate {
	out := make([]Candidate, len(items))
	for i, item := range items {
		item.data = nil
		if owner != viewer {
			item.Name = ""
		}
		out[i] = item
	}
	return out
}

// LocalSubmit asks the existing create and commit path to seal a checked file.
type LocalSubmit struct {
	WorkspaceID    uuid.UUID
	UserID         string
	ActorID        uuid.UUID
	OwnerScope     string
	OwnerID        uuid.UUID
	Title          string
	Path           string
	ArtifactID     *uuid.UUID
	BaseRevisionID *uuid.UUID
	LocalDeleted   bool
}

// Submit reads a file only after the local checks, then calls Service.Create
// and Service.Commit. A local delete returns before either call. A stale base
// still goes through Commit, which records conflict_draft and does not move head.
func (b LocalBridge) Submit(ctx context.Context, svc *Service, in LocalSubmit) (CommitResult, error) {
	if in.LocalDeleted {
		return CommitResult{}, ErrLocalDeleteKept
	}
	if !b.Enabled {
		return CommitResult{}, ErrBridgeDisabled
	}
	if svc == nil {
		return CommitResult{}, ErrInvalidInput
	}
	cand := b.one(in.WorkspaceID.String(), in.UserID, Signal{Source: SignalWriteFile, Path: in.Path})
	if !cand.Submittable {
		return CommitResult{}, HintError{Reason: cand.Hint}
	}
	artifactID := in.ArtifactID
	if artifactID == nil {
		created, err := svc.Create(ctx, CreateInput{
			WorkspaceID: in.WorkspaceID,
			OwnerScope:  in.OwnerScope,
			OwnerID:     in.OwnerID,
			Title:       in.Title,
			CreatedBy:   in.ActorID,
		})
		if err != nil {
			return CommitResult{}, err
		}
		artifactID = &created.ID
	}
	sum := sha256.Sum256(cand.data)
	key, err := uuid.NewV7()
	if err != nil {
		return CommitResult{}, err
	}
	return svc.Commit(ctx, CommitInput{
		WorkspaceID:    in.WorkspaceID,
		ActorID:        in.ActorID,
		ArtifactID:     *artifactID,
		BaseRevisionID: in.BaseRevisionID,
		IdempotencyKey: "local:" + key.String(),
		Filename:       cand.Name,
		MediaType:      cand.MediaType,
		SHA256:         hex.EncodeToString(sum[:]),
		Source:         "human_edit",
		Data:           cand.data,
	})
}

// Reject writes the bridge auth error and reports whether the handler may continue.
func (b LocalBridge) Reject(w http.ResponseWriter, r *http.Request) bool {
	if err := b.Authorize(r); err != nil {
		WriteLocalBridgeError(w, err)
		return false
	}
	return true
}

// Authorize requires the bridge, a bearer token, an allowlisted Host, and an
// allowlisted Origin on writes. Host is the DNS-rebinding check. Origin is the
// cross-origin check. The token is the CSRF check. Cookie-only writes fail.
func (b LocalBridge) Authorize(r *http.Request) error {
	if r == nil || !b.Enabled {
		return ErrBridgeDisabled
	}
	if bearerToken(r.Header.Get("Authorization")) == "" {
		return ErrUnauthorized
	}
	if !b.hostAllowed(requestHost(r)) {
		return ErrForbiddenHost
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		if unsafeMethod(r.Method) {
			return ErrCrossOrigin
		}
		return nil
	}
	if !b.originAllowed(origin) {
		return ErrCrossOrigin
	}
	return nil
}

// WriteLocalBridgeError maps bridge failures to HTTP statuses. The body has no path.
func WriteLocalBridgeError(w http.ResponseWriter, err error) {
	status := http.StatusForbidden
	switch {
	case errors.Is(err, ErrBridgeDisabled):
		status = http.StatusNotFound
	case errors.Is(err, ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, ErrHintOnly):
		status = http.StatusBadRequest
	case errors.Is(err, ErrLocalDeleteKept):
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (b LocalBridge) one(workspace, user string, signal Signal) Candidate {
	cand := Candidate{Kind: CandidateKind, Source: signal.Source, Saved: false}
	if !b.Enabled {
		cand.Hint = HintBridgeOff
		return cand
	}
	switch signal.Source {
	case SignalFileLink, SignalWriteFile, SignalMediaHint:
	default:
		cand.Hint = HintSource
		return cand
	}
	path := localPath(signal.Path)
	if path == "" || !filepath.IsAbs(path) {
		cand.Hint = HintMissing
		return cand
	}
	resolved, err := util.ResolveSymlinks(path)
	if err != nil {
		cand.Hint = HintMissing
		return cand
	}
	if !b.insideAuthorized(workspace, user, resolved) {
		cand.Hint = HintEscape
		return cand
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		cand.Hint = HintMissing
		return cand
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		cand.Hint = HintEscape
		return cand
	}
	if info.Size() > LocalBridgeMaxBytes {
		cand.Hint = HintSize
		return cand
	}
	if info.Mode().Perm()&0o444 == 0 {
		cand.Hint = HintUnreadable
		return cand
	}
	owned, err := b.fileOwner(info, resolved)
	if err != nil || !owned {
		cand.Hint = HintOwnership
		return cand
	}
	data, err := b.read(resolved)
	if err != nil {
		cand.Hint = HintUnreadable
		return cand
	}
	if int64(len(data)) != info.Size() {
		cand.Hint = HintSize
		return cand
	}
	media, ok := localMedia(resolved, data)
	if !ok {
		cand.Hint = HintFormat
		return cand
	}
	cand.Submittable = true
	cand.Name = filepath.Base(resolved)
	cand.Size = info.Size()
	cand.MediaType = media
	cand.data = data
	return cand
}

func (b LocalBridge) insideAuthorized(workspace, user, resolved string) bool {
	for _, binding := range b.Bindings {
		if binding.WorkspaceID != workspace || binding.UserID != user {
			continue
		}
		for _, root := range binding.Roots {
			rr, err := util.ResolveSymlinks(root)
			if err != nil {
				continue
			}
			if insideRoot(resolved, rr) {
				return true
			}
		}
	}
	return false
}

func insideRoot(resolved, root string) bool {
	resolved = filepath.Clean(resolved)
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return filepath.IsAbs(resolved)
}

func (b LocalBridge) read(path string) ([]byte, error) {
	if b.open != nil {
		return b.open(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, LocalBridgeMaxBytes+1))
}

func (b LocalBridge) fileOwner(info os.FileInfo, path string) (bool, error) {
	if b.owner != nil {
		return b.owner(path)
	}
	return processOwns(info)
}

func localPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") || strings.HasPrefix(strings.ToLower(raw), "file:") {
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Scheme, "file") {
			return ""
		}
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			return ""
		}
		if u.Path == "" || !filepath.IsAbs(u.Path) {
			return ""
		}
		return u.Path
	}
	return raw
}

func localMedia(path string, data []byte) (string, bool) {
	declared := declaredLocalMedia(path)
	if declared == "" {
		declared = canonicalMediaType(http.DetectContentType(data))
	}
	return resolveMediaType(data, declared)
}

func declaredLocalMedia(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return MediaTypeDocx
	case ".xlsx":
		return MediaTypeXlsx
	case ".pptx":
		return MediaTypePptx
	case ".txt", ".log":
		return "text/plain"
	case ".png":
		return "image/png"
	default:
		return ""
	}
}

func (b LocalBridge) hostAllowed(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), ".")
	allowed := b.AllowedHosts
	if len(allowed) == 0 {
		allowed = []string{"localhost", "127.0.0.1", "::1"}
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), host) {
			return true
		}
	}
	return false
}

func (b LocalBridge) originAllowed(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return b.hostAllowed(u.Hostname())
}

func requestHost(r *http.Request) string {
	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return strings.Trim(host, "[]")
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" || strings.Contains(token, " ") {
		return ""
	}
	return token
}

func unsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
