package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"time"
)

// ArtifactCommandResult is the stable JSON for artifact create, update, get,
// and status. Storage keys are not part of it.
type ArtifactCommandResult struct {
	ArtifactID  string `json:"artifact_id"`
	RevisionID  string `json:"revision_id"`
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
	Title       string `json:"title,omitempty"`
	Filename    string `json:"filename,omitempty"`
	RevisionNo  int64  `json:"revision_no,omitempty"`
}

// CreateArtifact submits a new task-owned artifact and its first revision.
func (c *APIClient) CreateArtifact(ctx context.Context, taskID, title, filename string, data []byte) (ArtifactCommandResult, error) {
	body, contentType, err := artifactMultipart(map[string]string{
		"task_id": taskID,
		"title":   title,
	}, filename, data)
	if err != nil {
		return ArtifactCommandResult{}, err
	}
	return c.postArtifact(ctx, http.MethodPost, "/api/artifacts", body, contentType)
}

// UpdateArtifact seals a new revision on an existing artifact.
func (c *APIClient) UpdateArtifact(ctx context.Context, artifactID, baseRevisionID, filename string, data []byte) (ArtifactCommandResult, error) {
	body, contentType, err := artifactMultipart(map[string]string{
		"base_revision_id": baseRevisionID,
	}, filename, data)
	if err != nil {
		return ArtifactCommandResult{}, err
	}
	path := "/api/artifacts/" + url.PathEscape(artifactID) + "/revisions"
	return c.postArtifact(ctx, http.MethodPost, path, body, contentType)
}

// GetArtifact loads metadata for an artifact, optionally one revision.
func (c *APIClient) GetArtifact(ctx context.Context, artifactID, revisionID string) (ArtifactCommandResult, error) {
	path := "/api/artifacts/" + url.PathEscape(artifactID)
	if revisionID != "" {
		path += "?revision_id=" + url.QueryEscape(revisionID)
	}
	var meta struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		Title       string `json:"title"`
		Filename    string `json:"filename"`
		RevisionID  string `json:"revision_id"`
		OperationID string `json:"operation_id"`
		RevisionNo  int64  `json:"revision_no"`
	}
	if err := c.GetJSON(ctx, path, &meta); err != nil {
		return ArtifactCommandResult{}, err
	}
	state := meta.Status
	if meta.RevisionID != "" {
		state = "committed"
	}
	return ArtifactCommandResult{
		ArtifactID:  meta.ID,
		RevisionID:  meta.RevisionID,
		OperationID: meta.OperationID,
		State:       state,
		Title:       meta.Title,
		Filename:    meta.Filename,
		RevisionNo:  meta.RevisionNo,
	}, nil
}

// ArtifactOperationStatus reads one submit operation.
func (c *APIClient) ArtifactOperationStatus(ctx context.Context, operationID string) (ArtifactCommandResult, error) {
	var result ArtifactCommandResult
	path := "/api/artifact-operations/" + url.PathEscape(operationID)
	if err := c.GetJSON(ctx, path, &result); err != nil {
		return ArtifactCommandResult{}, err
	}
	return result, nil
}

func (c *APIClient) postArtifact(ctx context.Context, method, path string, body *bytes.Buffer, contentType string) (ArtifactCommandResult, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return ArtifactCommandResult{}, err
	}
	req.Header.Set("Content-Type", contentType)
	c.setHeaders(req)

	httpClient := c.HTTPClient
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > httpClient.Timeout {
			clientCopy := *httpClient
			clientCopy.Timeout = remaining
			httpClient = &clientCopy
		}
	}
	resp, err := httpClient.Do(req)
	err = wrapTransport(req, err)
	if err != nil {
		return ArtifactCommandResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ArtifactCommandResult{}, newHTTPError(method, path, resp)
	}
	var result ArtifactCommandResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ArtifactCommandResult{}, fmt.Errorf("decode artifact response: %w", wrapBodyRead(req, err))
	}
	if result.ArtifactID == "" || result.State == "" {
		return ArtifactCommandResult{}, fmt.Errorf("artifact response missing artifact_id or state")
	}
	return result, nil
}

func artifactMultipart(fields map[string]string, filename string, data []byte) (*bytes.Buffer, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", fmt.Errorf("write %s field: %w", key, err)
		}
	}
	part, err := writer.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return nil, "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", fmt.Errorf("write file data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return &body, writer.FormDataContentType(), nil
}
