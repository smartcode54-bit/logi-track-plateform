package auth

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// Outbox routing keys emitted by auth (Appendix B §B.5.2, identity row).
const (
	RouteUserLoggedIn       = "user.logged_in"
	RouteSessionsRevoked    = "user.sessions_revoked"
	RoutePasswordResetAsked = "auth.password_reset_requested"
	RouteSecurityEvent      = "security.event"
)

// Revocation reasons: sessions.revoked_reason and the reason of SSE session.revoked (C.4.4, C.4.7).
// claims_changed revokes no session; the others end sessions.
const (
	RevokeClaimsChanged   = "claims_changed"
	RevokeLogout          = "logout"
	RevokeLogoutAll       = "logout_all"
	RevokeAdmin           = "admin_revoke"
	RevokeDisabled        = "disabled"
	RevokePasswordChanged = "password_changed"
	RevokePasswordReset   = "password_reset"
	RevokeRefreshReuse    = "refresh_reuse"
	RevokeDeviceRelogin   = "device_relogin"
)

// outboxEvent is one outbox_events row.
type outboxEvent struct {
	RoutingKey    string
	AggregateType string
	AggregateID   string
	TenantID      *uuid.UUID
	Payload       any
	RequestID     string
	Topics        []string
}

// insertOutbox emits e in tx through outbox.Append, the single emit path (main spec §7.1): it validates
// the routing key and the realtime topics and carries requestId and the caller's traceparent in the
// message headers (Appendix B §B.5.2).
func insertOutbox(ctx context.Context, tx pgx.Tx, e outboxEvent) error {
	var headers map[string]string
	if e.RequestID != "" {
		headers = map[string]string{"requestId": e.RequestID}
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: e.RoutingKey, AggregateType: e.AggregateType, AggregateID: e.AggregateID,
		TenantID: e.TenantID, Payload: e.Payload, Topics: e.Topics, Headers: headers,
	})
	return err
}

// emitSecurityEvent queues a consumer-written security event (outbox security.event, C.4.13): the
// security.audit consumer appends it with security.Append. In-transaction events call security.Append
// directly.
func emitSecurityEvent(ctx context.Context, tx pgx.Tx, e security.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	agg := "auth"
	if e.TargetUserID != nil {
		agg = e.TargetUserID.String()
	}
	return insertOutbox(ctx, tx, outboxEvent{
		RoutingKey: RouteSecurityEvent, AggregateType: "user", AggregateID: agg, TenantID: e.TenantID,
		Payload: e, RequestID: e.RequestID,
	})
}

// sessionsRevokedPayload is the payload of user.sessions_revoked; the relay publishes it as SSE
// session.revoked on user:{uid} and notify.fcm pushes session_revoked to the revoked installs (R50).
type sessionsRevokedPayload struct {
	UserID     uuid.UUID   `json:"userId"`
	SessionIDs []uuid.UUID `json:"sessionIds"`
	Reason     string      `json:"reason"`
}

func emitSessionsRevoked(ctx context.Context, tx pgx.Tx, uid uuid.UUID, sids []uuid.UUID, reason, requestID string) error {
	if sids == nil {
		sids = []uuid.UUID{}
	}
	return insertOutbox(ctx, tx, outboxEvent{
		RoutingKey: RouteSessionsRevoked, AggregateType: "user", AggregateID: uid.String(),
		Payload:   sessionsRevokedPayload{UserID: uid, SessionIDs: sids, Reason: reason},
		RequestID: requestID, Topics: []string{"user:" + uid.String()},
	})
}
