// Package broadcasts serves the broadcasts of T48 (issue #68; main spec §5, §7.6, §12.1 class B, Appendix A §A.2.6,
// Appendix B §B.2.16, §B.2.21, §B.2.22): the staff history and composer /v1/broadcasts (list with read counts, send,
// void) and the driver's side below /v1/mobile (its broadcasts, the latest one, the read receipt that is also the
// target of the markBroadcastRead callable shim).
//
// They replace the sendBroadcast and markBroadcastRead callables (fn:chat.ts:135-248) and the web's Firestore reads and
// deletes of the broadcasts collection (BroadcastComposer.tsx:139-178, BroadcastHistoryModal.tsx:80-102):
//
//   - a send resolves its recipients on the server (the legacy browser listed every driver's authId and sent the list,
//     BroadcastComposer.tsx:56-74) and persists them (broadcast_recipients), so a driver's list is a join and the push
//     goes exactly to them: one outbox event broadcast.created per broadcast, which notify.fcm turns into one FCM
//     "broadcast" message per recipient device (main spec §7.6), firestore.shadow into the Firestore document the
//     installed 3.x APKs list until P7b, and the relay into broadcast.created on each recipient's user:{uid};
//   - a delete is a soft delete (voided_at, voided_by, R14): the row stays for the audit, the history and the drivers'
//     lists leave it out, and broadcast.voided makes firestore.shadow delete its Firestore document;
//   - a read receipt is a broadcast_reads row (readDriverAuthIds[] + readCount before); its primary key makes it
//     idempotent per user.
//
// Every statement runs in db.WithPrincipal, so RLS decides (Appendix C §C.3.5, 0007 and 0022): staff read the
// broadcasts of their tenant reach and the platform-wide ones and write those of their reach; platform-wide rows
// (tenant_id NULL, the all_tenants group) are written by stewards only (R60), which the service checks first (403) and
// RLS checks again; a driver reads the broadcasts of its tenant, the platform-wide ones and those addressed to it,
// never a voided one. Capabilities decide the verbs: broadcasts:view reads the history, broadcasts:send sends and
// voids, mobile:broadcasts is the driver's side.
//
// Which store (main spec §12.1): broadcasts are class B (the web writes, the 3.x APKs read), so PostgreSQL writes them
// in the request transaction whatever PG_OWNED_DOMAINS says, like the holidays of T45; firestore.shadow projects them
// once comms is PostgreSQL-owned (P5). Before that the legacy web keeps writing Firestore and the mirror loads it.
package broadcasts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/broadcasts/broadcastsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Recipient groups (broadcasts.recipient_group). The first three are the legacy composer's groups
// (BroadcastComposer.tsx:51-74: every driver, the own fleet's employees, the subcontractors), now read in the caller's
// reach: the drivers of its tenant and of the carriers working for it (contractor reach, R60). all_tenants is the
// platform-wide group (the drivers of every tenant, tenant_id NULL), stewards only. selected is a send to an explicit
// recipientUserIds list.
const (
	GroupAllDriver     = "all_driver"
	GroupOwnFleet      = "own_fleet"
	GroupSubcontractor = "subcontractor"
	GroupAllTenants    = "all_tenants"
	GroupSelected      = "selected"
)

// RecipientGroups is the vocabulary of recipientGroup in a send (selected is implied by recipientUserIds).
var RecipientGroups = []string{GroupAllDriver, GroupOwnFleet, GroupSubcontractor, GroupAllTenants}

// Paging (Appendix B §B.1.5; Appendix E §E.10: the history had a silent cap of 50).
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Bounds of a send (characters). The legacy composer had none; these are far above what a push or a phone screen shows.
const (
	MaxTitleLength   = 300
	MaxMessageLength = 5000
	// MaxSelected bounds recipientUserIds.
	MaxSelected = 5000
)

