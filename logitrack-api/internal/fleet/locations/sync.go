package locations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/locations/locationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cartrack"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// probeScope is the lock:job scope of the probe (lock:job:cartrack.sync:probe, one probe at a time). The cron fires
// take no job lock: the queue's single active consumer and prefetch 1 already run the sync one command at a time, and
// lock:cron gives each slot one command.
const probeScope = "probe"

// StaleAfter: a scheduled command older than this when the worker takes it (a worker that was down for a while) is
// skipped and succeeds without calling Cartrack; the next slot fetches the fresh positions anyway.
const StaleAfter = 2 * SyncInterval

// Job causes (jobs.error) of a run that could not fetch.
const (
	ErrTextDisabled = "cartrack: off (CARTRACK_API_BASE_URL, CARTRACK_API_USERNAME and CARTRACK_API_PASSWORD are required on the worker)"
)

// syncParams are the params of a cartrack.sync job: a cron fire's scheduledFor, or the probe flag.
type syncParams struct {
	Probe        bool   `json:"probe,omitempty"`
	ScheduledFor string `json:"scheduledFor,omitempty"`
}

// probeOutcome is the result of a probe run.
type probeOutcome struct {
	Probe        bool `json:"probe"`
	HTTPStatus   *int `json:"httpStatus,omitempty"`
	VehicleCount *int `json:"vehicleCount,omitempty"`
}

// SyncResult is the result of a sync run (jobs.result).
type SyncResult struct {
	HTTPStatus     int    `json:"httpStatus,omitempty"`
	Vehicles       int    `json:"vehicles"`       // vehicles Cartrack listed
	Trucks         int    `json:"trucks"`         // trucks with a GPS vehicle id (outside the quarantine tenant)
	Updated        int    `json:"updated"`        // positions written
	Unmatched      int    `json:"unmatched"`      // vehicles of no truck ("not one of our trucks")
	BadCoordinates int    `json:"badCoordinates"` // no numeric position, outside the globe, or 0,0
	Stale          int    `json:"stale"`          // a fix older than the stored one (latest only)
	Tenants        int    `json:"tenants"`        // tenants that got a vehicle_locations.updated event
	Skipped        string `json:"skipped,omitempty"`
	ScheduledFor   string `json:"scheduledFor,omitempty"`
}

// Batch is the payload of vehicle_locations.updated: an outbox payload is a JSON object, so the batch of Appendix B
// §B.4.3 travels as {"positions": [...]}.
type Batch struct {
	Positions []Position `json:"positions"`
}

// Position is one entry of the vehicle_locations.updated batch (Appendix B §B.4.3).
type Position struct {
	TruckID uuid.UUID `json:"truckId"`
	Lat     float64   `json:"lat"`
	Lng     float64   `json:"lng"`
	Speed   *float64  `json:"speed"`
	Heading *float64  `json:"heading"`
	At      time.Time `json:"at"`
}

// Locker releases the probe's lock:job key (jobs.RedisLocker).
type Locker interface {
	JobKey(jobType, scope string) string
	Release(ctx context.Context, key, holder string) error
}

// SyncDeps are the collaborators of the cartrack.sync consumer.
type SyncDeps struct {
	// Pool is the logitrack_app pool (the consumer's system transactions).
	Pool db.Beginner
	// Cartrack is the status client; nil = off (every run ends failed with ErrTextDisabled, no request leaves).
	Cartrack cartrack.Client
	// Locks releases the probe's lock:job key; optional.
	Locks Locker
	// Now is the sync clock (tests pin it); nil = time.Now.
	Now func() time.Time
	Log zerolog.Logger
}

// SyncJob is the cartrack.sync consumer of the worker (P6, R70).
type SyncJob struct {
	d SyncDeps
}

