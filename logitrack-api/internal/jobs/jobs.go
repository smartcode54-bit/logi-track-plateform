// Package jobs owns the jobs table (main spec §7.5, Appendix A §A.2.8, R14, R64): long-running admin
// and scheduler work that replaces the 540 s callables. A job is a row plus, for work a worker queue
// does, an lt.jobs outbox command job.{type} committed in the same transaction; consumers move it
// through queued -> running -> succeeded | failed, report {done,total} progress, and every change of
// a job with an owner is pushed as job.updated on the realtime topic user:{owner}.
//
// It also owns the lock: namespace of Redis (Appendix B §B.6.1): lock:job:{type}:{scope} stops a
// second submission while one runs, lock:cron:{job}:{scheduledFor} backs the scheduler's advisory
// lock.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs/jobsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
)

// Statuses of jobs.status (R64).
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// RoutingKeyUpdated is the realtime-only event of a job change (Appendix B §B.4.3).
const RoutingKeyUpdated = "job.updated"

// ProgressEvery is how often consumers report progress (main spec §7.5: every 50 rows).
const ProgressEvery = 50

// LockTTL is the life of lock:job:{type}:{scope} (Appendix B §B.6.2).
const LockTTL = time.Hour

var typeRx = regexp.MustCompile(`^[a-z]+(\.[a-z0-9-]+)+$`) // the CHECK of jobs.type