// Error codes and reasons of this package (Appendix B §B.1.5).
const (
	// ReasonBroadcastNotFound is the details.reason of the 404 of the driver's read receipt for a broadcast that does
	// not exist for the driver (no such id or document id, voided, not readable). It tells a missing broadcast from
	// every other 404 a shim can meet (a route an older API lacks, an API without FIREBASE_PROJECT_ID): the
	// markBroadcastRead shim answers "Broadcast not found" for this reason only.
	ReasonBroadcastNotFound = "broadcast_not_found"
	// CodeDriverProfileRequired: a driver-app route called by a principal without a driver profile.
	CodeDriverProfileRequired = "driver_profile_required"
	// ReasonUnsupportedUntilP7b is the field reason of recipientUserIds while installed 3.x APKs may be recipients
	// (Deps.LegacyFleet): a send to selected users waits for every domain to be PostgreSQL-owned.
	ReasonUnsupportedUntilP7b = "unsupported_until_p7b"
)

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every request runs in db.WithPrincipal.
	Pool db.Beginner
	Log  zerolog.Logger
	// Now is the clock (nil: time.Now).
	Now func() time.Time
	// LegacyFleet is whether installed 3.x APKs may still be the recipients' app: not every domain is PostgreSQL-owned
	// (P0-P7a; the api's classCPGOwned). The 3.x app lists every document of the Firestore broadcasts collection and
	// the Firestore rules let any signed-in user read it, so a send to selected users (recipientUserIds) cannot stay
	// private there (projected: every 3.x user lists it) nor reach its 3.x recipients' list (not projected: the push
	// only); such a send is refused (422 recipientUserIds unsupported_until_p7b) until P7b. The group sends are the
	// legacy composer's and stay open.
	LegacyFleet bool
}

// Service serves /v1/broadcasts* and /v1/mobile/broadcasts*.
type Service struct {
	pool        db.Beginner
	log         zerolog.Logger
	now         func() time.Time
	legacyFleet bool
}

// New builds the service. It does no I/O.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("broadcasts: the service needs a pool")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: d.Pool, log: d.Log, now: now, legacyFleet: d.LegacyFleet}, nil
}

// at is the service's clock at the precision PostgreSQL keeps.
func (s *Service) at() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

// tx runs fn in a request transaction of p: RLS decides every row it reads or writes.
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *broadcastsdb.Queries) error) error {
	return mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, broadcastsdb.New(tx)) }))
}

// --- payloads ------------------------------------------------------------------------------------------------------

// Broadcast is one broadcast as the staff history shows it. tenantId null is a platform-wide broadcast; recipientCount
// is the number of recipients at send time (persisted rows from the cut-over; the legacy count before), readCount the
// number of readers.
type Broadcast struct {
	ID              uuid.UUID  `json:"id"`
	TenantID        *uuid.UUID `json:"tenantId"`
	LegacyDocID     *string    `json:"legacyDocId"`
	CreatedByUserID *uuid.UUID `json:"createdByUserId"`
	CreatedByName   string     `json:"createdByName"`
	Title           *string    `json:"title"`
	MessageText     string     `json:"messageText"`
	RecipientGroup  string     `json:"recipientGroup"`
	RecipientCount  int32      `json:"recipientCount"`
	ReadCount       int64      `json:"readCount"`
	SentAt          time.Time  `json:"sentAt"`
	VoidedAt        *time.Time `json:"voidedAt"`
}

func broadcastOf(b broadcastsdb.Broadcast, reads int64) Broadcast {
	out := Broadcast{ID: b.ID, TenantID: b.TenantID, LegacyDocID: b.LegacyDocID, CreatedByUserID: b.CreatedByUserID,
		Title: b.Title, MessageText: b.MessageText, RecipientGroup: b.RecipientGroup, RecipientCount: b.RecipientCount,
		ReadCount: reads, SentAt: b.SentAt.UTC(), VoidedAt: b.VoidedAt}
	if b.CreatedByName != nil {
		out.CreatedByName = *b.CreatedByName
	}
	if out.VoidedAt != nil {
		t := out.VoidedAt.UTC()
		out.VoidedAt = &t
	}
	return out
}

// DriverBroadcast is one broadcast as the driver app shows it (broadcast_list_page.dart:118-130): ref is the id the app
// keeps and sends back (the Firestore document id, else the uuid), readAt the driver's own read receipt.
type DriverBroadcast struct {
	ID            uuid.UUID  `json:"id"`
	Ref           string     `json:"ref"`
	CreatedByName string     `json:"createdByName"`
	Title         *string    `json:"title"`
	MessageText   string     `json:"messageText"`
	SentAt        time.Time  `json:"sentAt"`
	Read          bool       `json:"read"`
	ReadAt        *time.Time `json:"readAt"`
}

