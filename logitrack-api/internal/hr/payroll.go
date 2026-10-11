package hr

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/hrdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Run is a payroll run as the list answers it: one driver's pay of one round. Money is whole baht; periodStart and
// periodEnd are the Bangkok midnight bounds of the round.
type Run struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenantId"`
	DriverID            *uuid.UUID `json:"driverId"`
	DriverName          *string    `json:"driverName"`
	PayPeriod           string     `json:"payPeriod"`
	Round               string     `json:"round"`
	PeriodStart         time.Time  `json:"periodStart"`
	PeriodEnd           time.Time  `json:"periodEnd"`
	Status              string     `json:"status"`
	TotalEarningsThb    float64    `json:"totalEarningsThb"`
	TotalDeductionsThb  float64    `json:"totalDeductionsThb"`
	NetPayThb           float64    `json:"netPayThb"`
	Currency            string     `json:"currency"`
	PaymentDate         *time.Time `json:"paymentDate"`
	PaymentMethod       *string    `json:"paymentMethod"`
	Remarks             *string    `json:"remarks"`
	ApprovedBy          *uuid.UUID `json:"approvedBy"`
	ApprovedAt          *time.Time `json:"approvedAt"`
	LedgerTransactionID *uuid.UUID `json:"ledgerTransactionId"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// LineItem is one line of a run (ADR 0013): type earning | deduction, the engine's category, the settled amount and
// its breakdown (quantity x unitRateThb where it applies, meta otherwise).
type LineItem struct {
	LineNo      int32           `json:"lineNo"`
	Type        string          `json:"type"`
	Category    string          `json:"category"`
	Name        string          `json:"name"`
	AmountThb   float64         `json:"amountThb"`
	Quantity    *float64        `json:"quantity"`
	UnitRateThb *float64        `json:"unitRateThb"`
	Description *string         `json:"description"`
	ReferenceID *string         `json:"referenceId"`
	Meta        json.RawMessage `json:"meta"`
}

// PenaltyApplication is a penalty deduction of a run, committed to the penalty when the run is approved.
type PenaltyApplication struct {
	PenaltyID  uuid.UUID `json:"penaltyId"`
	AppliedThb float64   `json:"appliedThb"`
}

// RunDetail is GET /v1/payroll/{id}: the run with its line items and penalty applications.
type RunDetail struct {
	Run
	LineItems           []LineItem           `json:"lineItems"`
	PenaltyApplications []PenaltyApplication `json:"penaltyApplications"`
}

func runOf(r hrdb.PayrollRun, driverName string) Run {
	return Run{ID: r.ID, TenantID: r.TenantID, DriverID: r.DriverID, DriverName: nameOrNil(driverName), PayPeriod: r.PayPeriod,
		Round: r.PayRound, PeriodStart: r.PeriodStart.UTC(), PeriodEnd: r.PeriodEnd.UTC(), Status: r.Status,
		TotalEarningsThb: r.TotalEarningsThb, TotalDeductionsThb: r.TotalDeductionsThb, NetPayThb: r.NetPayThb, Currency: r.Currency,
		PaymentDate: utc(r.PaymentDate), PaymentMethod: r.PaymentMethod, Remarks: r.Remarks, ApprovedBy: r.ApprovedBy,
		ApprovedAt: utc(r.ApprovedAt), LedgerTransactionID: r.LedgerTransactionID, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC()}
}

// RunQuery are the query parameters of GET /v1/payroll.
type RunQuery struct {
	Period, Round, Status, DriverID string
	Limit                           int
	Cursor                          string
}

// StatusCounts are the runs per status (meta.statusCounts of the first page).
type StatusCounts map[string]int64

// Runs is GET /v1/payroll (hr:view_payroll): the runs of the caller's reach by period (yyyy-MM), round, status and
// driver, keyset (period_end, id), newest first, no cap. The first page (no cursor) carries meta.statusCounts over
// every run matching the other filters (the page's summary cards, W8).
func (s *Service) Runs(ctx context.Context, p *authz.Principal, in RunQuery) ([]Run, string, StatusCounts, error) {
	var ck checker
	var period, round, status *string
	if v := strings.TrimSpace(in.Period); v != "" {
		if _, ok := compute.ParsePeriod(v, compute.R1); !ok {
			ck.add("period", "invalid")
		}
		period = &v
	}
	if v := strings.ToUpper(strings.TrimSpace(in.Round)); v != "" {
		if v != string(compute.R1) && v != string(compute.R2) {
			ck.add("round", "invalid")
		}
		round = &v
	}
	if v := normalize(in.Status); v != "" {
		if !slices.Contains(RunStatuses, v) {
			ck.add("status", "invalid")
		}
		status = &v
	}
	driver := ck.uuidParam("driverId", in.DriverID)
	limit := ck.limit(in.Limit)
	after := ck.cursor(in.Cursor)
	if err := ck.err(); err != nil {
		return nil, "", nil, err
	}
	all, tenants := p.TenantReach()
	arg := hrdb.ListRunsParams{AllTenants: all, TenantIds: tenants, PayPeriod: period, PayRound: round, Status: status, DriverID: driver,
		RowLimit: int32(limit) + 1}
	if after != nil {
		arg.AfterEnd, arg.AfterID = &after.T, after.ID
	}
	var rows []hrdb.ListRunsRow
	var counts StatusCounts
	err := s.tx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		var err error
		if rows, err = q.ListRuns(ctx, arg); err != nil {
			return err
		}
		if after != nil {
			return nil
		}
		cs, err := q.CountRunsByStatus(ctx, hrdb.CountRunsByStatusParams{AllTenants: all, TenantIds: tenants, PayPeriod: period,
			PayRound: round, DriverID: driver})
		if err != nil {
			return err
		}
		counts = StatusCounts{}
		for _, st := range RunStatuses {
			counts[st] = 0
		}
		for _, c := range cs {
			counts[c.Status] = c.N
		}
		return nil
	})
	if err != nil {
		return nil, "", nil, err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1].PayrollRun
		next = cursor{T: last.PeriodEnd, ID: last.ID}.encode()
	}
	out := make([]Run, 0, len(rows))
	for _, r := range rows {
		out = append(out, runOf(r.PayrollRun, r.DriverName))
	}
	return out, next, counts, nil
}

// detail reads a run with its lines and applications in q (404 outside the caller's reach).
func detail(ctx context.Context, q *hrdb.Queries, p *authz.Principal, id uuid.UUID) (RunDetail, error) {
	all, tenants := p.TenantReach()
	r, err := q.GetRun(ctx, hrdb.GetRunParams{ID: id, AllTenants: all, TenantIds: tenants})
	if errors.Is(err, pgx.ErrNoRows) {
		return RunDetail{}, httpx.ErrNotFound()
	}
	if err != nil {
		return RunDetail{}, err
	}
	lines, err := q.ListRunLines(ctx, id)
	if err != nil {
		return RunDetail{}, err
	}
	apps, err := q.ListRunApplications(ctx, id)
	if err != nil {
		return RunDetail{}, err
	}
	out := RunDetail{Run: runOf(r.PayrollRun, r.DriverName), LineItems: make([]LineItem, 0, len(lines)),
		PenaltyApplications: make([]PenaltyApplication, 0, len(apps))}
	for _, l := range lines {
		var meta json.RawMessage
		if len(l.Meta) > 0 {
			meta = l.Meta
		}
		out.LineItems = append(out.LineItems, LineItem{LineNo: l.LineNo, Type: l.ItemType, Category: l.Category, Name: l.Name,
			AmountThb: l.AmountThb, Quantity: l.Quantity, UnitRateThb: l.UnitRateThb, Description: l.Description,
			ReferenceID: l.ReferenceID, Meta: meta})
	}
	for _, a := range apps {
		out.PenaltyApplications = append(out.PenaltyApplications, PenaltyApplication{PenaltyID: a.PenaltyID, AppliedThb: a.AppliedThb})
	}
	return out, nil
}

// Run is GET /v1/payroll/{id} (hr:view_payroll): the run with its payroll_line_items and penalty applications (the
// server-side lock check of EditTripDetailsDialog.tsx:123 reads status here).
func (s *Service) Run(ctx context.Context, p *authz.Principal, id uuid.UUID) (RunDetail, error) {
	var out RunDetail
	err := s.tx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		var err error
		out, err = detail(ctx, q, p, id)
		return err
	})
	return out, err
}

// Approve is POST /v1/payroll/{id}/approve (hr:manage_payroll), one transaction (approveDriverPayout,
// fn:driverCompensation.ts:336-405): the run (draft or pending_approval, else 409 failed_precondition) is locked, each
// penalty application is committed to its penalty (remaining - applied, never below zero; installments_paid + 1, at
// most installments_total; cleared at zero, else partially_deducted), one driver_payout ledger row of the net pay is
// written dated on the Bangkok calendar (§6.18 #11), and the run becomes approved with approved_by / approved_at and
// ledger_transaction_id; outbox payroll.approved. A run whose applications no longer fit their penalties (a penalty
// cancelled, cleared or paid down by another approval since the run was generated) is 409 failed_precondition
// reason stale_run with the penalty ids: regenerate the run first (the legacy callable clamped the balance silently).
func (s *Service) Approve(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID) (RunDetail, error) {
	if _, err := writeTenant(p); err != nil {
		return RunDetail{}, err
	}
	all, tenants := p.TenantReach()
	var out RunDetail
	err := s.tx(ctx, p, func(tx pgx.Tx, q *hrdb.Queries) error {
		run, err := q.LockRun(ctx, hrdb.LockRunParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if run.Status != StatusDraft && run.Status != StatusPendingApproval {
			return errState("run_not_approvable", run.Status, nil)
		}
		apps, err := q.ListRunApplications(ctx, id)
		if err != nil {
			return err
		}
		if len(apps) > 0 {
			ids := make([]uuid.UUID, len(apps))
			for i, a := range apps {
				ids[i] = a.PenaltyID
			}
			pens, err := q.LockPenalties(ctx, ids)
			if err != nil {
				return err
			}
			byID := map[uuid.UUID]hrdb.DriverPenalty{}
			for _, pen := range pens {
				byID[pen.ID] = pen
			}
			var stale []string
			for _, a := range apps {
				pen, ok := byID[a.PenaltyID]
				if !ok || (pen.Status != PenaltyPending && pen.Status != PenaltyPartiallyDeducted) || pen.RemainingThb < a.AppliedThb {
					stale = append(stale, a.PenaltyID.String())
				}
			}
			if len(stale) > 0 {
				return errState("stale_run", run.Status, map[string]any{"penaltyIds": stale})
			}
			for _, a := range apps {
				if err := q.ApplyPenalty(ctx, hrdb.ApplyPenaltyParams{AppliedThb: a.AppliedThb, ID: a.PenaltyID}); err != nil {
					return err
				}
			}
		}
		ledger, err := q.InsertPayout(ctx, hrdb.InsertPayoutParams{TenantID: run.TenantID, AmountThb: run.NetPayThb, DriverID: run.DriverID,
			RunID: &run.ID, PayPeriod: &run.PayPeriod, PayRound: &run.PayRound, PerformedBy: actor(p)})
		if err != nil {
			return err
		}
		if err := q.ApproveRun(ctx, hrdb.ApproveRunParams{ApprovedBy: actor(p), LedgerID: &ledger.ID, ID: id}); err != nil {
			return err
		}
		if err := emit(ctx, tx, RouteApproved, run.TenantID, id.String(), map[string]any{"entity": "payroll", "id": id.String(),
			"period": run.PayPeriod, "round": run.PayRound, "status": StatusApproved, "from": run.Status,
			"ledgerTransactionId": ledger.ID.String(), "txDate": ledger.TxDate.Format(time.DateOnly)}, rid); err != nil {
			return err
		}
		out, err = detail(ctx, q, p, id)
		return err
	})
	return out, err
}

// StatusInput is the body of POST /v1/payroll/{id}/status.
type StatusInput struct {
	Status string `json:"status"`
	// PaymentDate (RFC 3339 or a Bangkok day) of a paid run; absent = now.
	PaymentDate   *string `json:"paymentDate"`
	PaymentMethod *string `json:"paymentMethod"`
	Remarks       *string `json:"remarks"`
}

// transitions are the moves POST /v1/payroll/{id}/status allows (to <- from). approved is reached only through
// /approve, which writes the ledger; draft re-opens a run for regeneration (from pending_approval, or a cancelled
// run, which would otherwise hold the driver's period and round for good: the UNIQUE key and the draft-only
// regeneration).
var transitions = map[string][]string{
	StatusPendingApproval: {StatusDraft},
	StatusPaid:            {StatusApproved},
	StatusCancelled:       {StatusDraft, StatusPendingApproval},
	StatusDraft:           {StatusPendingApproval, StatusCancelled},
}

// Allowed reports whether a run may move from one status to another through POST /v1/payroll/{id}/status.
func Allowed(from, to string) bool { return slices.Contains(transitions[to], from) }

const maxPaymentMethodChars, maxRemarksChars = 100, 2000

// SetStatus is POST /v1/payroll/{id}/status (hr:manage_payroll): {status: pending_approval | paid | cancelled | draft,
// paymentDate?, paymentMethod?, remarks?} with validated transitions (Allowed; anything else 409 failed_precondition,
// approved only through /approve: 422); paid stamps paymentDate (now unless given). Outbox payroll.status_changed. An
// approved or paid run is never cancelled: its ledger row is immutable.
func (s *Service) SetStatus(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in StatusInput) (RunDetail, error) {
	if _, err := writeTenant(p); err != nil {
		return RunDetail{}, err
	}
	var ck checker
	to := normalize(in.Status)
	switch {
	case to == "":
		ck.add("status", "required")
	case to == StatusApproved:
		ck.add("status", "use_approve")
	case transitions[to] == nil:
		ck.add("status", "invalid")
	}
	var payDate *time.Time
	if in.PaymentDate != nil && strings.TrimSpace(*in.PaymentDate) != "" {
		t, ok := parseInstant(*in.PaymentDate)
		if !ok {
			ck.add("paymentDate", "invalid")
		}
		payDate = &t
	}
	method := ck.text("paymentMethod", in.PaymentMethod, maxPaymentMethodChars)
	remarks := ck.text("remarks", in.Remarks, maxRemarksChars)
	if err := ck.err(); err != nil {
		return RunDetail{}, err
	}
	all, tenants := p.TenantReach()
	var out RunDetail
	err := s.tx(ctx, p, func(tx pgx.Tx, q *hrdb.Queries) error {
		run, err := q.LockRun(ctx, hrdb.LockRunParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if !Allowed(run.Status, to) {
			return errState("transition_not_allowed", run.Status, map[string]any{"to": to})
		}
		if err := q.SetRunStatus(ctx, hrdb.SetRunStatusParams{Status: to, PaymentDate: payDate, PaymentMethod: method, Remarks: remarks,
			ID: id}); err != nil {
			return err
		}
		if err := emit(ctx, tx, RouteStatusChanged, run.TenantID, id.String(), map[string]any{"entity": "payroll", "id": id.String(),
			"period": run.PayPeriod, "round": run.PayRound, "status": to, "from": run.Status}, rid); err != nil {
			return err
		}
		out, err = detail(ctx, q, p, id)
		return err
	})
	return out, err
}
