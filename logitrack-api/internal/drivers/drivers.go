// Package drivers is the driver master of T23 (Appendix B §B.2.7): drivers with their append-only status history
// (status_history, entity 'driver'), the customer driver ids (driver_customer_codes), the profile photo and the PII
// documents (ID card, licence: file_objects, private), and the driver directory of the pickers
// (GET /v1/drivers?fields=minimal, R33).
//
// Every statement runs in db.WithPrincipal, so RLS decides (Appendix C §C.3.0): drivers are tenant rows of the
// caller's reach (own tenant plus the carriers working for it, R60); driver_customer_codes and status_history follow
// their driver. Capabilities decide the verbs first (drivers:view, drivers:create, drivers:edit, drivers:view_pii;
// dispatch:view_operations reads the scope_drivers projection), and the list and detail queries still name the reach
// (main spec §4.7, Appendix C §C.3.8: authz.Principal.TenantReach).
//
// PII (Appendix C §C.2.3 row 15: the ID card number, its expiry, the birth date and the ID card and licence
// documents) is in a payload only for a caller holding drivers:view_pii: absent otherwise, never null-masked, and the
// documents are read through GET /v1/drivers/{id}/documents/{kind} (a 5-minute presigned GET) or the payload's
// short-lived URLs. Writing a PII field needs the same capability.
//
// Accounts are not this package's: a driver's login is created by POST /v1/users {role: driver, driverId} or linked
// by PUT /v1/users/{id}/driver-link, and its password is a generated temporary one from
// POST /v1/users/{id}/password/temporary (drivers:set_password, R29), all in internal/iam (T19). This package never
// writes users, memberships or drivers.user_id, and refuses a body that tries (no unauthenticated or caller-chosen
// password path survives from createDriverAccount / updateDriverAccount, functions/src/triggers.ts:48-131, 137-145).
// The one cross-tenant write, a broker-carrier move (PATCH {tenantId}, Appendix C §C.1.4), is handed to the Mover
// (iam.Admin.MoveDriver), which moves the driver membership in the same transaction.
//
// Three truck concepts stay apart (.vibe-rules.md "Vehicle identity"): currentAssignment is the home truck (the truck
// assignments of internal/fleet write it), activeTruck the truck the driver is responsible for right now (the driver
// app's check-in), tasks.truck_id the truck of one job. This package reads the first two and writes neither.
package drivers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/drivers/driversdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Outbox routing keys of the driver aggregate (Appendix B §B.5.2, family fleet): both reach the realtime topic
// tenant:{tid}:fleet (§B.4.2) with the payload {entity: "driver", id, changed?}, and firestore.shadow binds both.
const (
	RouteDriverCreated = "driver.created"
	RouteDriverUpdated = "driver.updated"
)

// Paging (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Vocabularies of the drivers CHECKs (Appendix A §A.2.2), canonical lower_snake (R65).
var (
	Statuses        = []string{StatusActive, StatusInactive, StatusOnDuty}
	EmploymentTypes = []string{"full_time", "subcontractor", "part_time"}
	LicenseTypes    = []string{"บ.1", "บ.2", "บ.3", "บ.4", "ท.1", "ท.2", "ท.3", "ท.4"}
)

// Driver statuses (drivers.status): on_duty while the driver holds a truck assignment (internal/fleet).
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
	StatusOnDuty   = "on_duty"
)

// PIITTL is the lifetime of a PII document URL (main spec §9.5; storage.RestrictedTTL caps the purposes too).
const PIITTL = storage.RestrictedTTL

// Files is the storage service as this package uses it (internal/storage implements it). Commit runs in the request
// transaction; SignedFile and FileIDsByKey take their own pool connection, so they run before or after s.tx, never
// inside it (a request that holds one connection while it waits for a second deadlocks the pool under load).
type Files interface {
	Commit(ctx context.Context, tx pgx.Tx, in storage.CommitInput) (storage.Committed, error)
	SignedFile(ctx context.Context, fileID uuid.UUID, ttl time.Duration) (key, url string, err error)
	FileIDsByKey(ctx context.Context, keys []string) (map[string][]uuid.UUID, error)
}

// Mover moves a driver to another tenant: the broker-carrier move of Appendix C §C.1.4 (iam.Admin.MoveDriver
// implements it). It authorizes the caller (a platform admin or a steward), moves the driver row, its files and the
// linked user's driver membership under app.tenant_move in one transaction with driver_tenant_moved and outbox
// driver.updated, and reports whether anything moved.
type Mover interface {
	MoveDriver(ctx context.Context, p *authz.Principal, rid string, driverID, to uuid.UUID) (bool, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every request runs in db.WithPrincipal.
	Pool db.Beginner
	// Files commits uploaded keys and signs download URLs; optional (without it a key is refused, not_found).
	Files Files
	// Mover performs a tenant move; optional (without it PATCH {tenantId} to another tenant is 503 unavailable).
	Mover Mover
	Log   zerolog.Logger
	// Now is the clock of the status history and of the birth-date rule (tests pin it); nil = time.Now.
	Now func() time.Time
}

// Service serves /v1/drivers*.
type Service struct {
	pool  db.Beginner
	files Files
	mover Mover
	log   zerolog.Logger
	now   func() time.Time
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("drivers: the service needs a pool")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: d.Pool, files: d.Files, mover: d.Mover, log: d.Log, now: now}, nil
}