func driverBroadcastOf(r broadcastsdb.DriverBroadcastsRow) DriverBroadcast {
	out := DriverBroadcast{ID: r.ID, Ref: r.ID.String(), Title: r.Title, MessageText: r.MessageText, SentAt: r.SentAt.UTC(),
		Read: r.ReadAt != nil}
	if r.LegacyDocID != nil && *r.LegacyDocID != "" {
		out.Ref = *r.LegacyDocID
	}
	if r.CreatedByName != nil {
		out.CreatedByName = *r.CreatedByName
	}
	if r.ReadAt != nil {
		t := r.ReadAt.UTC()
		out.ReadAt = &t
	}
	return out
}

// ReadResult is the answer of POST /v1/mobile/broadcasts/{id}/read: alreadyRead is true when the driver had read the
// broadcast before (markBroadcastRead answered {ok, alreadyRead}).
type ReadResult struct {
	BroadcastID uuid.UUID `json:"broadcastId"`
	AlreadyRead bool      `json:"alreadyRead"`
}

// --- principals ----------------------------------------------------------------------------------------------------

// staff reports a person acting with a staff role in a tenant (the RLS role of p: app_is_staff()). A platform
// principal without X-Act-On-Tenant is not: broadcast_recipients is written by staff only (0007 p_write).
func staff(p *authz.Principal) bool {
	return p != nil && !p.IsMachine() && p.EffectiveTenant() != nil && p.EffectiveRole().IsStaff()
}

// driverOf is the driver of a driver-app principal: a person signed in with a driver membership and profile; anyone
// else is 403 driver_profile_required.
func driverOf(p *authz.Principal) (uuid.UUID, error) {
	if p == nil || p.IsMachine() || p.EffectiveRole() != authz.Driver || p.DriverID == nil || p.TenantID == nil {
		return uuid.Nil, httpx.NewError(http.StatusForbidden, CodeDriverProfileRequired, "a driver profile is required")
	}
	return *p.DriverID, nil
}

// --- request helpers -----------------------------------------------------------------------------------------------

func violation(field, reason string) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason}
}

// parseLimit reads ?limit (1..max, default DefaultLimit).
func parseLimit(raw string, max int) (int, error) {
	if raw == "" {
		return min(DefaultLimit, max), nil
	}
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' || n > max {
			n = -1
			break
		}
		n = n*10 + int(ch-'0')
	}
	if n < 1 || n > max {
		return 0, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "limit", Reason: "out_of_range",
			Params: map[string]any{"min": 1, "max": max}})
	}
	return n, nil
}

// --- keyset cursors ------------------------------------------------------------------------------------------------

// cursor is the opaque keyset position (Appendix B §B.1.5), base64url JSON: (sent_at, id) of the last broadcast of a
// page.
type cursor struct {
	T  time.Time `json:"t"`
	ID uuid.UUID `json:"id"`
}

func (c cursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	var c cursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil || c.T.IsZero() {
		return cursor{}, httpx.ErrInvalidArgument(violation("cursor", "invalid"))
	}
	return c, nil
}

// --- errors --------------------------------------------------------------------------------------------------------

func errBroadcastNotFound() *httpx.Error {
	return httpx.ErrNotFound().WithDetails(map[string]any{"reason": ReasonBroadcastNotFound})
}

// errPlatformWide is the steward rule (R60): platform-wide broadcasts are sent and voided by the own fleet's staff or a
// platform admin acting as a tenant.
func errPlatformWide() *httpx.Error {
	return authz.ErrPermissionDenied("platform-wide broadcasts are sent by the own fleet or a platform admin")
}

// mapDBError turns the database refusals a request can still meet into the API's codes: unique keys (409), checks and
// foreign keys (422), RLS and the stewards-only audience (42501: 403) and a deadlock or serialization failure (503
// unavailable, details.reason contention); anything else is returned as is.
func mapDBError(err error) error {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return err
	}
	switch pe.Code {
	case "23505":
		return httpx.NewError(http.StatusConflict, "already_exists", "already exists").
			WithDetails(map[string]any{"constraint": pe.ConstraintName}).Wrap(err)
	case "23503":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "not_found")).Wrap(err)
	case "23514":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "invalid")).Wrap(err)
	case "42501":
		return authz.ErrPermissionDenied("row-level security refused the write").Wrap(err)
	case "40P01", "40001":
		return httpx.NewError(http.StatusServiceUnavailable, httpx.CodeUnavailable, "the database aborted a conflicting write").
			WithDetails(map[string]any{"reason": "contention"}).Wrap(err)
	}
	return err
}

func validGroup(g string) bool { return slices.Contains(RecipientGroups, g) }
