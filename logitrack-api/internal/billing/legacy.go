package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The class C write-back of the two billing endpoints that change a Firestore-written record (main spec §12.1 class C,
// §13.8; Appendix B §B.5.3): tasks and standby_records stay written in Firestore by the installed 3.x APKs until P7b,
// and the FS->PG mirror applies every newer version of their documents whole. POST /v1/trips/{id}/job-category changes
// the task's หลัก/เสริม and PATCH /v1/standby/{id}/customer the record's customer; neither field is a billing field the
// trip_billing / standby_billing projections own, so while the mirror runs each write is merged into the record's
// Firestore document in the legacy shape (Deps.WriteBack) before PostgreSQL commits, as the legacy callers wrote it,
// and the mirror then brings back what PostgreSQL already holds. A failed merge answers 503 unavailable
// (details.reason write_back) and writes nothing. Without a writer (the staging VM, which runs PostgreSQL only, and
// every deployment until T31 wires the Firestore adapter) the endpoints write PostgreSQL only.

// TaskJobCategoryLegacyWrite is the Firestore write of POST /v1/trips/{id}/job-category on the trip's task
// (setTripJobCategory, fn:tripBillingOnDelivered.ts:931-935: the task's category is the source of truth, ADR 0002).
func TaskJobCategoryLegacyWrite(cat compute.JobCategory, now time.Time) map[string]any {
	return map[string]any{"jobCategory": string(cat), "updatedAt": now.UTC()}
}

// StandbyCustomerLegacyWrite is the Firestore write of PATCH /v1/standby/{id}/customer on the standby record
// (assignStandbyCustomerAndPrice, billing.ts:1623-1628). customerDocID is the legacy document id of the customer (or
// carrier) the party bills, the id the mirror resolves back to the same party.
func StandbyCustomerLegacyWrite(customerDocID string, now time.Time) map[string]any {
	return map[string]any{"customerId": customerDocID, "customerResolved": true, "customerResolvedFrom": "manual",
		"updatedAt": now.UTC()}
}

// writeBackTaskCategory merges the task's new category into its Firestore document (a task born in PostgreSQL has none).
func (s *Service) writeBackTaskCategory(ctx context.Context, tx pgx.Tx, trip uuid.UUID, cat compute.JobCategory) error {
	if s.writeBack == nil {
		return nil
	}
	doc, err := billingdb.New(tx).TripTaskLegacyDocID(ctx, trip)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (doc == nil || *doc == "")) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.writeBack.Merge(ctx, "tasks", *doc, TaskJobCategoryLegacyWrite(cat, s.now())); err != nil {
		return writeback.ErrUnavailable(err)
	}
	return nil
}

// writeBackStandbyCustomer merges the record's new customer into its Firestore document (a record born in PostgreSQL
// has none).
func (s *Service) writeBackStandbyCustomer(ctx context.Context, tx pgx.Tx, legacyDocID *string, party uuid.UUID) error {
	if s.writeBack == nil || legacyDocID == nil || *legacyDocID == "" {
		return nil
	}
	customer, err := billingdb.New(tx).PartyLegacyDocID(ctx, party)
	if err != nil {
		return err
	}
	if err := s.writeBack.Merge(ctx, "standby_records", *legacyDocID, StandbyCustomerLegacyWrite(customer, s.now())); err != nil {
		return writeback.ErrUnavailable(err)
	}
	return nil
}