// tx runs fn in a request transaction of p: RLS decides every row it reads or writes.
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *driversdb.Queries) error) error {
	return mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, driversdb.New(tx)) }))
}

// today is the Bangkok calendar day (main spec §6.3) as a date value (midnight UTC, the form pgx gives a date).
func (s *Service) today() time.Time {
	d, _ := time.Parse(time.DateOnly, clock.DateString(s.now()))
	return d
}

// emit appends an outbox event of a driver in the change's transaction: topic tenant:{tid}:fleet.
func emit(ctx context.Context, tx pgx.Tx, routingKey string, id, tenant uuid.UUID, changed []string, rid string) error {
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	payload := map[string]any{"entity": "driver", "id": id.String()}
	if len(changed) > 0 {
		payload["changed"] = changed
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: routingKey, AggregateType: "driver", AggregateID: id.String(), TenantID: &tenant, Payload: payload,
		Topics: []string{realtime.TenantTopic(tenant.String(), realtime.FamilyFleet)}, Headers: headers,
	})
	return err
}

// actor names who writes: the user (status_history.changed_by_user_id references users) and, for a machine
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

// writeTenant is the tenant a write acts in; writes are refused without one (a customer-scope principal, a
// platform principal without X-Act-On-Tenant, or the read-only X-Act-On-Tenant: *).
func writeTenant(p *authz.Principal) (uuid.UUID, error) {
	t := p.EffectiveTenant()
	if t == nil {
		return uuid.Nil, authz.ErrTenantRequired()
	}
	return *t, nil
}

// scoped: a dispatcher or a customer-scope principal reads the scope_drivers projection (Appendix C §C.3.7, R85).
func scoped(p *authz.Principal) bool { return (p.IsCustomerScope() || p.Dispatcher) && !p.ActOnAll }

// --- request helpers ---------------------------------------------------------------------------------------

// Field is an optional JSON field of a body (the type of the T19 administration): Set when the key is present, Null
// when its value is null.
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

// cleanText is the trimmed value and the reason it is refused ("" when it is not): not valid UTF-8, a control
// character (NUL among them; PostgreSQL text cannot hold NUL), or longer than max characters.
func cleanText(v string, max int) (string, string) {
	v = strings.TrimSpace(v)
	switch {
	case !utf8.ValidString(v):
		return "", "invalid"
	case strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return "", "invalid"
	case utf8.RuneCountInString(v) > max:
		return "", "too_long"
	}
	return v, ""
}

// parseDate reads a yyyy-MM-dd calendar date (a Bangkok calendar day, Appendix B §B.1.5).
func parseDate(v string) (time.Time, bool) {
	d, err := time.Parse(time.DateOnly, strings.TrimSpace(v))
	return d, err == nil
}

// dateString is the yyyy-MM-dd of a date column.
func dateString(d *time.Time) *string {
	if d == nil {
		return nil
	}
	s := d.Format(time.DateOnly)
	return &s
}

// emailRx is the shape of an e-mail address (the form's z.string().email(), loosely).
var emailRx = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func eq[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func eqTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func ptr[T any](v T) *T { return &v }

// --- keyset cursors ---------------------------------------------------------------------------------------

// cursor is the opaque keyset position (sort value, id) of Appendix B §B.1.5, base64url JSON: T for the staff list
// (created_at), S for the scope projection (display name).
type cursor struct {
	T  *time.Time `json:"t,omitempty"`
	S  *string    `json:"s,omitempty"`
	ID uuid.UUID  `json:"id"`
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
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil || (c.T == nil) == (c.S == nil) {
		return cursor{}, false
	}
	return c, true
}

// pageLimit checks ?limit (0 = the default).
func pageLimit(ck *checker, limit int) int {
	if limit == 0 {
		return DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		ck.addParams("limit", "out_of_range", map[string]any{"min": 1, "max": MaxLimit})
	}
	return limit
}

// --- errors ---------------------------------------------------------------------------------------------

// mapDBError turns the database refusals a request can still meet into the API's codes: unique keys (409), checks
// and foreign keys (422), a NUL in text (422) and RLS (42501: a row outside the caller's write reach, 403); anything
// else is returned as is (500 for an unknown database error).
func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "23505":
		return httpx.NewError(http.StatusConflict, "already_exists", "already exists").
			WithDetails(map[string]any{"field": pe.ConstraintName, "reason": "taken"}).Wrap(err)
	case "23503":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "not_found")).Wrap(err)
	case "23514":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "invalid")).Wrap(err)
	case "22021":
		return httpx.ErrInvalidArgument(violation("body", "invalid_encoding")).Wrap(err)
	case "42501":
		return authz.ErrPermissionDenied("row-level security refused the write").Wrap(err)
	}
	return err
}

// statusOK reports whether s is a driver status.
func statusOK(s string) bool { return slices.Contains(Statuses, s) }
