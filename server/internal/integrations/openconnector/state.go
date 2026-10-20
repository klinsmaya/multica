package openconnector

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Signed-state errors. The handler maps all of them to a generic "connect
// failed" redirect so a tampered or expired state never leaks which check
// failed.
var (
	// ErrStateMalformed is returned when the state is not "<payload>.<sig>".
	ErrStateMalformed = errors.New("open-connector: state malformed")
	// ErrStateSignature is returned when the HMAC signature does not match.
	ErrStateSignature = errors.New("open-connector: state signature mismatch")
	// ErrStateExpired is returned when the state's exp claim is in the past.
	ErrStateExpired = errors.New("open-connector: state expired")
)

// stateTTL bounds the time between BeginOAuthConnect and the callback. It
// matches open-connector's own 10 minute connection-request lifetime.
const stateTTL = 10 * time.Minute

// stateClaims is the payload of the signed connect-state. Field names are
// single letters to keep the token compact; they are an internal wire format.
type stateClaims struct {
	UserID  string `json:"u"`
	Service string `json:"s"`
	// Nonce correlates the callback with the in-memory pending connect of the
	// service (see Service.pending). open-connector's return redirect carries
	// no request id, so the request id cannot ride in the state itself.
	Nonce string `json:"r"`
	Exp   int64  `json:"e"`
}

// signState produces a URL-safe "<payload>.<sig>" token: base64url JSON claims
// plus the base64url HMAC-SHA256 of that encoded payload.
func signState(secret []byte, claims stateClaims) (string, error) {
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + signPayload(secret, payload), nil
}

// verifyState checks the signature (constant time) before trusting the payload,
// then the expiry against now.
func verifyState(secret []byte, token string, now time.Time) (stateClaims, error) {
	payload, sig, found := strings.Cut(token, ".")
	if !found || payload == "" || sig == "" {
		return stateClaims{}, ErrStateMalformed
	}
	if !hmac.Equal([]byte(sig), []byte(signPayload(secret, payload))) {
		return stateClaims{}, ErrStateSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return stateClaims{}, ErrStateMalformed
	}
	var claims stateClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return stateClaims{}, ErrStateMalformed
	}
	if now.Unix() > claims.Exp {
		return stateClaims{}, ErrStateExpired
	}
	return claims, nil
}

func signPayload(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
