package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SupportVisit is private operational state. Tenant visibility comes from security events.
type SupportVisit struct {
	ID                    uuid.UUID
	SessionID             *uuid.UUID
	ActorUserID           uuid.UUID
	TenantID              uuid.UUID
	StartedAt             time.Time
	EndedAt               *time.Time
	EndReason             *string
	EndRecordedAt         *time.Time
	ConsentVersionAtEntry int64
}

const (
	SupportVisitLeave       = "leave"
	SupportVisitSwitch      = "switch"
	SupportVisitLogout      = "logout"
	SupportVisitRevoked     = "revoked"
	SupportVisitRoleRemoved = "role_removed"
	SupportVisitExpired     = "expired"
)

var ErrSupportVisitTarget = errors.New("db: support visit requires an eligible tenant and the actor's session")

const supportVisitColumns = `id, session_id, actor_user_id, tenant_id, started_at, ended_at,
 end_reason, end_recorded_at, consent_version_at_entry`

func scanSupportVisit(row pgx.Row) (v SupportVisit, err error) {
	err = row.Scan(&v.ID, &v.SessionID, &v.ActorUserID, &v.TenantID, &v.StartedAt, &v.EndedAt,
		&v.EndReason, &v.EndRecordedAt, &v.ConsentVersionAtEntry)
	return v, err
}

// CreateSupportVisit runs in the caller's authorized WithSystem transaction. The caller appends
// the entry event and changes session context in that same transaction; this repository grants no
// admission. startedAt is the effective entry instant, not worker time; IDs are database-generated.
func CreateSupportVisit(ctx context.Context, tx pgx.Tx, sessionID, actorID, tenantID uuid.UUID, startedAt time.Time, consentVersion int64) (SupportVisit, error) {
	v, err := scanSupportVisit(tx.QueryRow(ctx, `INSERT INTO support_visits
 (session_id, actor_user_id, tenant_id, started_at, consent_version_at_entry)
 SELECT s.id, s.user_id, t.id, $4, $5 FROM sessions s JOIN tenants t ON t.id = $3
 WHERE s.id = $1 AND s.user_id = $2 AND t.kind IN ('own_fleet','carrier')
 RETURNING `+supportVisitColumns, sessionID, actorID, tenantID, startedAt, consentVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return SupportVisit{}, ErrSupportVisitTarget
	}
	if err != nil {
		return SupportVisit{}, fmt.Errorf("db: create support visit: %w", err)
	}
	return v, nil
}

// OpenSupportVisit locks the session's open visit for context transitions. No row is pgx.ErrNoRows.
func OpenSupportVisit(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (SupportVisit, error) {
	v, err := scanSupportVisit(tx.QueryRow(ctx, `SELECT `+supportVisitColumns+`
 FROM support_visits WHERE session_id = $1 AND ended_at IS NULL FOR UPDATE`, sessionID))
	if err != nil {
		return SupportVisit{}, fmt.Errorf("db: open support visit: %w", err)
	}
	return v, nil
}

// CloseSupportVisit returns nil on a repeated/missing close. Only the caller receiving a visit
// appends its exit event, in this transaction. effectiveAt preserves actual revocation/expiry time
// after restart; recordedAt captures when cleanup observed it. Close before deleting the session.
func CloseSupportVisit(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, reason string, effectiveAt, recordedAt time.Time) (*SupportVisit, error) {
	v, err := scanSupportVisit(tx.QueryRow(ctx, `UPDATE support_visits
 SET ended_at = $2, end_reason = $3, end_recorded_at = $4
 WHERE session_id = $1 AND ended_at IS NULL RETURNING `+supportVisitColumns, sessionID, effectiveAt, reason, recordedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("db: close support visit: %w", err)
	}
	return &v, nil
}
