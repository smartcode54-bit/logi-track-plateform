package fleet

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/fleetdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Renewal types (transactions.tx_type of a renewal) and the overview's states.
const (
	RenewalTax       = "tax"
	RenewalInsurance = "insurance"

	StateOK           = "ok"
	StateExpiringSoon = "expiring_soon"
	StateOverdue      = "overdue"
	StateInProgress   = "in_progress"

	renewalCompleted  = "completed"
	renewalInProgress = "in_progress"
)

// ExpiringSoonDays is the window of the expiring_soon state (renewals/actions.client.ts getStatus: 30 days).
const ExpiringSoonDays = 30

// MaxDueDays bounds ?due= of GET /v1/trucks/renewals.
const MaxDueDays = 366

// missingDue is the due key of a truck missing an expiry date: before every real date (ListRenewals).
var missingDue = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)

// RenewalState is one of a truck's two renewals in the overview. state is in_progress while a renewal is being worked
// on, else overdue (expired, or no expiry date at all, as the legacy overview counts it), expiring_soon (within
// ExpiringSoonDays) or ok; daysRemaining counts Bangkok calendar days (negative once expired, null without a date).
type RenewalState struct {
	ExpiryDate    *string  `json:"expiryDate"`
	RenewalStatus *string  `json:"renewalStatus"`
	State         string   `json:"state"`
	DaysRemaining *int     `json:"daysRemaining"`
	AmountThb     *float64 `json:"amountThb"`
}

// TaxRenewal is the tax side: amountThb is the last tax expense.
type TaxRenewal struct {
	RenewalState
	Responsible string `json:"responsible"`
}

// InsuranceRenewal is the insurance side: amountThb is the premium, coverage the insurance type (1, 2, 2+, 3, 3+).
type InsuranceRenewal struct {
	RenewalState
	Company      *string `json:"company"`
	PolicyNumber *string `json:"policyNumber"`
	Coverage     *string `json:"coverage"`
}

// Renewal is one item of GET /v1/trucks/renewals. nextDueDate is the earlier expiry date (null when one is missing:
// the truck sorts first and counts as due).
type Renewal struct {
	TruckID       uuid.UUID        `json:"truckId"`
	TenantID      uuid.UUID        `json:"tenantId"`
	OwnershipType string           `json:"ownershipType"`
	LicensePlate  string           `json:"licensePlate"`
	Province      string           `json:"province"`
	Status        string           `json:"status"`
	Type          string           `json:"type"`
	VehicleClass  *string          `json:"vehicleClass"`
	PaymentMethod *string          `json:"paymentMethod"`
	NextDueDate   *string          `json:"nextDueDate"`
	Tax           TaxRenewal       `json:"tax"`
	Insurance     InsuranceRenewal `json:"insurance"`
}

// renewalState is the overview state of one renewal on day today (Bangkok).
func renewalState(expiry *time.Time, status *string, today time.Time) (string, *int) {
	var days *int
	if expiry != nil {
		// Both are midnight UTC: count whole days on the Unix clock, not through time.Duration, which saturates
		// about 292 years away (a Buddhist-era year typed as Gregorian is 543 years away).
		d := int((expiry.Unix() - today.Unix()) / 86400)
		days = &d
	}
	switch {
	case status != nil && *status == renewalInProgress:
		return StateInProgress, days
	case days == nil || *days < 0:
		return StateOverdue, days
	case *days <= ExpiringSoonDays:
		return StateExpiringSoon, days
	}
	return StateOK, days
}

// ListRenewalsInput are the query parameters of GET /v1/trucks/renewals: due is a number of days (0-366); only trucks
// with a renewal due by then (or missing a date) are listed.
type ListRenewalsInput struct {
	Due, Ownership, Status, TenantID, Q string
	Limit                               int
	Cursor                              string
}

