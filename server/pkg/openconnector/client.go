package openconnector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// DefaultUserAgent is sent on every request.
const DefaultUserAgent = "multica-openconnector-go/0.1"

// DefaultTimeout is the per-request timeout when the injected HTTP client has none.
const DefaultTimeout = 30 * time.Second

// Options configures a [Client].
type Options struct {
	// BaseURL is the open-connector origin, e.g. "http://127.0.0.1:3000". Required; http or https.
	BaseURL string
	// AdminToken is sent as "Authorization: Bearer <token>". It must never be
	// persisted or logged by callers.
	AdminToken string
	// HTTPClient is adopted in full when non-nil. If it has no Timeout,
	// DefaultTimeout is applied.
	HTTPClient *http.Client
}

// Client is the open-connector admin API client. Safe for concurrent use.
type Client struct {
	rc      *resty.Client
	baseURL string
}

// NewClient validates opts and builds a Client.
func NewClient(opts Options) (*Client, error) {
	raw := strings.TrimSpace(opts.BaseURL)
	if raw == "" {
		return nil, errors.New("openconnector: BaseURL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("openconnector: BaseURL must be an absolute http or https URL")
	}
	baseURL := strings.TrimRight(raw, "/")

	var rc *resty.Client
	if opts.HTTPClient != nil {
		rc = resty.NewWithClient(opts.HTTPClient)
		if opts.HTTPClient.Timeout == 0 {
			rc.SetTimeout(DefaultTimeout)
		}
	} else {
		rc = resty.New().SetTimeout(DefaultTimeout)
	}
	rc.SetBaseURL(baseURL).
		SetHeader("Accept", "application/json").
		SetHeader("User-Agent", DefaultUserAgent)
	if opts.AdminToken != "" {
		rc.SetAuthToken(opts.AdminToken)
	}
	return &Client{rc: rc, baseURL: baseURL}, nil
}

// BaseURL returns the normalized API root.
func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) newRequest(ctx context.Context) *resty.Request {
	return c.rc.R().SetContext(ctx)
}

// isEnvelopePath reports whether the path family wraps responses in
// {success,message,data,meta}. Only /api/* returns bare JSON.
func isEnvelopePath(path string) bool { return !strings.HasPrefix(path, "/api/") }

// do executes the request and decodes the success payload into out (nil to
// discard). Errors never include request headers or bodies.
func (c *Client) do(req *resty.Request, method, path string, out any) error {
	if len(req.Header.Values("Content-Type")) == 0 && req.Body != nil {
		req.SetHeader("Content-Type", "application/json")
	}
	resp, err := req.Execute(method, path)
	if err != nil {
		// resty errors carry the method/URL only (no headers or body).
		return fmt.Errorf("openconnector: %s %s: %w", method, path, err)
	}
	if resp.IsError() {
		return parseAPIError(resp.StatusCode(), resp.Body())
	}
	payload := resp.Body()
	if isEnvelopePath(path) {
		var env struct {
			Success   bool            `json:"success"`
			Message   string          `json:"message"`
			ErrorCode string          `json:"errorCode"`
			Data      json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(payload, &env); err != nil {
			return fmt.Errorf("openconnector: %s %s: decode envelope: %w", method, path, err)
		}
		if !env.Success {
			return &APIError{Status: resp.StatusCode(), Code: env.ErrorCode, Message: env.Message}
		}
		payload = env.Data
	}
	if out == nil {
		return nil
	}
	if len(payload) == 0 || string(payload) == "null" {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("openconnector: %s %s: decode response: %w", method, path, err)
	}
	return nil
}

func esc(s string) string { return url.PathEscape(s) }
