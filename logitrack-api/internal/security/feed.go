package security

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security/securitydb"
)

// The read side of security_events (T51): the keyset feed of GET /v1/security-events and the activity counters
// of GET /v1/security/overview. Both run in the caller's db.WithPrincipal transaction, so RLS decides which rows
// a principal sees (Appendix C §C.3.5, 0008_platform): tenant staff the rows of their tenant, a platform principal
// under X-Act-On-Tenant: * every row, under X-Act-On-Tenant: <uuid> the rows of that tenant. Rows with tenant_id
// NULL are therefore platform-only: the platform-level events (login_failed, login_lockout, refresh_token_reuse,
// platform_cross_tenant_access with *, platform role and dispatcher grants) and the administration of a platform
// admin acting without a tenant context; an admin event otherwise carries the caller's tenant (internal/iam audit).

// EventTypes is the catalogue of security_events.event_type values, in the order of Appendix C §C.4.13 (R85,
// Appendix B §B.5.7): the in-transaction events of auth and IAM, the cross-tenant access row, the consumer-written
// events, then the events of other domains. The four legacy Firestore types (role_matrix_saved, user_role_changed,
// user_created, user_sessions_revoked, logitrack-web/lib/securityEventsConstants.ts, deleted in F87; last tree
// 4fc3d70e) are all in it. The audit page's type filter comes from here (GET /v1/security-events meta.types); a
// row of another type (a legacy document) is still listed.
var EventTypes = []string{
	"refresh_token_reuse", "device_token_moved", "user_created", "user_updated", "user_invited", "user_role_changed",
	"membership_added", "membership_role_changed", "membership_removed", "tenant_switched", "user_scope_changed", "user_disabled", "user_enabled", "user_deleted", "user_sessions_revoked",
	"user_password_temporary_issued", "password_changed", "password_reset_completed", "google_identity_linked",
	"driver_linked", "driver_unlinked", "driver_tenant_moved", "task_tenant_reassigned", "platform_role_granted",
	"platform_role_revoked", "role_matrix_saved", "api_key_created", "api_key_revoked", "tenant_created",
	"tenant_updated", "tenant_contractor_changed", "tenant_rehomed",
	"platform_cross_tenant_access",
	"login_failed", "login_lockout", "password_reset_requested", "tenant_orphans_detected",
	"trip_record_renamed", "mobile_floor_changed", "mobile_release_published", "queue_replayed", "evidence_viewed",
	"webhook_signature_failed", "platform_support_entered", "platform_support_left", "platform_support_denied", "support_access_enabled", "support_access_disabled", "platform_membership_ratified", "platform_membership_granted", "platform_account_reset", "platform_account_invited", "platform_admin_account_changed",
}

// KnownType reports whether t is in EventTypes.
func KnownType(t string) bool { return slices.Contains(EventTypes, t) }

// ValidType reports whether t has the form the table's CHECK accepts (snake_case).
func ValidType(t string) bool { return eventType.MatchString(t) }

// ValidSeverity reports whether s is info, warning or critical.
func ValidSeverity(s string) bool {
	return s == SeverityInfo || s == SeverityWarning || s == SeverityCritical
}

// Row is one security_events row as GET /v1/security-events returns it. Type and the actor e-mail keep the names
// of the legacy feed (logitrack-web/hooks/useSecurityEventsFeed.ts); actorEmail is the actor's address as recorded
// when the event was written (InsertSecurityEvent; internal/iam names the actor of older rows on read);
// actorLegacyUid is the legacy actorUid that no users row matched at ETL.
type Row struct {
	ID             uuid.UUID       `json:"id"`
	CreatedAt      time.Time       `json:"createdAt"`
	Type           string          `json:"type"`
	Severity       string          `json:"severity"`
	Summary        string          `json:"summary"`
	Details        json.RawMessage `json:"details"`
	ActorUserID    *uuid.UUID      `json:"actorUserId"`
	ActorEmail     *string         `json:"actorEmail"`
	ActorLegacyUID *string         `json:"actorLegacyUid"`
	TargetUserID   *uuid.UUID      `json:"targetUserId"`
	TenantID       *uuid.UUID      `json:"tenantId"`
	RequestID      *string         `json:"requestId"`
}

