package billing

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
)

// The billing endpoints of T38 (Appendix B §B.2.11-§B.2.14): the synchronous compute of a trip or a standby record,
// the หลัก/เสริม change of a delivered trip, the manual price and the standby customer. Each checks the caller's reach
// under its RLS (a row it cannot read is 404), then prices in a db.WithSystem transaction through PriceTrip /
// PriceStandby (main spec §6.1: pricing reads one carrier's rate tables whoever asks). Amounts and invoice numbers are
// answered only to the billing carrier's staff (the own fleet, R61): a carrier tenant that ran a trip may ask for its
// compute, never read its price.

// Codes of the domain's refusals (Appendix B §B.1.5).
const (
	ReasonNotDelivered = "not_delivered"
)

// priceVisible reports whether p reads prices: staff of the billing carrier (the own fleet, R61), or a platform admin
// acting on it.
func priceVisible(p *authz.Principal) bool {
	_, err := billingTenantOf(p)
	return err == nil && p.EffectiveRole().IsStaff()
}

// forceAllowed is the check of forceRecompute (accounting:recompute_force, a billing write of the own fleet).
func forceAllowed(p *authz.Principal) error {
	if !p.Can(authz.AccountingRecomputeForce) {
		return authz.ErrPermissionDenied("forceRecompute requires accounting:recompute_force", authz.AccountingRecomputeForce)
	}
	_, err := billingTenantOf(p)
	return err
}

// visible checks the caller's reach on a trip or a standby record (RLS of its transaction): 404 when it reads none.
func (s *Service) visible(ctx context.Context, p *authz.Principal, id uuid.UUID, standby bool) error {
	return s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		var err error
		if standby {
			_, err = q.StandbyVisible(ctx, id)
		} else {
			_, err = q.TripVisible(ctx, id)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		return err
	})
}

// system runs fn in the pricing transaction: db.WithSystem on the money path (R17: every cache read refuses).
func (s *Service) system(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.WithSystem(cache.MoneyPath(ctx), s.pool, nil, fn)
}

// ComputeInput is the body of the two compute routes.
type ComputeInput struct {
	ForceRecompute bool `json:"forceRecompute"`
}

// ComputeTrip is POST /v1/trips/{id}/billing/compute (computeTripBillingSnapshot, fn:tripBillingOnDelivered.ts:624):
// a plain call prices an unpriced delivered trip; forceRecompute (accounting:recompute_force, own fleet) reprices a
// priced one, a frozen one only moving its billing date. A forced reprice whose old or new period is sent or paid is
// 409 billing_period_locked with details.blockedInvoiceNumber, nothing written (main spec §6.10 state 6).
func (s *Service) ComputeTrip(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in ComputeInput) (ComputeResult, error) {
	if in.ForceRecompute {
		if err := forceAllowed(p); err != nil {
			return ComputeResult{}, err
		}
	}
	if err := s.visible(ctx, p, id, false); err != nil {
		return ComputeResult{}, err
	}
	req := TripRequest{Mode: ModeAuto, ComputedBy: ComputedByScheduler, At: s.now(), RequestID: rid}
	if in.ForceRecompute {
		req.Mode, req.ComputedBy = ModeForce, ComputedByAdminForce
	}
	var out TripOutcome
	err := s.system(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = PriceTrip(ctx, tx, id, req)
		return err
	})
	if errors.Is(err, ErrRecordNotFound) {
		return ComputeResult{}, httpx.ErrNotFound()
	}
	if err != nil {
		return ComputeResult{}, err
	}
	if out.Outcome == OutcomeBlocked {
		return ComputeResult{}, ErrPeriodLocked(*out.Lock)
	}
	res := out.Result
	if !priceVisible(p) {
		res.BillingEstimateTHB, res.BlockedInvoiceNumber = nil, ""
	}
	return res, nil
}

// JobCategoryInput is the body of POST /v1/trips/{id}/job-category.
type JobCategoryInput struct {
	JobCategory string `json:"jobCategory"`
}

// parseCategory reads a job category: PRIMARY or SUPPLEMENTARY in any case (Appendix B writes primary|supplementary).
func parseCategory(v string) (compute.JobCategory, bool) {
	return compute.ExplicitJobCategory(strings.ToUpper(strings.TrimSpace(v)))
}

