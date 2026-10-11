package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/bangchak"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// QueueBangchak is the work queue of the snapshot job (Appendix B §B.5.3: group integrations, prefetch 1).
const QueueBangchak = "bangchak.snapshot"

// RouteBangchakJob is the lt.jobs routing key of the job's command (the 05:00 cron and the POST route).
const RouteBangchakJob = "job." + jobs.TypeBangchakSnapshot

// RetailTTL is the life of cache:fuel:retail:{locale} (Appendix B §B.6.2).
const RetailTTL = time.Hour

// Failure texts of a snapshot run that stores nothing.
const (
	ErrTextBangchakDisabled = "the Bangchak price feed is off on this deployment (BANGCHAK_API_BASE_URL)"
	ErrTextBangchakNoItems  = "the Bangchak response held no recognisable price"
)

// FuelSnapshot is a daily or monthly Bangchak snapshot (fuel_daily_snapshots, fuel_monthly_snapshots).
type FuelSnapshot struct {
	DayKey       string          `json:"dayKey,omitempty"`
	MonthKey     string          `json:"monthKey"`
	CapturedAt   time.Time       `json:"capturedAt"`
	FetchedAt    *time.Time      `json:"fetchedAt"`
	Source       string          `json:"source"`
	Locale       string          `json:"locale"`
	Status       string          `json:"status"`
	ErrorMessage *string         `json:"errorMessage,omitempty"`
	Items        []bangchak.Item `json:"items"`
	UpdatedAt    *time.Time      `json:"updatedAt,omitempty"`
}

// items decodes the stored [{nameTh, nameEn, price, unit}] (an unreadable value reads as no items).
func items(raw []byte) []bangchak.Item {
	var out []bangchak.Item
	if json.Unmarshal(raw, &out) != nil || out == nil {
		return []bangchak.Item{}
	}
	return out
}

func dailyOf(r billingdb.FuelDailySnapshot) FuelSnapshot {
	return FuelSnapshot{DayKey: r.DayKey.Format(time.DateOnly), MonthKey: deref(r.MonthKey), CapturedAt: r.CapturedAt.UTC(),
		FetchedAt: utc(r.FetchedAt), Source: r.Source, Locale: r.Locale, Status: r.Status, Items: items(r.Items)}
}

func monthlyOf(r billingdb.FuelMonthlySnapshot) FuelSnapshot {
	return FuelSnapshot{MonthKey: r.MonthKey, CapturedAt: r.CapturedAt.UTC(), FetchedAt: utc(r.FetchedAt), Source: r.Source,
		Locale: r.Locale, Status: r.Status, ErrorMessage: r.ErrorMessage, Items: items(r.Items), UpdatedAt: utc(&r.UpdatedAt)}
}

// DailyInput are the query parameters of GET /v1/fuel/daily.
type DailyInput struct {
	From   string
	Limit  int
	Cursor string
}

// ListDailySnapshots is GET /v1/fuel/daily (accounting:view_fuel): the days from ?from (yyyy-MM-dd, inclusive), newest
// first, keyset on the day (the fuel price history page, which read up to 400 days).
func (s *Service) ListDailySnapshots(ctx context.Context, p *authz.Principal, in DailyInput) ([]FuelSnapshot, string, error) {
	var ck checker
	limit := ck.limitOf(in.Limit, DefaultLimit)
	var from, before *time.Time
	if strings.TrimSpace(in.From) != "" {
		d := ck.date("from", in.From)
		from = &d
	}
	if in.Cursor != "" {
		c, ok := decodeCursor(in.Cursor)
		d, err := time.Parse(time.DateOnly, c.D)
		if !ok || err != nil {
			ck.add("cursor", "invalid")
		}
		before = &d
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	out := []FuelSnapshot{}
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		rows, err := q.ListDailySnapshots(ctx, billingdb.ListDailySnapshotsParams{FromDay: from, BeforeDay: before,
			RowLimit: int32(limit) + 1})
		for _, r := range rows {
			out = append(out, dailyOf(r))
		}
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = cursor{D: out[len(out)-1].DayKey}.encode()
	}
	return out, next, nil
}

