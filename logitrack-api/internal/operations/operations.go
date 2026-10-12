// Package operations is the data layer of the operations domain (T27, issue #47; Appendix A §A.2.3, 0004_operations):
// tasks with their delivery stops and helper, task numbers, trip_records with their stop progress, photos and renames,
// standby_records with their photos, and incident_reports. The read APIs (T30), the write-back endpoints (T31), the
// mirror (T29), the realtime events (T33), the seed (T35) and the driver app routes (P7a) are built on it.
//
// Two layers:
//
//   - the sqlc queries (queries/*.sql -> operationsdb), every statement under the caller's transaction, so RLS decides
//     (Appendix C §C.3.0): G-tenant for the four tenant tables, the driver's own rows (and, for tasks, the helper's,
//     R28), the parent's reach for the child tables; the staff lists and lookups name the reach as well (Reach,
//     Appendix C §C.3.8);
//   - the services of this package: the writes that must be one statement sequence in the caller's transaction
//     (CreateTask with its number and queue position, AddTaskStop, CheckSeal, CreateTrip, RenameTrip, PutTripPhoto,
//     CreateStandby, CreateIncident; the caller owns the transaction and appends its outbox events there, main spec
//     §2.3), and the principal reads of Service (keyset pages, lookups, the standby billing projection).
//
// Task numbers (R10): FM|LH-ddMMyyyy-NNN from next_task_seq() over task_number_counters, global per (task_type,
// plan_date) across tenants, three digits padded, allocated inside the task insert transaction (no count()+1 race);
// logitrack_app holds no privilege on the counter table (R66, R67). Legacy numbers, unpadded or duplicated, are kept
// verbatim: task_no is indexed, not unique; a canonical padded one moves the counter past its sequence
// (bump_task_seq(), 0014), so the allocator never mints it again.
//
// Trip identity (main spec §3.1 #3): surrogate id plus trip_no, UNIQUE and mutable, with the Firestore document-id
// rule of core/tripDocId.ts (tripcheck.ValidateTripNo; the same CHECK in the schema); a rename is an UPDATE plus one
// trip_no_history row, and storage objects never move. The duplicate-seal check (CheckSeal) reads every trip through
// the SECURITY DEFINER trip_seal_taken() of 0014, as the legacy check did.
//
// Trip photo types (R19): free text, merged by type (last wins); photo_type_known is a STORED generated column (the 14
// workflow types and stop_{n}_*; tripcheck.PhotoTypeKnown), so an unknown legacy type still loads and is reported,
// never refused.
package operations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Row types of the data layer: the sqlc models of 0004_operations. The HTTP layers (T30, T31, P7a) build their payloads
// from them (the staff, driver and scope DTOs differ).
type (
	Task         = operationsdb.Task
	TaskStop     = operationsdb.TaskDeliveryStop
	Trip         = operationsdb.TripRecord
	TripStop     = operationsdb.TripDeliveryStop
	TripPhoto    = operationsdb.TripPhoto
	TripRename   = operationsdb.TripNoHistory
	Standby      = operationsdb.StandbyRecord
	StandbyPhoto = operationsdb.StandbyPhoto
	Incident     = operationsdb.IncidentReport
)

// Task types (tasks.task_type, trip_records.job_type; legacy FIRST_MILE / LINE_HAUL).
const (
	FirstMile = "first_mile"
	LineHaul  = "line_haul"
)

// Task statuses (tasks.status, canonical lower_snake of 'Pending' ... 'Cancelled', D2).
const (
	TaskPending   = "pending"
	TaskAssigned  = "assigned"
	TaskCheckedIn = "checked_in"
	TaskInTransit = "in_transit"
	TaskCompleted = "completed"
	TaskCancelled = "cancelled"
)

// Trip statuses (trip_records.status).
const (
	TripInTransit = "in_transit"
	TripIncident  = "incident"
	TripDelivered = "delivered"
	TripStandby   = "standby"
	TripCancelled = "cancelled"
)

