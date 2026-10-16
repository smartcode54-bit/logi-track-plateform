package iam

import (
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// A sessionless legacy human keeps its normal contract. Session-backed writes require the live context.
// This reread does not lock sessions behind target users or attempt to cancel earlier authorized work.
func liveIAMContext(c call, tx pgx.Tx) error {
	if c.p.SessionID == uuid.Nil {
		return nil
	}
	var mode, status string
	var scv int64
	var version int32
	var tenant *uuid.UUID
	var revoked *time.Time
	var expires time.Time
	err := tx.QueryRow(c.ctx, `SELECT s.access_mode,s.context_version,s.active_tenant_id,s.revoked_at,s.absolute_expires_at,u.auth_version,u.status FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id=$1 AND s.user_id=$2`, c.p.SessionID, c.p.UserID).Scan(&mode, &scv, &tenant, &revoked, &expires, &version, &status)
	stale := func() error {
		return httpx.NewError(401, "token_expired", "token expired").WithDetails(map[string]any{"reason": "claims_changed"})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return stale()
	}
	if err != nil {
		return err
	}
	// HTTP calls carry the verified principal's version (zero for legacy claims). Service-only
	// adapters such as MoveDriver rely on their already verified admission version.
	expected := c.contextVersion
	if mode != "member" || expected != "" && strconv.FormatInt(scv, 10) != expected || !equalID(tenant, c.p.TenantID) || revoked != nil || !expires.After(time.Now()) || version != c.p.AuthVersion || status != "active" {
		return stale()
	}
	return nil
}
