package hr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/hrdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// QueuePayroll is the work queue of the job (Appendix B §B.5.3: group hr, prefetch 1).
const QueuePayroll = "payroll.run"

// RoutePayrollJob is the lt.jobs routing key of the job's command.
const RoutePayrollJob = "job." + jobs.TypePayrollRun

// RunParams are the parameters of POST /v1/jobs/payroll.run and of its job.
type RunParams struct {
	Period string `json:"period"`
	Round  string `json:"round"`
}

// LockScope is the lock:job scope of a run: one generation of a tenant's period and round at a time
// (lock:job:payroll.run:{tenantId}:{period}:{round}); the consumer also takes the advisory lock of the same triple.
func LockScope(tenant uuid.UUID, period, round string) string {
	return tenant.String() + ":" + period + ":" + round
}

// ErrTextNoConfig is the failure of a run without any compensation config (the legacy callable's message).
const ErrTextNoConfig = "No compensation config exists — create one in Compensation Config first"

// SubmitRun is POST /v1/jobs/payroll.run (hr:manage_payroll, Appendix B §B.2.17): {period: "YYYY-MM", round: R1|R2}
// -> 202 {jobId}. One payroll.run job of the caller's effective tenant under lock:job:payroll.run:{tenant}:{period}:
// {round}: a run of the same tenant, period and round in progress is 409 already_exists with its jobId (concurrent
// runs are rejected). A tenant without any compensation config is 409 failed_precondition (reason
// no_compensation_config), as the job would fail.
func (s *Service) SubmitRun(ctx context.Context, p *authz.Principal, in RunParams) (jobs.Job, error) {
	tenant, err := writeTenant(p)
	if err != nil {
		return jobs.Job{}, err
	}
	var ck checker
	period := strings.TrimSpace(in.Period)
	round := compute.Round(strings.ToUpper(strings.TrimSpace(in.Round)))
	if _, ok := compute.ParsePeriod(period, compute.R1); period == "" {
		ck.add("period", "required")
	} else if !ok {
		ck.add("period", "invalid")
	}
	switch {
	case round == "":
		ck.add("round", "required")
	case round != compute.R1 && round != compute.R2:
		ck.add("round", "invalid")
	}
	if err := ck.err(); err != nil {
		return jobs.Job{}, err
	}
	if s.jobs == nil {
		return jobs.Job{}, httpx.ErrUnavailable("jobs are not available")
	}
	var has bool
	if err := s.tx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		var err error
		has, err = q.HasConfig(ctx, tenant)
		return err
	}); err != nil {
		return jobs.Job{}, err
	}
	if !has {
		return jobs.Job{}, httpx.NewError(http.StatusConflict, CodeFailedPrecondition, ErrTextNoConfig).
			WithDetails(map[string]any{"reason": "no_compensation_config"})
	}
	return s.jobs.Submit(ctx, jobs.SubmitInput{
		NewInput: jobs.NewInput{Type: jobs.TypePayrollRun, OwnerUserID: actor(p), TenantID: &tenant,
			Params: RunParams{Period: period, Round: string(round)}, Scope: LockScope(tenant, period, string(round))},
		Command: true,
	})
}

// --- the worker consumer -------------------------------------------------------------------------------------

// JobDeps are the collaborators of the payroll.run consumer.
type JobDeps struct {
	// Pool is the logitrack_app pool: the job has no request principal and writes in db.WithSystem with its tenant.
	Pool db.Beginner
	// Locks releases lock:job:payroll.run:{scope} when the job ends.
	Locks jobs.Locker
	Log   zerolog.Logger
}

// PayrollJob is the payroll.run consumer of the worker (P4): the port of generateDriverPayoutRun.
type PayrollJob struct {
	d JobDeps
}

// NewPayrollJob builds the consumer.
func NewPayrollJob(d JobDeps) (*PayrollJob, error) {
	if d.Pool == nil {
		return nil, errors.New("hr: the payroll job needs a pool")
	}
	return &PayrollJob{d: d}, nil
}