// Job is a jobs row as GET /v1/jobs/{id} returns it (Appendix B §B.2.20).
type Job struct {
	ID          uuid.UUID       `json:"id"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Params      json.RawMessage `json:"params"`
	Progress    json.RawMessage `json:"progress"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       *string         `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	StartedAt   *time.Time      `json:"startedAt"`
	FinishedAt  *time.Time      `json:"finishedAt"`
	OwnerUserID *uuid.UUID      `json:"-"`
	TenantID    *uuid.UUID      `json:"-"`
}

func fromRow(r jobsdb.Job) Job {
	j := Job{
		ID: r.ID, Type: r.Type, Status: r.Status, Params: r.Params, Progress: r.Progress, Error: r.Error,
		CreatedAt: r.CreatedAt.UTC(), StartedAt: utc(r.StartedAt), FinishedAt: utc(r.FinishedAt),
		OwnerUserID: r.OwnerUserID, TenantID: r.TenantID,
	}
	if len(r.Result) > 0 {
		j.Result = r.Result
	}
	return j
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// Progress is jobs.progress.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Command is the payload of the lt.jobs outbox message job.{type}: consumers re-read the row.
type Command struct {
	JobID  uuid.UUID       `json:"jobId"`
	Type   string          `json:"type"`
	Params json.RawMessage `json:"params"`
	// Scope is the lock:job scope of a job submitted with a lock (Service.Submit): the consumer that
	// ends such a job releases lock:job:{type}:{scope} (holder = the job id) after its terminal
	// Succeed or Fail commits. In T10 only queue.replay takes the lock, and the Replayer releases it;
	// the first worker job behind POST /v1/jobs/{type} brings the release to its consumer (LockTTL
	// bounds a lock nobody releases).
	Scope string `json:"scope,omitempty"`
}

// DecodeCommand reads the job command of a delivery from lt.jobs.
func DecodeCommand(d *mq.Delivery) (Command, error) {
	var c Command
	if err := d.Decode(&c); err != nil {
		return Command{}, err
	}
	if c.JobID == uuid.Nil {
		return Command{}, mq.Permanent(errors.New("jobs: command without jobId"))
	}
	return c, nil
}

// NewInput describes a job to create.
type NewInput struct {
	Type        string
	OwnerUserID *uuid.UUID // nil for the scheduler
	TenantID    *uuid.UUID // nil = platform-wide
	Params      any        // a JSON object; nil = {}
	Scope       string     // lock scope, carried in the command
	ID          uuid.UUID  // zero = a new uuidv7
}

// Insert creates the jobs row only: for jobs the scheduler runs itself (SQL housekeeping, queue
// replays), which have no worker queue.
func Insert(ctx context.Context, tx jobsdb.DBTX, in NewInput) (Job, error) {
	if !typeRx.MatchString(in.Type) {
		return Job{}, fmt.Errorf("jobs: type %q does not match %s", in.Type, typeRx)
	}
	params, err := object(in.Params)
	if err != nil {
		return Job{}, err
	}
	id := in.ID
	if id == uuid.Nil {
		if id, err = uuid.NewV7(); err != nil {
			return Job{}, err
		}
	}
	row, err := jobsdb.New(tx).InsertJob(ctx, jobsdb.InsertJobParams{
		ID: id, Type: in.Type, OwnerUserID: in.OwnerUserID, TenantID: in.TenantID, Params: params,
	})
	if err != nil {
		return Job{}, fmt.Errorf("jobs: insert %s: %w", in.Type, err)
	}
	return fromRow(row), nil
}

// Enqueue creates the jobs row and its lt.jobs command job.{type} in tx (main spec §7.5): the worker
// queue bound to that key picks it up once tx commits.
func Enqueue(ctx context.Context, tx pgx.Tx, in NewInput) (Job, error) {
	j, err := Insert(ctx, tx, in)
	if err != nil {
		return Job{}, err
	}
	if _, err := outbox.Append(ctx, tx, outbox.Event{
		Exchange: mq.ExchangeJobs, RoutingKey: "job." + j.Type, AggregateType: "job", AggregateID: j.ID.String(),
		TenantID: j.TenantID, Payload: Command{JobID: j.ID, Type: j.Type, Params: j.Params, Scope: in.Scope},
	}); err != nil {
		return Job{}, err
	}
	return j, nil
}

// Start marks a queued job running (a redelivered command finds it running and keeps going).
func Start(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Job, error) {
	row, err := jobsdb.New(tx).StartJob(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotActive
	}
	if err != nil {
		return Job{}, fmt.Errorf("jobs: start: %w", err)
	}
	j := fromRow(row)
	return j, notify(ctx, tx, j)
}

// SetProgress stores {done,total} on a running job.
func SetProgress(ctx context.Context, tx pgx.Tx, id uuid.UUID, p Progress) (Job, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return Job{}, err
	}
	row, err := jobsdb.New(tx).SetJobProgress(ctx, jobsdb.SetJobProgressParams{ID: id, Progress: b})
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotActive
	}
	if err != nil {
		return Job{}, fmt.Errorf("jobs: progress: %w", err)
	}
	j := fromRow(row)
	return j, notify(ctx, tx, j)
}

// Succeed ends a job with its result (a JSON object or nil).
func Succeed(ctx context.Context, tx pgx.Tx, id uuid.UUID, result any) (Job, error) {
	return finish(ctx, tx, id, StatusSucceeded, result, nil)
}

// Fail ends a job with an error text for the caller; result may carry what was done before it.
func Fail(ctx context.Context, tx pgx.Tx, id uuid.UUID, cause string, result any) (Job, error) {
	return finish(ctx, tx, id, StatusFailed, result, &cause)
}

func finish(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string, result any, cause *string) (Job, error) {
	var res []byte
	if result != nil {
		var err error
		if res, err = object(result); err != nil {
			return Job{}, err
		}
	}
	row, err := jobsdb.New(tx).FinishJob(ctx, jobsdb.FinishJobParams{ID: id, Status: status, Result: res, Error: cause})
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotActive
	}
	if err != nil {
		return Job{}, fmt.Errorf("jobs: finish: %w", err)
	}
	j := fromRow(row)
	return j, notify(ctx, tx, j)
}

// ErrNotActive is returned for a job that is missing or already finished.
var ErrNotActive = errors.New("jobs: the job is not queued or running")

// notify appends job.updated with the full job on user:{owner}; scheduler jobs have no owner and no
// topic (platform principals list them).
func notify(ctx context.Context, tx pgx.Tx, j Job) error {
	if j.OwnerUserID == nil {
		return nil
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: RoutingKeyUpdated, AggregateType: "job", AggregateID: j.ID.String(), TenantID: j.TenantID,
		Payload: j, Topics: []string{"user:" + j.OwnerUserID.String()},
	})
	return err
}

// Get reads one job.
func Get(ctx context.Context, q jobsdb.DBTX, id uuid.UUID) (Job, bool, error) {
	row, err := jobsdb.New(q).GetJob(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("jobs: get: %w", err)
	}
	return fromRow(row), true, nil
}

// Service ties the jobs table to the lock namespace for the job endpoints.
type Service struct {
	pool  db.Beginner
	query jobsdb.DBTX
	locks Locker
}

// NewService builds the service on the logitrack_app pool (used both to begin transactions and for
// reads) and the job locker.
func NewService(pool interface {
	db.Beginner
	jobsdb.DBTX
}, locks Locker) *Service {
	return &Service{pool: pool, query: pool, locks: locks}
}

// SubmitInput is a job request from an endpoint.
type SubmitInput struct {
	NewInput
	// Command false creates only the row, for jobs the scheduler runs itself (queue.replay).
	Command bool
	// InTx runs in the same transaction after the row exists, e.g. an in-transaction audit row. That
	// transaction is a system one (db.WithSystem, app.bypass_tenant = on), so InTx may only touch
	// RLS-exempt tables or call security.Append; a request that must read tenant rows does that read
	// under the principal's transaction (WithPrincipal, T07) before it calls Submit (Appendix C §C.3.2).
	// tools/analyzers/withsystem reports an InTx hook set outside the WithSystem allow-list.
	InTx func(ctx context.Context, tx pgx.Tx, j Job) error
}

// Submit takes lock:job:{type}:{scope} (SET NX, LockTTL) and creates the job in one system
// transaction (main spec §7.5): jobs has no RLS (R67) and the replay's security_events append needs
// bypass, so the request path runs it under WithSystem after Go authorization (Appendix C §C.3.2;
// kept by T07, since WithPrincipal refuses a writable bypass). A held lock is 409 already_exists with
// details.jobId of the running job; the lock is released if the transaction fails. A Redis failure is
// 503 unavailable: the lock is the only guard against a second concurrent run, so the request is
// refused rather than risk one.
func (s *Service) Submit(ctx context.Context, in SubmitInput) (Job, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Job{}, err
	}
	in.ID = id
	key := s.locks.JobKey(in.Type, in.Scope)
	ok, holder, err := s.locks.Acquire(ctx, key, id.String(), LockTTL)
	if err != nil {
		return Job{}, httpx.ErrUnavailable("job lock unavailable").Wrap(err)
	}
	if !ok {
		return Job{}, httpx.NewError(http.StatusConflict, CodeAlreadyExists, "a job of this type is already running").
			WithDetails(map[string]any{"jobId": holder})
	}
	var j Job
	err = db.WithSystem(ctx, s.pool, in.TenantID, func(tx pgx.Tx) error {
		var err error
		if in.Command {
			j, err = Enqueue(ctx, tx, in.NewInput)
		} else {
			j, err = Insert(ctx, tx, in.NewInput)
		}
		if err != nil {
			return err
		}
		if in.InTx != nil {
			return in.InTx(ctx, tx, j)
		}
		return nil
	})
	if err != nil {
		_ = s.locks.Release(context.WithoutCancel(ctx), key, id.String())
		return Job{}, err
	}
	return j, nil
}

// Release frees the lock of a finished job (only if it still holds it).
func (s *Service) Release(ctx context.Context, jobType, scope string, id uuid.UUID) error {
	return s.locks.Release(ctx, s.locks.JobKey(jobType, scope), id.String())
}

// CodeAlreadyExists is the 409 code of a held job lock (Appendix B §B.1.5).
const CodeAlreadyExists = "already_exists"

func object(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	var b []byte
	switch x := v.(type) {
	case json.RawMessage:
		b = x
	case []byte:
		b = x
	default:
		var err error
		if b, err = json.Marshal(v); err != nil {
			return nil, fmt.Errorf("jobs: encode: %w", err)
		}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil, errors.New("jobs: params and results are JSON objects")
	}
	return b, nil
}
