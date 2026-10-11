package hubs

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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs/hubsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/gmaps"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
)

// QueueDistances is the work queue of the job (Appendix B §B.5.3: group integrations, prefetch 1).
const QueueDistances = "distances.compute"

// RouteDistancesJob is the lt.jobs routing key of the job's command.
const RouteDistancesJob = "job." + jobs.TypeDistancesCompute

// DistanceSpacing is the pause between two Distance Matrix requests (the legacy delayMs, fn:distances.ts:313).
const DistanceSpacing = 200 * time.Millisecond

// MaxRunsPerMonth is the cost cap of the Hub-SOC Distance section of .vibe-rules.md (as of commit 4f552099): at most 4
// runs a Bangkok month keep the Distance Matrix inside the free tier. Queued, running and succeeded runs count, and so
// does a failed run that completed at least one (billed) request; a run refused on its first request (a key or API
// problem) is free. A platform_admin is not capped (the way to run again after a fix).
const MaxRunsPerMonth = 4

// CodeFailedPrecondition is the 409 code of the monthly cap (Appendix B §B.1.5).
const CodeFailedPrecondition = "failed_precondition"

// SubmitDistances is POST /v1/jobs/distances.compute (operations:calculate_distances, a steward key: R60): one
// distances.compute job under lock:job:distances.compute:all (a run in progress is 409 already_exists with its
// jobId), owned by the caller and filed under its effective tenant, so the tenant's staff that hold the key read it
// (developer-spec.md §7.5). The worker runs it. A fifth run in a Bangkok month (MaxRunsPerMonth: failed runs that
// spent requests count) is 409 failed_precondition (details.reason monthly_run_limit) unless the caller is a
// platform_admin.
func (s *Service) SubmitDistances(ctx context.Context, p *authz.Principal) (jobs.Job, error) {
	if s.jobs == nil {
		return jobs.Job{}, httpx.ErrUnavailable("jobs are not available")
	}
	if !p.HasPlatform(authz.PlatformAdmin) {
		y, m := clock.YearMonth(s.now())
		since := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, clock.Bangkok)
		var runs int32
		if err := s.tx(ctx, p, func(_ pgx.Tx, q *hubsdb.Queries) error {
			var err error
			runs, err = q.CountDistanceRuns(ctx, since)
			return err
		}); err != nil {
			return jobs.Job{}, err
		}
		if runs >= MaxRunsPerMonth {
			return jobs.Job{}, httpx.NewError(http.StatusConflict, CodeFailedPrecondition, "the distance matrix ran the most times this month allows").
				WithDetails(map[string]any{"reason": "monthly_run_limit", "limit": MaxRunsPerMonth, "runs": runs})
		}
	}
	return s.jobs.Submit(ctx, jobs.SubmitInput{
		NewInput: jobs.NewInput{Type: jobs.TypeDistancesCompute, OwnerUserID: actor(p), TenantID: p.EffectiveTenant()},
		Command:  true,
	})
}

// --- the worker consumer -------------------------------------------------------------------------------------

// JobDeps are the collaborators of the distances.compute consumer.
type JobDeps struct {
	// Pool is the logitrack_app pool: the job has no request principal and writes in db.WithSystem.
	Pool db.Beginner
	// Matrix is the Distance Matrix client; nil when DISTANCE_MATRIX_ENABLED is off (the job then fails, naming it).
	Matrix gmaps.DistanceMatrix
	// Locks releases lock:job:distances.compute:all when the job ends.
	Locks jobs.Locker
	// Spacing is the pause between two requests (default DistanceSpacing).
	Spacing time.Duration
	// Sleep waits (default: a timer that honours ctx); tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Now dates the run (default time.Now).
	Now func() time.Time
	Log zerolog.Logger
}

// DistanceJob is the distances.compute consumer of the worker.
type DistanceJob struct {
	d JobDeps
}