// GetDailySnapshot is GET /v1/fuel/daily/{dayKey} (accounting:view_fuel): the prices captured on one Bangkok day (an
// announcement is priced from its own effective day, ADR 0009 §5); a day never captured is 404, never another day.
func (s *Service) GetDailySnapshot(ctx context.Context, p *authz.Principal, dayKey string) (*FuelSnapshot, error) {
	d, err := time.Parse(time.DateOnly, dayKey)
	if err != nil {
		return nil, httpx.ErrNotFound()
	}
	var out FuelSnapshot
	err = s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		r, err := q.GetDailySnapshot(ctx, d)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		out = dailyOf(r)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// MonthlyInput are the query parameters of GET /v1/fuel/monthly.
type MonthlyInput struct {
	Limit  int
	Cursor string
}

// ListMonthlySnapshots is GET /v1/fuel/monthly (accounting:view_fuel): newest month first (?limit, default 36 as
// getFuelMonthlySnapshots), keyset on the month.
func (s *Service) ListMonthlySnapshots(ctx context.Context, p *authz.Principal, in MonthlyInput) ([]FuelSnapshot, string, error) {
	var ck checker
	limit := ck.limitOf(in.Limit, 36)
	var before *string
	if in.Cursor != "" {
		c, ok := decodeCursor(in.Cursor)
		if !ok || len(c.D) != 7 {
			ck.add("cursor", "invalid")
		}
		before = &c.D
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	out := []FuelSnapshot{}
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		rows, err := q.ListMonthlySnapshots(ctx, billingdb.ListMonthlySnapshotsParams{BeforeMonth: before,
			RowLimit: int32(limit) + 1})
		for _, r := range rows {
			out = append(out, monthlyOf(r))
		}
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = cursor{D: out[len(out)-1].MonthKey}.encode()
	}
	return out, next, nil
}

// RetailPrices is GET /v1/fuel/retail (accounting:view_fuel; getBangchakRetailOilPrices): the live Bangchak prices of
// a locale through cache:fuel:retail:{locale} (one hour). The feed off (no BANGCHAK_API_BASE_URL) or failing is 503
// unavailable (details.reason bangchak_disabled | bangchak_unavailable); a failure is never cached.
func (s *Service) RetailPrices(ctx context.Context, locale string) (*bangchak.Prices, error) {
	switch locale {
	case "":
		locale = bangchak.LocaleTH
	case bangchak.LocaleTH, bangchak.LocaleEN:
	default:
		return nil, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "locale", Reason: "invalid",
			Params: map[string]any{"allowed": []string{bangchak.LocaleTH, bangchak.LocaleEN}}})
	}
	if s.bangchak == nil {
		return nil, httpx.NewError(http.StatusServiceUnavailable, "unavailable", "the Bangchak price feed is off").
			WithDetails(map[string]any{"reason": "bangchak_disabled"})
	}
	load := func(ctx context.Context) (bangchak.Prices, error) { return s.bangchak.RetailPrices(ctx, locale) }
	var p bangchak.Prices
	var err error
	if s.cache == nil {
		p, err = load(ctx)
	} else {
		p, err = cache.GetJSON(ctx, s.cache, s.cache.Keys().FuelRetail(locale), RetailTTL, load)
	}
	if err != nil {
		s.log.Warn().Err(err).Str("locale", locale).Msg("bangchak retail prices failed")
		return nil, httpx.NewError(http.StatusServiceUnavailable, "unavailable", "Unable to fetch Bangchak fuel prices").
			WithDetails(map[string]any{"reason": "bangchak_unavailable"})
	}
	return &p, nil
}

// SubmitBangchakSnapshot is POST /v1/jobs/bangchak.snapshot (accounting:edit_rate_card; syncBangchakFuelMonthlySnapshot):
// one bangchak.snapshot job under lock:job:bangchak.snapshot:all (a run in progress is 409 already_exists with its
// jobId), owned by the caller and filed under its effective tenant so the tenant's staff that hold the key read it
// (main spec §7.5). The worker runs it.
func (s *Service) SubmitBangchakSnapshot(ctx context.Context, p *authz.Principal) (jobs.Job, error) {
	if s.jobs == nil {
		return jobs.Job{}, httpx.ErrUnavailable("jobs are not available")
	}
	return s.jobs.Submit(ctx, jobs.SubmitInput{
		NewInput: jobs.NewInput{Type: jobs.TypeBangchakSnapshot, OwnerUserID: actor(p), TenantID: p.EffectiveTenant()},
		Command:  true,
	})
}

// --- the worker consumer -------------------------------------------------------------------------------------

// FuelJobDeps are the collaborators of the bangchak.snapshot consumer.
type FuelJobDeps struct {
	// Pool is the logitrack_app pool: the job has no request principal and writes in db.WithSystem.
	Pool db.Beginner
	// Bangchak is the price client; nil when BANGCHAK_API_BASE_URL is unset (a run then ends failed, naming it).
	Bangchak bangchak.Client
	// Locks releases lock:job:bangchak.snapshot:all when the job ends (a no-op for a cron fire, which holds no job lock).
	Locks jobs.Locker
	// Now dates the snapshot (default time.Now).
	Now func() time.Time
	Log zerolog.Logger
}

// FuelJob is the bangchak.snapshot consumer of the worker (P3, R70).
type FuelJob struct {
	d FuelJobDeps
}

