package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// StandbyRequest is one pricing run of a standby record (main spec §6.9).
type StandbyRequest struct {
	// Force reprices a priced record (accounting:recompute_force), refused in a sent or paid period.
	Force bool
	// EventID is the outbox id of the message being applied (billing.compute); 0 for a request or a job.
	EventID   int64
	At        time.Time
	RequestID string
}

// StandbyComputeResult is the answer of POST /v1/standby/{id}/billing/compute, in the shape of the
// computeStandbyBillingSnapshot callable (fn:standbyBilling.ts:111-121): a price left alone by the period lock is
// {ok: true, skipped: true, blocked: true, invoiceNumber}. Reason is the stable code of an unpriced run (no_customer,
// no_rate, no_ended_at; R62).
type StandbyComputeResult struct {
	OK                 bool     `json:"ok"`
	Skipped            bool     `json:"skipped,omitempty"`
	Blocked            bool     `json:"blocked,omitempty"`
	InvoiceNumber      string   `json:"invoiceNumber,omitempty"`
	BillingEstimateTHB *float64 `json:"billingEstimateThb,omitempty"`
	Error              string   `json:"error,omitempty"`
	Reason             string   `json:"reason,omitempty"`
}

// StandbyOutcome is what a standby pricing run did.
type StandbyOutcome struct {
	Result                StandbyComputeResult
	Outcome               string
	Reason                compute.UnpricedReason
	Lock                  *compute.LockedPeriod
	RecordTenant, Carrier uuid.UUID
	WasPriced, Written    bool
}

// standbyEvent appends standby.priced on the record's tenant's and its billing carrier's trips topics (no amount in the
// payload) and returns its outbox id.
func standbyEvent(ctx context.Context, tx pgx.Tx, sr billingdb.LockStandbyForPricingRow, carrier uuid.UUID,
	payload map[string]any, rid string) (int64, error) {
	topics := []string{realtime.TenantTopic(sr.TenantID.String(), realtime.FamilyTrips)}
	if carrier != uuid.Nil && carrier != sr.TenantID {
		topics = append(topics, realtime.TenantTopic(carrier.String(), realtime.FamilyTrips))
	}
	p := map[string]any{"id": sr.ID.String(), "tenantId": sr.TenantID.String()}
	if sr.TaskID != nil {
		p["taskId"] = sr.TaskID.String()
	}
	if sr.LegacyDocID != nil {
		p["legacyDocId"] = *sr.LegacyDocID
	}
	for k, v := range payload {
		p[k] = v
	}
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	t := sr.TenantID
	a, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteStandbyPriced, AggregateType: "standby",
		AggregateID: sr.ID.String(), TenantID: &t, Payload: p, Topics: topics, Headers: headers})
	return a.ID, err
}

// standbyErrorText is the legacy message of an unpriced standby record (fn:standbyBilling.ts:156,209).
func standbyErrorText(reason compute.UnpricedReason, party string) string {
	switch reason {
	case compute.NoCustomer:
		return "No customerId — set customerId on the standby record or link a task"
	case compute.NoRate:
		return "No standby rate entry or service fee found for customer " + party
	}
	return errorText(string(reason), "", "", "", false)
}