// NewDistanceJob builds the consumer.
func NewDistanceJob(d JobDeps) (*DistanceJob, error) {
	if d.Pool == nil {
		return nil, errors.New("hubs: the distance job needs a pool")
	}
	if d.Spacing <= 0 {
		d.Spacing = DistanceSpacing
	}
	if d.Sleep == nil {
		d.Sleep = sleep
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &DistanceJob{d: d}, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Registration is the consumer of queue distances.compute (one delivery at a time; a full run makes a few dozen
// requests, so the handler gets the legacy callable's 540 s and more).
func (j *DistanceJob) Registration() mq.Registration {
	return mq.Registration{Queue: QueueDistances, Handler: j.Handle, Timeout: 15 * time.Minute}
}

// Result is the result of a succeeded run (computeHubSocDistances' answer, plus the per-network counts).
type Result struct {
	Written      int                     `json:"written"`
	HubsCount    int                     `json:"hubsCount"`
	SocsCount    int                     `json:"socsCount"`
	Requests     int                     `json:"requests"`
	CalculatedAt time.Time               `json:"calculatedAt"`
	Networks     map[string]NetworkCount `json:"networks"`
	Warnings     []string                `json:"warnings,omitempty"`
}

// NetworkCount are the points and pairs of one network.
type NetworkCount struct {
	Hubs  int `json:"hubs"`
	Socs  int `json:"socs"`
	Pairs int `json:"pairs"`
}

// Failure texts of a run that cannot compute (the job's error; the legacy callable's messages).
const (
	ErrTextDisabled = "the Distance Matrix is off on this deployment (DISTANCE_MATRIX_ENABLED, GOOGLE_MAPS_SERVER_API_KEY)"
	ErrTextNoSocs   = "No SOCs with coordinates"
	ErrTextNoHubs   = "No Hubs with coordinates"
	ErrTextNoPairs  = "No Hub–SOC pairs computed (each network needs at least one Hub and one SOC with coordinates)."
	ErrTextManySocs = "Too many SOCs (max 25 per request) in one network group."
)

// Handle runs one job.distances.compute command: the jobs row goes running, the points are read, the matrix is asked
// network by network, then every pair, settings distances_last_calculated, settings.changed and the job's success are
// written in one transaction; lock:job:distances.compute:all is released at the end. A run that cannot compute (the
// API is off or refuses, no pairs) ends failed without writing a pair; a database failure is retried by the ladder.
func (j *DistanceJob) Handle(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey != RouteDistancesJob {
		return mq.Permanent(fmt.Errorf("distances.compute: unexpected routing key %s", d.RoutingKey))
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
		var spent any // what a failed run still cost: its requests were billed
		if res.Requests > 0 {
			spent = map[string]any{"requests": res.Requests, "hubsCount": res.HubsCount, "socsCount": res.SocsCount}
		}
		err = db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
			_, err := jobs.Fail(ctx, tx, job.ID, cause, spent)
			return err
		})
	}
	if errors.Is(err, jobs.ErrNotActive) {
		err = nil
	}
	if err == nil {
		j.release(ctx, cmd)
		j.d.Log.Info().Str("job_id", job.ID.String()).Int("written", res.Written).Int("requests", res.Requests).
			Str("failure", cause).Msg("distances.compute")
	}
	return err
}

func (j *DistanceJob) release(ctx context.Context, cmd jobs.Command) {
	if j.d.Locks == nil {
		return
	}
	if err := j.d.Locks.Release(context.WithoutCancel(ctx), j.d.Locks.JobKey(jobs.TypeDistancesCompute, cmd.Scope), cmd.JobID.String()); err != nil {
		j.d.Log.Warn().Err(err).Msg("distances.compute: lock release failed; it expires on its own")
	}
}

// point is a hub or SOC with coordinates.
type point struct {
	id      uuid.UUID
	code    string
	key     string // SOC key (NormalizeSocKey) of a SOC
	network string
	lat     float64
	lng     float64
}

// pairRow is one computed pair.
type pairRow struct {
	hub, soc  point
	direction string
	meters    int
	seconds   int
}

// Points splits the job input as getHubsAndSocs did (fn:distances.ts): HUBs, and SOCs without the stand-by codes
// starting with 0, one SOC per network and key (the first in document order wins).
func points(rows []hubsdb.ListDistancePointsRow) (hubs, socs []point) {
	seen := map[string]bool{}
	for _, r := range rows {
		p := point{id: r.ID, code: r.SourceID, network: r.Network, lat: r.Latitude, lng: r.Longitude}
		if p.network != NetworkSPK {
			p.network = NetworkSPX
		}
		if NormalizeStationType(r.StationType) == StationHub {
			hubs = append(hubs, p)
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(r.SourceID), "0") {
			continue // SOC ids 0* are stand-by points, never computed
		}
		p.key = NormalizeSocKey(r.SourceID)
		if p.key == "" || seen[p.network+"_"+p.key] {
			continue
		}
		seen[p.network+"_"+p.key] = true
		socs = append(socs, p)
	}
	return hubs, socs
}

// request is one planned Distance Matrix call.
type request struct {
	direction    string
	origins      []point
	destinations []point
}

// plan is computeRowsForNetwork's batching (fn:distances.ts:154-237) for one network: HUB -> SOC with every SOC as a
// destination and as many hubs as fit in 25 origins and 100 elements; SOC -> HUB with SOC chunks of
// max(1, 100 / min(hubs, 25)) origins against hub chunks of up to 25 destinations. More than 25 SOCs in one network is
// refused (the API's destination limit), as it was.
func plan(hubs, socs []point) ([]request, string) {
	if len(hubs) == 0 || len(socs) == 0 {
		return nil, ""
	}
	if len(socs) > gmaps.MaxDestinations {
		return nil, ErrTextManySocs
	}
	var out []request
	perHub := min(gmaps.MaxOrigins, gmaps.MaxElements/len(socs))
	for start := 0; start < len(hubs); start += perHub {
		out = append(out, request{direction: DirectionHubToSOC, origins: hubs[start:min(start+perHub, len(hubs))], destinations: socs})
	}
	socBatch := min(gmaps.MaxOrigins, max(1, gmaps.MaxElements/min(len(hubs), gmaps.MaxDestinations)))
	hubBatch := min(gmaps.MaxDestinations, len(hubs), gmaps.MaxElements/socBatch)
	for s := 0; s < len(socs); s += socBatch {
		chunk := socs[s:min(s+socBatch, len(socs))]
		for h := 0; h < len(hubs); h += hubBatch {
			out = append(out, request{direction: DirectionSOCToHub, origins: chunk, destinations: hubs[h:min(h+hubBatch, len(hubs))]})
		}
	}
	return out, ""
}

func latLngs(ps []point) []gmaps.LatLng {
	out := make([]gmaps.LatLng, len(ps))
	for i, p := range ps {
		out[i] = gmaps.LatLng{Lat: p.lat, Lng: p.lng}
	}
	return out
}

// run computes and writes; cause is the failure text of a run that ends failed (nothing written), err a failure to
// retry.
func (j *DistanceJob) run(ctx context.Context, job jobs.Job) (Result, string, error) {
	res := Result{Networks: map[string]NetworkCount{}}
	if j.d.Matrix == nil {
		return res, ErrTextDisabled, nil
	}
	var rows []hubsdb.ListDistancePointsRow
	if err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		var err error
		rows, err = hubsdb.New(tx).ListDistancePoints(ctx)
		return err
	}); err != nil {
		return res, "", err
	}
	hubs, socs := points(rows)
	res.HubsCount, res.SocsCount = len(hubs), len(socs)
	if len(socs) == 0 {
		return res, ErrTextNoSocs, nil
	}
	if len(hubs) == 0 {
		return res, ErrTextNoHubs, nil
	}
	var reqs []request
	for _, n := range []string{NetworkSPX, NetworkSPK} { // never across networks
		var gh, gs []point
		for _, h := range hubs {
			if h.network == n {
				gh = append(gh, h)
			}
		}
		for _, s := range socs {
			if s.network == n {
				gs = append(gs, s)
			}
		}
		res.Networks[n] = NetworkCount{Hubs: len(gh), Socs: len(gs)}
		r, warn := plan(gh, gs)
		if warn != "" {
			res.Warnings = append(res.Warnings, n+": "+warn)
		}
		reqs = append(reqs, r...)
	}
	var pairs []pairRow
	for i, r := range reqs {
		if i > 0 {
			if err := j.d.Sleep(ctx, j.d.Spacing); err != nil {
				return res, "", err
			}
		}
		m, err := j.d.Matrix.DistanceMatrix(ctx, latLngs(r.origins), latLngs(r.destinations))
		if err != nil {
			if ctx.Err() != nil {
				return res, "", ctx.Err() // shutdown: the redelivered command runs again
			}
			return res, err.Error(), nil
		}
		res.Requests++
		pairs = append(pairs, collect(r, m)...)
		if err := j.progress(ctx, job.ID, i+1, len(reqs)); err != nil {
			return res, "", err
		}
	}
	if len(pairs) == 0 {
		cause := ErrTextNoPairs
		if len(res.Warnings) > 0 {
			cause = strings.Join(res.Warnings, " ")
		}
		return res, cause, nil
	}
	for _, p := range pairs {
		c := res.Networks[p.hub.network]
		c.Pairs++
		res.Networks[p.hub.network] = c
	}
	res.Written = len(pairs)
	res.CalculatedAt = j.d.Now().UTC().Truncate(time.Millisecond)
	err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		return j.write(ctx, tx, job, pairs, res)
	})
	if errors.Is(err, jobs.ErrNotActive) {
		return res, "", nil // finished elsewhere meanwhile (a replay); nothing of this run was kept
	}
	return res, "", err
}

