package broadcasts

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/broadcasts/broadcastsdb"
)

// MobileMaxLimit bounds a page of the driver's list (the 3.x app streamed the newest 100).
const MobileMaxLimit = 100

// maxRef bounds the broadcast reference of the read receipt (a Firestore document id is at most 1500 bytes).
const maxRef = 1500

// Mine is GET /v1/mobile/broadcasts (mobile:broadcasts; broadcast_repository.dart:8-28): the broadcasts addressed to
// the driver and the legacy ones without recipients it may read, never a voided one, newest first, keyset pages.
func (s *Service) Mine(ctx context.Context, p *authz.Principal, limit int, rawCursor string) ([]DriverBroadcast, string, error) {
	if _, err := driverOf(p); err != nil {
		return nil, "", err
	}
	arg := broadcastsdb.DriverBroadcastsParams{UserID: p.UserID, TenantID: *p.TenantID, RowLimit: int32(limit + 1)}
	if rawCursor != "" {
		c, err := decodeCursor(rawCursor)
		if err != nil {
			return nil, "", err
		}
		arg.HasCursor, arg.CursorAt, arg.CursorID = true, c.T, c.ID
	}
	rows, err := s.driverRows(ctx, p, arg)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = cursor{T: last.SentAt.UTC(), ID: last.ID}.encode()
	}
	out := make([]DriverBroadcast, 0, len(rows))
	for _, r := range rows {
		out = append(out, driverBroadcastOf(r))
	}
	return out, next, nil
}

// Latest is GET /v1/mobile/broadcasts/latest (mobile:broadcasts; broadcast_repository.dart:22, the Home badge after an
// FCM "broadcast" push): the newest broadcast of Mine, nil when there is none.
func (s *Service) Latest(ctx context.Context, p *authz.Principal) (*DriverBroadcast, error) {
	if _, err := driverOf(p); err != nil {
		return nil, err
	}
	rows, err := s.driverRows(ctx, p, broadcastsdb.DriverBroadcastsParams{UserID: p.UserID, TenantID: *p.TenantID, RowLimit: 1})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	b := driverBroadcastOf(rows[0])
	return &b, nil
}

func (s *Service) driverRows(ctx context.Context, p *authz.Principal, arg broadcastsdb.DriverBroadcastsParams) ([]broadcastsdb.DriverBroadcastsRow, error) {
	var rows []broadcastsdb.DriverBroadcastsRow
	err := s.tx(ctx, p, func(_ pgx.Tx, q *broadcastsdb.Queries) error {
		var err error
		rows, err = q.DriverBroadcasts(ctx, arg)
		return err
	})
	return rows, err
}

// MarkRead is POST /v1/mobile/broadcasts/{id}/read (mobile:broadcasts; markBroadcastRead, fn:chat.ts:203, the shim
// target from P5): the driver's read receipt of the broadcast named by its uuid or its Firestore document id. The
// primary key of broadcast_reads makes it idempotent per user: a second read (concurrent ones included) writes nothing
// and answers alreadyRead true. A broadcast that does not exist for the driver (unknown, voided, not readable) is 404
// with details.reason broadcast_not_found.
func (s *Service) MarkRead(ctx context.Context, p *authz.Principal, ref string) (ReadResult, error) {
	if _, err := driverOf(p); err != nil {
		return ReadResult{}, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > maxRef || !utf8.ValidString(ref) || strings.ContainsRune(ref, 0) {
		return ReadResult{}, errBroadcastNotFound()
	}
	arg := broadcastsdb.FindDriverBroadcastParams{Ref: ref}
	if id, err := uuid.Parse(ref); err == nil && id.String() == strings.ToLower(ref) {
		arg.ID = &id
	}
	var out ReadResult
	err := s.tx(ctx, p, func(_ pgx.Tx, q *broadcastsdb.Queries) error {
		id, err := q.FindDriverBroadcast(ctx, arg)
		if errors.Is(err, pgx.ErrNoRows) {
			return errBroadcastNotFound()
		}
		if err != nil {
			return err
		}
		n, err := q.MarkRead(ctx, broadcastsdb.MarkReadParams{BroadcastID: id, UserID: p.UserID, ReadAt: s.at()})
		if err != nil {
			return err
		}
		out = ReadResult{BroadcastID: id, AlreadyRead: n == 0}
		return nil
	})
	return out, err
}
