// Package fleet is the truck master of T22 (Appendix B §B.2.8): trucks with their append-only status history
// (status_history, entity 'truck'), files (truck_files -> file_objects), tax and insurance renewals with their ledger
// rows (transactions), the renewal overview and the spreadsheet import, and the truck assignments of T23 (the
// driver's home truck, truck_assignments with drivers.current_assignment_id, in one transaction). The PM check (P3)
// and vehicle locations (P6) join this package later.
//
// Every statement runs in db.WithPrincipal, so RLS decides (Appendix C §C.3.0): trucks and transactions are tenant
// rows of the caller's reach (own tenant plus the carriers working for it, R60); truck_files and status_history follow
// their truck. Capabilities decide the verbs first (fleet:view_trucks, fleet:create_truck, fleet:edit_truck,
// fleet:view_renewals, fleet:manage_renewals, fleet:view_assignments, fleet:manage_assignments;
// dispatch:view_operations reads the scope_trucks projection), and the list queries still name the reach (main spec
// §4.7, Appendix C §C.3.8: authz.Principal.TenantReach).
//
// The vehicle class of a truck is derived once, from its master type, by the one mapper (internal/fleet/
// vehicleclass): unknown is NULL, never a guess (R15). A plate is a display string, unique per tenant
// (trucks_plate_per_tenant, D4); identity is trucks.id. Files go through internal/storage: a key from
// POST /v1/uploads/presign is committed in the truck transaction and the payload carries a short-lived presigned URL,
// signed after the transaction closes; a key the truck already links stays linked, whoever uploaded it.
package fleet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/fleetdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Outbox routing keys of the fleet family (Appendix B §B.5.2); every one reaches the realtime topic
// tenant:{tid}:fleet (§B.4.2) with the payload {entity, id, changed?} (§B.4.3).
const (
	RouteTruckCreated = "truck.created"
	RouteTruckUpdated = "truck.updated"
	RouteTruckRenewed = "truck.renewed"
)

// Stable error codes besides the shared ones (Appendix B §B.1.5).
const CodeAlreadyExists = "already_exists"

// Paging (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Files is the storage service as this package uses it (internal/storage implements it). Commit runs in the request
// transaction; SignedFile and FileIDsByKey take their own pool connection, so they run before or after s.tx, never
// inside it (a request that holds one connection while it waits for a second deadlocks the pool under load).
type Files interface {
	Commit(ctx context.Context, tx pgx.Tx, in storage.CommitInput) (storage.Committed, error)
	SignedFile(ctx context.Context, fileID uuid.UUID, ttl time.Duration) (key, url string, err error)
	FileIDsByKey(ctx context.Context, keys []string) (map[string][]uuid.UUID, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every request runs in db.WithPrincipal.
	Pool db.Beginner
	// Files commits uploaded keys and signs download URLs; optional (without it a key is refused, not_found).
	Files Files
	Log   zerolog.Logger
	// Now is the clock of the renewal overview and of a renewal's default ledger date (tests pin it); nil = time.Now.
	Now func() time.Time
}

// Service serves /v1/trucks*.
type Service struct {
	pool  db.Beginner
	files Files
	log   zerolog.Logger
	now   func() time.Time
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("fleet: the service needs a pool")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: d.Pool, files: d.Files, log: d.Log, now: now}, nil
}

// tx runs fn in a request transaction of p: RLS decides every row it reads or writes.
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *fleetdb.Queries) error) error {
	return mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, fleetdb.New(tx)) }))
}

// today is the Bangkok calendar day (main spec §6.3) as a date value (midnight UTC, the form pgx gives a date).
func (s *Service) today() time.Time {
	d, _ := time.Parse(time.DateOnly, clock.DateString(s.now()))
	return d
}

// emit appends an outbox event of a truck in the change's transaction: topic tenant:{tid}:fleet.
func emit(ctx context.Context, tx pgx.Tx, routingKey string, id, tenant uuid.UUID, changed []string, rid string) error {
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	payload := map[string]any{"entity": "truck", "id": id.String()}
	if len(changed) > 0 {
		payload["changed"] = changed
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: routingKey, AggregateType: "truck", AggregateID: id.String(), TenantID: &tenant, Payload: payload,
		Topics: []string{realtime.TenantTopic(tenant.String(), realtime.FamilyFleet)}, Headers: headers,
	})
	return err
}