// Registration is the consumer of queue payroll.run (one delivery at a time).
func (j *PayrollJob) Registration() mq.Registration {
	return mq.Registration{Queue: QueuePayroll, Handler: j.Handle, Timeout: 10 * time.Minute}
}

// RunResult is the result of a succeeded run (generateDriverPayoutRun's answer, plus what it read).
type RunResult struct {
	Period string `json:"period"`
	Round  string `json:"round"`
	// Written runs were created or rewritten as draft; SkippedApproved runs had left draft (or belong to another
	// tenant) and were left alone.
	Written         int       `json:"written"`
	SkippedApproved int       `json:"skippedApproved"`
	Drivers         int       `json:"drivers"`
	ConfigID        uuid.UUID `json:"configId"`
}

// Handle runs one job.payroll.run command: the jobs row goes running, then one system transaction with the job's
// tenant takes the advisory lock of (tenant, period, round), reads the config, holidays, drivers, trips, tasks and (R2)
// open penalties, computes every driver's payslip (internal/hr/compute) and writes the draft runs, their line items
// and penalty applications, and the job's success; lock:job is released at the end. A run that cannot compute (no
// config, no tenant, bad parameters) ends failed without writing a run; a database failure is retried by the ladder.
func (j *PayrollJob) Handle(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey != RoutePayrollJob {
		return mq.Permanent(fmt.Errorf("payroll.run: unexpected routing key %s", d.RoutingKey))
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
	res, cause, err := j.run(ctx, job)
	if err != nil {
		return err
	}
	if cause != "" {
		err = db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
			_, err := jobs.Fail(ctx, tx, job.ID, cause, nil)
			return err
		})
	}
	if errors.Is(err, jobs.ErrNotActive) {
		err = nil
	}
	if err == nil {
		j.release(ctx, cmd)
		j.d.Log.Info().Str("job_id", job.ID.String()).Str("period", res.Period).Str("round", res.Round).Int("written", res.Written).
			Int("skipped", res.SkippedApproved).Str("failure", cause).Msg("payroll.run")
	}
	return err
}

func (j *PayrollJob) release(ctx context.Context, cmd jobs.Command) {
	if j.d.Locks == nil {
		return
	}
	if err := j.d.Locks.Release(context.WithoutCancel(ctx), j.d.Locks.JobKey(jobs.TypePayrollRun, cmd.Scope), cmd.JobID.String()); err != nil {
		j.d.Log.Warn().Err(err).Msg("payroll.run: lock release failed; it expires on its own")
	}
}

// run generates the round; cause is the failure text of a run that ends failed (nothing written), err a failure to
// retry.
func (j *PayrollJob) run(ctx context.Context, job jobs.Job) (RunResult, string, error) {
	var params RunParams
	if err := json.Unmarshal(job.Params, &params); err != nil {
		return RunResult{}, "the job's parameters are not {period, round}", nil
	}
	res := RunResult{Period: params.Period, Round: params.Round}
	period, ok := compute.ParsePeriod(params.Period, compute.Round(params.Round))
	if !ok {
		return res, "period must be 'YYYY-MM' and round 'R1' or 'R2'", nil
	}
	if job.TenantID == nil {
		return res, "a payroll run belongs to a tenant", nil
	}
	tenant := *job.TenantID
	cause := ""
	err := db.WithSystem(cache.MoneyPath(ctx), j.d.Pool, &tenant, func(tx pgx.Tx) error {
		q := hrdb.New(tx)
		if err := q.LockPayrollPeriod(ctx, "payroll.run:"+LockScope(tenant, period.Key, string(period.Round))); err != nil {
			return err
		}
		cfg, err := q.ConfigAt(ctx, hrdb.ConfigAtParams{TenantID: tenant, At: period.MonthEnd})
		if errors.Is(err, pgx.ErrNoRows) {
			cfg, err = q.EarliestConfig(ctx, tenant)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			cause = ErrTextNoConfig
			return nil
		}
		if err != nil {
			return err
		}
		res.ConfigID = cfg.ID
		in, err := load(ctx, q, tenant, period)
		if err != nil {
			return err
		}
		res.Drivers = len(in.drivers)
		ecfg := engineConfig(cfg)
		for _, drv := range in.drivers {
			slip := compute.Compute(period, ecfg, in.isHoliday, in.byDriver[drv])
			written, err := writeRun(ctx, q, tenant, period, drv, slip)
			if err != nil {
				return err
			}
			if written {
				res.Written++
			} else {
				res.SkippedApproved++
			}
		}
		if _, err := jobs.SetProgress(ctx, tx, job.ID, jobs.Progress{Done: res.Drivers, Total: res.Drivers}); err != nil {
			return err
		}
		_, err = jobs.Succeed(ctx, tx, job.ID, res)
		return err
	})
	if errors.Is(err, jobs.ErrNotActive) {
		return res, "", nil // finished elsewhere meanwhile (a replay); nothing of this run was kept
	}
	return res, cause, err
}

