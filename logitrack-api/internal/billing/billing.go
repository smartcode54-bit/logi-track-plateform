// Package billing is the data layer and API of the billing domain (T37, main spec §6.11-§6.14, Appendix B §B.2.14):
// the rate tables (/v1/billing/rate-entries, fuel-adjustments, service-fees, standby-rates), the billing statements with
// their lines and gap-free invoice numbers (/v1/billing/statements*), the period-lock read every pricing transaction
// makes (PeriodLock), the pricing transaction's rate-table read (PricingTables), and the Bangchak fuel snapshots
// (/v1/fuel/*, POST /v1/jobs/bangchak.snapshot and the bangchak.snapshot consumer of the worker, R70). The pure engine
// is internal/billing/compute (T36); compute-on-delivery and the backfills are T38, the read aggregates and documents
// T39.
//
// Billing tenant (R61): rate rows, statements and counters carry the billing carrier's tenant, the rate-card owner (the
// own fleet today), never the tenant that ran a trip. Writes stamp the request's effective tenant (tenant_source
// 'form'); RLS G-tenant decides which rows a request reads or writes (staff of the tenant reach), and the reads name the
// reach (main spec §4.7, authz.Principal.TenantReach).
//
// Transactions (Appendix C §C.3.2): requests run in db.WithPrincipal. Three paths run in db.WithSystem, which puts this
// package on the analyzer's allow-list: the load of the rate-card bundle cache:ratecard:{partyId} (READ ONLY, so every
// reader caches the same value; each response then keeps the caller's reach), the bangchak.snapshot consumer (no request
// principal; jobs and the RLS-exempt fuel tables), and, from T38, pricing (main spec §6.1).
//
// Money (R17, R53): pricing never reads Redis. The rate-table cache serves the list endpoints only, and the period-lock
// hint cache:period_locks:{partyId} is never consulted by a write; PricingTables and PeriodLock read PostgreSQL in the
// caller's transaction, FOR SHARE.
package billing

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/bangchak"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// Outbox routing keys of the domain (Appendix B §B.5.2) and their aggregate types.
const (
	RouteRateCardChanged        = "ratecard.changed"
	RouteStatementCreated       = "statement.created"
	RouteStatementStatusChanged = "statement.status_changed"
)

// Tables named by ratecard.changed (Appendix B §B.4.3: the web invalidates only the matching rateCard key).
const (
	TableEntries     = "entries"
	TableFuelAdj     = "fuelAdj"
	TableServiceFees = "serviceFees"
	TableStandby     = "standby"
)

// Stable error codes of the domain (Appendix B §B.1.5).
const (
	CodeAnnouncementImmutable = "announcement_immutable"
	CodeStatementNotDraft     = "statement_not_draft"
	CodeFailedPrecondition    = "failed_precondition"
	CodeAlreadyExists         = "already_exists"
	CodeBillingPeriodLocked   = "billing_period_locked"
)

// Paging (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// MaxImportRows bounds one rate-card import (the 4 MiB body holds a few thousand rows).
const MaxImportRows = 5000

// Invalidator drops the cache: keys an outbox event makes stale (cache.Cache implements it); the relay does the same for
// every relayed event, so a failure here only delays the drop (Appendix B §B.6.1).
type Invalidator interface {
	OnEvent(ctx context.Context, ev cache.Event) error
}

// Jobs submits POST /v1/jobs/{type} work (jobs.Service implements it).
type Jobs interface {
	Submit(ctx context.Context, in jobs.SubmitInput) (jobs.Job, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: requests run in db.WithPrincipal.
	Pool db.Beginner
	// Cache serves the rate-card bundle and the Bangchak retail prices (UI only) and takes the post-commit
	// invalidations; optional (without it every list reads PostgreSQL and the retail prices are fetched each time).
	Cache *cache.Cache
	// Jobs submits bangchak.snapshot; optional (without it the route answers 503).
	Jobs Jobs
	// Bangchak serves GET /v1/fuel/retail; nil when BANGCHAK_API_BASE_URL is unset (the route answers 503).
	Bangchak bangchak.Client
	// Now is the clock of generated_at, imports and voids (default time.Now).
	Now func() time.Time
	Log zerolog.Logger
}

// Service serves the billing routes of T37.
type Service struct {
	pool     db.Beginner
	cache    *cache.Cache
	jobs     Jobs
	bangchak bangchak.Client
	now      func() time.Time
	log      zerolog.Logger
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("billing: the service needs a pool")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{pool: d.Pool, cache: d.Cache, jobs: d.Jobs, bangchak: d.Bangchak, now: d.Now, log: d.Log}, nil
}

// tx runs fn in a request transaction of p: RLS decides every row it reads or writes.
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *billingdb.Queries) error) error {
	return db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, billingdb.New(tx)) })
}

