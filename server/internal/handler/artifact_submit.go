package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/artifact"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// artifactCommandResponse is the stable JSON the CLI prints for create,
// update, get, and status. It never includes a storage key.
type artifactCommandResponse struct {
	ArtifactID             string `json:"artifact_id"`
	RevisionID             string `json:"revision_id"`
	OperationID            string `json:"operation_id"`
	State                  string `json:"state"`
	Title                  string `json:"title,omitempty"`
	Filename               string `json:"filename,omitempty"`
	RevisionNo             int64  `json:"revision_no,omitempty"`
	RestoredFromRevisionID string `json:"restored_from_revision_id,omitempty"`
}

// CreateArtifact — POST /api/artifacts
//
// Multipart fields: task_id, title, file. The caller must be the task token
// bound to that task. The first file is sealed as revision 1 through the
// staging commit path.
func (h *Handler) CreateArtifact(w http.ResponseWriter, r *http.Request) {
	binding, ok := h.requireArtifactTask(w, r)
	if !ok {
		return
	}
	file, ok := readArtifactUpload(w, r)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(r.FormValue("task_id"))
	if taskID == "" || !strings.EqualFold(taskID, binding.TaskID) {
		writeError(w, http.StatusForbidden, "task_id must match the request's task token")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	svc := h.artifactService()
	created, err := svc.Create(r.Context(), artifact.CreateInput{
		WorkspaceID: binding.Workspace,
		OwnerScope:  "task",
		OwnerID:     binding.Task,
		Title:       title,
		CreatedBy:   binding.Agent,
	})
	if err != nil {
		writeArtifactServiceError(w, err)
		return
	}
	result, err := h.commitArtifactFile(r, svc, binding, created.ID, nil, file, "agent", nil, &binding.Task)
	if err != nil {
		if h.DB != nil {
			_, _ = h.DB.Exec(r.Context(), `DELETE FROM artifacts WHERE id = $1 AND head_revision_id IS NULL`, created.ID)
		}
		writeArtifactServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, artifactCommandFromCommit(result, "committed", title, file.name))
}

// UpdateArtifact — POST /api/artifacts/{artifactID}/revisions
//
// Multipart fields: base_revision_id, file. A task token may only append a
// revision to an artifact owned by its own task.
func (h *Handler) UpdateArtifact(w http.ResponseWriter, r *http.Request) {
	file, ok := readArtifactUpload(w, r)
	if !ok {
		return
	}
	lease, hasLease, ok := submittedEditLease(w, r)
	if !ok {
		return
	}
	baseRaw := strings.TrimSpace(r.FormValue("base_revision_id"))
	if !ifMatchAgrees(r, baseRaw) {
		writeErrorCode(w, http.StatusConflict, artifact.CodeRevisionConflict, "revision conflict")
		return
	}
	if !hasLease {
		h.updateArtifactFromTask(w, r, file, baseRaw)
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	art, ok := h.loadArtifactForMember(w, r)
	if !ok {
		return
	}
	actor, _, ok := editorIdentity(w, r)
	if !ok {
		return
	}
	baseID, ok := parseUUIDOrBadRequest(w, baseRaw, "base_revision_id")
	if !ok {
		return
	}
	if art.HeadRevisionID.Valid {
		rev, err := h.Queries.GetArtifactRevision(r.Context(), db.GetArtifactRevisionParams{
			ID:         art.HeadRevisionID,
			ArtifactID: art.ID,
		})
		if err == nil && rev.Filename != "" {
			file.name = rev.Filename
		}
	}
	base := uuid.UUID(baseID.Bytes)
	source := "agent"
	var taskID *uuid.UUID
	if task, _, scoped := taskTokenBinding(r); scoped {
		if id, err := uuid.Parse(task); err == nil {
			taskID = &id
		}
	} else {
		source = "human_edit"
	}
	lease.ActorID = actor
	result, err := h.commitArtifactFile(r, h.artifactService(), artifactTaskBinding{
		Workspace: uuid.UUID(art.WorkspaceID.Bytes),
		Agent:     actor,
		Task:      uuid.UUID{},
	}, uuid.UUID(art.ID.Bytes), &base, file, source, &lease, taskID)
	if err != nil {
		writeArtifactServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, artifactCommandFromCommit(result, "committed", art.Title, file.name))
}

func (h *Handler) updateArtifactFromTask(w http.ResponseWriter, r *http.Request, file artifactUploadFile, baseRaw string) {
	binding, ok := h.requireArtifactTask(w, r)
	if !ok {
		return
	}
	art, ok := h.loadArtifactForMember(w, r)
	if !ok {
		return
	}
	if !artifactOwnedByTask(art, binding.TaskID) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	baseID, ok := parseUUIDOrBadRequest(w, baseRaw, "base_revision_id")
	if !ok {
		return
	}
	base := uuid.UUID(baseID.Bytes)
	result, err := h.commitArtifactFile(r, h.artifactService(), binding, uuid.UUID(art.ID.Bytes), &base, file, "agent", nil, &binding.Task)
	if err != nil {
		writeArtifactServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, artifactCommandFromCommit(result, "committed", art.Title, file.name))
}

// GetArtifactOperationStatus — GET /api/artifact-operations/{operationID}
//
// A task token can read only operations its own task submitted. A workspace
// member can read an operation in that workspace. Missing and out-of-scope
// ids both answer 404.
func (h *Handler) GetArtifactOperationStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if !h.authorizeArtifactWorkspace(w, r, workspaceID) {
		return
	}
	opID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationID"), "operation id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	op, err := h.Queries.GetArtifactOperationByID(r.Context(), db.GetArtifactOperationByIDParams{
		ID:          opID,
		WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load operation")
		return
	}
	if taskID, agentID, scoped := taskTokenBinding(r); scoped {
		if agentID == "" || !strings.EqualFold(uuidToString(op.ActorID), agentID) ||
			!strings.HasPrefix(op.IdempotencyKey, taskOperationPrefix(taskID)) {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
	}
	resp := artifactCommandResponse{
		OperationID: uuidToString(op.ID),
		State:       artifactOperationState(op),
	}
	if len(op.ResultRef) > 0 {
		var stored struct {
			ArtifactID string `json:"artifact_id"`
			RevisionID string `json:"revision_id"`
		}
		if err := json.Unmarshal(op.ResultRef, &stored); err == nil {
			resp.ArtifactID = stored.ArtifactID
			resp.RevisionID = stored.RevisionID
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type artifactTaskBinding struct {
	WorkspaceID string
	TaskID      string
	AgentID     string
	Workspace   uuid.UUID
	Task        uuid.UUID
	Agent       uuid.UUID
}

type artifactUploadFile struct {
	name string
	data []byte
}

func (h *Handler) requireArtifactTask(w http.ResponseWriter, r *http.Request) (artifactTaskBinding, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return artifactTaskBinding{}, false
	}
	taskID, agentID, scoped := taskTokenBinding(r)
	if !scoped || taskID == "" || agentID == "" {
		writeError(w, http.StatusForbidden, "artifact submit requires a task token")
		return artifactTaskBinding{}, false
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return artifactTaskBinding{}, false
	}
	if !h.authorizeArtifactWorkspace(w, r, workspaceID) {
		return artifactTaskBinding{}, false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return artifactTaskBinding{}, false
	}
	taskUUID, ok := parseUUIDOrBadRequest(w, taskID, "task id")
	if !ok {
		return artifactTaskBinding{}, false
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, agentID, "agent id")
	if !ok {
		return artifactTaskBinding{}, false
	}
	task, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{
		ID:          taskUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid task_id")
		return artifactTaskBinding{}, false
	}
	if !strings.EqualFold(uuidToString(task.AgentID), agentID) {
		writeError(w, http.StatusForbidden, "task token agent does not match the task")
		return artifactTaskBinding{}, false
	}
	return artifactTaskBinding{
		WorkspaceID: workspaceID,
		TaskID:      taskID,
		AgentID:     agentID,
		Workspace:   uuid.UUID(wsUUID.Bytes),
		Task:        uuid.UUID(taskUUID.Bytes),
		Agent:       uuid.UUID(agentUUID.Bytes),
	}, true
}

func (h *Handler) artifactService() *artifact.Service {
	var dbBegin artifact.TxBeginner
	if h != nil {
		if starter, ok := h.TxStarter.(artifact.TxBeginner); ok {
			dbBegin = starter
		}
	}
	return &artifact.Service{DB: dbBegin, Store: h.Storage}
}

func (h *Handler) commitArtifactFile(r *http.Request, svc *artifact.Service, binding artifactTaskBinding, artifactID uuid.UUID, base *uuid.UUID, file artifactUploadFile, source string, lease *artifact.EditLease, taskID *uuid.UUID) (artifact.CommitResult, error) {
	sum := sha256.Sum256(file.data)
	key := taskOperationPrefix(binding.TaskID) + uuid.NewString()
	if lease != nil {
		key = "edit:" + strings.ToLower(lease.SessionID.String()) + ":" + uuid.NewString()
	}
	return svc.Commit(r.Context(), artifact.CommitInput{
		WorkspaceID:    binding.Workspace,
		ActorID:        binding.Agent,
		ArtifactID:     artifactID,
		BaseRevisionID: base,
		IdempotencyKey: key,
		Filename:       file.name,
		MediaType:      declaredArtifactMediaType(file.name, file.data),
		SHA256:         hex.EncodeToString(sum[:]),
		Source:         source,
		TaskRunID:      taskID,
		Data:           file.data,
		EditLease:      lease,
	})
}

func readArtifactUpload(w http.ResponseWriter, r *http.Request) (artifactUploadFile, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if r.MultipartForm == nil {
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			writeError(w, http.StatusBadRequest, "file too large or invalid multipart form")
			return artifactUploadFile{}, false
		}
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	uploaded, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return artifactUploadFile{}, false
	}
	defer uploaded.Close()
	data, err := io.ReadAll(uploaded)
	if err != nil {
		writeError(w, http.StatusBadRequest, "file too large or invalid multipart form")
		return artifactUploadFile{}, false
	}
	name := filepath.Base(header.Filename)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return artifactUploadFile{}, false
	}
	return artifactUploadFile{name: name, data: data}, true
}

func taskOperationPrefix(taskID string) string {
	return "task:" + strings.ToLower(taskID) + ":"
}

func artifactOperationState(op db.ArtifactOperation) string {
	if op.ErrorCode.Valid && op.ErrorCode.String == artifact.CodeRevisionConflict {
		return "conflict"
	}
	switch op.State {
	case "succeeded":
		return "committed"
	case "failed":
		return "failed"
	case "pending":
		return "pending"
	default:
		return op.State
	}
}

func artifactCommandFromCommit(result artifact.CommitResult, state, title, filename string) artifactCommandResponse {
	return artifactCommandResponse{
		ArtifactID:  result.ArtifactID.String(),
		RevisionID:  result.RevisionID.String(),
		OperationID: result.OperationID.String(),
		State:       state,
		Title:       title,
		Filename:    filename,
		RevisionNo:  result.RevisionNo,
	}
}

func declaredArtifactMediaType(filename string, data []byte) string {
	switch strings.ToLower(filepath.Ext(filename)) {
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
	case ".txt", ".log":
		return "text/plain"
	}
	return http.DetectContentType(data)
}

func writeArtifactServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, artifact.ErrRevisionConflict):
		writeErrorCode(w, http.StatusConflict, artifact.CodeRevisionConflict, "revision conflict")
	case errors.Is(err, artifact.ErrQuotaExceeded):
		writeErrorCode(w, http.StatusTooManyRequests, "QUOTA_EXCEEDED", "artifact quota exceeded")
	case errors.Is(err, artifact.ErrMediaTypeMismatch):
		writeErrorCode(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "media type mismatch")
	case errors.Is(err, artifact.ErrDigestMismatch):
		writeErrorCode(w, http.StatusUnprocessableEntity, "DIGEST_MISMATCH", "digest mismatch")
	case errors.Is(err, artifact.ErrNotFound):
		writeError(w, http.StatusNotFound, "artifact not found")
	case errors.Is(err, artifact.ErrLeaseHeld):
		writeErrorCode(w, http.StatusConflict, artifact.CodeLeaseHeld, "edit lease held")
	case errors.Is(err, artifact.ErrLeaseExpired):
		writeErrorCode(w, http.StatusConflict, artifact.CodeLeaseExpired, "edit lease expired")
	case errors.Is(err, artifact.ErrNotEditable):
		writeErrorCode(w, http.StatusUnsupportedMediaType, artifact.CodeNotEditable, "artifact is not editable")
	case errors.Is(err, artifact.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid artifact request")
	default:
		slog.Error("artifact submit failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to commit artifact")
	}
}
