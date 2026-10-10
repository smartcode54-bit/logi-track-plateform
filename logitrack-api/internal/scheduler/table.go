package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs/jobsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/dbq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox/outboxdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scheduler/schedulerdb"
)

// Retention of the housekeeping jobs (main spec §7.4, Appendix B §B.5.6).
const (
	OutboxRetention      = 7 * 24 * time.Hour  // outbox.prune: published rows
	InboxRetention       = 30 * 24 * time.Hour // inbox.prune
	JobsRetention        = 30 * 24 * time.Hour // jobs.prune
	RefreshTokenGrace    = 24 * time.Hour      // auth.token-cleanup: refresh tokens expired more than a day ago
	EndedSessionRetained = 30 * 24 * time.Hour // auth.token-cleanup: sessions revoked or past absolute expiry
	pruneBatch           = 5000
)

// Pending lists the cron jobs of main spec §7.4 that are not in the table yet because the queue
// consumer that does their work arrives with a later issue (each turns its entry on there, behind its
// env gate where §7.4 names one). Firing them now would only pile commands up in an unconsumed queue.
var Pending = []struct{ Name, Spec, Issue, Gate string }{
	{"cartrack.sync", "*/3 * * * *", "T53", "CARTRACK_SYNC_ENABLED"},
	{"bangchak.snapshot", "0 5 * * *", "T37", "FUEL_MONTHLY_SNAPSHOT_ENABLED"},
	{"billing.safety-net", "*/15 * * * *", "T38", ""},
	{"etl.sync", "*/5 * * * *", "T24", "ETL_SYNC_ENABLED"},
	{"storage.gc", "0 * * * *", "T11", ""},
	{"tenancy.orphan-scan", "0 3 * * *", "T28", ""},
}

// Table is the live cron table (Asia/Bangkok): the P0 housekeeping that the scheduler runs itself.
func Table(pool *pgxpool.Pool, rt *realtime.Writer) []Job {
	return []Job{
		{Name: "auth.token-cleanup", Spec: "*/10 * * * *", Kind: Local, Run: func(ctx context.Context) (any, error) {
			return TokenCleanup(ctx, pool, time.Now())
		}},
		{Name: "outbox.prune", Spec: "0 4 * * *", Kind: Local, Run: func(ctx context.Context) (any, error) {
			n, err := prune(ctx, func(ctx context.Context) (int64, error) {
				return outboxdb.New(pool).PruneEvents(ctx, outboxdb.PruneEventsParams{Cutoff: time.Now().Add(-OutboxRetention), BatchSize: pruneBatch})
			})
			return map[string]any{"deleted": n}, err
		}},
		{Name: "inbox.prune", Spec: "0 4 * * *", Kind: Local, Run: func(ctx context.Context) (any, error) {
			n, err := prune(ctx, func(ctx context.Context) (int64, error) {
				return outboxdb.New(pool).PruneInbox(ctx, outboxdb.PruneInboxParams{Cutoff: time.Now().Add(-InboxRetention), BatchSize: pruneBatch})
			})
			return map[string]any{"deleted": n}, err
		}},
		{Name: "jobs.prune", Spec: "0 4 * * *", Kind: Local, Run: func(ctx context.Context) (any, error) {
			n, err := prune(ctx, func(ctx context.Context) (int64, error) {
				return jobsdb.New(pool).PruneJobs(ctx, jobsdb.PruneJobsParams{Cutoff: time.Now().Add(-JobsRetention), BatchSize: pruneBatch})
			})
			return map[string]any{"deleted": n}, err
		}},
		{Name: "idempotency.prune", Spec: "0 4 * * *", Kind: Local, Run: func(ctx context.Context) (any, error) {
			n, err := prune(ctx, func(ctx context.Context) (int64, error) {
				// T09's query: it skips rows a live claim holds (SKIP LOCKED) and rechecks the expiry on
				// the row it deletes, so a key re-claimed meanwhile is never deleted under its new owner.
				return dbq.New(pool).IdempotencyPrune(ctx, pruneBatch)
			})
			return map[string]any{"deleted": n}, err
		}},
		{Name: "rtlog.trim", Spec: "0 4 * * *", Kind: Local, Run: func(ctx context.Context) (any, error) {
			n, err := rt.Trim(ctx)
			return map[string]any{"streams": n}, err
		}},
	}
}

// prune repeats one batched delete until a batch comes back short.
func prune(ctx context.Context, batch func(context.Context) (int64, error)) (int64, error) {
	var total int64
	for {
		n, err := batch(ctx)
		total += n
		if err != nil || n < pruneBatch {
			return total, err
		}
	}
}

// TokenCleanup is auth.token-cleanup (R2): sessions revoked or past their absolute expiry for more
// than 30 days (their refresh tokens cascade), refresh tokens expired for more than a day, and used or
// expired reset and invite links. The identity tables are RLS-forced, so it runs under WithSystem.
func TokenCleanup(ctx context.Context, b db.Beginner, now time.Time) (map[string]int64, error) {
	out := map[string]int64{}
	err := db.WithSystem(ctx, b, nil, func(tx pgx.Tx) error {
		q := schedulerdb.New(tx)
		var err error
		if out["sessions"], err = q.DeleteEndedSessions(ctx, now.Add(-EndedSessionRetained)); err != nil {
			return err
		}
		if out["refreshTokens"], err = q.DeleteExpiredRefreshTokens(ctx, now.Add(-RefreshTokenGrace)); err != nil {
			return err
		}
		out["passwordResetTokens"], err = q.DeleteSpentPasswordResetTokens(ctx, now)
		return err
	})
	return out, err
}
