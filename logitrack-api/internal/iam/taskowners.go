package iam

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// TaskOwners reads past a driver's RLS what its task routes need (operations.TaskOwners, T32), each in a read-only
// system transaction: the task a route names (the callable shims forward a Firestore task doc id, and the legacy
// callables told a missing task (not-found) from another driver's (permission-denied), which the driver's own RLS
// cannot do: it sees only its own and helper tasks), returning only its id and the two drivers that may act on it; and
// the Firestore side of the billing party a stop takes from its hub (a carrier outside the driver's tenant reach). The
// caller then writes under the driver's own principal and RLS.
type TaskOwners struct {
	pool db.Beginner
}

// NewTaskOwners builds the resolver.
func NewTaskOwners(pool db.Beginner) (*TaskOwners, error) {
	if pool == nil {
		return nil, errors.New("iam: the task owner resolver needs a pool")
	}
	return &TaskOwners{pool: pool}, nil
}

var _ operations.TaskOwners = (*TaskOwners)(nil)

// TaskOwner resolves ref: a canonical uuid (lower case, hyphenated: the form the id compares equal to as text) is
// the PostgreSQL id, anything else the Firestore doc id (tasks.legacy_doc_id). A task of the quarantine tenant is not
// found, as RLS hides it from every principal but a platform reader.
func (r *TaskOwners) TaskOwner(ctx context.Context, ref string) (operations.TaskOwner, bool, error) {
	var out operations.TaskOwner
	found := false
	err := db.WithSystem(ctx, r.pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return err
		}
		q := operationsdb.New(tx)
		var t operationsdb.Task
		var err error
		if id, perr := uuid.Parse(ref); perr == nil && id.String() == ref {
			t, err = q.GetTask(ctx, operationsdb.GetTaskParams{ID: id, AllTenants: true, TenantIds: []uuid.UUID{}})
		} else {
			t, err = q.TaskByLegacyDocID(ctx, operationsdb.TaskByLegacyDocIDParams{LegacyDocID: ref, AllTenants: true,
				TenantIds: []uuid.UUID{}})
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if t.TenantID == uuid.MustParse(tenancy.QuarantineTenantID) {
			return nil
		}
		out, found = operations.TaskOwner{ID: t.ID, DriverID: t.DriverID, HelperDriverID: t.HelperDriverID}, true
		return nil
	})
	if err != nil {
		return operations.TaskOwner{}, false, err
	}
	return out, found, nil
}

// StopParty reads billing party id with the Firestore id, code and name of its customer or carrier
// (operationsdb.WriteParty) for the write-back of a driver's stop.
func (r *TaskOwners) StopParty(ctx context.Context, id uuid.UUID) (operationsdb.WritePartyRow, bool, error) {
	var out operationsdb.WritePartyRow
	found := false
	err := db.WithSystem(ctx, r.pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return err
		}
		row, err := operationsdb.New(tx).WriteParty(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, found = row, true
		return nil
	})
	if err != nil {
		return operationsdb.WritePartyRow{}, false, err
	}
	return out, found, nil
}