// inputs is what one generation reads: every driver of the tenant and its compute input.
type inputs struct {
	drivers   []uuid.UUID
	byDriver  map[uuid.UUID]compute.DriverInput
	isHoliday compute.HolidayFunc
}

// bangkokDay is a DATE column (UTC midnight from pgx) as Bangkok midnight of that calendar day.
func bangkokDay(d *time.Time) *time.Time {
	if d == nil {
		return nil
	}
	t := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, clock.Bangkok)
	return &t
}

// load reads the round's inputs: the drivers of the tenant (each gets a run), the holidays visible to it, the drivers'
// delivered trips of the month, their own and helper tasks planned within a day of the round (the window key decides,
// ADR 0012) and, in R2, their open penalties.
func load(ctx context.Context, q *hrdb.Queries, tenant uuid.UUID, p compute.Period) (inputs, error) {
	in := inputs{byDriver: map[uuid.UUID]compute.DriverInput{}}
	drivers, err := q.ListPayrollDrivers(ctx, tenant)
	if err != nil {
		return in, err
	}
	for _, d := range drivers {
		in.drivers = append(in.drivers, d.ID)
		in.byDriver[d.ID] = compute.DriverInput{SSO: compute.SSODriver{HireDate: bangkokDay(d.HireDate), BirthDate: bangkokDay(d.BirthDate),
			ProbationPassed: d.ProbationPassed}}
	}
	days, err := q.ListHolidayDates(ctx, &tenant)
	if err != nil {
		return in, err
	}
	keys := map[string]bool{}
	for _, d := range days {
		keys[d.Format(time.DateOnly)] = true
	}
	in.isHoliday = compute.HolidayChecker(keys)
	if len(in.drivers) == 0 {
		return in, nil
	}
	from, to := p.MonthStart, p.MonthEnd
	trips, err := q.ListMonthTrips(ctx, hrdb.ListMonthTripsParams{DriverIds: in.drivers, FromAt: &from, ToAt: &to})
	if err != nil {
		return in, err
	}
	for _, t := range trips {
		di := in.byDriver[t.DriverID]
		di.MonthTrips = append(di.MonthTrips, compute.Trip{DeliveredAt: t.DeliveredAt, MultiStop: t.IsMultiDelivery})
		in.byDriver[t.DriverID] = di
	}
	// checkInAt is not the query key: plan_at widened by a day each side, so the 12 h window shift cannot drop a
	// boundary task (fn:driverCompensation.ts:181-187).
	wFrom, wTo := p.Start.AddDate(0, 0, -1), p.End.AddDate(0, 0, 1)
	own, err := q.ListOwnTasks(ctx, hrdb.ListOwnTasksParams{DriverIds: in.drivers, FromAt: wFrom, ToAt: wTo})
	if err != nil {
		return in, err
	}
	for _, t := range own {
		di := in.byDriver[t.DriverID]
		di.OwnTasks = append(di.OwnTasks, compute.Task{CheckInAt: t.CheckInAt, PlanAt: t.PlanAt})
		in.byDriver[t.DriverID] = di
	}
	helper, err := q.ListHelperTasks(ctx, hrdb.ListHelperTasksParams{DriverIds: in.drivers, FromAt: wFrom, ToAt: wTo})
	if err != nil {
		return in, err
	}
	for _, t := range helper {
		di := in.byDriver[t.DriverID]
		di.HelperTasks = append(di.HelperTasks, compute.Task{CheckInAt: t.CheckInAt, PlanAt: t.PlanAt})
		in.byDriver[t.DriverID] = di
	}
	if p.Round != compute.R2 {
		return in, nil
	}
	pens, err := q.ListOpenPenalties(ctx, in.drivers)
	if err != nil {
		return in, err
	}
	for _, pen := range pens {
		name := ""
		if pen.TypeNameSnapshot != nil {
			name = *pen.TypeNameSnapshot
		}
		di := in.byDriver[pen.DriverID]
		di.Penalties = append(di.Penalties, compute.Penalty{ID: pen.ID.String(), TypeCode: pen.TypeCode, TypeName: name,
			TotalTHB: pen.TotalThb, RemainingTHB: pen.RemainingThb, InstallmentsTotal: int(pen.InstallmentsTotal),
			InstallmentsPaid: int(pen.InstallmentsPaid)})
		in.byDriver[pen.DriverID] = di
	}
	return in, nil
}

