// Package security is the single writer of the append-only security_events table (Appendix C §C.4.13,
// R85; Appendix A 0008_platform). In-transaction events ("no audit, no change") are appended by Append
// inside the caller's db.WithSystem transaction, together with the change they describe: internal/auth
// (T05) and internal/iam (T19) call it, and later domains use it the same way. Consumer-written events
// (login_failed, login_lockout, password_reset_requested, ...) travel as outbox security.event with the
// Event JSON below as the payload (Appendix B §B.5.7); the security.audit consumer appends them with
// Append as well.
package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security/securitydb"
)

// Severities of security_events.severity.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Event is one security_events row. Its JSON form (camelCase) is the payload of outbox security.event.
type Event struct {
	EventType    string         `json:"eventType"`
	Severity     string         `json:"severity"` // info | warning | critical
	Summary      string         `json:"summary"`
	Details      map[string]any `json:"details"`
	ActorUserID  *uuid.UUID     `json:"actorUserId,omitempty"`
	ActorEmail   *string        `json:"actorEmail,omitempty"`
	TargetUserID *uuid.UUID     `json:"targetUserId,omitempty"`
	TenantID     *uuid.UUID     `json:"tenantId,omitempty"`
	RequestID    string         `json:"requestId,omitempty"`
	OccurredAt   time.Time      `json:"occurredAt"`
}

// eventType mirrors the CHECK of security_events.event_type.
var eventType = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate reports an event the table would refuse, before it reaches the database.
func (e Event) Validate() error {
	switch {
	case !eventType.MatchString(e.EventType):
		return fmt.Errorf("security: event type %q is not snake_case", e.EventType)
	case e.Severity != SeverityInfo && e.Severity != SeverityWarning && e.Severity != SeverityCritical:
		return fmt.Errorf("security: severity %q is not info, warning or critical", e.Severity)
	case e.Summary == "":
		return errors.New("security: summary is required")
	case e.OccurredAt.IsZero():
		return errors.New("security: occurredAt is required")
	}
	return nil
}

// Append writes e in tx, the caller's WithSystem transaction, so the row commits or rolls back with
// the change it describes. Details default to {}.
func Append(ctx context.Context, tx pgx.Tx, e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("security: details: %w", err)
	}
	var rid *string
	if e.RequestID != "" {
		rid = &e.RequestID
	}
	return securitydb.New(tx).InsertSecurityEvent(ctx, securitydb.InsertSecurityEventParams{
		EventType: e.EventType, Severity: e.Severity, Summary: e.Summary, Details: b,
		ActorUserID: e.ActorUserID, ActorEmail: e.ActorEmail, TargetUserID: e.TargetUserID,
		TenantID: e.TenantID, RequestID: rid, CreatedAt: e.OccurredAt,
	})
}