// actor names who writes: the user (created_by, changed_by_user_id, performed_by_user_id reference users) and, for a
// machine principal without a users row (an API key), a label instead.
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

// --- request helpers ---------------------------------------------------------------------------------------

// Field is an optional JSON field of a PATCH body (the type of the T19 administration): Set when the key is present,
// Null when its value is null.
type Field[T any] = iam.Field[T]

func violation(field, reason string) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason}
}

// checker collects the field violations of one request (or one import row).
type checker struct{ bad []httpx.FieldViolation }

func (c *checker) add(field, reason string) { c.bad = append(c.bad, violation(field, reason)) }

func (c *checker) addParams(field, reason string, params map[string]any) {
	c.bad = append(c.bad, httpx.FieldViolation{Field: field, Reason: reason, Params: params})
}

func (c *checker) has(field string) bool {
	return slices.ContainsFunc(c.bad, func(v httpx.FieldViolation) bool { return v.Field == field })
}

func (c *checker) err() error {
	if len(c.bad) == 0 {
		return nil
	}
	return httpx.ErrInvalidArgument(c.bad...)
}

// cleanText is the trimmed value: ok false when it is not valid UTF-8 or longer than max characters.
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

// cents reports whether v has at most two decimals (NUMERIC(…,2)).
func cents(v float64) bool {
	c := v * 100
	return math.Abs(c-math.Round(c)) < 1e-6
}

// --- plates ----------------------------------------------------------------------------------------------

// plateRx is the Thai plate rule of the truck form (web validate/truckSchema.ts licensePlate): two Thai consonants, or
// a digit and two consonants, an optional dash, then 1-4 digits ("กก-1234", "1กก1234"); the form also requires at
// least six characters. A plate is a display string: it is trimmed, never otherwise normalised (lib/truckPlate.ts).
var plateRx = regexp.MustCompile(`^([ก-ฮ]{2}|[0-9][ก-ฮ]{2})-?[0-9]{1,4}$`)

// MinPlateLength is the form's minimum plate length in characters.
const MinPlateLength = 6

// ValidPlate reports whether a trimmed plate passes the truck form's rule.
func ValidPlate(plate string) bool {
	return utf8.RuneCountInString(plate) >= MinPlateLength && plateRx.MatchString(plate)
}

// --- keyset cursors ---------------------------------------------------------------------------------------

// cursor is the opaque keyset position (sort value, id) of Appendix B §B.1.5, base64url JSON: T for a time sort
// (trucks by created_at), S for a text sort (the scope projection by plate), D for a date sort (renewals by due date).
type cursor struct {
	T  *time.Time `json:"t,omitempty"`
	S  *string    `json:"s,omitempty"`
	D  *string    `json:"d,omitempty"`
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
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil {
		return cursor{}, false
	}
	n := 0
	for _, set := range []bool{c.T != nil, c.S != nil, c.D != nil} {
		if set {
			n++
		}
	}
	return c, n == 1
}

// pageLimit checks ?limit (0 = the default).
func pageLimit(ck *checker, limit int) int {
	if limit == 0 {
		return DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		ck.add("limit", "out_of_range")
	}
	return limit
}

// --- errors ---------------------------------------------------------------------------------------------

// uniqueFields maps the unique constraints a write can meet to the request field and reason.
var uniqueFields = map[string][2]string{
	"trucks_plate_per_tenant": {"licensePlate", "taken"},
	"trucks_gps":              {"gpsVehicleId", "taken"},
	"truck_files_pkey":        {"files", "duplicate"},
}

// errAlreadyExists is 409 already_exists naming the field (Appendix B §B.1.5).
func errAlreadyExists(field, reason string) *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeAlreadyExists, "already exists").
		WithDetails(map[string]any{"field": field, "reason": reason})
}

// mapDBError turns the database refusals a request can still meet into the API's codes: unique keys (409), checks
// and foreign keys (422), and RLS (42501: a row outside the caller's write reach, 403); anything else is returned as
// is (500 for an unknown database error).
func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "23505":
		f, ok := uniqueFields[pe.ConstraintName]
		if !ok {
			f = [2]string{"", "taken"}
		}
		return errAlreadyExists(f[0], f[1]).Wrap(err)
	case "23503":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "not_found")).Wrap(err)
	case "23514":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "invalid")).Wrap(err)
	case "42501":
		return authz.ErrPermissionDenied("row-level security refused the write").Wrap(err)
	}
	return err
}
