// Package locations is the live truck positions of T53 (P6, R70; Appendix A §A.2.3, Appendix B §B.2.8): the worker's
// cartrack.sync consumer (SyncJob) that pulls the Cartrack status of every vehicle every three minutes and keeps the
// latest position per truck in vehicle_locations, GET /v1/vehicle-locations (the dashboard map and the current vehicle
// position widget), and the platform's GET /v1/admin/cartrack/probe, which replaces the diagnostic scripts
// functions/scripts/{check-creds,test-cartrack,test-cartrack-full}.js.
//
// The Cartrack credentials live in the worker only (main spec §16.1): the api never holds them, so the probe is a
// cartrack.sync job in probe mode ({"probe": true}) that the worker runs, and the route waits for its outcome.
//
// The reads run in db.WithPrincipal (RLS decides: G-tenant for staff, p_dispatch_read for a dispatcher's recent work);
// the consumer writes in its own system transaction, the only process without a request principal that touches
// vehicle_locations (tools/analyzers/withsystem allow-list).
package locations

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/locations/locationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs/jobsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Queue, routing keys and the realtime event (Appendix B §B.5.2, §B.5.3, §B.4.3).
const (
	// QueueCartrack is the work queue of the sync (group integrations, prefetch 1, single active consumer).
	QueueCartrack = "cartrack.sync"
	// RouteCartrackJob is the lt.jobs command of the */3 cron and of the probe.
	RouteCartrackJob = "job." + jobs.TypeCartrackSync
	// RouteUpdated is the realtime-only event of a sync: one per tenant whose positions moved, on
	// tenant:{tid}:vehicle_locations (ephemeral, no replay), payload {positions: [{truckId, lat, lng, speed, heading, at}]}.
	RouteUpdated = "vehicle_locations.updated"
)

// SyncInterval is the cron interval of cartrack.sync (*/3 * * * *, main spec §7.4).
const SyncInterval = 3 * time.Minute

// MaxTruckIDs bounds ?truckId of GET /v1/vehicle-locations.
const MaxTruckIDs = 500

// Probe timing: the route waits up to ProbeWait for the worker, polling the jobs row every ProbePoll. ProbeWait stays
// below the BFF's upstream timeout (GO_API_INTERNAL_TIMEOUT_MS, 30 s); the fetch has its own 20 s timeout and the probe
// may queue behind one running sync, so a slow answer is a 202 with the job id rather than a gateway timeout.
const (
	ProbeWait = 25 * time.Second
	ProbePoll = 250 * time.Millisecond
)

// Pool is the logitrack_app pool: transactions, and the jobs row reads of the probe (jobs is RLS-exempt).
type Pool interface {
	db.Beginner
	jobsdb.DBTX
}

// Submitter submits the probe job (jobs.Service).
type Submitter interface {
	Submit(ctx context.Context, in jobs.SubmitInput) (jobs.Job, error)
}

// Deps are the collaborators of the api side.
type Deps struct {
	Pool Pool
	// Jobs submits the probe (lock:job:cartrack.sync:probe, one probe at a time); nil leaves the probe 503.
	Jobs Submitter
	Log  zerolog.Logger
	// ProbeWait and ProbePoll override the probe timing (tests); zero = the constants.
	ProbeWait, ProbePoll time.Duration
}

// Service serves /v1/vehicle-locations and /v1/admin/cartrack/probe.
type Service struct {
	pool      Pool
	jobs      Submitter
	log       zerolog.Logger
	probeWait time.Duration
	probePoll time.Duration
}

// New builds the service. It does no I/O.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("locations: a pool is required")
	}
	if d.ProbeWait <= 0 {
		d.ProbeWait = ProbeWait
	}
	if d.ProbePoll <= 0 {
		d.ProbePoll = ProbePoll
	}
	return &Service{pool: d.Pool, jobs: d.Jobs, log: d.Log, probeWait: d.ProbeWait, probePoll: d.ProbePoll}, nil
}

