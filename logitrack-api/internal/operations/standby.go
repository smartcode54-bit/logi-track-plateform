package operations

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
)

// StandbyInsert are the columns of a new standby record (operationsdb.InsertStandbyParams).
type StandbyInsert = operationsdb.InsertStandbyParams

// NewStandby is a standby record with its photos (customer_worksheet, site_photo).
type NewStandby struct {
	StandbyInsert
	Photos []StandbyPhotoInput
}

// StandbyPhotoInput is one standby photo: its type and the committed file.
type StandbyPhotoInput struct {
	Type        string
	FileID      uuid.UUID
	OverlayText *string
}

// CreateStandby inserts a standby record and its photos in the caller's transaction (the driver's submit, the admin
// backfill). A record with a client_op_id is idempotent per (driver_id, client_op_id) (R63): a replay returns the stored
// record with replayed = true. The billing columns stay NULL: billing.compute prices the record. The deferred
// standby_records_tenant_check keeps it on its task's (else its trip's) tenant.
func CreateStandby(ctx context.Context, tx pgx.Tx, in NewStandby) (rec Standby, replayed bool, err error) {
	var ck checker
	if !slices.Contains(TenantSources, in.TenantSource) {
		ck.add("tenantSource", "invalid")
	}
	if in.TenantID == uuid.Nil {
		ck.add("tenantId", "required")
	}
	if in.JobCategory != nil && !slices.Contains(JobCategories, *in.JobCategory) {
		ck.add("jobCategory", "invalid")
	}
	if in.StartedAt != nil && in.EndedAt != nil && in.EndedAt.Before(*in.StartedAt) {
		ck.add("endedAt", "before_start")
	}
	for _, ph := range in.Photos {
		if !slices.Contains(StandbyPhotoKind, ph.Type) {
			ck.add("photos.type", "invalid")
		}
		if ph.FileID == uuid.Nil {
			ck.add("photos.fileId", "required")
		}
	}
	if err := ck.err(); err != nil {
		return Standby{}, false, err
	}
	q := operationsdb.New(tx)
	if in.ClientOpID != nil && in.DriverID != nil {
		r, err := q.StandbyByClientOp(ctx, operationsdb.StandbyByClientOpParams{DriverID: *in.DriverID, ClientOpID: *in.ClientOpID})
		switch {
		case err == nil:
			return r, true, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return Standby{}, false, MapError(err)
		}
	}
	rec, err = q.InsertStandby(ctx, in.StandbyInsert)
	if errors.Is(err, pgx.ErrNoRows) && in.ClientOpID != nil && in.DriverID != nil {
		r, err := q.StandbyByClientOp(ctx, operationsdb.StandbyByClientOpParams{DriverID: *in.DriverID, ClientOpID: *in.ClientOpID})
		return r, err == nil, MapError(err)
	}
	if err != nil {
		return Standby{}, false, MapError(err)
	}
	for _, ph := range in.Photos {
		if _, err := q.UpsertStandbyPhoto(ctx, operationsdb.UpsertStandbyPhotoParams{StandbyID: rec.ID, PhotoType: ph.Type,
			FileID: ph.FileID, OverlayText: ph.OverlayText}); err != nil {
			return Standby{}, false, MapError(err)
		}
	}
	return rec, false, nil
}

// --- reads and the billing-carrier projection (Appendix C §C.3.4, R61) ----------------------------------------

// StandbyView is a standby record as a principal may see it: Billing tells whether the billing_* columns are the
// stored ones; when false they are cleared (absent in a payload, never null-masked by the HTTP layer).
type StandbyView struct {
	Standby Standby
	Billing bool
}

// BillingVisible reports whether p reads the billing columns of a standby record (Appendix C §C.3.4): only the billing
// carrier's principals do. The billing carrier is fixed by what the record itself names, never by today's fee
// table; the statement's invoice set applies the same rule (internal/billing, T37):
//
//   - a record that stores its carrier (billing_tenant_id, written with the price since 0015) belongs to it;
//   - otherwise a record priced from a standby rate entry (billing_rate_entry_id set) belongs to the tenant owning that
//     entry (entryTenant, read through the caller's RLS: the zero uuid when the entry is outside the caller's reach);
//     the entry is immutable in that respect and never deleted (standby_rate_entries_no_delete);
//   - a record that names no rate entry (unpriced, priced from the party's standby service fee, or a legacy rate the
//     ETL has not linked yet) belongs to the own fleet, the billing carrier of today's rate tables (R61, D6). A
//     service-fee price names no fee row (the 0004 CHECK), and the party's one standby fee may since have been
//     replaced or deleted and re-created by another tenant, so resolving it through customer_service_fees would hand
//     the columns to whoever holds the fee now.
//
// X-Act-On-Tenant: * reads everything; a driver, a customer scope or a dispatcher never.
func BillingVisible(p *authz.Principal, rec Standby, entryTenant uuid.UUID) bool {
	if p.ActOnAll {
		return true
	}
	t := p.EffectiveTenant()
	if t == nil || !p.EffectiveRole().IsStaff() || p.Dispatcher {
		return false
	}
	if rec.BillingTenantID != nil {
		return *rec.BillingTenantID == *t
	}
	if rec.BillingRateEntryID == nil {
		return p.TenantKind == "own_fleet"
	}
	return entryTenant != uuid.Nil && entryTenant == *t
}

