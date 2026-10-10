package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagedb"
)

// storage.gc (Appendix B §B.5.3, hourly cron of §B.5.6): the queue, the job type the scheduler fires and its
// routing key on lt.jobs.
const (
	QueueGC   = "storage.gc"
	JobTypeGC = "storage.gc"
	RouteGC   = "job.storage.gc"
)

// GC bounds: rows per listing, rows per run (the next hourly run takes the rest) and the age of an abandoned
// partial upload under the local .tmp/.
const (
	gcBatch      = 500
	gcMaxPerRun  = 50_000
	gcTempMaxAge = time.Hour
)

// GCResult is the result stored on the jobs row.
type GCResult struct {
	Deleted     int `json:"deleted"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
	TempRemoved int `json:"tempRemoved"`
}

// GC deletes the pending uploads whose expires_at has passed, each with its object on the row's own backend, one
// row per transaction: the row is locked (SKIP LOCKED, so a commit in progress wins), deleted, then the object is
// removed and the transaction commits. A failed object delete rolls the row back for the next run; a row whose
// backend this process does not have is skipped. Committed and missing_at_source rows are never touched (the
// queries select status = 'pending' only). On the local backend, partial uploads older than an hour are swept.
func (s *Service) GC(ctx context.Context) (GCResult, error) {
	var res GCResult
	seen := map[uuid.UUID]bool{}
	for len(seen) < gcMaxPerRun {
		now := s.now()
		backends := make([]string, 0, len(s.backends))
		for name := range s.backends {
			backends = append(backends, name)
		}
		var ids []uuid.UUID
		err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
			var err error
			ids, err = storagedb.New(tx).ListExpiredPending(ctx, storagedb.ListExpiredPendingParams{Now: now, Backends: backends, RowLimit: gcBatch})
			return err
		})
		if err != nil {
			return res, err
		}
		fresh := 0
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			fresh++
			outcome, err := s.collect(ctx, id, now)
			switch {
			case err != nil:
				res.Failed++
				s.log.Warn().Err(err).Str("file_id", id.String()).Msg("storage.gc: pending object kept for the next run")
			case outcome == gcSkipped:
				res.Skipped++
			case outcome == gcDeleted:
				res.Deleted++
			}
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
		}
		if fresh == 0 || len(ids) < gcBatch {
			break
		}
	}
	if s.local != nil {
		n, err := s.local.SweepTemp(gcTempMaxAge)
		res.TempRemoved = n
		if err != nil {
			return res, fmt.Errorf("storage.gc: sweep %s: %w", tmpDir, err)
		}
	}
	return res, nil
}

type gcOutcome int

const (
	gcGone gcOutcome = iota // committed, re-signed or locked meanwhile
	gcDeleted
	gcSkipped
)

func (s *Service) collect(ctx context.Context, id uuid.UUID, now time.Time) (gcOutcome, error) {
	out := gcGone
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		q := storagedb.New(tx)
		row, err := q.LockExpiredPending(ctx, storagedb.LockExpiredPendingParams{ID: id, Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		b, ok := s.backends[row.StorageBackend]
		if !ok {
			out = gcSkipped
			return nil
		}
		// Row first: a foreign key that still points at it fails here, before the object is gone.
		if err := q.DeleteFile(ctx, row.ID); err != nil {
			return err
		}
		if err := b.Delete(ctx, object(row)); err != nil {
			return err
		}
		out = gcDeleted
		return nil
	})
	return out, err
}

// GCRegistration is the storage.gc consumer of the worker (group platform, prefetch 1).
func (s *Service) GCRegistration() mq.Registration {
	return mq.Registration{Queue: QueueGC, Handler: s.handleGC, Timeout: 30 * time.Minute}
}

// handleGC runs one job.storage.gc command: the jobs row goes running, the sweep runs, the row ends succeeded with
// the counts. A duplicate of a finished command is acknowledged; a database or storage failure is retried by the
// ladder (the sweep is idempotent).
func (s *Service) handleGC(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey != RouteGC {
		return mq.Permanent(fmt.Errorf("storage.gc: unexpected routing key %s", d.RoutingKey))
	}
	cmd, err := jobs.DecodeCommand(d)
	if err != nil {
		return err
	}
	if err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.Start(ctx, tx, cmd.JobID)
		return err
	}); errors.Is(err, jobs.ErrNotActive) {
		return nil
	} else if err != nil {
		return err
	}
	res, err := s.GC(ctx)
	if err != nil {
		return err
	}
	s.log.Info().Int("deleted", res.Deleted).Int("failed", res.Failed).Int("skipped", res.Skipped).
		Int("temp_removed", res.TempRemoved).Msg("storage.gc")
	return db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.Succeed(ctx, tx, cmd.JobID, res)
		if errors.Is(err, jobs.ErrNotActive) {
			return nil
		}
		return err
	})
}