// ListRenewals is GET /v1/trucks/renewals (fleet:view_renewals): the trucks of the caller's reach, most urgent first,
// with both renewals' state on the Bangkok calendar (replaces the full-collection scans of renewals/actions.client.ts,
// TruckComplianceCards.tsx and ComplianceSummary.tsx).
func (s *Service) ListRenewals(ctx context.Context, p *authz.Principal, in ListRenewalsInput) ([]Renewal, string, error) {
	var ck checker
	today := s.today()
	var dueBy *time.Time
	if in.Due != "" {
		n, err := strconv.Atoi(in.Due)
		if err != nil || n < 0 || n > MaxDueDays {
			ck.addParams("due", "out_of_range", map[string]any{"min": 0, "max": MaxDueDays})
		} else {
			d := today.AddDate(0, 0, n)
			dueBy = &d
		}
	}
	opt := func(field, v string, allowed []string) *string {
		if v == "" {
			return nil
		}
		if !slices.Contains(allowed, v) {
			ck.add(field, "invalid")
		}
		return &v
	}
	ownership := opt("ownership", in.Ownership, Ownerships)
	status := opt("status", in.Status, TruckStatuses)
	var q *string
	if v := strings.TrimSpace(in.Q); v != "" {
		if utf8.RuneCountInString(v) > 100 {
			ck.add("q", "too_long")
		}
		q = &v
	}
	var tenant *uuid.UUID
	if in.TenantID != "" {
		id, err := uuid.Parse(in.TenantID)
		if err != nil {
			ck.add("tenantId", "invalid")
		}
		tenant = &id
	}
	limit := pageLimit(&ck, in.Limit)
	var afterDue *time.Time
	var afterID uuid.UUID
	if in.Cursor != "" {
		c, ok := decodeCursor(in.Cursor)
		var d time.Time
		if ok && c.D != nil {
			d, ok = parseDate(*c.D)
		} else {
			ok = false
		}
		if !ok {
			ck.add("cursor", "invalid")
		}
		afterDue, afterID = &d, c.ID
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	all, tenants := p.TenantReach()
	var rows []fleetdb.ListRenewalsRow
	err := s.tx(ctx, p, func(_ pgx.Tx, qq *fleetdb.Queries) (err error) {
		rows, err = qq.ListRenewals(ctx, fleetdb.ListRenewalsParams{DueBy: dueBy, AfterDue: afterDue, AfterID: afterID,
			RowLimit: int32(limit) + 1, AllTenants: all, TenantIds: tenants, TenantID: tenant, Ownership: ownership,
			Status: status, Q: q})
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		d := last.DueKey.Format(time.DateOnly)
		next = cursor{D: &d, ID: last.ID}.encode()
	}
	out := make([]Renewal, 0, len(rows))
	for _, r := range rows {
		item := Renewal{TruckID: r.ID, TenantID: r.TenantID, OwnershipType: r.OwnershipType, LicensePlate: r.LicensePlate,
			Province: r.Province, Status: r.Status, Type: r.TypeRaw, VehicleClass: r.VehicleClass, PaymentMethod: r.PaymentMethod}
		if !r.DueKey.Equal(missingDue) {
			item.NextDueDate = dateString(&r.DueKey)
		}
		item.Tax = TaxRenewal{Responsible: r.TaxResponsible, RenewalState: RenewalState{ExpiryDate: dateString(r.TaxExpiryDate),
			RenewalStatus: r.TaxRenewalStatus, AmountThb: r.TaxExpenseThb}}
		item.Tax.State, item.Tax.DaysRemaining = renewalState(r.TaxExpiryDate, r.TaxRenewalStatus, today)
		item.Insurance = InsuranceRenewal{Company: r.InsuranceCompany, PolicyNumber: r.InsurancePolicyNumber,
			Coverage: r.InsuranceType, RenewalState: RenewalState{ExpiryDate: dateString(r.InsuranceExpiryDate),
				RenewalStatus: r.InsuranceRenewalStatus, AmountThb: r.InsurancePremiumThb}}
		item.Insurance.State, item.Insurance.DaysRemaining = renewalState(r.InsuranceExpiryDate, r.InsuranceRenewalStatus, today)
		out = append(out, item)
	}
	return out, next, nil
}

// --- recording a renewal -----------------------------------------------------------------------------------------

// RenewalInput is the body of POST /v1/trucks/{id}/renewals (RenewalForm.tsx). status completed (the default) records
// the renewal: expiryDate and amount are required and the ledger row is written; pending and in_progress save the
// work in progress on the truck only (the form's save without completing). receiptKey (purpose truck_receipt) is
// linked as a tax or insurance receipt and, when completed, on the ledger row; documentKeys (insurance only,
// purpose insurance_document) are added to the insurance documents. date is the ledger date (yyyy-MM-dd, default the
// Bangkok today); notes default to the legacy "Expiry: …" / "Policy: … | Expiry: …" text.
type RenewalInput struct {
	Type          string          `json:"type"`
	Status        Field[string]   `json:"status"`
	Amount        Field[float64]  `json:"amount"`
	PaymentMethod Field[string]   `json:"paymentMethod"`
	Date          Field[string]   `json:"date"`
	ExpiryDate    Field[string]   `json:"expiryDate"`
	ReceiptKey    Field[string]   `json:"receiptKey"`
	Notes         Field[string]   `json:"notes"`
	Responsible   Field[string]   `json:"responsible"`
	StartDate     Field[string]   `json:"startDate"`
	Provider      Field[string]   `json:"provider"`
	PolicyID      Field[string]   `json:"policyId"`
	PolicyNumber  Field[string]   `json:"policyNumber"`
	Coverage      Field[string]   `json:"coverage"`
	DocumentKeys  Field[[]string] `json:"documentKeys"`
}

// Transaction is the ledger row a completed renewal wrote (transactions, append-only).
type Transaction struct {
	ID            uuid.UUID `json:"id"`
	Type          string    `json:"type"`
	SubType       *string   `json:"subType"`
	AmountThb     float64   `json:"amountThb"`
	PaymentMethod *string   `json:"paymentMethod"`
	Date          string    `json:"date"`
	ReceiptKey    *string   `json:"receiptKey"`
	Notes         *string   `json:"notes"`
	CreatedAt     time.Time `json:"createdAt"`
}

// RenewalResult is the answer of POST /v1/trucks/{id}/renewals: the truck after the renewal and the ledger row
// (null for pending / in_progress).
type RenewalResult struct {
	Truck       *TruckDetail `json:"truck"`
	Transaction *Transaction `json:"transaction"`
}

// renewal is a validated RenewalInput.
type renewal struct {
	typ, status   string
	completed     bool
	amount        *float64
	date          time.Time
	expiry, start *time.Time
	receipt       string
	docs          []string
	notes         *string
	paymentMethod *string
	responsible   *string
	provider      *string
	policyID      *string
	policyNumber  *string
	coverage      *string
}

func (s *Service) validateRenewal(in *RenewalInput) (*renewal, error) {
	var ck checker
	r := &renewal{typ: in.Type, status: renewalCompleted, date: s.today()}
	switch {
	case in.Type == "":
		ck.add("type", "required")
	case !slices.Contains([]string{RenewalTax, RenewalInsurance}, in.Type):
		ck.add("type", "invalid")
	}
	insurance := in.Type == RenewalInsurance
	if in.Status.Set {
		if in.Status.Null || !slices.Contains(RenewalStatuses, in.Status.V) {
			ck.add("status", "invalid")
		} else {
			r.status = in.Status.V
		}
	}
	r.completed = r.status == renewalCompleted
	switch f := in.Amount; {
	case r.completed && (!f.Set || f.Null):
		ck.add("amount", "required")
	case f.Set && !f.Null && (f.V < 0 || f.V > MaxMoney):
		ck.addParams("amount", "out_of_range", map[string]any{"min": 0, "max": MaxMoney})
	case f.Set && !f.Null && !cents(f.V):
		ck.add("amount", "too_precise")
	case f.Set && !f.Null:
		v := f.V
		r.amount = &v
	}
	date := func(field string, f Field[string], required bool) *time.Time {
		if !f.Set || f.Null || strings.TrimSpace(f.V) == "" {
			if required {
				ck.add(field, "required")
			}
			return nil
		}
		d, ok := parseDate(f.V)
		if !ok {
			ck.add(field, "invalid")
			return nil
		}
		return &d
	}
	r.expiry = date("expiryDate", in.ExpiryDate, r.completed)
	if d := date("date", in.Date, false); d != nil {
		r.date = *d
	}
	text := func(field string, f Field[string], max int, allowed []string, onlyInsurance, onlyTax bool) *string {
		if !f.Set || f.Null {
			return nil
		}
		v, bad := cleanText(f.V, max)
		switch {
		case bad != "":
			ck.add(field, bad)
		case v == "":
			return nil
		case (onlyInsurance && !insurance) || (onlyTax && insurance):
			ck.add(field, "not_available")
		case allowed != nil && !slices.Contains(allowed, v):
			ck.add(field, "invalid")
		default:
			return &v
		}
		return nil
	}
	r.paymentMethod = text("paymentMethod", in.PaymentMethod, 20, PaymentMethods, false, false)
	r.notes = text("notes", in.Notes, 2000, nil, false, false)
	r.responsible = text("responsible", in.Responsible, 100, nil, false, true)
	r.provider = text("provider", in.Provider, 200, nil, true, false)
	r.policyID = text("policyId", in.PolicyID, 100, nil, true, false)
	r.policyNumber = text("policyNumber", in.PolicyNumber, 100, nil, true, false)
	r.coverage = text("coverage", in.Coverage, 2, InsuranceTypes, true, false)
	// Like every other field of the other type, a null or blank startDate on a tax renewal is absent.
	if f := in.StartDate; f.Set && !f.Null && strings.TrimSpace(f.V) != "" && !insurance {
		ck.add("startDate", "not_available")
	} else if insurance {
		r.start = date("startDate", in.StartDate, false)
	}
	if f := in.ReceiptKey; f.Set && !f.Null {
		if k := strings.TrimSpace(f.V); k == "" || len(k) > storage.MaxKeyBytes {
			ck.add("receiptKey", "invalid")
		} else {
			r.receipt = k
		}
	}
	if f := in.DocumentKeys; f.Set && !f.Null && len(f.V) > 0 {
		switch {
		case !insurance:
			ck.add("documentKeys", "not_available")
		case len(f.V) > MaxFilesPerKind:
			ck.addParams("documentKeys", "too_many", map[string]any{"max": MaxFilesPerKind})
		default:
			for _, k := range f.V {
				k = strings.TrimSpace(k)
				if k == "" || len(k) > storage.MaxKeyBytes || slices.Contains(r.docs, k) || k == r.receipt {
					ck.add("documentKeys", "invalid")
					break
				}
				r.docs = append(r.docs, k)
			}
		}
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	return r, nil
}

// apply writes the renewal's fields onto the truck row.
func (r *renewal) apply(m *fleetdb.UpdateTruckParams) {
	status := r.status
	set := func(dst **string, v *string) {
		if v != nil {
			*dst = v
		}
	}
	if r.typ == RenewalTax {
		m.TaxRenewalStatus = &status
		if r.responsible != nil {
			m.TaxResponsible = *r.responsible
		}
		if r.amount != nil {
			m.TaxExpenseThb = r.amount
		}
		if r.expiry != nil {
			m.TaxExpiryDate = r.expiry
		}
	} else {
		m.InsuranceRenewalStatus = &status
		set(&m.InsuranceCompany, r.provider)
		set(&m.InsurancePolicyID, r.policyID)
		set(&m.InsurancePolicyNumber, r.policyNumber)
		set(&m.InsuranceType, r.coverage)
		if r.amount != nil {
			m.InsurancePremiumThb = r.amount
		}
		if r.expiry != nil {
			m.InsuranceExpiryDate = r.expiry
		}
		if r.start != nil {
			m.InsuranceStartDate = r.start
		}
	}
	set(&m.PaymentMethod, r.paymentMethod)
}

// legacyNote is the text the legacy form wrote on the ledger row and the status entry.
func (r *renewal) legacyNote() string {
	exp := ""
	if r.expiry != nil {
		exp = r.expiry.Format(time.DateOnly)
	}
	if r.typ == RenewalTax {
		return "Expiry: " + exp
	}
	policy := ""
	if r.policyNumber != nil {
		policy = *r.policyNumber
	}
	return "Policy: " + policy + " | Expiry: " + exp
}

// subType is the legacy transactions.subType of a renewal.
func (r *renewal) subType() string {
	if r.typ == RenewalTax {
		return "Tax Renewal"
	}
	coverage := "Unknown"
	if r.coverage != nil {
		coverage = *r.coverage
	}
	return "Insurance Renewal (" + coverage + ")"
}

// RecordRenewal is POST /v1/trucks/{id}/renewals (fleet:manage_renewals; the gate was the read capability before,
// R5). Completed, in one transaction: the truck's renewal fields (status completed, new expiry, amount, payment
// method, insurer and policy), the receipt and documents linked to the truck, the status_history entry tax_renewed /
// insurance_renewed, the ledger row (transactions, tenant of the truck, tenant_source truck) and outbox truck.renewed.
// pending / in_progress: the truck fields and files only, outbox truck.updated when something changed. created is
// true when a ledger row was written (201).
func (s *Service) RecordRenewal(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in RenewalInput) (*RenewalResult, bool, error) {
	if _, err := writeTenant(p); err != nil {
		return nil, false, err
	}
	r, err := s.validateRenewal(&in)
	if err != nil {
		return nil, false, err
	}
	keys := slices.Clone(r.docs)
	if r.receipt != "" {
		keys = append(keys, r.receipt)
	}
	held, err := s.heldKeys(ctx, keys)
	if err != nil {
		return nil, false, err
	}
	var tr *Transaction
	err = s.tx(ctx, p, func(tx pgx.Tx, q *fleetdb.Queries) error {
		cur, err := q.LockTruck(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		m := rowOf(cur)
		r.apply(&m)
		appendFiles := map[string][]string{}
		if r.receipt != "" {
			kind := "taxReceipt"
			if r.typ == RenewalInsurance {
				kind = "insuranceReceipt"
			}
			appendFiles[kind] = []string{r.receipt}
		}
		if len(r.docs) > 0 {
			appendFiles["insuranceDocument"] = r.docs
		}
		filesChanged, fileIDs, err := s.setFiles(ctx, tx, q, p, id, nil, appendFiles, held)
		if err != nil {
			return err
		}
		fieldsChanged := !reflect.DeepEqual(m, rowOf(cur)) // values, not pointers
		if fieldsChanged {
			if err := updateTruck(ctx, q, p, m, cur.Status, nil, s.now()); err != nil {
				return err
			}
		}
		if !r.completed {
			if !fieldsChanged && !filesChanged {
				return nil
			}
			return emit(ctx, tx, RouteTruckUpdated, id, cur.TenantID, []string{r.typ + "Renewal"}, rid)
		}
		user, label := actor(p)
		reason := r.legacyNote()
		if err := q.InsertTruckStatus(ctx, fleetdb.InsertTruckStatusParams{TruckID: id, Status: r.typ + "_renewed",
			ChangedAt: s.now(), UserID: user, Label: label, Reason: &reason}); err != nil {
			return err
		}
		notes := r.notes
		if notes == nil {
			notes = &reason
		}
		var receiptID *uuid.UUID
		var receiptKey *string
		if r.receipt != "" {
			fid := fileIDs[r.receipt]
			receiptID, receiptKey = &fid, &r.receipt
		}
		sub := r.subType()
		row, err := q.InsertRenewalTransaction(ctx, fleetdb.InsertRenewalTransactionParams{TenantID: cur.TenantID,
			TxType: r.typ, SubType: &sub, AmountThb: *r.amount, PaymentMethod: r.paymentMethod, TxDate: r.date,
			TruckID: &id, ReceiptFileID: receiptID, UserID: user, Label: label, Notes: notes})
		if err != nil {
			return err
		}
		tr = &Transaction{ID: row.ID, Type: r.typ, SubType: &sub, AmountThb: *r.amount, PaymentMethod: r.paymentMethod,
			Date: r.date.Format(time.DateOnly), ReceiptKey: receiptKey, Notes: notes, CreatedAt: row.CreatedAt}
		return emit(ctx, tx, RouteTruckRenewed, id, cur.TenantID, []string{r.typ + "Renewal"}, rid)
	})
	if err != nil {
		return nil, false, err
	}
	d, err := s.GetTruck(ctx, p, id)
	if err != nil {
		return nil, false, err
	}
	return &RenewalResult{Truck: d, Transaction: tr}, tr != nil, nil
}