// Vocabularies of the 0004 CHECKs.
var (
	TaskTypes        = []string{FirstMile, LineHaul}
	TaskStatuses     = []string{TaskPending, TaskAssigned, TaskCheckedIn, TaskInTransit, TaskCompleted, TaskCancelled}
	TripStatuses     = []string{TripInTransit, TripIncident, TripDelivered, TripStandby, TripCancelled}
	JobCategories    = []string{"PRIMARY", "SUPPLEMENTARY"}
	VehicleClasses   = []string{"4W", "4WJ", "6WH", "10WH", "18WH", "VAN"}
	TenantSources    = []string{"task", "trip", "driver", "truck", "self", "form", "quarantine"}
	StopStatuses     = []string{"pending", "delivered", "failed"}
	DeliveredVia     = []string{"mobile", "admin_web"}
	StandbyPhotoKind = []string{"customer_worksheet", "site_photo"}
	ReportedByKinds  = []string{"driver", "admin"}
)

// Paging (Appendix B §B.1.5): keyset only, no silent cap.
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// --- reach --------------------------------------------------------------------------------------------------

// Reach is what a query names besides RLS (Appendix C §C.3.8): every tenant the transaction can read (All: a system
// transaction, or X-Act-On-Tenant: *), the staff reach (the effective tenant and its contractor reach), or a driver's
// own rows. An empty Reach reads nothing (a customer-scope principal or a dispatcher reads the scope_* views of
// internal/scope instead).
type Reach struct {
	All     bool
	Tenants []uuid.UUID
	Driver  *uuid.UUID
}

// ReachOf is the reach of a request principal: authz.Principal.TenantReach for staff (the same tenants RLS admits),
// the driver id for a driver.
func ReachOf(p *authz.Principal) Reach {
	all, tenants := p.TenantReach()
	r := Reach{All: all, Tenants: tenants}
	if !all && p.EffectiveRole() == authz.Driver && p.DriverID != nil {
		d := *p.DriverID
		r.Driver = &d
	}
	return r
}

// SystemReach is the reach of a db.WithSystem transaction (the mirror, the workers, the seed): every tenant.
func SystemReach() Reach { return Reach{All: true, Tenants: []uuid.UUID{}} }

func (r Reach) tenants() []uuid.UUID {
	if r.Tenants == nil {
		return []uuid.UUID{}
	}
	return r.Tenants
}

// --- service --------------------------------------------------------------------------------------------------

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every request runs in db.WithPrincipal.
	Pool db.Beginner
	Log  zerolog.Logger
	// Now is the clock of the writes that stamp a time (tests pin it); nil = time.Now.
	Now func() time.Time
	// Reassigner performs the R13 task reassignment in its system transaction (iam.TaskMover, T28); nil answers
	// PATCH /v1/tasks/{id} with a 500 (the route is listed but not wired).
	Reassigner Reassigner
	// Files signs the photo URLs of the read payloads (storage.Service, T30); nil leaves every URL absent.
	Files Files
	// Hubs is the hub master the place keys and labels read (hubs.Service through internal/app, T30); nil leaves every
	// stored hub as hub:<uuid> and labels the raw text. The writes (T31) resolve a raw place to its hub through it.
	Hubs Hubs
	// PGOwned is whether PG_OWNED_DOMAINS holds operations: the writes (T31) then go to PostgreSQL. Otherwise they go
	// to Firestore first through WriteBack (main spec §12.1, §13.8).
	PGOwned bool
	// WriteBack is the Firestore-first path of the writes while the domain is Firestore-owned; nil (or without a
	// Writer) refuses those writes with 503 write_back_unconfigured.
	WriteBack *WriteBack
	// Renamer performs a trip rename and its in-transaction audit row (iam.TripRenamer: security_events is written in
	// a system transaction, Appendix C §C.4.13); nil answers POST /v1/trips/{id}/rename with a 500.
	Renamer TripRenamer
	// Files commits the evidence photos a write references (storage.Service); nil refuses a photo key (not_found).
	Commits FileCommitter
	// Owners resolves the task a driver-app shim target names, of any tenant, to tell another driver's task (403
	// not_task_owner) from a missing one (404) (iam.TaskOwners: a read-only system transaction, T32); nil answers the
	// shim targets with a 500 (the route is listed but not wired).
	Owners TaskOwners
}