// NewFuelJob builds the consumer.
func NewFuelJob(d FuelJobDeps) (*FuelJob, error) {
	if d.Pool == nil {
		return nil, errors.New("billing: the fuel snapshot job needs a pool")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &FuelJob{d: d}, nil
}

// Registration is the consumer of queue bangchak.snapshot (one delivery at a time; the fetch has its own 15 s timeout).
func (j *FuelJob) Registration() mq.Registration {
	return mq.Registration{Queue: QueueBangchak, Handler: j.Handle, Timeout: 2 * time.Minute}
}

// SnapshotResult is the result of a succeeded run (syncBangchakFuelMonthlySnapshot's answer, plus the day).
type SnapshotResult struct {
	MonthKey      string    `json:"monthKey"`
	DayKey        string    `json:"dayKey"`
	ItemCount     int       `json:"itemCount"`
	DailyInserted bool      `json:"dailyInserted"`
	FetchedAt     time.Time `json:"fetchedAt"`
}

// Handle runs one job.bangchak.snapshot command (the 05:00 Asia/Bangkok cron, FUEL_MONTHLY_SNAPSHOT_ENABLED, or POST
// /v1/jobs/bangchak.snapshot): the jobs row goes running, the Thai prices are fetched (15 s), and on success the day's
// row is inserted unless the day was captured already (insert-only, ADR 0009 §5), the month's row is upserted, and the
// job succeeds, in one transaction. A failed fetch, or a response without one recognisable price, ends the job failed
// and writes no snapshot, so the month keeps its previous row (fixes fn:core/persistFuelMonthlySnapshot.ts:88-96, which
// overwrote it with status error and no items). A database failure is retried by the ladder.
func (j *FuelJob) Handle(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey != RouteBangchakJob {
		return mq.Permanent(fmt.Errorf("bangchak.snapshot: unexpected routing key %s", d.RoutingKey))
	}
	cmd, err := jobs.DecodeCommand(d)
	if err != nil {
		return err
	}
	var job jobs.Job
	if err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		var err error
		job, err = jobs.Start(ctx, tx, cmd.JobID)
		return err
	}); errors.Is(err, jobs.ErrNotActive) {
		j.release(ctx, cmd)
		return nil // a duplicate of a finished command
	} else if err != nil {
		return err
	}
	at := j.d.Now()
	res := SnapshotResult{MonthKey: clock.DateString(at)[:7], DayKey: clock.DateString(at)}
	cause := ErrTextBangchakDisabled
	var prices bangchak.Prices
	if j.d.Bangchak != nil {
		prices, err = j.d.Bangchak.RetailPrices(ctx, bangchak.LocaleTH)
		switch {
		case err != nil && ctx.Err() != nil:
			return ctx.Err() // shutdown: the redelivered command runs again
		case err != nil:
			cause = err.Error()
		case len(prices.Items) == 0:
			cause = ErrTextBangchakNoItems
		default:
			cause = ""
		}
	}
	if cause != "" {
		err = db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
			_, err := jobs.Fail(ctx, tx, job.ID, cause, map[string]any{"monthKey": res.MonthKey, "dayKey": res.DayKey})
			return err
		})
	} else {
		res.ItemCount, res.FetchedAt = len(prices.Items), prices.FetchedAt
		err = db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
			return j.write(ctx, tx, job, at, prices, &res)
		})
	}
	if errors.Is(err, jobs.ErrNotActive) {
		err = nil // finished elsewhere meanwhile (a replay); nothing of this run was kept
	}
	if err == nil {
		j.release(ctx, cmd)
		j.d.Log.Info().Str("job_id", job.ID.String()).Str("month", res.MonthKey).Int("items", res.ItemCount).
			Str("failure", cause).Msg("bangchak.snapshot")
	}
	return err
}

// write stores the snapshots and the job's success in one transaction.
func (j *FuelJob) write(ctx context.Context, tx pgx.Tx, job jobs.Job, at time.Time, p bangchak.Prices, res *SnapshotResult) error {
	payload, err := json.Marshal(p.Items)
	if err != nil {
		return err
	}
	q := billingdb.New(tx)
	day, err := time.Parse(time.DateOnly, res.DayKey)
	if err != nil {
		return err
	}
	captured := at.UTC().Truncate(time.Microsecond)
	fetched := p.FetchedAt.UTC()
	n, err := q.InsertDailySnapshot(ctx, billingdb.InsertDailySnapshotParams{DayKey: day, CapturedAt: captured,
		FetchedAt: &fetched, Source: p.Source, Locale: bangchak.LocaleTH, Items: payload})
	if err != nil {
		return err
	}
	res.DailyInserted = n == 1
	if err := q.UpsertMonthlySnapshot(ctx, billingdb.UpsertMonthlySnapshotParams{MonthKey: res.MonthKey,
		CapturedAt: captured, FetchedAt: &fetched, Source: p.Source, Locale: bangchak.LocaleTH, Items: payload}); err != nil {
		return err
	}
	_, err = jobs.Succeed(ctx, tx, job.ID, res)
	return err
}

func (j *FuelJob) release(ctx context.Context, cmd jobs.Command) {
	if j.d.Locks == nil {
		return
	}
	if err := j.d.Locks.Release(context.WithoutCancel(ctx), j.d.Locks.JobKey(jobs.TypeBangchakSnapshot, cmd.Scope), cmd.JobID.String()); err != nil {
		j.d.Log.Warn().Err(err).Msg("bangchak.snapshot: lock release failed; it expires on its own")
	}
}