// NewSyncJob builds the consumer. It does no I/O.
func NewSyncJob(d SyncDeps) (*SyncJob, error) {
	if d.Pool == nil {
		return nil, errors.New("locations: the cartrack.sync consumer needs a pool")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &SyncJob{d: d}, nil
}

// Registration is the consumer of queue cartrack.sync (one delivery at a time; the fetch has its own 20 s timeout).
func (j *SyncJob) Registration() mq.Registration {
	return mq.Registration{Queue: QueueCartrack, Handler: j.Handle, Timeout: 2 * time.Minute}
}

// Handle runs one job.cartrack.sync command: the jobs row goes running, the status of every vehicle is fetched once
// (20 s), and in one transaction each position of a known truck is upserted latest-only, one vehicle_locations.updated
// event per tenant is appended (realtime only) and the job succeeds. A failed fetch (no answer, a non-2xx status, a body
// that is not JSON) or the client off ends the job failed and writes nothing, so every truck keeps its last position
// (the legacy sync logged and returned, fn:cartrack.ts:141-146). A probe command fetches the same way and records only
// the HTTP status and the vehicle count. A database failure is retried by the ladder.
func (j *SyncJob) Handle(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey != RouteCartrackJob {
		return mq.Permanent(fmt.Errorf("cartrack.sync: unexpected routing key %s", d.RoutingKey))
	}
	cmd, err := jobs.DecodeCommand(d)
	if err != nil {
		return err
	}
	var params syncParams
	if len(cmd.Params) > 0 {
		_ = json.Unmarshal(cmd.Params, &params) // unknown or malformed params run a plain sync
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
	now := j.d.Now()
	switch {
	case params.Probe:
		err = j.probe(ctx, job)
	case stale(params.ScheduledFor, now):
		err = j.finish(ctx, job, SyncResult{Skipped: "stale_slot", ScheduledFor: params.ScheduledFor})
	default:
		err = j.sync(ctx, job, now)
	}
	if errors.Is(err, jobs.ErrNotActive) {
		err = nil // finished elsewhere meanwhile (a replay); nothing of this run was kept
	}
	if err == nil {
		j.release(ctx, cmd)
	}
	return err
}

// stale reports whether a cron fire's slot is older than StaleAfter at now.
func stale(scheduledFor string, now time.Time) bool {
	if scheduledFor == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339, scheduledFor)
	return err == nil && now.Sub(at) > StaleAfter
}

// probe is a probe run: the HTTP status and vehicle count of one fetch, nothing written.
func (j *SyncJob) probe(ctx context.Context, job jobs.Job) error {
	out := probeOutcome{Probe: true}
	if j.d.Cartrack == nil {
		return j.fail(ctx, job, ErrTextDisabled, out)
	}
	st, err := j.d.Cartrack.VehicleStatus(ctx)
	if err != nil && ctx.Err() != nil {
		return ctx.Err() // shutdown: the redelivered command runs again
	}
	if st.HTTPStatus == 0 { // no answer at all
		cause := "cartrack: no answer"
		if err != nil {
			cause = err.Error()
		}
		return j.fail(ctx, job, cause, out)
	}
	code, n := st.HTTPStatus, len(st.Vehicles)
	out.HTTPStatus, out.VehicleCount = &code, &n
	j.d.Log.Info().Str("job_id", job.ID.String()).Int("http_status", code).Int("vehicles", n).Msg("cartrack.sync probe")
	return db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.Succeed(ctx, tx, job.ID, out)
		return err
	})
}

// sync is a scheduled (or replayed) run.
func (j *SyncJob) sync(ctx context.Context, job jobs.Job, now time.Time) error {
	if j.d.Cartrack == nil {
		return j.fail(ctx, job, ErrTextDisabled, SyncResult{})
	}
	st, err := j.d.Cartrack.VehicleStatus(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return j.fail(ctx, job, err.Error(), SyncResult{HTTPStatus: st.HTTPStatus})
	}
	res := SyncResult{HTTPStatus: st.HTTPStatus, Vehicles: len(st.Vehicles)}
	err = db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		res = SyncResult{HTTPStatus: st.HTTPStatus, Vehicles: len(st.Vehicles)} // a retried transaction starts over
		if err := j.write(ctx, tx, st.Vehicles, now, &res); err != nil {
			return err
		}
		_, err := jobs.Succeed(ctx, tx, job.ID, res)
		return err
	})
	if err == nil {
		j.d.Log.Info().Str("job_id", job.ID.String()).Int("vehicles", res.Vehicles).Int("updated", res.Updated).
			Int("unmatched", res.Unmatched).Int("bad_coordinates", res.BadCoordinates).Int("stale", res.Stale).Msg("cartrack.sync")
	}
	return err
}

