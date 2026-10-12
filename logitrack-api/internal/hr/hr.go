// Package hr serves driver compensation and payroll (T44, Appendix B §B.2.17, main spec §6.15), leave requests and
// holidays (T45: leave.go, holidays.go): the effective-dated
// compensation config versions (GET/POST /v1/payroll/config), driver penalties (GET/POST /v1/penalties, POST
// /v1/penalties/{id}/cancel), the payroll runs (GET /v1/payroll, GET /v1/payroll/{id}, POST /v1/payroll/{id}/approve,
// POST /v1/payroll/{id}/status) and the payroll.run job (POST /v1/jobs/payroll.run and the worker's consumer), which
// ports generateDriverPayoutRun onto the pure engine internal/hr/compute.
//
// Every request runs in db.WithPrincipal, so RLS decides (Appendix C §C.3.0): configs, penalties and runs are tenant
// rows of the caller's reach (own tenant plus the carriers working for it, R60); dispatchers and customer scopes never
// read one. Every request statement also runs under cache.MoneyPath: payroll never reads Redis (R17). The consumer has
// no principal and runs in db.WithSystem with the job's tenant.
//
// Runs are keyed by drivers.id (payroll_runs UNIQUE (driver_id, pay_period, pay_round)): the legacy own-task query
// matched the auth uid (fn:driverCompensation.ts:199-204, §6.18 #21). A run is written while it is draft only; its line
// items are frozen once it leaves draft (trg_payroll_lines_frozen). Approval commits the penalty installments, the
// payout ledger row (dated on the Bangkok calendar, §6.18 #11) and the run's status in one transaction. Statuses are
// the canonical lower_snake vocabulary (R65); the legacy upper-case literals are accepted on input. Money is whole baht
// (payroll and penalties, Appendix B §B.1.5).
package hr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/hrdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Outbox routing keys of the payroll family (Appendix B §B.5.2), on the realtime topic tenant:{tid}:hr (§B.4.2,
// hr:view_payroll | hr:view_leave), payload {entity, id, period, round, status, ...}.
const (
	RouteApproved      = "payroll.approved"
	RouteStatusChanged = "payroll.status_changed"
)

// Run statuses (payroll_runs.status, R65).
const (
	StatusDraft           = "draft"
	StatusPendingApproval = "pending_approval"
	StatusApproved        = "approved"
	StatusPaid            = "paid"
	StatusCancelled       = "cancelled"
)

// RunStatuses is the vocabulary of payroll_runs.status.
var RunStatuses = []string{StatusDraft, StatusPendingApproval, StatusApproved, StatusPaid, StatusCancelled}

// Penalty statuses (driver_penalties.status).
const (
	PenaltyPending           = "pending"
	PenaltyPartiallyDeducted = "partially_deducted"
	PenaltyCleared           = "cleared"
	PenaltyCancelled         = "cancelled"
)

// PenaltyStatuses is the vocabulary of driver_penalties.status.
var PenaltyStatuses = []string{PenaltyPending, PenaltyPartiallyDeducted, PenaltyCleared, PenaltyCancelled}

// CodeFailedPrecondition is the 409 of a state that refuses the change (Appendix B §B.1.5); details.reason names it.
const CodeFailedPrecondition = "failed_precondition"

// Paging (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Files is the storage service as this package uses it (internal/storage implements it): Commit runs in the request
// transaction; SignedFiles signs the leave evidence after it (on its own connection).
type Files interface {
	Commit(ctx context.Context, tx pgx.Tx, in storage.CommitInput) (storage.Committed, error)
	SignedFiles(ctx context.Context, ids []uuid.UUID, ttl time.Duration) (map[uuid.UUID]storage.Signed, error)
}

// Submitter submits a job under its lock (internal/jobs.Service).
type Submitter interface {
	Submit(ctx context.Context, in jobs.SubmitInput) (jobs.Job, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every request runs in db.WithPrincipal.
	Pool db.Beginner
	// Jobs submits payroll.run; nil answers the route 503 (no Redis lock, no run).
	Jobs Submitter
	// Files commits penalty evidence and signs leave evidence; optional (without it an evidence key is refused and the
	// leave evidence has no URL).
	Files Files
	// LeavePGOwned is whether PostgreSQL writes leave_requests: every domain is PostgreSQL-owned (PG_OWNED_DOMAINS=all:
	// P7b, when the 3.x APKs and the FS->PG mirror are gone, and a deployment without Firestore such as the staging
	// VM). Otherwise the installed 3.x APKs still write them in Firestore (class C) and a staff decision is written back
	// (LeaveWriteBack), whatever hr's own flag says (hr is PostgreSQL-owned from P4 for payroll and the holiday
	// projection).
	LeavePGOwned bool
	// LeaveWriteBack is the Firestore-first path of the leave decisions while Firestore writes leave_requests; nil
	// answers them 503 unavailable (write_back_unconfigured): PostgreSQL is never written in Firestore's place.
	LeaveWriteBack *LeaveWriteBack
	Log            zerolog.Logger
}

// Service serves /v1/payroll*, /v1/penalties*, POST /v1/jobs/payroll.run, /v1/leave-requests* and /v1/holidays*.
type Service struct {
	pool    db.Beginner
	jobs    Submitter
	files   Files
	leavePG bool
	leaveWB *LeaveWriteBack
	log     zerolog.Logger
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("hr: the service needs a pool")
	}
	return &Service{pool: d.Pool, jobs: d.Jobs, files: d.Files, leavePG: d.LeavePGOwned, leaveWB: d.LeaveWriteBack, log: d.Log}, nil
}

// tx runs fn in a request transaction of p under the money-path mark: RLS decides every row, and no statement of
// payroll reads Redis (R17).
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *hrdb.Queries) error) error {
	return mapDBError(db.WithPrincipal(cache.MoneyPath(ctx), s.pool, p, func(tx pgx.Tx) error { return fn(tx, hrdb.New(tx)) }))
}