// Cursor is the keyset position (created_at, id) of a feed page.
type Cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"id"`
}

// Encode is the opaque cursor of Appendix B §B.1.5 (base64url JSON).
func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses an opaque cursor; ok is false when it is not one.
func DecodeCursor(s string) (Cursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, false
	}
	var c Cursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil || c.CreatedAt.IsZero() {
		return Cursor{}, false
	}
	return c, true
}

// Filter selects a feed page. The caller validates it (types and severity in the table's vocabulary, From before
// To, 1 <= Limit <= 500).
type Filter struct {
	Types    []string   // any of these; empty = every type
	Severity string     // "" = every severity
	From     *time.Time // created_at >= From
	To       *time.Time // created_at < To
	After    *Cursor    // the page after this position
	Limit    int
}

// List reads one keyset page in tx, the caller's db.WithPrincipal transaction (RLS decides the rows). next is the
// cursor of the following page, "" on the last one.
func List(ctx context.Context, tx pgx.Tx, f Filter) (rows []Row, next string, err error) {
	if f.Limit < 1 {
		return nil, "", fmt.Errorf("security: a feed page needs a positive limit")
	}
	p := securitydb.ListSecurityEventsParams{Types: f.Types, FromAt: f.From, ToAt: f.To, RowLimit: int32(f.Limit) + 1}
	if p.Types == nil {
		p.Types = []string{}
	}
	if f.Severity != "" {
		p.Severity = &f.Severity
	}
	if f.After != nil {
		t := f.After.CreatedAt
		p.AfterCreated, p.AfterID = &t, f.After.ID
	}
	got, err := securitydb.New(tx).ListSecurityEvents(ctx, p)
	if err != nil {
		return nil, "", fmt.Errorf("security: list events: %w", err)
	}
	if len(got) > f.Limit {
		got = got[:f.Limit]
		last := got[f.Limit-1]
		next = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	rows = make([]Row, len(got))
	for i, r := range got {
		details := json.RawMessage(r.Details)
		if len(details) == 0 {
			details = json.RawMessage("{}")
		}
		rows[i] = Row{ID: r.ID, CreatedAt: r.CreatedAt, Type: r.EventType, Severity: r.Severity, Summary: r.Summary,
			Details: details, ActorUserID: r.ActorUserID, ActorEmail: r.ActorEmail, ActorLegacyUID: r.ActorLegacyUid,
			TargetUserID: r.TargetUserID, TenantID: r.TenantID, RequestID: r.RequestID}
	}
	return rows, next, nil
}

// Activity counts the events a principal sees around now, for the overview cards and the histogram of the
// Security Center (the same slots as logitrack-web/lib/securityEventsHistogram.ts: oldest first, newest last).
type Activity struct {
	Last24h int `json:"last24h"`
	Last7d  int `json:"last7d"`
	// BySeverity24h has the three severities, zero included.
	BySeverity24h map[string]int `json:"bySeverity24h"`
	// Hourly24h[23] counts the hour before now, Hourly24h[0] the hour that ended 23 hours before now.
	Hourly24h []int `json:"hourly24h"`
	// Daily7d[6] counts the 24 hours before now, Daily7d[0] the day that ended 6 days before now.
	Daily7d []int `json:"daily7d"`
}

// ReadActivity counts in tx, the caller's db.WithPrincipal transaction, the events of the 7 days before now.
func ReadActivity(ctx context.Context, tx pgx.Tx, now time.Time) (Activity, error) {
	a := Activity{BySeverity24h: map[string]int{SeverityInfo: 0, SeverityWarning: 0, SeverityCritical: 0},
		Hourly24h: make([]int, 24), Daily7d: make([]int, 7)}
	rows, err := securitydb.New(tx).SecurityEventAges(ctx, now)
	if err != nil {
		return a, fmt.Errorf("security: event activity: %w", err)
	}
	for _, r := range rows {
		age, n := int(r.AgeHours), int(r.Events)
		if age < 0 || age >= 7*24 {
			continue
		}
		a.Last7d += n
		a.Daily7d[6-age/24] += n
		if age < 24 {
			a.Last24h += n
			a.Hourly24h[23-age] += n
			a.BySeverity24h[r.Severity] += n
		}
	}
	return a, nil
}
