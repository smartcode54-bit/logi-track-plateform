// Package maintenance serves the maintenance records of T40 (Appendix B §B.2.15): GET/POST /v1/maintenance,
// PATCH /v1/maintenance/{id} with validated status transitions, POST /v1/maintenance/{id}/remind, and the PM check of
// the fuel audit (POST /v1/trucks/{id}/pm-check, and CheckPMInTx inside an expense approval; the port of
// checkMaintenanceAlert, fn:triggers.ts:432, which ran unauthenticated and is retired).
//
// A record and the truck columns it drives (the truck's status while it is in the shop, active_maintenance_id,
// last_alert_mileage, current_mileage, last_service_date, next_service_mileage) change in one transaction
// (maintenance.ts:110-241 wrote them in separate client calls). Every statement runs in db.WithPrincipal, so RLS
// decides (Appendix C §C.3.0): records and trucks are tenant rows of the caller's reach (own tenant plus the carriers
// working for it, R60); maintenance_files follow their record. fleet:manage_maintenance decides the verbs of
// /v1/maintenance, accounting:audit_expense the PM check route; dispatchers and customer scopes never read a record.
//
// Statuses are the canonical lower_snake vocabulary of maintenance_records (pm_booking, scheduled, in_progress,
// completed, cancelled; R65); the legacy literals ('PM Booking', 'Scheduled', 'In-Progress') are accepted on input and
// normalised (NormalizeStatus), never stored or answered. While the maintenance domain is Firestore-owned (class C,
// main spec §12.1) a configured writeback.Writer receives every staff write in the legacy shape before COMMIT.
package maintenance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance/maintenancedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// Outbox routing keys (Appendix B §B.5.2): the maintenance family on tenant:{tid}:fleet and driver:{driverId}
// (maintenance.changed, §B.4.3); maintenance.created and maintenance.reminder_requested also reach notify.fcm (FCM
// maintenance_scheduled to the truck's drivers). A truck the flow changes emits truck.updated (the fleet family, bound
// to firestore.shadow).
const (
	RouteCreated           = "maintenance.created"
	RouteUpdated           = "maintenance.updated"
	RouteReminderRequested = "maintenance.reminder_requested"
	RouteTruckUpdated      = "truck.updated"
)

// Statuses of maintenance_records (canonical lower_snake, R65).
const (
	StatusPMBooking  = "pm_booking"
	StatusScheduled  = "scheduled"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusCancelled  = "cancelled"
)

// Statuses is the vocabulary of maintenance_records.status.
var Statuses = []string{StatusPMBooking, StatusScheduled, StatusInProgress, StatusCompleted, StatusCancelled}

// Types are the maintenance types (domain codes, kept upper case): preventive and corrective.
var Types = []string{"PM", "CM"}

// PaymentMethods is the vocabulary of maintenance_records.payment_method.
var PaymentMethods = []string{"cash", "credit_card", "billing", "transfer", "insurance_claim"}

// Truck statuses the maintenance flows set and restore (trucks.status).
const (
	truckActive      = "active"
	truckMaintenance = "maintenance"
)

// open reports whether a status is not terminal: the record still holds the truck.
func open(status string) bool { return status != StatusCompleted && status != StatusCancelled }

// NormalizeStatus maps a status onto the canonical vocabulary: the lower_snake value itself, or a legacy literal
// ('PM Booking', 'Scheduled', 'In-Progress', 'in_progress'; case-insensitive, '-' and ' ' read as '_', the ETL's
// canon). ok is false outside the vocabulary.
func NormalizeStatus(s string) (string, bool) {
	v := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(s)))
	return v, slices.Contains(Statuses, v)
}