// Project applies BillingVisible: the billing columns of a record the principal may not price are cleared.
func Project(p *authz.Principal, rec Standby, entryTenant uuid.UUID) StandbyView {
	if BillingVisible(p, rec, entryTenant) {
		return StandbyView{Standby: rec, Billing: true}
	}
	rec.BillingEstimateThb, rec.BillingPartyID, rec.BillingRateSource, rec.BillingRateEntryID = nil, nil, nil, nil
	rec.BillingEffectiveFromDate, rec.BillingUnpricedReason, rec.BillingComputedAt = nil, nil, nil
	rec.BillingTenantID, rec.BillingLastEventID = nil, nil
	return StandbyView{Standby: rec}
}

// GetStandby reads one standby record of the principal's reach with its photos, projected for the principal.
func (s *Service) GetStandby(ctx context.Context, p *authz.Principal, id uuid.UUID) (StandbyView, []StandbyPhoto, error) {
	r := ReachOf(p)
	var (
		view   StandbyView
		photos []StandbyPhoto
	)
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		row, err := q.GetStandby(ctx, operationsdb.GetStandbyParams{ID: id, AllTenants: r.All, TenantIds: r.tenants(), ReachDriver: r.Driver})
		if err != nil {
			return notFound(err)
		}
		view = Project(p, row.StandbyRecord, row.EntryTenantID)
		photos, err = q.StandbyPhotos(ctx, id)
		return err
	})
	return view, photos, err
}

// Standby list axes (GET /v1/standby?sort=).
const (
	StandbyByCreated = "created"
	StandbyByEnded   = "ended"
)

// StandbyFilter is the query of GET /v1/standby: newest first on created_at, or latest first on ended_at (records
// that ended); From / To bound the axis column, [From, To). CustomerID matches the record's customer or its billing
// party.
type StandbyFilter struct {
	Sort       string
	From, To   *time.Time
	DriverID   *uuid.UUID
	CustomerID *uuid.UUID
	Limit      int
	Cursor     string
}

// ListStandby is a keyset page of the principal's staff reach, projected for the principal.
func (s *Service) ListStandby(ctx context.Context, p *authz.Principal, f StandbyFilter) (Page[StandbyView], error) {
	var ck checker
	limit := pageLimit(&ck, f.Limit)
	axis := map[string]string{"": axisCreated, StandbyByCreated: axisCreated, StandbyByEnded: axisEnded}[f.Sort]
	if axis == "" {
		ck.add("sort", "invalid")
	}
	c, ok := decodeCursor(f.Cursor, axis)
	if !ok && axis != "" {
		ck.add("cursor", "invalid")
	}
	if err := ck.err(); err != nil {
		return Page[StandbyView]{}, err
	}
	r := ReachOf(p)
	var rows []StandbyView
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		if axis == axisEnded {
			res, err := q.ListStandbyByEnded(ctx, operationsdb.ListStandbyByEndedParams{AllTenants: r.All,
				TenantIds: r.tenants(), FromAt: f.From, ToAt: f.To, DriverID: f.DriverID, CustomerID: f.CustomerID,
				AfterEnded: c.at(), AfterID: c.id(), RowLimit: int32(limit + 1)})
			for _, row := range res {
				rows = append(rows, Project(p, row.StandbyRecord, row.EntryTenantID))
			}
			return err
		}
		res, err := q.ListStandbyByCreated(ctx, operationsdb.ListStandbyByCreatedParams{AllTenants: r.All,
			TenantIds: r.tenants(), FromAt: f.From, ToAt: f.To, DriverID: f.DriverID, CustomerID: f.CustomerID,
			AfterCreated: c.at(), AfterID: c.id(), RowLimit: int32(limit + 1)})
		for _, row := range res {
			rows = append(rows, Project(p, row.StandbyRecord, row.EntryTenantID))
		}
		return err
	})
	if err != nil {
		return Page[StandbyView]{}, err
	}
	if rows == nil {
		rows = []StandbyView{}
	}
	return page(rows, limit, func(v StandbyView) cursor {
		if axis == axisEnded {
			return cursor{Axis: axis, At: v.Standby.EndedAt, ID: v.Standby.ID}
		}
		return cursor{Axis: axis, At: ptr(v.Standby.CreatedAt), ID: v.Standby.ID}
	}), nil
}