// PriceStandby runs one pricing of standby record id in tx, a db.WithSystem transaction of the caller (main spec §6.9;
// tryWriteStandbyBillingSnapshot, fn:standbyBilling.ts:134-209). Party: the record's own, else its task's source then
// destination link, never the task's explicit billing party (R19). Rate on ended_at (newest effective, else the oldest),
// else the party's standby service fee; no ended_at is no_ended_at. A priced record is repriced only when forced, and
// then never in a sent or paid period of its billed party or the new one. ErrRecordNotFound for an unknown record.
func PriceStandby(ctx context.Context, tx pgx.Tx, id uuid.UUID, req StandbyRequest) (StandbyOutcome, error) {
	q := billingdb.New(tx)
	sr, err := q.LockStandbyForPricing(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return StandbyOutcome{}, ErrRecordNotFound
	}
	if err != nil {
		return StandbyOutcome{}, err
	}
	out := StandbyOutcome{RecordTenant: sr.TenantID, WasPriced: sr.BillingEstimateThb != nil}
	if req.EventID > 0 && sr.BillingLastEventID != nil && req.EventID <= *sr.BillingLastEventID {
		out.Outcome, out.Result = OutcomeStale, StandbyComputeResult{OK: true, Skipped: true, Reason: OutcomeStale}
		return out, nil
	}
	keep := func() error {
		if req.EventID > 0 {
			return q.SetStandbyLastEvent(ctx, billingdb.SetStandbyLastEventParams{LastEventID: req.EventID, ID: id})
		}
		return nil
	}
	var stored *float64
	if out.WasPriced {
		v := *sr.BillingEstimateThb
		stored = &v
	}
	if sr.Status != "completed" { // every record is completed (0004); kept for the legacy contract
		out.Outcome = OutcomeNotDelivered
		out.Result = StandbyComputeResult{OK: true, Skipped: true, Error: "Standby record is not completed yet"}
		return out, nil
	}
	if !req.Force && out.WasPriced {
		out.Outcome, out.Result = OutcomeSkipped, StandbyComputeResult{OK: true, Skipped: true, BillingEstimateTHB: stored}
		return out, keep()
	}
	in := compute.StandbyInput{CustomerPartyID: idStr(sr.CustomerPartyID), EndedAt: instant(sr.EndedAt),
		StartedAt: instant(sr.StartedAt), CreatedAt: sr.CreatedAt}
	var taskCategory *string
	if sr.TaskID != nil {
		t, err := q.StandbyTaskForPricing(ctx, *sr.TaskID)
		switch {
		case err == nil:
			in.TaskSourceLinkedPartyID, in.TaskDestinationLinkedPartyID = idStr(t.SourceLinkedPartyID), idStr(t.DestinationLinkedPartyID)
			taskCategory = t.JobCategory
		case !errors.Is(err, pgx.ErrNoRows):
			return out, err
		}
	}
	party, hasParty := compute.StandbyParty(in)
	if out.WasPriced {
		// ADR 0008 §5: a price in an issued period keeps its number; checked against the party it was billed under, and
		// against the party it would move to.
		at, _ := compute.StandbyAxisDate(in)
		for _, pid := range []*uuid.UUID{sr.BillingPartyID, uuidPtr(party)} {
			if pid == nil {
				continue
			}
			l, ok, err := PeriodLock(ctx, tx, *pid, at)
			if err != nil {
				return out, err
			}
			if ok {
				out.Outcome, out.Lock = OutcomeBlocked, &l
				out.Result = StandbyComputeResult{OK: true, Skipped: true, Blocked: true, InvoiceNumber: l.InvoiceNumber,
					BillingEstimateTHB: stored, Error: "Period already invoiced (" + l.InvoiceNumber + ", " + l.Status +
						") — cancel or credit-note it before repricing", Reason: CodeBillingPeriodLocked}
				return out, keep()
			}
		}
	}
	carrier, err := BillingCarrier(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("billing: the billing carrier (own fleet): %w", err)
	}
	out.Carrier = carrier
	var rates []compute.StandbyRate
	var fee *float64
	if hasParty {
		pid, err := uuid.Parse(party)
		if err != nil {
			return out, fmt.Errorf("billing: standby %s party %q: %w", id, party, err)
		}
		pt, err := LoadPricingTables(ctx, tx, pid, carrier)
		if err != nil {
			return out, err
		}
		rates, fee = pt.StandbyRates, pt.StandbyFeeTHB
	}
	res := compute.PriceStandby(in, rates, fee)
	if res.Price == nil {
		out.Outcome, out.Reason = OutcomeUnpriced, res.Reason
		out.Result = StandbyComputeResult{OK: false, Error: standbyErrorText(res.Reason, party), Reason: string(res.Reason)}
		if out.WasPriced || deref(sr.BillingUnpricedReason) == string(res.Reason) {
			return out, keep() // a priced record keeps its price; an unchanged reason is not rewritten
		}
		evID, err := standbyEvent(ctx, tx, sr, carrier, map[string]any{"priced": false, "reason": string(res.Reason)}, req.RequestID)
		if err != nil {
			return out, err
		}
		out.Written = true
		return out, q.SetStandbyUnpriced(ctx, billingdb.SetStandbyUnpricedParams{Reason: string(res.Reason), ComputedAt: req.At,
			LastEventID: &evID, ID: id})
	}
	evID, err := standbyEvent(ctx, tx, sr, carrier, map[string]any{"priced": true}, req.RequestID)
	if err != nil {
		return out, err
	}
	p := res.Price
	if err := q.SetStandbyPrice(ctx, billingdb.SetStandbyPriceParams{EstimateThb: money2(p.EstimateTHB),
		PartyID: *uuidPtr(p.PartyID), RateSource: p.RateSource, RateEntryID: uuidPtr(p.RateEntryID),
		EffectiveFromDate: textPtr(p.EffectiveFromDate), ComputedAt: req.At, CarrierID: carrier, JobCategory: taskCategory,
		LastEventID: &evID, ID: id}); err != nil {
		return out, err
	}
	out.Outcome, out.Written = OutcomePriced, true
	if out.WasPriced {
		out.Outcome = OutcomeRepriced
	}
	out.Result = StandbyComputeResult{OK: true, BillingEstimateTHB: round2Ptr(p.EstimateTHB)}
	return out, nil
}