// Paging (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Files is the storage service as this package uses it (internal/storage implements it). Commit runs in the request
// transaction; FileIDsByKey takes its own pool connection, so it runs before the transaction.
type Files interface {
	Commit(ctx context.Context, tx pgx.Tx, in storage.CommitInput) (storage.Committed, error)
	FileIDsByKey(ctx context.Context, keys []string) (map[string][]uuid.UUID, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every request runs in db.WithPrincipal.
	Pool db.Beginner
	// Files commits uploaded keys; optional (without it a new key is refused, not_found).
	Files Files
	// WriteBack, when set, receives every staff write in the legacy Firestore shape before COMMIT (class C until
	// P7b); nil = PostgreSQL only.
	WriteBack writeback.Writer
	Log       zerolog.Logger
	// Now is the clock of the PM booking's start date and of status_history (tests pin it); nil = time.Now.
	Now func() time.Time
}

// Service serves /v1/maintenance* and the PM check.
type Service struct {
	pool  db.Beginner
	files Files
	wb    writeback.Writer
	log   zerolog.Logger
	now   func() time.Time
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("maintenance: the service needs a pool")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: d.Pool, files: d.Files, wb: d.WriteBack, log: d.Log, now: now}, nil
}

// tx runs fn in a request transaction of p: RLS decides every row it reads or writes.
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *maintenancedb.Queries) error) error {
	return mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, maintenancedb.New(tx)) }))
}

// today is the Bangkok calendar day (main spec §6.3) as a date value (midnight UTC, the form pgx gives a date).
func (s *Service) today() time.Time {
	d, _ := time.Parse(time.DateOnly, clock.DateString(s.now()))
	return d
}

// actor names who writes: the user (created_by, updated_by, status_history reference users) and, for a machine
// principal without a users row (an API key), a label instead.
func actor(p *authz.Principal) (user *uuid.UUID, label *string) {
	if p.IsMachine() {
		l := "api_key"
		if p.APIKeyID != nil {
			l += ":" + p.APIKeyID.String()
		}
		return nil, &l
	}
	id := p.UserID
	return &id, nil
}

// writeTenant refuses a write without an acting tenant (a customer-scope principal, a platform principal without
// X-Act-On-Tenant, or the read-only X-Act-On-Tenant: *).
func writeTenant(p *authz.Principal) error {
	if p.EffectiveTenant() == nil {
		return authz.ErrTenantRequired()
	}
	return nil
}

// --- events ------------------------------------------------------------------------------------------------

func headers(rid string) map[string]string {
	if rid == "" {
		return nil
	}
	return map[string]string{"requestId": rid}
}

// emitRecord appends maintenance.created / maintenance.updated: tenant:{tid}:fleet plus driver:{id} of the drivers the
// truck's maintenance reaches (realtime maintenance.changed), payload {entity, id, truckId, status, changed?}.
func emitRecord(ctx context.Context, tx pgx.Tx, q *maintenancedb.Queries, key string, r maintenancedb.MaintenanceRecord,
	changed []string, rid string) error {
	topics := []string{realtime.TenantTopic(r.TenantID.String(), realtime.FamilyFleet)}
	if r.TruckID != nil {
		drivers, err := q.TruckDriverIDs(ctx, *r.TruckID)
		if err != nil {
			return err
		}
		for _, d := range drivers {
			topics = append(topics, realtime.DriverTopic(d.String()))
		}
	}
	payload := map[string]any{"entity": "maintenance", "id": r.ID.String(), "status": r.Status}
	if r.TruckID != nil {
		payload["truckId"] = r.TruckID.String()
	}
	if len(changed) > 0 {
		payload["changed"] = changed
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: key, AggregateType: "maintenance", AggregateID: r.ID.String(),
		TenantID: &r.TenantID, Payload: payload, Topics: topics, Headers: headers(rid)})
	return err
}

