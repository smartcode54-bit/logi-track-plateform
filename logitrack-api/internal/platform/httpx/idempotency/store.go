package idempotency

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/dbq"
)

// Record is a stored response: the Redis value of idem:http:{userId}:{key} and, split into columns,
// an idempotency_keys row. The body is kept verbatim, so a replay is byte-identical (a jsonb copy of
// the JSON itself would reorder its keys): as text when it is UTF-8 without a NUL byte, else base64
// (jsonb rejects the \u0000 escape a NUL would become).
type Record struct {
	Fingerprint string `json:"fingerprint"`
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	Body        string `json:"body,omitempty"`
	BodyBase64  string `json:"bodyBase64,omitempty"`
}

// NewRecord copies a response into a Record.
func NewRecord(fingerprint string, status int, contentType string, body []byte) Record {
	r := Record{Fingerprint: fingerprint, Status: status, ContentType: contentType}
	if utf8.Valid(body) && bytes.IndexByte(body, 0) < 0 {
		r.Body = string(body)
	} else {
		r.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	}
	return r
}

// Bytes returns the stored body.
func (r Record) Bytes() []byte {
	if r.BodyBase64 != "" {
		b, err := base64.StdEncoding.DecodeString(r.BodyBase64)
		if err == nil {
			return b
		}
	}
	return []byte(r.Body)
}

// responseBody is idempotency_keys.response_body: the response minus the columns of their own.
type responseBody struct {
	ContentType string `json:"contentType,omitempty"`
	Body        string `json:"body,omitempty"`
	BodyBase64  string `json:"bodyBase64,omitempty"`
}

// Row is a live idempotency_keys row.
type Row struct {
	Completed bool
	Record    Record // Fingerprint always; Status and body when Completed
	ExpiresAt time.Time
}

// ErrTakenOver is returned by Complete when another request took the claim over (this one outlived
// its lease): the other request owns the key now.
var ErrTakenOver = errors.New("idempotency: the claim was taken over")

// Store is the durable copy (PostgreSQL in production, a fake in unit tests).
type Store interface {
	// Claim takes (scope, key) for one execution; ok false means the key is completed or held.
	Claim(ctx context.Context, scope, key, fingerprint string, ttl, lock time.Duration) (claimedAt time.Time, ok bool, err error)
	// Get returns the live row; found false when there is none (or it expired).
	Get(ctx context.Context, scope, key string) (row Row, found bool, err error)
	// Complete stores the response of the claim made at claimedAt; ErrTakenOver when that claim is
	// no longer the live one.
	Complete(ctx context.Context, scope, key string, claimedAt time.Time, rec Record) error
	// Release drops the claim made at claimedAt (the request committed nothing).
	Release(ctx context.Context, scope, key string, claimedAt time.Time) error
}

// PGStore keeps the records in idempotency_keys (0009_infra) through logitrack_app.
type PGStore struct {
	q *dbq.Queries
}

// NewPGStore wraps a pool or connection of the logitrack_app login (DATABASE_URL).
func NewPGStore(db dbq.DBTX) *PGStore { return &PGStore{q: dbq.New(db)} }

// Claim implements Store.
func (s *PGStore) Claim(ctx context.Context, scope, key, fingerprint string, ttl, lock time.Duration) (time.Time, bool, error) {
	at, err := s.q.IdempotencyClaim(ctx, dbq.IdempotencyClaimParams{
		Scope: scope, Key: key, RequestHash: fingerprint, TtlSeconds: ttl.Seconds(), LockSeconds: lock.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("idempotency: claim: %w", err)
	}
	return at.Time, true, nil
}

// Get implements Store.
func (s *PGStore) Get(ctx context.Context, scope, key string) (Row, bool, error) {
	r, err := s.q.IdempotencyGet(ctx, dbq.IdempotencyGetParams{Scope: scope, Key: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, fmt.Errorf("idempotency: get: %w", err)
	}
	row := Row{Completed: r.Status == "completed", Record: Record{Fingerprint: r.RequestHash}, ExpiresAt: r.ExpiresAt.Time}
	if row.Completed {
		if r.ResponseCode != nil {
			row.Record.Status = int(*r.ResponseCode)
		}
		var b responseBody
		if len(r.ResponseBody) > 0 {
			if err := json.Unmarshal(r.ResponseBody, &b); err != nil {
				return Row{}, false, fmt.Errorf("idempotency: decode stored response: %w", err)
			}
		}
		row.Record.ContentType, row.Record.Body, row.Record.BodyBase64 = b.ContentType, b.Body, b.BodyBase64
	}
	return row, true, nil
}

// Complete implements Store.
func (s *PGStore) Complete(ctx context.Context, scope, key string, claimedAt time.Time, rec Record) error {
	body, err := json.Marshal(responseBody{ContentType: rec.ContentType, Body: rec.Body, BodyBase64: rec.BodyBase64})
	if err != nil {
		return err
	}
	n, err := s.q.IdempotencyComplete(ctx, dbq.IdempotencyCompleteParams{
		ResponseCode: int32(rec.Status), ResponseBody: body, Scope: scope, Key: key, ClaimedAt: ts(claimedAt),
	})
	if err != nil {
		return fmt.Errorf("idempotency: complete: %w", err)
	}
	if n == 0 {
		return ErrTakenOver
	}
	return nil
}

// Release implements Store.
func (s *PGStore) Release(ctx context.Context, scope, key string, claimedAt time.Time) error {
	if _, err := s.q.IdempotencyRelease(ctx, dbq.IdempotencyReleaseParams{Scope: scope, Key: key, ClaimedAt: ts(claimedAt)}); err != nil {
		return fmt.Errorf("idempotency: release: %w", err)
	}
	return nil
}

// Prune deletes expired rows in batches (idempotency.prune, Appendix B §B.5.6) and returns how many.
// batch <= 0 means 1000.
func (s *PGStore) Prune(ctx context.Context, batch int) (int64, error) {
	if batch <= 0 {
		batch = 1000
	}
	var total int64
	for {
		n, err := s.q.IdempotencyPrune(ctx, int32(batch))
		total += n
		if err != nil {
			return total, fmt.Errorf("idempotency: prune: %w", err)
		}
		if n < int64(batch) {
			return total, nil
		}
	}
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