// write upserts the positions of the known trucks and appends one realtime event per tenant whose positions moved.
func (j *SyncJob) write(ctx context.Context, tx pgx.Tx, vehicles []cartrack.Vehicle, now time.Time, res *SyncResult) error {
	q := locationsdb.New(tx)
	trucks, err := q.GPSTrucks(ctx)
	if err != nil {
		return err
	}
	res.Trucks = len(trucks)
	byGPS := make(map[string]locationsdb.GPSTrucksRow, len(trucks))
	for _, t := range trucks {
		byGPS[strings.TrimSpace(t.GpsVehicleID)] = t
	}
	// One fix per truck: the newest when the answer lists a vehicle twice.
	type pick struct {
		truck locationsdb.GPSTrucksRow
		v     cartrack.Vehicle
		at    time.Time
		lat   float64
		lng   float64
	}
	picks := map[uuid.UUID]pick{}
	for _, v := range vehicles {
		t, ok := byGPS[v.VehicleID]
		if !ok {
			res.Unmatched++
			continue
		}
		lat, lng, ok := coordinates(v)
		if !ok {
			res.BadCoordinates++
			continue
		}
		at := fixTime(v.EventAt, now)
		if cur, ok := picks[t.ID]; ok && cur.at.After(at) {
			continue
		}
		picks[t.ID] = pick{truck: t, v: v, at: at, lat: lat, lng: lng}
	}
	ids := make([]uuid.UUID, 0, len(picks))
	for id := range picks {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) }) // a stable lock order
	batches := map[uuid.UUID][]Position{}
	for _, id := range ids {
		p := picks[id]
		plate := strings.TrimSpace(p.truck.LicensePlate)
		at := p.at.UTC().Truncate(time.Microsecond)
		_, err := q.UpsertVehicleLocation(ctx, locationsdb.UpsertVehicleLocationParams{
			TruckID: p.truck.ID, TenantID: p.truck.TenantID, GpsVehicleID: p.v.VehicleID, LicensePlate: textOrNil(plate),
			Lat: p.lat, Lng: p.lng, SpeedKmh: within(p.v.SpeedKmh, 0, 1000), Heading: within(p.v.Heading, 0, 360),
			EngineOn: p.v.EngineOn, PositionDescription: textOrNil(p.v.PositionDescription), DriverNameRaw: textOrNil(p.v.DriverName),
			LastGpsAt: &at, OdometerKm: within(p.v.OdometerKm, 0, 1e10), FuelLevel: within(p.v.FuelLevel, 0, 1e5),
			FuelPercentage: within(p.v.FuelPercentage, 0, 100),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			res.Stale++ // the stored fix is newer: latest only
			continue
		}
		if err != nil {
			return fmt.Errorf("cartrack.sync: upsert truck %s: %w", p.truck.ID, err)
		}
		res.Updated++
		batches[p.truck.TenantID] = append(batches[p.truck.TenantID], Position{TruckID: p.truck.ID, Lat: p.lat, Lng: p.lng,
			Speed: within(p.v.SpeedKmh, 0, 1000), Heading: within(p.v.Heading, 0, 360), At: at})
	}
	tenants := make([]uuid.UUID, 0, len(batches))
	for t := range batches {
		tenants = append(tenants, t)
	}
	slices.SortFunc(tenants, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, t := range tenants {
		tid := t
		if _, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteUpdated, AggregateType: "vehicle_locations",
			AggregateID: tid.String(), TenantID: &tid, Payload: Batch{Positions: batches[tid]},
			Topics: []string{realtime.TenantTopic(tid.String(), realtime.FamilyVehicleLocations)}}); err != nil {
			return err
		}
	}
	res.Tenants = len(tenants)
	return nil
}

// coordinates are the position of a vehicle: numeric, on the globe, and not 0,0 (a tracker without a fix); anything
// else is a bad coordinate the sync skips (the legacy sync skipped non-numbers, fn:cartrack.ts:158-160).
func coordinates(v cartrack.Vehicle) (float64, float64, bool) {
	if v.Lat == nil || v.Lng == nil {
		return 0, 0, false
	}
	lat, lng := *v.Lat, *v.Lng
	if math.Abs(lat) > 90 || math.Abs(lng) > 180 || (lat == 0 && lng == 0) {
		return 0, 0, false
	}
	return lat, lng, true
}

// futureSkew bounds a GPS time ahead of the sync clock: a tracker whose clock runs ahead would otherwise keep every
// later real fix out (latest only) until its time passed.
const futureSkew = 5 * time.Minute

// fixTime is the time of a fix: the GPS event time, else (missing, unparseable, or more than futureSkew ahead) the sync
// time, as the legacy sync stamped a missing event_ts (fn:cartrack.ts:175-177).
func fixTime(at *time.Time, now time.Time) time.Time {
	if at == nil || at.After(now.Add(futureSkew)) {
		return now.UTC()
	}
	return at.UTC()
}

// within keeps a measurement inside the range its column holds (numeric(6,1) speed, numeric(5,1) heading, ...); a
// value outside it is unknown (nil) rather than a failed run.
func within(v *float64, lo, hi float64) *float64 {
	if v == nil || *v < lo || *v > hi {
		return nil
	}
	return v
}

func textOrNil(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// finish succeeds a run with res (a skipped slot).
func (j *SyncJob) finish(ctx context.Context, job jobs.Job, res SyncResult) error {
	return db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.Succeed(ctx, tx, job.ID, res)
		return err
	})
}

// fail ends a run failed with cause (the HTTP status only, never a body, a URL or a credential).
func (j *SyncJob) fail(ctx context.Context, job jobs.Job, cause string, result any) error {
	j.d.Log.Warn().Str("job_id", job.ID.String()).Str("failure", cause).Msg("cartrack.sync")
	return db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.Fail(ctx, tx, job.ID, cause, result)
		return err
	})
}

func (j *SyncJob) release(ctx context.Context, cmd jobs.Command) {
	if j.d.Locks == nil || cmd.Scope == "" {
		return
	}
	if err := j.d.Locks.Release(context.WithoutCancel(ctx), j.d.Locks.JobKey(jobs.TypeCartrackSync, cmd.Scope), cmd.JobID.String()); err != nil {
		j.d.Log.Warn().Err(err).Msg("cartrack.sync: lock release failed; it expires on its own")
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