// readShared loads one party's rate tables for the shared bundle: the system context in a READ ONLY transaction, so
// the value is the same whoever asks (cache.GetJSON's contract) and the load can write nothing. Callers authorise and
// keep the caller's reach.
func readShared(ctx context.Context, pool db.Beginner, fn func(q *billingdb.Queries) error) error {
	return db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return err
		}
		return fn(billingdb.New(tx))
	})
}

// tenantOf is the billing tenant of a write: the request's effective tenant (authz.RequireTenant guards the routes).
func tenantOf(p *authz.Principal) (uuid.UUID, error) {
	t := p.EffectiveTenant()
	if t == nil {
		return uuid.Nil, authz.ErrTenantRequired()
	}
	return *t, nil
}

// actor is the users row of the caller; nil for a machine principal (an API key's id is no users row).
func actor(p *authz.Principal) *uuid.UUID {
	if p.IsMachine() || p.UserID == uuid.Nil {
		return nil
	}
	id := p.UserID
	return &id
}

// inReach reports whether a row of tenant t is in the caller's reach (the set RLS G-tenant uses).
func inReach(all bool, tenants []uuid.UUID, t uuid.UUID) bool {
	if all {
		return true
	}
	for _, x := range tenants {
		if x == t {
			return true
		}
	}
	return false
}

// --- events --------------------------------------------------------------------------------------------

// emit appends an outbox event in the change's transaction on the billing tenant's topic (tenant:{tid}:billing).
func emit(ctx context.Context, tx pgx.Tx, routingKey, aggType, aggID string, tenant uuid.UUID, payload map[string]any, rid string) error {
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	t := tenant
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: routingKey, AggregateType: aggType, AggregateID: aggID, TenantID: &t, Payload: payload,
		Topics: []string{realtime.TenantTopic(tenant.String(), realtime.FamilyBilling)}, Headers: headers,
	})
	return err
}

// rateCardChanged appends ratecard.changed {billingPartyId, customerId, table} (Appendix B §B.4.3).
func rateCardChanged(ctx context.Context, tx pgx.Tx, tenant, party uuid.UUID, table, rid string) error {
	return emit(ctx, tx, RouteRateCardChanged, "ratecard", party.String(), tenant,
		map[string]any{"billingPartyId": party.String(), "customerId": party.String(), "table": table}, rid)
}

// invalidate drops what a committed event made stale; the relay repeats it, so a Redis failure is only logged.
func (s *Service) invalidate(ctx context.Context, routingKey string, party uuid.UUID) {
	if s.cache == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"billingPartyId": party.String()})
	if err := s.cache.OnEvent(ctx, cache.Event{RoutingKey: routingKey, AggregateID: party.String(), Payload: payload}); err != nil {
		s.log.Warn().Err(err).Str("event", routingKey).Msg("cache invalidation failed; the outbox relay retries it")
	}
}

// --- request helpers -----------------------------------------------------------------------------------

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

// text checks a free-text field: at most max characters, valid UTF-8; required when asked.
func (c *checker) text(field, v string, max int, required bool) string {
	v = strings.TrimSpace(v)
	switch n := utf8.RuneCountInString(v); {
	case !utf8.ValidString(v):
		c.add(field, "invalid")
	case n == 0 && required:
		c.add(field, "required")
	case n > max:
		c.addParams(field, "too_long", map[string]any{"max": max})
	}
	return v
}

// party checks a billing party reference (customerId: a billing_parties id, or the customer or carrier it bills).
func (c *checker) party(field, v string) uuid.UUID {
	v = strings.TrimSpace(v)
	if v == "" {
		c.add(field, "required")
		return uuid.Nil
	}
	id, err := uuid.Parse(v)
	if err != nil || id == uuid.Nil {
		c.add(field, "invalid")
		return uuid.Nil
	}
	return id
}

// date checks a yyyy-MM-dd Bangkok calendar day; a day past the month's end is refused (no V8 roll-over on input).
func (c *checker) date(field, v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		c.add(field, "required")
		return time.Time{}
	}
	d, err := time.Parse(time.DateOnly, v)
	if err != nil || d.Year() < 2000 || d.Year() > 2999 {
		c.add(field, "invalid")
		return time.Time{}
	}
	return d // UTC midnight: the calendar day pgx writes into a date column
}