// writeRun upserts the driver's draft run and replaces its line items and penalty applications; false when the run
// has left draft (or belongs to another tenant) and was left alone.
func writeRun(ctx context.Context, q *hrdb.Queries, tenant uuid.UUID, p compute.Period, driver uuid.UUID, s compute.Payslip) (bool, error) {
	row, err := q.UpsertDraftRun(ctx, hrdb.UpsertDraftRunParams{TenantID: tenant, DriverID: &driver, PayPeriod: p.Key,
		PayRound: string(p.Round), PeriodStart: p.Start, PeriodEnd: p.End, TotalEarningsThb: s.TotalEarningsTHB,
		TotalDeductionsThb: s.TotalDeductionsTHB, NetPayThb: s.NetPayTHB})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !row.Inserted {
		if err := q.DeleteRunLines(ctx, row.ID); err != nil {
			return false, err
		}
		if err := q.DeleteRunApplications(ctx, row.ID); err != nil {
			return false, err
		}
	}
	lines := make([]hrdb.InsertRunLineParams, len(s.Lines))
	for i, l := range s.Lines {
		var meta []byte
		if l.Meta != nil {
			if meta, err = json.Marshal(l.Meta); err != nil {
				return false, err
			}
		}
		var ref *string
		if l.ReferenceID != "" {
			r := l.ReferenceID
			ref = &r
		}
		lines[i] = hrdb.InsertRunLineParams{RunID: row.ID, LineNo: int32(i), ItemType: l.Type, Category: l.Category, Name: l.Name,
			AmountThb: l.AmountTHB, ReferenceID: ref, Quantity: l.Quantity, UnitRateThb: l.UnitRateTHB, Meta: meta}
	}
	if err := batch(q.InsertRunLine(ctx, lines)); err != nil {
		return false, err
	}
	if len(s.Applications) > 0 {
		apps := make([]hrdb.InsertRunApplicationParams, len(s.Applications))
		for i, a := range s.Applications {
			id, err := uuid.Parse(a.PenaltyID)
			if err != nil {
				return false, err
			}
			apps[i] = hrdb.InsertRunApplicationParams{RunID: row.ID, PenaltyID: id, AppliedThb: a.AppliedTHB}
		}
		if err := batch(q.InsertRunApplication(ctx, apps)); err != nil {
			return false, err
		}
	}
	return true, nil
}

// batch runs a sqlc batch and returns its first error.
func batch(b interface{ Exec(func(int, error)) }) error {
	var first error
	b.Exec(func(_ int, err error) {
		if err != nil && first == nil {
			first = err
		}
	})
	return first
}
