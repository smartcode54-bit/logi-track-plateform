package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagedb"
)

// Evidence tokens (R30, R47; main spec §9.6): an unguessable key of the public gallery /evidence/{token} on a
// trip_records or standby_records row. Tokens do not expire while EVIDENCE_TOKEN_TTL_DAYS is 0 (the default; LINE
// cards already sent keep opening) and are revocable: evidence_token_revoked_at set means the gallery answers 404
// until the next forced LINE send mints a new token.

// EvidenceKind says which table holds a token.
type EvidenceKind string

// Evidence kinds.
const (
	EvidenceTrip    EvidenceKind = "trip"
	EvidenceStandby EvidenceKind = "standby"
)

// EvidenceTarget is the row a valid token opens.
type EvidenceTarget struct {
	Kind     EvidenceKind
	ID       uuid.UUID
	TenantID uuid.UUID
}

// Evidence errors: the gallery answers 400 for ErrEvidenceEmpty and 404 for ErrEvidenceNotFound (unknown,
// revoked or, with a TTL, expired).
var (
	ErrEvidenceEmpty    = errors.New("storage: empty evidence token")
	ErrEvidenceNotFound = errors.New("storage: evidence token unknown, revoked or expired")
)

// evidenceTokenV1 starts a token minted by Go: version byte, issue time (Unix seconds, big endian), 16 random
// bytes, base64url without padding (34 characters). Legacy tokens (12 random bytes, 16 characters,
// fn:lineNotify.ts:355,427) carry no time; with a TTL their age counts from the row's created_at.
const (
	evidenceTokenV1     = 1
	evidenceTokenV1Size = 1 + 8 + 16
)

// NewEvidenceToken mints a token issued at now.
func NewEvidenceToken(now time.Time) (string, error) {
	b := make([]byte, evidenceTokenV1Size)
	b[0] = evidenceTokenV1
	binary.BigEndian.PutUint64(b[1:9], uint64(now.Unix()))
	if _, err := rand.Read(b[9:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// EvidenceIssuedAt returns the issue time a Go-minted token carries; ok is false for a legacy token.
func EvidenceIssuedAt(token string) (time.Time, bool) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != evidenceTokenV1Size || b[0] != evidenceTokenV1 {
		return time.Time{}, false
	}
	return time.Unix(int64(binary.BigEndian.Uint64(b[1:9])), 0).UTC(), true
}

// evidenceValid applies revocation and the optional TTL.
func evidenceValid(token string, revokedAt *time.Time, createdAt, now time.Time, ttlDays int) bool {
	if revokedAt != nil {
		return false
	}
	if ttlDays <= 0 {
		return true
	}
	issued, ok := EvidenceIssuedAt(token)
	if !ok {
		issued = createdAt
	}
	return now.Before(issued.Add(time.Duration(ttlDays) * 24 * time.Hour))
}

// ResolveEvidence is the verifier of GET /evidence/{token}: trip_records first, then standby_records (as
// fn:tripEvidence.ts:135-148), refusing revoked tokens and, when EVIDENCE_TOKEN_TTL_DAYS > 0, expired ones.
func (s *Service) ResolveEvidence(ctx context.Context, token string) (EvidenceTarget, error) {
	if token == "" {
		return EvidenceTarget{}, ErrEvidenceEmpty
	}
	if len(token) > 256 {
		return EvidenceTarget{}, ErrEvidenceNotFound
	}
	now := s.now()
	var out EvidenceTarget
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		q := storagedb.New(tx)
		t, err := q.TripByEvidenceToken(ctx, token)
		if err == nil {
			if !evidenceValid(token, t.EvidenceTokenRevokedAt, t.CreatedAt, now, s.cfg.EvidenceTokenTTLDays) {
				return ErrEvidenceNotFound
			}
			out = EvidenceTarget{Kind: EvidenceTrip, ID: t.ID, TenantID: t.TenantID}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		sb, err := q.StandbyByEvidenceToken(ctx, token)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEvidenceNotFound
		}
		if err != nil {
			return err
		}
		if !evidenceValid(token, sb.EvidenceTokenRevokedAt, sb.CreatedAt, now, s.cfg.EvidenceTokenTTLDays) {
			return ErrEvidenceNotFound
		}
		out = EvidenceTarget{Kind: EvidenceStandby, ID: sb.ID, TenantID: sb.TenantID}
		return nil
	})
	return out, err
}

// RevokeEvidence sets evidence_token_revoked_at in tx (POST /v1/{trips|standby}/{id}/evidence/revoke, authorised
// by the caller). It reports whether a live token was revoked; revoking twice keeps the first instant.
func RevokeEvidence(ctx context.Context, tx pgx.Tx, kind EvidenceKind, id uuid.UUID, now time.Time) (bool, error) {
	q := storagedb.New(tx)
	var n int64
	var err error
	switch kind {
	case EvidenceTrip:
		n, err = q.RevokeTripEvidence(ctx, storagedb.RevokeTripEvidenceParams{RevokedAt: now, ID: id})
	case EvidenceStandby:
		n, err = q.RevokeStandbyEvidence(ctx, storagedb.RevokeStandbyEvidenceParams{RevokedAt: now, ID: id})
	default:
		return false, errors.New("storage: unknown evidence kind")
	}
	return n > 0, err
}

// EvidenceTokenForSend decides the token a LINE card links (notify.line, R47): no photos, no link; a live token is
// reused; a missing one is minted; a revoked one is replaced only by a forced send (job.notify.line.force), so an
// automatic resend never re-opens a revoked gallery. mint reports a token that SetEvidenceToken must store in the
// send's transaction.
func EvidenceTokenForSend(current *string, revokedAt *time.Time, photos int, forced bool, now time.Time) (token string, mint bool, err error) {
	switch {
	case photos <= 0:
		return "", false, nil
	case current != nil && *current != "" && revokedAt == nil:
		return *current, false, nil
	case current != nil && *current != "" && !forced:
		return "", false, nil
	}
	token, err = NewEvidenceToken(now)
	return token, err == nil, err
}

// SetEvidenceToken stores a minted token in tx and clears any revocation.
func SetEvidenceToken(ctx context.Context, tx pgx.Tx, kind EvidenceKind, id uuid.UUID, token string) error {
	q := storagedb.New(tx)
	var n int64
	var err error
	switch kind {
	case EvidenceTrip:
		n, err = q.SetTripEvidenceToken(ctx, storagedb.SetTripEvidenceTokenParams{Token: token, ID: id})
	case EvidenceStandby:
		n, err = q.SetStandbyEvidenceToken(ctx, storagedb.SetStandbyEvidenceTokenParams{Token: token, ID: id})
	default:
		return errors.New("storage: unknown evidence kind")
	}
	if err == nil && n == 0 {
		return pgx.ErrNoRows
	}
	return err
}

// EvidenceImageURL is a gallery image: a committed file signed for EVIDENCE_PRESIGN_TTL (15 min) on its own backend.
func (s *Service) EvidenceImageURL(ctx context.Context, fileID uuid.UUID) (string, error) {
	return s.SignedURL(ctx, fileID, s.cfg.EvidencePresignTTL)
}