// emitReminder appends maintenance.reminder_requested (notify.fcm: maintenance_scheduled to the truck's drivers; the
// consumer reads the recipients and the default text from the record). title and body are the staff's text, if any.
func emitReminder(ctx context.Context, tx pgx.Tx, r maintenancedb.MaintenanceRecord, title, body, rid string) (uuid.UUID, error) {
	payload := map[string]any{"entity": "maintenance", "id": r.ID.String()}
	if r.TruckID != nil {
		payload["truckId"] = r.TruckID.String()
	}
	if title != "" {
		payload["title"] = title
	}
	if body != "" {
		payload["body"] = body
	}
	a, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteReminderRequested, AggregateType: "maintenance",
		AggregateID: r.ID.String(), TenantID: &r.TenantID, Payload: payload, Headers: headers(rid)})
	return a.EventID, err
}

// emitTruck appends truck.updated for the truck columns a maintenance flow changed (topic tenant:{tid}:fleet; the
// firestore.shadow projection re-reads the truck by id).
func emitTruck(ctx context.Context, tx pgx.Tx, id, tenant uuid.UUID, changed []string, rid string) error {
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteTruckUpdated, AggregateType: "truck", AggregateID: id.String(),
		TenantID: &tenant, Payload: map[string]any{"entity": "truck", "id": id.String(), "changed": changed},
		Topics: []string{realtime.TenantTopic(tenant.String(), realtime.FamilyFleet)}, Headers: headers(rid)})
	return err
}

// --- request helpers ---------------------------------------------------------------------------------------

// Field is an optional JSON field of a PATCH body: Set when the key is present, Null when its value is null.
type Field[T any] = iam.Field[T]

func violation(field, reason string) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason}
}

// checker collects the field violations of one request.
type checker struct{ bad []httpx.FieldViolation }

func (c *checker) add(field, reason string) { c.bad = append(c.bad, violation(field, reason)) }

func (c *checker) addParams(field, reason string, params map[string]any) {
	c.bad = append(c.bad, httpx.FieldViolation{Field: field, Reason: reason, Params: params})
}

func (c *checker) err() error {
	if len(c.bad) == 0 {
		return nil
	}
	return httpx.ErrInvalidArgument(c.bad...)
}

// cleanText is the trimmed value, or a reason: invalid (not UTF-8) or too_long (more than max characters).
func cleanText(v string, max int) (string, string) {
	v = strings.TrimSpace(v)
	switch {
	case !utf8.ValidString(v):
		return "", "invalid"
	case utf8.RuneCountInString(v) > max:
		return "", "too_long"
	}
	return v, ""
}

// cents reports whether v has at most two decimals (NUMERIC(14,2)).
func cents(v float64) bool {
	c := v * 100
	return math.Abs(c-math.Round(c)) < 1e-6
}

func dateString(d *time.Time) *string {
	if d == nil {
		return nil
	}
	s := d.Format(time.DateOnly)
	return &s
}

// --- keyset cursor -----------------------------------------------------------------------------------------

// cursor is the opaque keyset position (created_at, id) of Appendix B §B.1.5, base64url JSON.
type cursor struct {
	T  time.Time `json:"t"`
	ID uuid.UUID `json:"id"`
}

func (c cursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil || c.T.IsZero() {
		return cursor{}, false
	}
	return c, true
}

// --- errors ---------------------------------------------------------------------------------------------

// errFailedPrecondition is 409 failed_precondition with details.reason (Appendix B §B.1.5).
func errFailedPrecondition(reason string, details map[string]any) *httpx.Error {
	d := map[string]any{"reason": reason}
	for k, v := range details {
		d[k] = v
	}
	return httpx.NewError(http.StatusConflict, "failed_precondition", "failed precondition").WithDetails(d)
}

// mapDBError turns the database refusals a request can still meet into the API's codes: checks and foreign keys
// (422), unique keys (409) and RLS (42501: a row outside the caller's write reach, 403); anything else is returned
// as is (500 for an unknown database error).
func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
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
	}
	return err
}

// groupThousands writes n with comma thousands separators, as JavaScript's toLocaleString() does in the en-US locale
// of Cloud Functions (fn:triggers.ts:491-493): 12345 -> "12,345", -1500 -> "-1,500".
func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