// SetTripJobCategory is POST /v1/trips/{id}/job-category (setTripJobCategory, fn:tripBillingOnDelivered.ts:679-926;
// ADR 0002): the only path that reprices a frozen row. It prices the explicit target category (no fallback) first; a
// target that prices nothing is 422 with its reason (no_rate, no_customer, no_vehicle_class, no_billing_date) and
// nothing written. Otherwise the task and the trip take the target, the snapshot the full provenance set, and
// manual_override = (target is SUPPLEMENTARY): false un-freezes a PRIMARY row. The period locks of the old and the new
// period are checked in the transaction (fixes the legacy callable, which checked none). Own fleet only.
func (s *Service) SetTripJobCategory(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in JobCategoryInput) (ComputeResult, error) {
	if _, err := billingTenantOf(p); err != nil {
		return ComputeResult{}, err
	}
	cat, ok := parseCategory(in.JobCategory)
	if !ok {
		reason := "invalid"
		if strings.TrimSpace(in.JobCategory) == "" {
			reason = "required"
		}
		return ComputeResult{}, httpx.ErrInvalidArgument(violation("jobCategory", reason))
	}
	if err := s.visible(ctx, p, id, false); err != nil {
		return ComputeResult{}, err
	}
	var out TripOutcome
	err := s.system(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = PriceTrip(ctx, tx, id, TripRequest{Mode: ModeJobCategory, Category: cat, At: s.now(), RequestID: rid})
		return err
	})
	if errors.Is(err, ErrRecordNotFound) {
		return ComputeResult{}, httpx.ErrNotFound()
	}
	if err != nil {
		return ComputeResult{}, err
	}
	if err := writeRefusal(out); err != nil {
		return ComputeResult{}, err
	}
	return out.Result, nil
}

// writeRefusal maps the outcomes an explicit admin write refuses: a trip not delivered, without its task or with too
// few delivered stops (409 failed_precondition), a sent or paid period (409 billing_period_locked) and a price the
// rules cannot make (422 with the unpriced reason as its code).
func writeRefusal(out TripOutcome) error {
	switch out.Outcome {
	case OutcomeNotDelivered:
		return errPrecondition(ReasonNotDelivered, "the trip is not delivered yet")
	case OutcomeTaskMissing:
		return errPrecondition(OutcomeTaskMissing, out.Result.Error)
	case OutcomeMultiStops:
		return errPrecondition(ReasonMultiInsufficientStops, out.Result.Error)
	case OutcomeBlocked:
		return ErrPeriodLocked(*out.Lock)
	case OutcomeUnpriced:
		return httpx.NewError(http.StatusUnprocessableEntity, string(out.Reason), out.Result.Error)
	}
	return nil
}

// ManualPriceInput is the body of PUT /v1/trips/{id}/billing/manual.
type ManualPriceInput struct {
	BillingEstimateTHB *float64 `json:"billingEstimateThb"`
	JobCategory        string   `json:"jobCategory"`
}

// SetTripManualPrice is PUT /v1/trips/{id}/billing/manual (EditBillingDialog.tsx:120-190 and writeTripBillingSnapshot,
// billing.ts:444-466; accounting:override_price, own fleet): the single manual-price exception. The price is above 0
// with at most two decimals; the reference is what the rules price for the row's category; the row is a manual
// override when there is no reference or the price differs from it by more than 0.001, and always for SUPPLEMENTARY.
// computed_by 'manual_edit'; the period locks of the old and new period are checked in the transaction.
func (s *Service) SetTripManualPrice(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in ManualPriceInput) (ComputeResult, error) {
	if _, err := billingTenantOf(p); err != nil {
		return ComputeResult{}, err
	}
	var ck checker
	price, ok := ck.money("billingEstimateThb", in.BillingEstimateTHB, 0, true)
	if ok && price <= 0 {
		ck.addParams("billingEstimateThb", "out_of_range", map[string]any{"min": 0.01})
	}
	cat, catOK := parseCategory(in.JobCategory)
	switch {
	case strings.TrimSpace(in.JobCategory) == "":
		ck.add("jobCategory", "required")
	case !catOK:
		ck.add("jobCategory", "invalid")
	}
	if err := ck.err(); err != nil {
		return ComputeResult{}, err
	}
	if err := s.visible(ctx, p, id, false); err != nil {
		return ComputeResult{}, err
	}
	var out TripOutcome
	err := s.system(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = PriceTrip(ctx, tx, id, TripRequest{Mode: ModeManual, Category: cat, ManualTHB: price, At: s.now(), RequestID: rid})
		return err
	})
	if errors.Is(err, ErrRecordNotFound) {
		return ComputeResult{}, httpx.ErrNotFound()
	}
	if err != nil {
		return ComputeResult{}, err
	}
	if err := writeRefusal(out); err != nil {
		return ComputeResult{}, err
	}
	return out.Result, nil
}