// actor names who writes: the user (the *_by columns reference users) and, for a machine principal, nobody.
func actor(p *authz.Principal) *uuid.UUID {
	if p.IsMachine() {
		return nil
	}
	id := p.UserID
	return &id
}

// writeTenant refuses a write without an acting tenant (a customer-scope principal, a platform principal without
// X-Act-On-Tenant, or the read-only X-Act-On-Tenant: *).
func writeTenant(p *authz.Principal) (uuid.UUID, error) {
	t := p.EffectiveTenant()
	if t == nil {
		return uuid.Nil, authz.ErrTenantRequired()
	}
	return *t, nil
}

// emit appends a payroll event in the change's transaction: topic tenant:{tid}:hr.
func emit(ctx context.Context, tx pgx.Tx, key string, tenant uuid.UUID, aggregate string, payload map[string]any, rid string) error {
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: key, AggregateType: "payroll", AggregateID: aggregate,
		TenantID: &tenant, Payload: payload, Headers: headers, Topics: []string{realtime.TenantTopic(tenant.String(), realtime.FamilyHR)}})
	return err
}

// --- request helpers ---------------------------------------------------------------------------------------

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

// text is a trimmed optional text of at most max characters ("" = absent).
func (c *checker) text(field string, v *string, max int) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	switch {
	case !utf8.ValidString(t):
		c.add(field, "invalid")
		return nil
	case utf8.RuneCountInString(t) > max:
		c.addParams(field, "too_long", map[string]any{"max": max})
		return nil
	case t == "":
		return nil
	}
	return &t
}

// MaxWholeTHB bounds a payroll or penalty amount: NUMERIC(14,2) holds less than 10^12.
const MaxWholeTHB = 1e11

// wholeTHB checks a whole-baht amount in [min, MaxWholeTHB].
func (c *checker) wholeTHB(field string, v float64, min float64) {
	if v != float64(int64(v)) || v < min || v > MaxWholeTHB {
		c.addParams(field, "invalid", map[string]any{"min": min, "wholeBaht": true})
	}
}

// parseInstant reads an instant: RFC 3339, or a Bangkok calendar day yyyy-MM-dd (Bangkok midnight).
func parseInstant(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, true
	}
	if t, ok := clock.MidnightFromDateString(v); ok && clock.DateString(t) == v {
		return t, true
	}
	return time.Time{}, false
}

// normalize maps a status onto the canonical vocabulary (the legacy upper-case literals included).
func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// uuidParam parses an optional uuid query parameter.
func (c *checker) uuidParam(field, v string) *uuid.UUID {
	if v == "" {
		return nil
	}
	u, err := uuid.Parse(v)
	if err != nil {
		c.add(field, "invalid")
		return nil
	}
	return &u
}

// limit reads ?limit= (default DefaultLimit, at most MaxLimit).
func (c *checker) limit(v int) int {
	switch {
	case v == 0:
		return DefaultLimit
	case v < 1 || v > MaxLimit:
		c.addParams("limit", "out_of_range", map[string]any{"min": 1, "max": MaxLimit})
	}
	return v
}

// --- keyset cursor -----------------------------------------------------------------------------------------

// cursor is the opaque keyset position (sort instant, id) of Appendix B §B.1.5, base64url JSON.
type cursor struct {
	T  time.Time `json:"t"`
	ID uuid.UUID `json:"id"`
}

func (c cursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (c *checker) cursor(s string) *cursor {
	if s == "" {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	var cur cursor
	if err != nil || json.Unmarshal(b, &cur) != nil || cur.ID == uuid.Nil || cur.T.IsZero() {
		c.add("cursor", "invalid")
		return nil
	}
	return &cur
}

// --- errors ---------------------------------------------------------------------------------------------

// errState is 409 failed_precondition: the row's state refuses the change (details.reason, details.status).
func errState(reason, status string, extra map[string]any) *httpx.Error {
	d := map[string]any{"reason": reason, "status": status}
	for k, v := range extra {
		d[k] = v
	}
	return httpx.NewError(http.StatusConflict, CodeFailedPrecondition, "the record's state does not allow this change").WithDetails(d)
}

// mapDBError turns the database refusals a request can still meet into the API's codes: unique keys (409), checks and
// foreign keys (422), RLS (42501: a row outside the caller's write reach, 403), the frozen line items of a run that
// left draft (23000, 409 failed_precondition) and a deadlock or serialization failure (40P01, 40001: rolled back, 503
// unavailable with details.reason contention, so the client may retry); anything else is returned as is.
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
	case "23000":
		return errState("run_not_draft", "", nil).Wrap(err)
	case "42501":
		return authz.ErrPermissionDenied("row-level security refused the write").Wrap(err)
	case "40P01", "40001":
		return httpx.NewError(http.StatusServiceUnavailable, httpx.CodeUnavailable, "the database aborted a conflicting write").
			WithDetails(map[string]any{"reason": "contention"}).Wrap(err)
	}
	return err
}

// utc returns a pointer to t in UTC (nil stays nil).
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// nameOrNil is a display name, nil when empty.
func nameOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
