package openconnector

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, err := signState(testSecret, stateClaims{UserID: "u", Service: "github", Nonce: "n", Exp: now.Add(stateTTL).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := verifyState(testSecret, tok, now)
	if err != nil || got.UserID != "u" || got.Service != "github" || got.Nonce != "n" {
		t.Fatalf("verify = %+v, %v", got, err)
	}
}

func TestStateRejects(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, _ := signState(testSecret, stateClaims{UserID: "u", Service: "github", Exp: now.Add(time.Minute).Unix()})
	payload, sig, _ := strings.Cut(tok, ".")

	tampered, _ := signState(testSecret, stateClaims{UserID: "attacker", Service: "github", Exp: now.Add(time.Minute).Unix()})
	tamperedPayload, _, _ := strings.Cut(tampered, ".")

	cases := []struct {
		name  string
		token string
		at    time.Time
		want  error
	}{
		{"expired", tok, now.Add(2 * time.Minute), ErrStateExpired},
		{"payload swapped", tamperedPayload + "." + sig, now, ErrStateSignature},
		{"wrong secret", payload + "." + signPayload([]byte("other"), payload), now, ErrStateSignature},
		{"no separator", "abc", now, ErrStateMalformed},
		{"empty sig", payload + ".", now, ErrStateMalformed},
	}
	for _, c := range cases {
		if _, err := verifyState(testSecret, c.token, c.at); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}