// Service serves the principal reads of the operations tables and opens the request transactions of their writes.
type Service struct {
	pool    db.Beginner
	log     zerolog.Logger
	now     func() time.Time
	mover   Reassigner
	files   Files
	hubs    Hubs
	pgOwned bool
	wb      *WriteBack
	renamer TripRenamer
	commits FileCommitter
	owners  TaskOwners
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("operations: the service needs a pool")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: d.Pool, log: d.Log, now: now, mover: d.Reassigner, files: d.Files, hubs: d.Hubs,
		pgOwned: d.PGOwned, wb: d.WriteBack, renamer: d.Renamer, commits: d.Commits, owners: d.Owners}, nil
}

// Now is the service clock.
func (s *Service) Now() time.Time { return s.now() }

// Tx runs fn in a request transaction of p (db.WithPrincipal: RLS decides every row) and maps the database refusals
// to the API's codes (MapError). The write functions of this package run inside it, together with the caller's
// outbox appends.
func (s *Service) Tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx) error) error {
	return MapError(db.WithPrincipal(ctx, s.pool, p, fn))
}

// read runs fn with the queries of a request transaction of p.
func (s *Service) read(ctx context.Context, p *authz.Principal, fn func(q *operationsdb.Queries) error) error {
	return s.Tx(ctx, p, func(tx pgx.Tx) error { return fn(operationsdb.New(tx)) })
}

// --- errors -------------------------------------------------------------------------------------------------

// The 409 codes of Appendix B §B.1.5 this layer produces.
const (
	CodeDuplicateTripID      = "duplicate_trip_id"
	CodeDuplicateSeal        = "duplicate_seal"
	CodeDuplicateDestination = "duplicate_destination"
)

// ErrDuplicateTripNo is the 409 of a trip number another trip holds (trip_records_trip_no).
func ErrDuplicateTripNo(tripNo string) *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeDuplicateTripID, "another trip has this trip number").
		WithDetails(map[string]any{"field": "tripNo", "tripNo": tripNo})
}

// ErrDuplicateSeal is the 409 of a seal code another trip carries (the legacy duplicate check, CheckSeal): tripNo is
// the holder's number when the caller reaches that trip, else "" and details carry no tripNo.
func ErrDuplicateSeal(tripNo string) *httpx.Error {
	details := map[string]any{"field": "sealCode"}
	if tripNo != "" {
		details["tripNo"] = tripNo
	}
	return httpx.NewError(http.StatusConflict, CodeDuplicateSeal, "another trip has this seal code").WithDetails(details)
}

// ErrDuplicateDestination is the 409 of a stop destination the task already has (taskSchema refine).
func ErrDuplicateDestination(destination string) *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeDuplicateDestination, "the task already has a stop at this destination").
		WithDetails(map[string]any{"field": "destination", "destination": destination})
}

// errPrecondition is a 409 failed_precondition with details.reason.
func errPrecondition(reason, msg string) *httpx.Error {
	return httpx.NewError(http.StatusConflict, "failed_precondition", msg).WithDetails(map[string]any{"reason": reason})
}

func violation(field, reason string) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason}
}

// checker collects the field violations of one write.
type checker struct{ bad []httpx.FieldViolation }

func (c *checker) add(field, reason string) { c.bad = append(c.bad, violation(field, reason)) }

func (c *checker) err() error {
	if len(c.bad) == 0 {
		return nil
	}
	return httpx.ErrInvalidArgument(c.bad...)
}

// notFound maps pgx.ErrNoRows to 404 not_found (a row outside the reach is not found, never forbidden).
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound().Wrap(err)
	}
	return err
}

