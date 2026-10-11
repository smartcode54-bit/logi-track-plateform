// Package expenses serves the vehicle expenses of T40 (Appendix B §B.2.15): GET /v1/expenses with the W8 server
// filter (R35: type, status, driver, truck and dates are SQL filters, never a browser filter over every type,
// features/accounting/api/expenses.ts:90-101), staff entries (POST /v1/expenses, PATCH /v1/expenses/{id}), the audit
// decision (POST /v1/expenses/{id}/status and POST /v1/expenses/bulk-status, which run the PM check of an approved
// fuel log in the same transaction, internal/maintenance) and the toll import (POST /v1/expenses/toll-import).
//
// Every statement runs in db.WithPrincipal, so RLS decides (Appendix C §C.3.0): expenses are tenant rows of the
// caller's reach (own tenant plus the carriers working for it, R60); dispatchers and customer scopes never read one.
// Capabilities decide per type: accounting:view_fuel reads fuel rows, accounting:view_other the other rows,
// accounting:audit_expense both and decides them; accounting:edit_fuel / accounting:edit_other write them.
//
// Statuses are the canonical lower_snake vocabulary (pending, approved, rejected; R65); the legacy upper-case literals
// are accepted on input. The plate of a row is the snapshot taken when its truck was set (frozen, never re-resolved
// through the driver's current truck). (driver_id, tax_inv_id) of fuel rows is unique (D5): a second one is
// 409 duplicate_tax_invoice. While the expenses domain is Firestore-owned (class C, main spec §12.1) a configured
// writeback.Writer receives every staff write in the legacy shape before COMMIT.
package expenses

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses/expensesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// Outbox routing keys of the expenses family (Appendix B §B.5.2), on the realtime topic tenant:{tid}:expenses
// (§B.4.2, accounting:audit_expense), payload {entity, id, type, status, ...}.
const (
	RouteCreated       = "expense.created"
	RouteUpdated       = "expense.updated"
	RouteApproved      = "expense.approved"
	RouteStatusChanged = "expense.status_changed"
)

// CodeDuplicateTaxInvoice is the 409 of a second fuel row with the driver's tax invoice (Appendix B §B.1.5).
const CodeDuplicateTaxInvoice = "duplicate_tax_invoice"

// Types and statuses (vehicle_expenses CHECKs, canonical lower_snake).
const (
	TypeFuel  = "fuel"
	TypeOther = "other"

	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

var (
	// Types is the vocabulary of vehicle_expenses.expense_type.
	Types = []string{TypeFuel, TypeOther}
	// Statuses is the vocabulary of vehicle_expenses.status.
	Statuses = []string{StatusPending, StatusApproved, StatusRejected}
	// Categories is the vocabulary of vehicle_expenses.category (other expenses).
	Categories = []string{"tire_repair", "maintenance", "toll", "parking", "other"}
)

// NormalizeStatus maps a status onto the canonical vocabulary (the legacy PENDING / APPROVED / REJECTED included).
func NormalizeStatus(s string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(s))
	return v, slices.Contains(Statuses, v)
}

// Paging (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Files is the storage service as this package uses it (internal/storage implements it). Commit runs in the request
// transaction.
type Files interface {
	Commit(ctx context.Context, tx pgx.Tx, in storage.CommitInput) (storage.Committed, error)
}

// PMChecker runs the PM check of an approved fuel log in the caller's transaction (internal/maintenance).
type PMChecker interface {
	CheckPMInTx(ctx context.Context, tx pgx.Tx, p *authz.Principal, rid string, truckID uuid.UUID, mileage int32) (maintenance.PMResult, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every request runs in db.WithPrincipal.
	Pool db.Beginner
	// Files commits the receipt and odometer photos; optional (without it a key is refused, not_found).
	Files Files
	// PM is the PM check of an approval; required.
	PM PMChecker
	// WriteBack, when set, receives every staff write in the legacy Firestore shape before COMMIT (class C until
	// P7b); nil = PostgreSQL only.
	WriteBack writeback.Writer
	Log       zerolog.Logger
	// Now is the clock of the write-back's timestamps (tests pin it); nil = time.Now.
	Now func() time.Time
}

// Service serves /v1/expenses*.
type Service struct {
	pool  db.Beginner
	files Files
	pm    PMChecker
	wb    writeback.Writer
	log   zerolog.Logger
	now   func() time.Time
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("expenses: the service needs a pool")
	}
	if d.PM == nil {
		return nil, errors.New("expenses: the service needs the PM check")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: d.Pool, files: d.Files, pm: d.PM, wb: d.WriteBack, log: d.Log, now: now}, nil
}

