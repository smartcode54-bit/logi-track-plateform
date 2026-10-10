package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// RevocationHook is the outbox relay's hook for user.sessions_revoked (Appendix C §C.4.7): before the
// event is published it re-applies the security-relevant half of the post-commit hook, idempotently,
// so a process that died between COMMIT and Apply (or whose retries never landed) loses nothing. It
// sets auth:sess:revoked:{sid} (TTL accessTTL + leeway) for every sessionIds entry, then raises
// auth:user:ver:{uid} to users.auth_version read under WithSystem (only ever upward). The marker is
// written before the relay publishes rt:user:{uid}, so a client that reconnects on SSE
// session.revoked already finds it. A payload that cannot be decoded, or a user that no longer
// exists, is logged and skipped: retrying it cannot succeed and would hold every later outbox row. A
// Redis or PostgreSQL error is returned, and the relay retries the row.
//
// The result is assignable to outbox.Hook; the scheduler registers it under RouteSessionsRevoked.
func RevocationHook(pool db.Beginner, store *Store, accessTTL time.Duration, log zerolog.Logger) func(ctx context.Context, payload []byte) error {
	return func(ctx context.Context, payload []byte) error {
		var p sessionsRevokedPayload
		if err := json.Unmarshal(payload, &p); err != nil || p.UserID == uuid.Nil {
			log.Error().Err(err).Msg("auth: user.sessions_revoked payload unreadable; revocation hook skipped")
			return nil
		}
		if err := store.markRevoked(ctx, accessTTL+token.Leeway, p.SessionIDs...); err != nil {
			return err
		}
		var ver int32
		err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
			u, err := authdb.New(tx).GetUser(ctx, p.UserID)
			ver = u.AuthVersion
			return err
		})
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn().Str("user_id", p.UserID.String()).Msg("auth: user.sessions_revoked for a user that no longer exists")
			return nil
		}
		if err != nil {
			return err
		}
		return store.raiseVersion(ctx, p.UserID, ver)
	}
}
