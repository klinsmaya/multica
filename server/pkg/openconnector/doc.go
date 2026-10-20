// Package openconnector provides a standalone client for the admin REST API of
// a self-hosted open-connector server. It covers provider discovery, OAuth
// client configuration, OAuth and API-key connections, and runtime token
// management. It has no Multica-specific dependencies. The MCP endpoint is
// out of scope: agents talk to it directly with a runtime token.
//
// open-connector answers with three different shapes depending on the path
// family: /v1/* wraps successes in {success,message,data,meta} and failures in
// a top-level errorCode; /api/* returns bare JSON and reports failures as
// {"error":{code,message}}. The client hides this difference: every non-success
// outcome is returned as an *APIError, which matches the ErrNotFound,
// ErrUnauthorized and ErrForbidden sentinels through errors.Is.
//
// Error values never contain request headers or bodies. Secrets that can
// appear in responses (runtime token plaintext) are redacted by String and
// slog formatting.
package openconnector