// ComputeStandby is POST /v1/standby/{id}/billing/compute (computeStandbyBillingSnapshot, fn:standbyBilling.ts:228):
// a plain call prices an unpriced record; forceRecompute needs accounting:recompute_force and the own fleet (the
// legacy callable did not check it, fn:standbyBilling.ts:238-261). A price in a sent or paid period is left alone and
// answered {ok: true, skipped: true, blocked: true, invoiceNumber}.
func (s *Service) ComputeStandby(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in ComputeInput) (StandbyComputeResult, error) {
	if in.ForceRecompute {
		if err := forceAllowed(p); err != nil {
			return StandbyComputeResult{}, err
		}
	}
	if err := s.visible(ctx, p, id, true); err != nil {
		return StandbyComputeResult{}, err
	}
	var out StandbyOutcome
	err := s.system(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = PriceStandby(ctx, tx, id, StandbyRequest{Force: in.ForceRecompute, At: s.now(), RequestID: rid})
		return err
	})
	if errors.Is(err, ErrRecordNotFound) {
		return StandbyComputeResult{}, httpx.ErrNotFound()
	}
	if err != nil {
		return StandbyComputeResult{}, err
	}
	res := out.Result
	if !priceVisible(p) {
		res.BillingEstimateTHB, res.InvoiceNumber = nil, ""
	}
	return res, nil
}

// StandbyCustomerInput is the body of PATCH /v1/standby/{id}/customer.
type StandbyCustomerInput struct {
	CustomerID string `json:"customerId"`
}

// AssignStandbyCustomer is PATCH /v1/standby/{id}/customer (assignStandbyCustomerAndPrice, billing.ts:1613-1632;
// accounting:edit_rate_card, own fleet): the admin's case-by-case repair of an unpriced record (ADR 0008 §7). In one
// transaction: the record's customer (customerId: a billing party, or the customer or carrier it bills;
// customer_resolved_from 'manual'), standby.customer_assigned, then the price. Repricing a priced record also needs
// accounting:recompute_force; when its old or new period is sent or paid nothing changes and the answer is 409
// billing_period_locked (the legacy page kept the new customer on a price it could not move).
func (s *Service) AssignStandbyCustomer(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in StandbyCustomerInput) (StandbyComputeResult, error) {
	if _, err := billingTenantOf(p); err != nil {
		return StandbyComputeResult{}, err
	}
	var ck checker
	ref := ck.party("customerId", in.CustomerID)
	if err := ck.err(); err != nil {
		return StandbyComputeResult{}, err
	}
	var party uuid.UUID
	if err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		if _, err := q.StandbyVisible(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		} else if err != nil {
			return err
		}
		r, err := writeParty(ctx, q, ref)
		party = r.ID
		return err
	}); err != nil {
		return StandbyComputeResult{}, err
	}
	var out StandbyOutcome
	err := s.system(ctx, func(tx pgx.Tx) error {
		q := billingdb.New(tx)
		sr, err := q.LockStandbyForPricing(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		priced := sr.BillingEstimateThb != nil
		if priced && !p.Can(authz.AccountingRecomputeForce) {
			return authz.ErrPermissionDenied("repricing a priced standby record requires accounting:recompute_force",
				authz.AccountingRecomputeForce)
		}
		if err := q.SetStandbyCustomer(ctx, billingdb.SetStandbyCustomerParams{PartyID: party, ID: id}); err != nil {
			return mapDBError(err)
		}
		t := sr.TenantID
		var headers map[string]string
		if rid != "" {
			headers = map[string]string{"requestId": rid}
		}
		ev, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteStandbyCustomerAssigned, AggregateType: "standby",
			AggregateID: id.String(), TenantID: &t, Headers: headers,
			Payload: map[string]any{"id": id.String(), "tenantId": t.String(), "customerPartyId": party.String(), "source": "api"}})
		if err != nil {
			return err
		}
		out, err = PriceStandby(ctx, tx, id, StandbyRequest{Force: priced, EventID: ev.ID, At: s.now(), RequestID: rid})
		if err != nil {
			return err
		}
		if out.Outcome == OutcomeBlocked {
			return ErrPeriodLocked(*out.Lock)
		}
		return nil
	})
	if err != nil {
		return StandbyComputeResult{}, err
	}
	return out.Result, nil
}