// tx runs fn in a request transaction of p: RLS decides every row it reads or writes.
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *expensesdb.Queries) error) error {
	return mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, expensesdb.New(tx)) }))
}

// --- capabilities ------------------------------------------------------------------------------------------

// canRead reports whether p reads expenses of type typ.
func canRead(p *authz.Principal, typ string) bool {
	if p.Can(authz.AccountingAuditExpense) {
		return true
	}
	if typ == TypeFuel {
		return p.Can(authz.AccountingViewFuel)
	}
	return p.Can(authz.AccountingViewOther)
}

// editCap is the capability that writes expenses of type typ.
func editCap(typ string) authz.Cap {
	if typ == TypeFuel {
		return authz.AccountingEditFuel
	}
	return authz.AccountingEditOther
}

// requireEdit is 403 permission_denied (details.missingCapability) when p may not write rows of type typ.
func requireEdit(p *authz.Principal, typ string) error {
	if c := editCap(typ); !p.Can(c) {
		return authz.ErrPermissionDenied("missing capability", c)
	}
	return nil
}

// actor names who writes: the user (created_by_user_id references users) and, for a machine principal without a
// users row (an API key), nobody.
func actor(p *authz.Principal) *uuid.UUID {
	if p.IsMachine() {
		return nil
	}
	id := p.UserID
	return &id
}

// writeTenant refuses a write without an acting tenant (a customer-scope principal, a platform principal without
// X-Act-On-Tenant, or the read-only X-Act-On-Tenant: *).
func writeTenant(p *authz.Principal) error {
	if p.EffectiveTenant() == nil {
		return authz.ErrTenantRequired()
	}
	return nil
}

// --- payload -----------------------------------------------------------------------------------------------

