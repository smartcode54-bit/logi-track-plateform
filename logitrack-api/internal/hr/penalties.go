package hr

import (
	"context"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Bounds of a penalty (logitrack-web/validate/driverPenaltySchema.ts, deleted in F87; last tree 4fc3d70e):
// at most MaxInstallments rounds.
const (
	MaxInstallments       = 1000
	maxPenaltyReasonChars = 2000
)

// Penalty is a driver penalty as every route answers it. installmentThb is what the next R2 run deducts at most
// (PenaltyInstallment); evidenceKey is an object key (read it through GET /v1/files?key=).
type Penalty struct {
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenantId"`
	DriverID          *uuid.UUID `json:"driverId"`
	DriverName        *string    `json:"driverName"`
	TypeCode          string     `json:"typeCode"`
	TypeName          *string    `json:"typeName"`
	TotalThb          float64    `json:"totalThb"`
	RemainingThb      float64    `json:"remainingThb"`
	InstallmentsTotal int32      `json:"installmentsTotal"`
	InstallmentsPaid  int32      `json:"installmentsPaid"`
	InstallmentThb    float64    `json:"installmentThb"`
	Reason            *string    `json:"reason"`
	EvidenceKey       *string    `json:"evidenceKey"`
	Status            string     `json:"status"`
	IncurredAt        time.Time  `json:"incurredAt"`
	CreatedBy         *uuid.UUID `json:"createdBy"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

func penaltyOf(p hrdb.DriverPenalty, driverName string, evidence *string) Penalty {
	next := 0.0
	if p.Status == PenaltyPending || p.Status == PenaltyPartiallyDeducted {
		next = compute.PenaltyInstallment(p.TotalThb, p.RemainingThb, int(p.InstallmentsTotal))
	}
	return Penalty{ID: p.ID, TenantID: p.TenantID, DriverID: p.DriverID, DriverName: nameOrNil(driverName), TypeCode: p.TypeCode,
		TypeName: p.TypeNameSnapshot, TotalThb: p.TotalThb, RemainingThb: p.RemainingThb, InstallmentsTotal: p.InstallmentsTotal,
		InstallmentsPaid: p.InstallmentsPaid, InstallmentThb: next, Reason: p.Reason, EvidenceKey: evidence, Status: p.Status,
		IncurredAt: p.IncurredAt.UTC(), CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC()}
}

// PenaltyQuery are the query parameters of GET /v1/penalties.
type PenaltyQuery struct {
	DriverID string
	// Statuses filters by status (?status=a&status=b or ?status=a,b); empty = every status.
	Statuses []string
	Limit    int
	Cursor   string
}

// Penalties is GET /v1/penalties (hr:manage_payroll): the penalties of the caller's reach, by driver and statuses,
// keyset (incurred_at, id), newest first, no cap (the legacy list read the latest 200, penalties.ts:39-50).
func (s *Service) Penalties(ctx context.Context, p *authz.Principal, in PenaltyQuery) ([]Penalty, string, error) {
	var ck checker
	driver := ck.uuidParam("driverId", in.DriverID)
	statuses := []string{}
	for _, raw := range in.Statuses {
		for _, v := range strings.Split(raw, ",") {
			v = normalize(v)
			if v == "" {
				continue
			}
			if !slices.Contains(PenaltyStatuses, v) {
				ck.add("status", "invalid")
				continue
			}
			if !slices.Contains(statuses, v) {
				statuses = append(statuses, v)
			}
		}
	}
	limit := ck.limit(in.Limit)
	after := ck.cursor(in.Cursor)
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	all, tenants := p.TenantReach()
	arg := hrdb.ListPenaltiesParams{AllTenants: all, TenantIds: tenants, DriverID: driver, Statuses: statuses, RowLimit: int32(limit) + 1}
	if after != nil {
		arg.AfterAt, arg.AfterID = &after.T, after.ID
	}
	var rows []hrdb.ListPenaltiesRow
	if err := s.tx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		var err error
		rows, err = q.ListPenalties(ctx, arg)
		return err
	}); err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1].DriverPenalty
		next = cursor{T: last.IncurredAt, ID: last.ID}.encode()
	}
	out := make([]Penalty, 0, len(rows))
	for _, r := range rows {
		out = append(out, penaltyOf(r.DriverPenalty, r.DriverName, r.EvidenceKey))
	}
	return out, next, nil
}

// PenaltyInput is the body of POST /v1/penalties (whole baht; logitrack-web/validate/driverPenaltySchema.ts,
// deleted in F87; last tree 4fc3d70e).
type PenaltyInput struct {
	DriverID string `json:"driverId"`
	TypeCode string `json:"typeCode"`
	// TypeName is the display name kept on the penalty; absent = the type's English name in the version in effect at
	// incurredAt (the legacy page sent nameEn).
	TypeName          *string  `json:"typeName"`
	TotalThb          *float64 `json:"totalThb"`
	InstallmentsTotal *float64 `json:"installmentsTotal"`
	Reason            *string  `json:"reason"`
	EvidenceKey       *string  `json:"evidenceKey"`
	IncurredAt        string   `json:"incurredAt"`
}

// CreatePenalty is POST /v1/penalties (hr:manage_payroll): a pending penalty of a driver of the caller's reach (the
// driver's tenant stamps it, tenant_source 'driver'), remaining = total, deducted by R2 runs in installments of
// total / installmentsTotal (1 = in full). An evidence key is committed to the penalty (penalty_evidence). 201.
func (s *Service) CreatePenalty(ctx context.Context, p *authz.Principal, in PenaltyInput) (Penalty, error) {
	if _, err := writeTenant(p); err != nil {
		return Penalty{}, err
	}
	var ck checker
	driverID, err := uuid.Parse(strings.TrimSpace(in.DriverID))
	switch {
	case strings.TrimSpace(in.DriverID) == "":
		ck.add("driverId", "required")
	case err != nil:
		ck.add("driverId", "invalid")
	}
	code := ck.text("typeCode", &in.TypeCode, maxPenaltyCodeLength)
	if code == nil && strings.TrimSpace(in.TypeCode) == "" {
		ck.add("typeCode", "required")
	}
	name := ck.text("typeName", in.TypeName, maxPenaltyNameLength)
	var total float64
	if in.TotalThb == nil {
		ck.add("totalThb", "required")
	} else {
		total = *in.TotalThb
		ck.wholeTHB("totalThb", total, 1) // a zero penalty would be open with nothing to deduct (Appendix D invariant 6)
	}
	installments := ck.integer("installmentsTotal", orDefault(in.InstallmentsTotal, 1), 1, MaxInstallments)
	reason := ck.text("reason", in.Reason, maxPenaltyReasonChars)
	at, ok := parseInstant(in.IncurredAt)
	switch {
	case strings.TrimSpace(in.IncurredAt) == "":
		ck.add("incurredAt", "required")
	case !ok:
		ck.add("incurredAt", "invalid")
	}
	if err := ck.err(); err != nil {
		return Penalty{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Penalty{}, err
	}
	all, tenants := p.TenantReach()
	var out Penalty
	err = s.tx(ctx, p, func(tx pgx.Tx, q *hrdb.Queries) error {
		d, err := q.GetDriver(ctx, hrdb.GetDriverParams{ID: driverID, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("driverId", "not_found"))
		}
		if err != nil {
			return err
		}
		if name == nil {
			if name, err = penaltyTypeName(ctx, q, d.TenantID, at, *code); err != nil {
				return err
			}
		}
		var evidence *uuid.UUID
		if k := in.EvidenceKey; k != nil && strings.TrimSpace(*k) != "" {
			if s.files == nil {
				return httpx.ErrInvalidArgument(violation("evidenceKey", "not_found"))
			}
			f, err := s.files.Commit(ctx, tx, storage.CommitInput{Key: strings.TrimSpace(*k), Field: "evidenceKey",
				Purposes: []string{"penalty_evidence"}, OwnerID: id, UserID: p.UserID, TenantID: p.EffectiveTenant()})
			if err != nil {
				return err
			}
			evidence = &f.ID
		}
		row, err := q.InsertPenalty(ctx, hrdb.InsertPenaltyParams{ID: id, TenantID: d.TenantID, DriverID: &d.ID, TypeCode: *code,
			TypeName: name, TotalThb: total, InstallmentsTotal: installments, Reason: reason, EvidenceFileID: evidence,
			IncurredAt: at, CreatedBy: actor(p)})
		if err != nil {
			return err
		}
		got, err := q.GetPenalty(ctx, row.ID)
		if err != nil {
			return err
		}
		out = penaltyOf(got.DriverPenalty, got.DriverName, got.EvidenceKey)
		return nil
	})
	return out, err
}

// penaltyTypeName is the English name of a penalty type in the version in effect at an instant (else the earliest
// version), nil when that version has no such code.
func penaltyTypeName(ctx context.Context, q *hrdb.Queries, tenant uuid.UUID, at time.Time, code string) (*string, error) {
	c, err := q.ConfigAt(ctx, hrdb.ConfigAtParams{TenantID: tenant, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		c, err = q.EarliestConfig(ctx, tenant)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	types, err := q.ListPenaltyTypes(ctx, []uuid.UUID{c.ID})
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if t.Code == code {
			return &t.NameEn, nil
		}
	}
	return nil, nil
}

// CancelPenalty is POST /v1/penalties/{id}/cancel (hr:manage_payroll): an open penalty (pending, partially_deducted)
// becomes cancelled and no run deducts it again; a cleared or cancelled one is 409 failed_precondition. A draft run
// that already scheduled it is refused at approval until regenerated (stale_run).
func (s *Service) CancelPenalty(ctx context.Context, p *authz.Principal, id uuid.UUID) (Penalty, error) {
	if _, err := writeTenant(p); err != nil {
		return Penalty{}, err
	}
	all, tenants := p.TenantReach()
	var out Penalty
	err := s.tx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		cur, err := q.LockPenalty(ctx, hrdb.LockPenaltyParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if cur.Status != PenaltyPending && cur.Status != PenaltyPartiallyDeducted {
			return errState("penalty_closed", cur.Status, nil)
		}
		if err := q.CancelPenalty(ctx, id); err != nil {
			return err
		}
		got, err := q.GetPenalty(ctx, id)
		if err != nil {
			return err
		}
		out = penaltyOf(got.DriverPenalty, got.DriverName, got.EvidenceKey)
		return nil
	})
	return out, err
}