// Location is one row of GET /v1/vehicle-locations: the latest Cartrack position of a truck.
type Location struct {
	TruckID             uuid.UUID  `json:"truckId"`
	TenantID            uuid.UUID  `json:"tenantId"`
	GPSVehicleID        string     `json:"gpsVehicleId"`
	LicensePlate        *string    `json:"licensePlate"`
	Lat                 float64    `json:"lat"`
	Lng                 float64    `json:"lng"`
	SpeedKmh            *float64   `json:"speedKmh"`
	Heading             *float64   `json:"heading"`
	EngineOn            *bool      `json:"engineOn"`
	PositionDescription *string    `json:"positionDescription"`
	DriverName          *string    `json:"driverName"`
	LastGPSAt           *time.Time `json:"lastGpsAt"`
	OdometerKm          *float64   `json:"odometerKm"`
	FuelLevel           *float64   `json:"fuelLevel"`
	FuelPercentage      *float64   `json:"fuelPercentage"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// List is GET /v1/vehicle-locations: the positions p's RLS admits, or only those of truckIDs.
func (s *Service) List(ctx context.Context, p *authz.Principal, truckIDs []uuid.UUID) ([]Location, error) {
	if truckIDs == nil {
		truckIDs = []uuid.UUID{}
	}
	var rows []locationsdb.ListVehicleLocationsRow
	err := db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		var err error
		rows, err = locationsdb.New(tx).ListVehicleLocations(ctx, truckIDs)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Location, len(rows))
	for i, r := range rows {
		out[i] = Location{TruckID: r.TruckID, TenantID: r.TenantID, GPSVehicleID: r.GpsVehicleID, LicensePlate: r.LicensePlate,
			Lat: r.Lat, Lng: r.Lng, SpeedKmh: r.SpeedKmh, Heading: r.Heading, EngineOn: r.EngineOn,
			PositionDescription: r.PositionDescription, DriverName: r.DriverNameRaw, LastGPSAt: utc(r.LastGpsAt),
			OdometerKm: r.OdometerKm, FuelLevel: r.FuelLevel, FuelPercentage: r.FuelPercentage, UpdatedAt: r.UpdatedAt.UTC()}
	}
	return out, nil
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// ProbeResult is the answer of GET /v1/admin/cartrack/probe: the probe job and, once it ended, the HTTP status
// Cartrack answered and how many vehicles it listed (never a body, a URL or a credential).
type ProbeResult struct {
	JobID        uuid.UUID `json:"jobId"`
	Status       string    `json:"status"` // queued | running | succeeded | failed
	HTTPStatus   *int      `json:"httpStatus,omitempty"`
	VehicleCount *int      `json:"vehicleCount,omitempty"`
	Error        *string   `json:"error,omitempty"`
	Done         bool      `json:"-"`
}

// Probe submits a probe run of cartrack.sync and waits for the worker (at most the service's probe wait). A probe that
// is still running is answered with its job id (the caller follows GET /v1/jobs/{id}); a second probe while one runs
// is 409 already_exists with details.jobId.
func (s *Service) Probe(ctx context.Context, p *authz.Principal) (ProbeResult, error) {
	if s.jobs == nil {
		return ProbeResult{}, httpx.ErrUnavailable("the probe is not wired")
	}
	owner := p.UserID
	j, err := s.jobs.Submit(ctx, jobs.SubmitInput{NewInput: jobs.NewInput{Type: jobs.TypeCartrackSync, OwnerUserID: &owner,
		Params: syncParams{Probe: true}, Scope: probeScope}, Command: true})
	if err != nil {
		return ProbeResult{}, err
	}
	deadline := time.Now().Add(s.probeWait)
	tick := time.NewTicker(s.probePoll)
	defer tick.Stop()
	for {
		cur, ok, err := jobs.Get(ctx, s.pool, j.ID)
		if err != nil {
			return ProbeResult{}, err
		}
		if !ok {
			return ProbeResult{}, httpx.ErrInternal().Wrap(errors.New("locations: the probe job disappeared"))
		}
		if res, done := probeResult(cur); done || !time.Now().Before(deadline) {
			return res, nil
		}
		select {
		case <-ctx.Done():
			return ProbeResult{}, ctx.Err()
		case <-tick.C:
		}
	}
}

// probeResult reads the outcome of a probe job row.
func probeResult(j jobs.Job) (ProbeResult, bool) {
	out := ProbeResult{JobID: j.ID, Status: j.Status, Error: j.Error}
	if j.Status != jobs.StatusSucceeded && j.Status != jobs.StatusFailed {
		return out, false
	}
	out.Done = true
	var r probeOutcome
	if len(j.Result) > 0 && jsonUnmarshal(j.Result, &r) == nil {
		out.HTTPStatus, out.VehicleCount = r.HTTPStatus, r.VehicleCount
	}
	return out, true
}

// probeStatusCode is the HTTP status of the probe route: 200 once the job ended, 202 while it runs.
func probeStatusCode(r ProbeResult) int {
	if r.Done {
		return http.StatusOK
	}
	return http.StatusAccepted
}