// RouteStandbyCustomerAssigned is the event of a standby record's new customer (Appendix B §B.5.2): billing.compute
// prices it (a record the endpoint priced already drops it as stale).
const RouteStandbyCustomerAssigned = "standby.customer_assigned"

// ShimResult is the answer of the mobile shim target: {ok, skipped?, error?}, never an amount (drivers never see
// billing; APK 3.x ignores it, delivery_trip_repository.dart:107-115).
type ShimResult struct {
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ReasonNotMirrored is the shim answer for a trip PostgreSQL does not hold yet: the mirror prices it when it emits
// trip.delivered.
const ReasonNotMirrored = "not_mirrored"

// ComputeTripForShim is POST /v1/mobile/trips/{id}/billing/compute, the target of the computeTripBillingSnapshot shim
// (Appendix B §B.2.22, R45): a plain compute (forceRecompute is refused) for the driver acting as itself, of a trip the
// driver ran (the trip's driver, or its task's driver or helper; another driver's trip is 403 not_task_owner). {id} is
// the PostgreSQL id, the Firestore doc id (legacy_doc_id) or the trip number.
func (s *Service) ComputeTripForShim(ctx context.Context, p *authz.Principal, rid, ref string, in ComputeInput) (ShimResult, error) {
	if in.ForceRecompute {
		return ShimResult{}, httpx.ErrInvalidArgument(violation("forceRecompute", "not_allowed"))
	}
	if p.DriverID == nil || p.EffectiveRole() != authz.Driver {
		return ShimResult{}, authz.ErrPermissionDenied("the shim target acts for a driver (driver:self)")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 1500 {
		return ShimResult{}, httpx.ErrNotFound()
	}
	var trip billingdb.ResolveTripRefRow
	found := false
	if err := s.system(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return err
		}
		r, err := billingdb.New(tx).ResolveTripRef(ctx, ref)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		trip, found = r, err == nil
		return err
	}); err != nil {
		return ShimResult{}, err
	}
	if !found {
		return ShimResult{OK: true, Skipped: true, Error: ReasonNotMirrored}, nil
	}
	me := *p.DriverID
	owns := func(d *uuid.UUID) bool { return d != nil && *d == me }
	if !owns(trip.DriverID) && !owns(trip.TaskDriverID) && !owns(trip.TaskHelperDriverID) {
		return ShimResult{}, httpx.NewError(http.StatusForbidden, CodeNotTaskOwner, "the trip belongs to another driver")
	}
	var out TripOutcome
	err := s.system(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = PriceTrip(ctx, tx, trip.ID, TripRequest{Mode: ModeAuto, ComputedBy: ComputedByMobile, At: s.now(), RequestID: rid})
		return err
	})
	if errors.Is(err, ErrRecordNotFound) {
		return ShimResult{OK: true, Skipped: true, Error: ReasonNotMirrored}, nil
	}
	if err != nil {
		return ShimResult{}, err
	}
	r := ShimResult{OK: out.Result.OK, Skipped: out.Result.Skipped, Error: out.Result.Error}
	if out.Result.Reason != "" && !out.Result.OK {
		r.Error = out.Result.Reason // a stable code, never a route or an amount
	}
	return r, nil
}

// CodeNotTaskOwner is the refusal of a driver acting on another driver's task or trip (Appendix B §B.1.5).
const CodeNotTaskOwner = "not_task_owner"

// eventID reads the outbox id a delivery carries (its AMQP message_id, R57); 0 when it is none.
func eventID(messageID string) int64 {
	n, err := strconv.ParseInt(messageID, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
