package marketinstall

import (
	"strings"
	"testing"
)

func TestHashChangesWhenPlanChanges(t *testing.T) {
	left := map[string]any{"listingId": "acme.reviewer", "version": "1.0.0", "planHash": "sha256:ignored", "n": jsonNumber("1")}
	right := map[string]any{"version": "1.0.0", "listingId": "acme.reviewer", "n": jsonNumber("2"), "planHash": "sha256:other"}
	a, err := hashObject(left)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hashObject(right)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("extra or changed field did not change the hash")
	}
	again, err := hashObject(map[string]any{"n": jsonNumber("1"), "version": "1.0.0", "listingId": "acme.reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if a != again {
		t.Fatalf("hash is not stable: %s vs %s", a, again)
	}
	if !strings.HasPrefix(string(a), "sha256:") {
		t.Fatalf("hash = %s", a)
	}
}

func TestParseRejectsEffectAndSecrets(t *testing.T) {
	if _, err := ParseCommitDocument([]byte(`{"effect":"task"}`)); err == nil {
		t.Fatal("accepted a task effect")
	}
	body := []byte(`{"effect":"none","planRef":"p","approvalRef":"a","idempotencyKey":"idempotency-key-1","plan":{"listingId":"acme.reviewer","version":"1.0.0","policyVersion":"pol","targetRevision":1,"releaseDigest":"rel","agent":{"name":"Reviewer","instructionsDigest":"abc"},"planHash":"sha256:abc","token":"x"},"approval":{"ref":"a","planHash":"sha256:abc","policyVersion":"pol","expiresAt":"2026-12-01T00:00:00Z"},"current":{"policyVersion":"pol","targetRevision":1,"releaseDigest":"rel","releaseState":"published"}}`)
	if _, err := ParseCommitDocument(body); err == nil {
		t.Fatal("accepted a secret key")
	}
}

func jsonNumber(raw string) any {
	return mustNumber(raw)
}
