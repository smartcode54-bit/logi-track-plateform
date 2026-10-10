package storage

import (
	"testing"
	"time"
)

func TestEvidenceTokens(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	a, err := NewEvidenceToken(now)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewEvidenceToken(now)
	if a == b || len(a) != 34 {
		t.Fatalf("tokens %q %q", a, b)
	}
	if at, ok := EvidenceIssuedAt(a); !ok || !at.Equal(now) {
		t.Fatalf("issued %v %v", at, ok)
	}
	// Legacy tokens (12 random bytes, fn:lineNotify.ts) carry no time.
	if _, ok := EvidenceIssuedAt("Q2xhdWRlQ29kZSEh"); ok {
		t.Fatal("a legacy token has an issue time")
	}

	created := now.Add(-24 * time.Hour)
	later := now.Add(365 * 24 * time.Hour)
	// EVIDENCE_TOKEN_TTL_DAYS=0: valid a year later, refused once revoked (R30, R47).
	if !evidenceValid(a, nil, created, later, 0) {
		t.Fatal("a non-expiring token was refused after 365 days")
	}
	revoked := now.Add(time.Hour)
	if evidenceValid(a, &revoked, created, later, 0) {
		t.Fatal("a revoked token was accepted")
	}
	// With a TTL the issue time counts; a legacy token counts from the row's created_at.
	if !evidenceValid(a, nil, created, now.Add(29*24*time.Hour), 30) || evidenceValid(a, nil, created, now.Add(30*24*time.Hour), 30) {
		t.Fatal("TTL from the token's issue time")
	}
	if evidenceValid("Q2xhdWRlQ29kZSEh", nil, created, now.Add(29*24*time.Hour+time.Minute), 30) {
		t.Fatal("legacy token: TTL from created_at")
	}
}

func TestEvidenceTokenForSend(t *testing.T) {
	now := time.Now()
	live, revokedAt := "live-token", now.Add(-time.Hour)
	if tok, mint, err := EvidenceTokenForSend(&live, nil, 0, true, now); tok != "" || mint || err != nil {
		t.Fatal("no photos, no link")
	}
	if tok, mint, _ := EvidenceTokenForSend(&live, nil, 3, false, now); tok != live || mint {
		t.Fatal("a live token is reused")
	}
	if tok, mint, _ := EvidenceTokenForSend(nil, nil, 3, false, now); tok == "" || !mint {
		t.Fatal("a missing token is minted on the first send with photos")
	}
	if tok, mint, _ := EvidenceTokenForSend(&live, &revokedAt, 3, false, now); tok != "" || mint {
		t.Fatal("an automatic send must not re-open a revoked gallery")
	}
	if tok, mint, _ := EvidenceTokenForSend(&live, &revokedAt, 3, true, now); tok == "" || tok == live || !mint {
		t.Fatal("a forced send mints a new token after revocation")
	}
}