// Expense is a vehicle expense as every route answers it. date is the instant of the expense, expenseDate its Bangkok
// calendar day; money is THB with two decimals; licensePlate is the row's frozen snapshot; driverName follows
// lib/driverName.ts. receiptKey / odometerKey are object keys (read them through GET /v1/files?key=).
type Expense struct {
	ID                 uuid.UUID  `json:"id"`
	TenantID           uuid.UUID  `json:"tenantId"`
	Type               string     `json:"type"`
	Category           *string    `json:"category"`
	Status             string     `json:"status"`
	Date               time.Time  `json:"date"`
	ExpenseDate        string     `json:"expenseDate"`
	AmountThb          float64    `json:"amountThb"`
	DriverID           *uuid.UUID `json:"driverId"`
	DriverName         *string    `json:"driverName"`
	TruckID            *uuid.UUID `json:"truckId"`
	LicensePlate       *string    `json:"licensePlate"`
	VolumeLiters       *float64   `json:"volumeLiters"`
	PricePerLiter      *float64   `json:"pricePerLiter"`
	OdometerKm         *int32     `json:"odometerKm"`
	StationTaxID       *string    `json:"stationTaxId"`
	TaxInvID           *string    `json:"taxInvId"`
	RefillLat          *float64   `json:"refillLat"`
	RefillLng          *float64   `json:"refillLng"`
	Note               *string    `json:"note"`
	Description        *string    `json:"description"`
	AdminNote          *string    `json:"adminNote"`
	DistanceKm         *float64   `json:"distanceKm"`
	TollImportSequence *int32     `json:"tollImportSequence"`
	TollLocation       *string    `json:"tollLocation"`
	TollLane           *string    `json:"tollLane"`
	TollSourceType     *string    `json:"tollSourceType"`
	ReceiptKey         *string    `json:"receiptKey"`
	OdometerKey        *string    `json:"odometerKey"`
	CreatedBy          *uuid.UUID `json:"createdBy"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// expenseOf is the payload of a row with its driver's display name ("" = none) and its file keys.
func expenseOf(e expensesdb.VehicleExpense, driverName string, receiptKey, odometerKey *string) Expense {
	var name *string
	if driverName != "" {
		name = &driverName
	}
	day := clock.DateString(e.ExpenseAt)
	if e.ExpenseDate != nil {
		day = e.ExpenseDate.Format(time.DateOnly)
	}
	return Expense{ID: e.ID, TenantID: e.TenantID, Type: e.ExpenseType, Category: e.Category, Status: e.Status,
		Date: e.ExpenseAt.UTC(), ExpenseDate: day, AmountThb: e.AmountThb, DriverID: e.DriverID,
		DriverName: name, TruckID: e.TruckID, LicensePlate: e.TruckLicensePlateSnapshot, VolumeLiters: e.VolumeLiters,
		PricePerLiter: e.PricePerLiter, OdometerKm: e.OdometerKm, StationTaxID: e.StationTaxID, TaxInvID: e.TaxInvID,
		RefillLat: e.RefillLat, RefillLng: e.RefillLng, Note: e.Note, Description: e.Description, AdminNote: e.AdminNote,
		DistanceKm: e.DistanceKm, TollImportSequence: e.TollImportSequence, TollLocation: e.TollLocation,
		TollLane: e.TollLane, TollSourceType: e.TollSourceType, ReceiptKey: receiptKey, OdometerKey: odometerKey,
		CreatedBy: e.CreatedByUserID, CreatedAt: e.CreatedAt.UTC(), UpdatedAt: e.UpdatedAt.UTC()}
}

// load reads the payload of rows written in tx (in id order).
func load(ctx context.Context, q *expensesdb.Queries, ids []uuid.UUID) (map[uuid.UUID]Expense, error) {
	rows, err := q.GetExpenses(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]Expense, len(rows))
	for _, r := range rows {
		out[r.VehicleExpense.ID] = expenseOf(r.VehicleExpense, r.DriverName, r.ReceiptKey, r.OdometerKey)
	}
	return out, nil
}

// --- events ------------------------------------------------------------------------------------------------

// emit appends an expenses event in the change's transaction: topic tenant:{tid}:expenses.
func emit(ctx context.Context, tx pgx.Tx, key string, e expensesdb.VehicleExpense, extra map[string]any, rid string) error {
	payload := map[string]any{"entity": "expense", "id": e.ID.String(), "type": e.ExpenseType, "status": e.Status}
	for k, v := range extra {
		payload[k] = v
	}
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: key, AggregateType: "expense", AggregateID: e.ID.String(),
		TenantID: &e.TenantID, Payload: payload, Headers: headers,
		Topics: []string{realtime.TenantTopic(e.TenantID.String(), realtime.FamilyExpenses)}})
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

// decimals reports whether v has at most n decimals.
func decimals(v float64, n int) bool {
	f := math.Pow10(n)
	c := v * f
	return math.Abs(c-math.Round(c)) < 1e-6
}

// parseInstant reads the date of an expense: an RFC 3339 instant, a Bangkok wall time yyyy-MM-ddTHH:mm[:ss] (no
// zone), or a calendar day yyyy-MM-dd (Bangkok midnight).
func parseInstant(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, v, clock.Bangkok); err == nil {
			return t, true
		}
	}
	if t, ok := clock.MidnightFromDateString(v); ok && clock.DateString(t) == v {
		return t, true
	}
	return time.Time{}, false
}

// --- keyset cursor -----------------------------------------------------------------------------------------

// cursor is the opaque keyset position (expense_at, id) of Appendix B §B.1.5, base64url JSON.
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

// errDuplicateTaxInvoice is 409 duplicate_tax_invoice (Appendix B §B.1.5): the driver already has a fuel row with this
// tax invoice (D5, vehicle_expenses_fuel_taxinv).
func errDuplicateTaxInvoice() *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeDuplicateTaxInvoice, "the driver already recorded this tax invoice").
		WithDetails(map[string]any{"field": "taxInvId"})
}

// mapDBError turns the database refusals a request can still meet into the API's codes: the D5 fuel tax-invoice index
// (409 duplicate_tax_invoice), other unique keys (409), checks and foreign keys (422), and RLS (42501: a row outside
// the caller's write reach, 403); anything else is returned as is (500 for an unknown database error).
func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "23505":
		if pe.ConstraintName == "vehicle_expenses_fuel_taxinv" {
			return errDuplicateTaxInvoice().Wrap(err)
		}
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
