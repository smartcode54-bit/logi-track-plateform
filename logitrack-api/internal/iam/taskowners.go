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

// TaskOwners resolves the task a driver-app route names (operations.TaskOwners, T32): the callable shims forward a
// Firestore task doc id and the legacy callables told a missing task (not-found) from another driver's
// (permission-denied), which the driver's own RLS cannot do (it sees only its own and helper tasks). The lookup runs
// in a read-only system transaction and returns only the task id and the two drivers that may act on it; the caller
// then writes under the driver's own principal and RLS.
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
