package openconnector

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors, matched with errors.Is against an *APIError by HTTP status.
var (
	ErrNotFound     = errors.New("openconnector: not found")
	ErrUnauthorized = errors.New("openconnector: unauthorized")
	ErrForbidden    = errors.New("openconnector: forbidden")
)

// APIError is returned for any non-2xx response and for 2xx /v1 responses with
// success=false. It deliberately carries neither request nor raw response data.
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Code is the machine-readable error code (errorCode on /v1, error.code on /api).
	Code string
	// Message is the human-readable message supplied by the server.
	Message string
}

// Error implements error.
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Code != "" {
		return fmt.Sprintf("openconnector: %d %s (%s)", e.Status, msg, e.Code)
	}
	return fmt.Sprintf("openconnector: %d %s", e.Status, msg)
}

// Is makes errors.Is(err, ErrNotFound/ErrUnauthorized/ErrForbidden) work.
func (e *APIError) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	case ErrForbidden:
		return e.Status == http.StatusForbidden
	}
	return false
}

// parseAPIError understands both error shapes regardless of path family.
func parseAPIError(status int, body []byte) *APIError {
	out := &APIError{Status: status}
	var wire struct {
		ErrorCode string `json:"errorCode"`
		Message   string `json:"message"`
		Error     *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(body) == 0 || json.Unmarshal(body, &wire) != nil {
		return out
	}
	switch {
	case wire.Error != nil:
		out.Code, out.Message = wire.Error.Code, wire.Error.Message
	default:
		out.Code, out.Message = wire.ErrorCode, wire.Message
	}
	return out
}

func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