// collect reads the matrix of one request into pairs (parseElement: only OK cells with both values).
func collect(r request, m gmaps.Matrix) []pairRow {
	var out []pairRow
	for i, o := range r.origins {
		if i >= len(m.Rows) {
			break
		}
		for k, d := range r.destinations {
			if k >= len(m.Rows[i]) || !m.Rows[i][k].OK {
				continue
			}
			el := m.Rows[i][k]
			p := pairRow{direction: r.direction, meters: el.DistanceMeters, seconds: el.DurationSeconds}
			if r.direction == DirectionHubToSOC {
				p.hub, p.soc = o, d
			} else {
				p.hub, p.soc = d, o
			}
			out = append(out, p)
		}
	}
	return out
}

func (j *DistanceJob) progress(ctx context.Context, id uuid.UUID, done, total int) error {
	err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.SetProgress(ctx, tx, id, jobs.Progress{Done: done, Total: total})
		return err
	})
	if errors.Is(err, jobs.ErrNotActive) {
		return nil
	}
	return err
}

// write stores every pair (both directions, R14; stale pairs kept), settings distances_last_calculated with
// settings.changed (cache only: no realtime topic, its SSE name on global is the mobile settings event), and the job's
// success, in one transaction.
func (j *DistanceJob) write(ctx context.Context, tx pgx.Tx, job jobs.Job, pairs []pairRow, res Result) error {
	n := len(pairs)
	p := hubsdb.UpsertDistancesParams{HubIds: make([]uuid.UUID, n), SocKeys: make([]string, n), Directions: make([]string, n),
		Networks: make([]string, n), DistanceM: make([]int32, n), DistanceKm: make([]float64, n), DurationS: make([]int32, n),
		DurationMin: make([]float64, n), HubLat: make([]float64, n), HubLng: make([]float64, n), SocLat: make([]float64, n),
		SocLng: make([]float64, n), Actor: job.OwnerUserID}
	for i, r := range pairs {
		p.HubIds[i], p.SocKeys[i], p.Directions[i], p.Networks[i] = r.hub.id, r.soc.key, r.direction, r.hub.network
		p.DistanceM[i], p.DurationS[i] = int32(r.meters), int32(r.seconds)
		// Math.round(x * 100) / 100 of the callable (fn:distances.ts:316-318).
		p.DistanceKm[i] = jsmath.Round2(float64(r.meters) / 1000)
		p.DurationMin[i] = jsmath.Round2(float64(r.seconds) / 60)
		p.HubLat[i], p.HubLng[i], p.SocLat[i], p.SocLng[i] = r.hub.lat, r.hub.lng, r.soc.lat, r.soc.lng
	}
	q := hubsdb.New(tx)
	if _, err := q.UpsertDistances(ctx, p); err != nil {
		return err
	}
	value, err := json.Marshal(map[string]any{"at": res.CalculatedAt, "pairs": res.Written, "hubs": res.HubsCount,
		"socs": res.SocsCount, "jobId": job.ID})
	if err != nil {
		return err
	}
	if err := q.UpsertSetting(ctx, hubsdb.UpsertSettingParams{Key: SettingDistancesLastCalculated, Value: value,
		UpdatedBy: job.OwnerUserID}); err != nil {
		return err
	}
	if _, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteSettingsChanged, AggregateType: "settings",
		AggregateID: SettingDistancesLastCalculated, Payload: map[string]any{"key": SettingDistancesLastCalculated}}); err != nil {
		return err
	}
	_, err = jobs.Succeed(ctx, tx, job.ID, res)
	return err
}