// money checks a THB amount: finite, at least min, at most two decimals and inside NUMERIC(14,2).
func (c *checker) money(field string, v *float64, min float64, required bool) (float64, bool) {
	if v == nil {
		if required {
			c.add(field, "required")
		}
		return 0, false
	}
	x := *v
	switch {
	case math.IsNaN(x) || math.IsInf(x, 0):
		c.add(field, "invalid")
	case x < min:
		c.addParams(field, "out_of_range", map[string]any{"min": min})
	case math.Abs(x) >= 1e12:
		c.addParams(field, "out_of_range", map[string]any{"max": 1e12})
	case !decimals(x, 2):
		c.addParams(field, "precision", map[string]any{"decimals": 2})
	default:
		return x, true
	}
	return 0, false
}

// decimals reports whether x has at most n decimals (as typed: the float nearest a decimal with n places).
func decimals(x float64, n int) bool {
	p := math.Pow(10, float64(n))
	y := x * p
	return math.Abs(y-math.Round(y)) < 1e-6*math.Max(1, math.Abs(y)/1e9)
}

// decimalText writes a checked amount as exact decimal text for a NUMERIC column (no float rounding on the way in).
func decimalText(x float64, n int) string {
	return strconv.FormatFloat(x, 'f', n, 64)
}

// limitOf validates ?limit (0 = absent).
func (c *checker) limitOf(n, def int) int {
	if n == 0 {
		return def
	}
	if n < 1 || n > MaxLimit {
		c.add("limit", "out_of_range")
	}
	return n
}

// --- keyset cursors ------------------------------------------------------------------------------------

// cursor is the opaque keyset position of Appendix B §B.1.5, base64url JSON: T a time sort value, S a text tie-break,
// D a date or month key, and the row id.
type cursor struct {
	T  *time.Time `json:"t,omitempty"`
	S  *string    `json:"s,omitempty"`
	D  string     `json:"d,omitempty"`
	ID uuid.UUID  `json:"id,omitempty"`
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
	if json.Unmarshal(b, &c) != nil {
		return cursor{}, false
	}
	return c, true
}

// --- errors ---------------------------------------------------------------------------------------------

// errAnnouncement is the refusal of any change to an announcement row other than its void (main spec §6.12).
func errAnnouncement(reason string) *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeAnnouncementImmutable, "announcement rows are immutable; void it or add a new row").
		WithDetails(map[string]any{"reason": reason})
}

func errPrecondition(reason, msg string) *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeFailedPrecondition, msg).WithDetails(map[string]any{"reason": reason})
}

// uniqueFields maps the unique constraints a write can meet to the request field.
var uniqueFields = map[string]string{
	"customer_service_fees_one_per_type": "feeType",
	"billing_statements_invoice_no":      "invoiceNumber",
}

// mapDBError turns the database refusals a request can still meet into the API's codes: unique keys (409), checks and
// foreign keys (422), the immutability triggers (23000, 409 announcement_immutable) and RLS or a revoked privilege
// (42501, 403); anything else is returned as is (500 for an unknown database error).
func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "23505":
		return httpx.NewError(http.StatusConflict, CodeAlreadyExists, "already exists").
			WithDetails(map[string]any{"field": uniqueFields[pe.ConstraintName], "reason": "taken"}).Wrap(err)
	case "23503":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "not_found")).Wrap(err)
	case "23514":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "invalid")).Wrap(err)
	case "23000":
		return errAnnouncement("immutable").Wrap(err)
	case "42501":
		return authz.ErrPermissionDenied("row-level security refused the write").Wrap(err)
	}
	return err
}

// bangkokMonth is [first 00:00, next first 00:00) of a Bangkok month (main spec §6.3).
func bangkokMonth(year, month int) (from, to time.Time) {
	from = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, clock.Bangkok)
	return from, from.AddDate(0, 1, 0)
}

// Field is an optional JSON field of a PATCH body: Set when the key is present, Null when its value is null (the type
// of the T19 administration, iam.Field).
type Field[T any] struct {
	Set  bool
	Null bool
	V    T
}

// UnmarshalJSON implements json.Unmarshaler (called for null too).
func (f *Field[T]) UnmarshalJSON(b []byte) error {
	f.Set = true
	if string(bytes.TrimSpace(b)) == "null" {
		f.Null = true
		return nil
	}
	return json.Unmarshal(b, &f.V)
}

// fieldText is the value a text field takes: the trimmed text, nil for absent, null or blank.
func fieldText(f Field[string]) *string {
	if !f.Set || f.Null {
		return nil
	}
	v := strings.TrimSpace(f.V)
	if v == "" {
		return nil
	}
	return &v
}