// MapError turns the database refusals an operations write can still meet into the API's codes: the unique keys with
// a code of their own (trip numbers 409 duplicate_trip_id, stop destinations 409 duplicate_destination), any other
// unique key 409 already_exists, checks and foreign keys 422, a NUL in text 422, the tenant-consistency and frozen
// tenant triggers (23000) 409 failed_precondition (tenant_mismatch), and RLS (42501, also a parent the writer cannot
// see) 403. An *httpx.Error passes unchanged; anything else is returned as is (500).
func MapError(err error) error {
	var pe *pgconn.PgError
	if err == nil || !errors.As(err, &pe) {
		return err
	}
	var he *httpx.Error
	if errors.As(err, &he) {
		return err
	}
	switch pe.Code {
	case "23505":
		switch pe.ConstraintName {
		case "trip_records_trip_no":
			return ErrDuplicateTripNo("").Wrap(err)
		case "task_delivery_stops_task_id_destination_raw_key":
			return ErrDuplicateDestination("").Wrap(err)
		}
		return httpx.NewError(http.StatusConflict, "already_exists", "already exists").
			WithDetails(map[string]any{"field": pe.ConstraintName, "reason": "taken"}).Wrap(err)
	case "23503":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "not_found")).Wrap(err)
	case "23514":
		if pe.ConstraintName == "trip_records_trip_no_check" {
			return httpx.ErrInvalidArgument(violation("tripNo", "invalid")).Wrap(err)
		}
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "invalid")).Wrap(err)
	case "22021":
		return httpx.ErrInvalidArgument(violation("body", "invalid_encoding")).Wrap(err)
	case "23000":
		return errPrecondition("tenant_mismatch", "the row and its parent are on different tenants").Wrap(err)
	case "42501":
		return authz.ErrPermissionDenied("row-level security refused the operation").Wrap(err)
	}
	return err
}

// --- keyset cursors -------------------------------------------------------------------------------------------

// Cursor axes: the sort column of a list (Appendix B §B.1.5: opaque base64 of (sort value, id)).
const (
	axisCreated   = "c" // created_at
	axisPlan      = "p" // tasks.plan_date
	axisDelivered = "d" // trip_records.delivered_at
	axisBilling   = "b" // trip_records.billing_axis_date
	axisEnded     = "e" // standby_records.ended_at
)

// cursor is the keyset position: the axis, its value (an instant, or a yyyy-MM-dd day) and the row id.
type cursor struct {
	Axis string     `json:"a"`
	At   *time.Time `json:"t,omitempty"`
	Day  string     `json:"d,omitempty"`
	ID   uuid.UUID  `json:"id"`
}

func (c cursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor reads a cursor of axis; "" is the first page. A cursor of another list or axis is refused.
func decodeCursor(s, axis string) (*cursor, bool) {
	if s == "" {
		return nil, true
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.Axis != axis || c.ID == uuid.Nil {
		return nil, false
	}
	switch axis {
	case axisPlan, axisBilling:
		if _, err := time.Parse(time.DateOnly, c.Day); err != nil || c.At != nil {
			return nil, false
		}
	default:
		if c.At == nil || c.Day != "" {
			return nil, false
		}
	}
	return &c, true
}

func (c *cursor) at() *time.Time {
	if c == nil {
		return nil
	}
	return c.At
}

func (c *cursor) day() *time.Time {
	if c == nil {
		return nil
	}
	d, _ := time.Parse(time.DateOnly, c.Day)
	return &d
}

func (c *cursor) id() uuid.UUID {
	if c == nil {
		return uuid.Nil
	}
	return c.ID
}

// Page is one keyset page; NextCursor is "" on the last page.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// pageLimit checks a limit (0 = the default).
func pageLimit(ck *checker, limit int) int {
	if limit == 0 {
		return DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		ck.add("limit", "out_of_range")
		return DefaultLimit
	}
	return limit
}

// page cuts rows fetched with limit + 1 to limit and builds the next cursor from the last row kept.
func page[T any](rows []T, limit int, next func(T) cursor) Page[T] {
	if len(rows) <= limit {
		return Page[T]{Items: rows}
	}
	rows = rows[:limit]
	return Page[T]{Items: rows, NextCursor: next(rows[len(rows)-1]).encode()}
}

func ptr[T any](v T) *T { return &v }
