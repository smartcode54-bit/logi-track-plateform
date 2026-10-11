package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/render"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// ShopeePhoto is one signed run-sheet of a trip (runsheet_received, stop_{n}_runsheet_received).
type ShopeePhoto struct {
	PhotoType string    `json:"photoType"`
	FileID    uuid.UUID `json:"fileId"`
}

// ShopeeTrip is one delivered trip of the Shopee Express report (ShopeeReportTripRow, billing.ts:1166-1180): price-free.
type ShopeeTrip struct {
	ID                   uuid.UUID     `json:"id"`
	TripNo               string        `json:"tripNo"`
	DeliveredAt          *time.Time    `json:"deliveredAt"`
	DriverID             *uuid.UUID    `json:"driverId"`
	DriverName           string        `json:"driverName"`
	OriginDisplay        string        `json:"originDisplay"`
	DestinationDisplay   string        `json:"destinationDisplay"`
	VehicleClass         *string       `json:"vehicleClass"`
	TruckLicensePlate    *string       `json:"truckLicensePlate"`
	ParcelCount          *int32        `json:"parcelCount"`
	SignedRunsheetPhotos []ShopeePhoto `json:"signedRunsheetPhotos"`
	changedAt            time.Time
}

// ShopeeReport is the answer of GET /v1/billing/shopee-report.
type ShopeeReport struct {
	CustomerID        string       `json:"customerId"`
	BillingPartyID    *uuid.UUID   `json:"billingPartyId"`
	Year              int          `json:"year"`
	Month             int          `json:"month"`
	Half              string       `json:"half"`
	PeriodFrom        time.Time    `json:"periodFrom"`
	PeriodTo          time.Time    `json:"periodTo"`
	Trips             []ShopeeTrip `json:"trips"`
	TripCount         int          `json:"tripCount"`
	DriverCount       int          `json:"driverCount"`
	PhotoCount        int          `json:"photoCount"`
	MissingPhotoCount int          `json:"missingPhotoCount"`
}

// ShopeeInput are the query parameters of the two Shopee report routes.
type ShopeeInput struct {
	CustomerID  string
	Year, Month int
	Half        string
}

var shopeeHalves = []string{string(render.HalfFull), string(render.HalfFirst), string(render.HalfSecond)}

// shopeeBounds are the Bangkok bounds of a month or half-month: the first round is days 1-15, the second day 16 to the
// month's end (billing.ts:1257-1262, which used the browser's zone).
func shopeeBounds(year, month int, half string) (time.Time, time.Time) {
	from, to := bangkokMonth(year, month)
	mid := time.Date(year, time.Month(month), 16, 0, 0, 0, 0, clock.Bangkok)
	switch half {
	case string(render.HalfFirst):
		return from, mid
	case string(render.HalfSecond):
		return mid, to
	}
	return from, to
}

// shopeeScope is a resolved Shopee report request.
type shopeeScope struct {
	party       billingdb.ResolvePartyRow
	found       bool
	year, month int
	half        string
	from, to    time.Time
	all         bool
	tenants     []uuid.UUID
	reachDigest string
}

func (s *Service) shopeeScope(ctx context.Context, p *authz.Principal, in ShopeeInput) (shopeeScope, error) {
	var ck checker
	ck.period(in.Year, in.Month)
	id := ck.party("customerId", in.CustomerID)
	half := strings.TrimSpace(in.Half)
	if half == "" {
		half = string(render.HalfFull)
	}
	if !slices.Contains(shopeeHalves, half) {
		ck.addParams("half", "invalid", map[string]any{"allowed": shopeeHalves})
	}
	if err := ck.err(); err != nil {
		return shopeeScope{}, err
	}
	sc := shopeeScope{year: in.Year, month: in.Month, half: half}
	sc.from, sc.to = shopeeBounds(in.Year, in.Month, half)
	sc.all, sc.tenants = p.TenantReach()
	slices.SortFunc(sc.tenants, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	sc.reachDigest = reachDigest(sc.all, sc.tenants)
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		var err error
		sc.party, sc.found, err = resolveParty(ctx, q, id)
		return err
	})
	return sc, err
}

// reachDigest names a tenant reach in the render's parameters, so a render is reused only for callers that see the same
// trips.
func reachDigest(all bool, tenants []uuid.UUID) string {
	if all {
		return "all"
	}
	h := sha256.New()
	for _, t := range tenants {
		h.Write([]byte(t.String()))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// shopeeTrips reads the report's trips and photos in tx (the request's, or the worker's READ ONLY system one with the
// same reach) and their rows version: the count and the latest change of a trip or its photos, which tells whether a
// stored render is still current.
func shopeeTrips(ctx context.Context, q *billingdb.Queries, party uuid.UUID, from, to time.Time, all bool,
	tenants []uuid.UUID) ([]ShopeeTrip, string, error) {
	rows, err := q.ShopeeReportTrips(ctx, billingdb.ShopeeReportTripsParams{PeriodFrom: from, PeriodTo: to, PartyID: party,
		AllTenants: all, TenantIds: tenants})
	if err != nil {
		return nil, "", err
	}
	hubs, err := q.DisplayHubs(ctx)
	if err != nil {
		return nil, "", err
	}
	disp := newHubDisplay(hubs)
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	photos := map[uuid.UUID][]ShopeePhoto{}
	if len(ids) > 0 {
		ps, err := q.ShopeeReportPhotos(ctx, ids)
		if err != nil {
			return nil, "", err
		}
		for _, ph := range ps {
			photos[ph.TripID] = append(photos[ph.TripID], ShopeePhoto{PhotoType: ph.PhotoType, FileID: ph.FileID})
		}
	}
	out := make([]ShopeeTrip, 0, len(rows))
	var latest time.Time
	for _, r := range rows {
		name := driverName(r.FullNameTh, r.FirstName, r.LastName, r.DriverEmail)
		if name == "" {
			name = "-"
		}
		t := ShopeeTrip{ID: r.ID, TripNo: r.TripNo, DeliveredAt: timeUTC(r.DeliveredAt), DriverID: r.DriverID, DriverName: name,
			OriginDisplay:      disp.name(firstNonBlank(deref(r.LookupHubCode), deref(r.SourceHubRaw), deref(r.OriginRaw))),
			DestinationDisplay: disp.name(firstNonBlank(deref(r.LookupDestinationCode), deref(r.DestinationRaw), deref(r.TripDestinationRaw))),
			VehicleClass:       firstStr(r.TruckType, r.TripVehicleClass), TruckLicensePlate: firstStr(r.TaskPlate, r.TripPlate),
			ParcelCount: r.ParcelCount, SignedRunsheetPhotos: photos[r.ID], changedAt: r.ChangedAt}
		if t.SignedRunsheetPhotos == nil {
			t.SignedRunsheetPhotos = []ShopeePhoto{}
		}
		if r.ChangedAt.After(latest) {
			latest = r.ChangedAt
		}
		out = append(out, t)
	}
	// Delivery order, then the driver's name (the browser compared names with the Thai locale; the code points order
	// Thai consonants the same way).
	slices.SortStableFunc(out, func(a, b ShopeeTrip) int {
		if c := cmpMillis(a.DeliveredAt, b.DeliveredAt); c != 0 {
			return c
		}
		return strings.Compare(a.DriverName, b.DriverName)
	})
	version := strconv.Itoa(len(out)) + ":" + strconv.FormatInt(latest.UnixMicro(), 10)
	return out, version, nil
}

// ShopeeReport is GET /v1/billing/shopee-report (accounting:shopee_report; fetchShopeeExpressReportTrips,
// billing.ts:1249-1363): every delivered trip of one customer in a Bangkok month or half-month, priced or not (the
// customer resolves per trip: its billing party, else the task's source then destination link), filtered on the server
// instead of reading every customer's trips. Price-free; under the caller's RLS. An id that names no party is an empty
// report.
func (s *Service) ShopeeReport(ctx context.Context, p *authz.Principal, in ShopeeInput) (*ShopeeReport, error) {
	sc, err := s.shopeeScope(ctx, p, in)
	if err != nil {
		return nil, err
	}
	out := &ShopeeReport{CustomerID: strings.TrimSpace(in.CustomerID), Year: in.Year, Month: in.Month, Half: sc.half,
		PeriodFrom: sc.from.UTC(), PeriodTo: sc.to.UTC(), Trips: []ShopeeTrip{}}
	if !sc.found {
		return out, nil
	}
	party := sc.party.ID
	out.BillingPartyID = &party
	err = s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		var err error
		out.Trips, _, err = shopeeTrips(ctx, q, party, sc.from, sc.to, sc.all, sc.tenants)
		return err
	})
	if err != nil {
		return nil, err
	}
	drivers := map[string]bool{}
	for _, t := range out.Trips {
		key := strings.ToLower(firstNonBlank(idStr(t.DriverID), t.DriverName, "-"))
		drivers[key] = true
		out.PhotoCount += len(t.SignedRunsheetPhotos)
		if len(t.SignedRunsheetPhotos) == 0 {
			out.MissingPhotoCount++
		}
	}
	out.TripCount, out.DriverCount = len(out.Trips), len(drivers)
	return out, nil
}

// shopeeParams are the parameters of a documents.shopee-report job; the first five also find a reusable render.
type shopeeParams struct {
	PartyID    uuid.UUID   `json:"partyId"`
	Year       int         `json:"year"`
	Month      int         `json:"month"`
	Half       string      `json:"half"`
	Reach      string      `json:"reach"`
	AllTenants bool        `json:"allTenants"`
	TenantIDs  []uuid.UUID `json:"tenantIds"`
}

// shopeeResult is the result of a finished render.
type shopeeResult struct {
	FileID            uuid.UUID `json:"fileId"`
	Key               string    `json:"key"`
	FileName          string    `json:"fileName"`
	RowsVersion       string    `json:"rowsVersion"`
	TripCount         int       `json:"tripCount"`
	PhotoCount        int       `json:"photoCount"`
	MissingPhotoCount int       `json:"missingPhotoCount"`
}

// ShopeePDF is the outcome of GET /v1/billing/shopee-report.pdf: the URL of a current render (302), or the render job
// that was started or is running (202).
type ShopeePDF struct {
	URL   string
	JobID uuid.UUID
}

// ShopeeReportPDF is GET /v1/billing/shopee-report.pdf (accounting:shopee_report; replaces the browser's jsPDF report,
// web:lib/shopeeExpressReport.ts): the newest finished render for these parameters and the caller's reach when its
// rows version is still the period's (302 to a signed download), else a documents.shopee-report job (202 {jobId};
// lock:job:documents.shopee-report:{partyId}:{yyyymm}:{half}, a render in progress answers its own jobId). The page
// waits for job.updated (or polls GET /v1/jobs/{id}) and asks again. A caller without a staff tenant reach is 403: the
// worker renders the trips of the reach the request names.
func (s *Service) ShopeeReportPDF(ctx context.Context, p *authz.Principal, in ShopeeInput) (ShopeePDF, error) {
	sc, err := s.shopeeScope(ctx, p, in)
	if err != nil {
		return ShopeePDF{}, err
	}
	if !sc.found {
		return ShopeePDF{}, httpx.ErrNotFound()
	}
	if !sc.all && len(sc.tenants) == 0 {
		return ShopeePDF{}, authz.ErrPermissionDenied("the report is rendered for a staff tenant reach")
	}
	if s.jobs == nil || s.files == nil {
		return ShopeePDF{}, httpx.ErrUnavailable("document rendering is not available")
	}
	params := shopeeParams{PartyID: sc.party.ID, Year: sc.year, Month: sc.month, Half: sc.half, Reach: sc.reachDigest,
		AllTenants: sc.all, TenantIDs: sc.tenants}
	match, _ := json.Marshal(map[string]any{"partyId": params.PartyID, "year": params.Year, "month": params.Month,
		"half": params.Half, "reach": params.Reach})
	var version string
	var latest *billingdb.LatestShopeeRenderRow
	err = s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		var err error
		if _, version, err = shopeeTrips(ctx, q, sc.party.ID, sc.from, sc.to, sc.all, sc.tenants); err != nil {
			return err
		}
		r, err := q.LatestShopeeRender(ctx, match)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		latest = &r
		return err
	})
	if err != nil {
		return ShopeePDF{}, err
	}
	if latest != nil {
		var res shopeeResult
		if json.Unmarshal(latest.Result, &res) == nil && res.FileID != uuid.Nil && res.RowsVersion == version {
			u, _, err := s.files.SignedDownload(ctx, res.FileID, 0, storage.GetOptions{
				ContentDisposition: `attachment; filename="` + res.FileName + `"`})
			if err == nil {
				return ShopeePDF{URL: u}, nil
			}
			if !errors.Is(err, storage.ErrObjectNotFound) {
				return ShopeePDF{}, err
			}
		}
	}
	scope := sc.party.ID.String() + ":" + fmt.Sprintf("%04d%02d", sc.year, sc.month) + ":" + sc.half
	j, err := s.jobs.Submit(ctx, jobs.SubmitInput{
		NewInput: jobs.NewInput{Type: jobs.TypeDocumentsShopeeReport, OwnerUserID: actor(p), TenantID: p.EffectiveTenant(),
			Params: params, Scope: scope},
		Command: true,
	})
	var he *httpx.Error
	if errors.As(err, &he) && he.Code == jobs.CodeAlreadyExists {
		if id, perr := uuid.Parse(fmt.Sprint(he.Details["jobId"])); perr == nil {
			return ShopeePDF{JobID: id}, nil
		}
	}
	if err != nil {
		return ShopeePDF{}, err
	}
	return ShopeePDF{JobID: j.ID}, nil
}

// shopee runs a documents.shopee-report job: the report of the job's parameters, rendered with the photos read from
// storage (downscaled; an unreadable one left out, as the browser skipped it), stored under
// documents/reports/{jobId}/ and the job's result naming the file and the rows version it was rendered from.
func (j *DocumentJob) shopee(ctx context.Context, d *mq.Delivery) error {
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
		j.release(ctx, jobs.TypeDocumentsShopeeReport, cmd)
		return nil
	} else if err != nil {
		return err
	}
	var params shopeeParams
	if json.Unmarshal(job.Params, &params) != nil || params.PartyID == uuid.Nil {
		return j.failJob(ctx, cmd, job.ID, "the job names no customer")
	}
	from, to := shopeeBounds(params.Year, params.Month, params.Half)
	var trips []ShopeeTrip
	var version, ownerName string
	var party billingdb.ResolvePartyRow
	err = readShared(ctx, j.d.Pool, func(q *billingdb.Queries) error {
		var err error
		if party, err = q.ResolveParty(ctx, params.PartyID); err != nil {
			return err
		}
		if trips, version, err = shopeeTrips(ctx, q, params.PartyID, from, to, params.AllTenants, params.TenantIDs); err != nil {
			return err
		}
		ownerName, err = q.OwnerCompanyName(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return j.failJob(ctx, cmd, job.ID, "the customer no longer exists")
	}
	if err != nil {
		return err
	}
	in := render.ShopeeInput{CustomerName: firstNonBlank(deref(party.CustomerName), deref(party.TenantName)),
		ProviderName: ownerName, Year: params.Year, Month: params.Month, Half: render.Half(params.Half), IssuedAt: j.d.Now()}
	res := shopeeResult{RowsVersion: version, TripCount: len(trips)}
	for _, t := range trips {
		row := render.ShopeeRow{ID: t.ID.String(), TripNo: t.TripNo, DriverKey: idStr(t.DriverID), DriverName: t.DriverName,
			Origin: t.OriginDisplay, Destination: t.DestinationDisplay, VehicleClass: deref(t.VehicleClass),
			Plate: deref(t.TruckLicensePlate), ParcelCount: t.ParcelCount, PhotoCount: len(t.SignedRunsheetPhotos)}
		if t.DeliveredAt != nil {
			row.DeliveredAt = *t.DeliveredAt
		}
		res.PhotoCount += row.PhotoCount
		if row.PhotoCount == 0 {
			res.MissingPhotoCount++
		}
		for _, ph := range t.SignedRunsheetPhotos {
			body, _, err := j.d.Files.ReadFile(ctx, ph.FileID)
			if err != nil {
				j.d.Log.Warn().Err(err).Str("file_id", ph.FileID.String()).Msg("shopee report: photo left out")
				continue
			}
			img, err := render.Downscale(body, render.PhotoMaxDim, render.PhotoJPEGQuality)
			if err != nil {
				j.d.Log.Warn().Err(err).Str("file_id", ph.FileID.String()).Msg("shopee report: photo left out")
				continue
			}
			row.Photos = append(row.Photos, img)
		}
		in.Rows = append(in.Rows, row)
	}
	pdf, _, err := render.ShopeeReportPDF(j.d.Fonts, in)
	if err != nil {
		return j.failJob(ctx, cmd, job.ID, "render: "+err.Error())
	}
	code := firstNonBlank(deref(party.CustomerCode), deref(party.TenantCode), "customer")
	suffix := map[string]string{string(render.HalfFirst): "_r1", string(render.HalfSecond): "_r2"}[params.Half]
	res.FileName = "shopee_express_report_" + unsafeName.ReplaceAllString(code, "") + "_" +
		fmt.Sprintf("%04d%02d", params.Year, params.Month) + suffix + ".pdf"
	res.Key = "documents/reports/" + job.ID.String() + "/" + res.FileName
	stored, err := j.d.Files.PutServerObject(ctx, storage.ServerObject{Purpose: purposeReport, Key: res.Key,
		TenantID: job.TenantID, OwnerID: job.ID, ContentType: contentTypePDF, Body: pdf})
	if err != nil {
		return err
	}
	at := j.d.Now().UTC().Truncate(time.Microsecond)
	err = db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		id, err := storage.RegisterServerObject(ctx, tx, stored, at)
		if err != nil {
			return err
		}
		res.FileID = id
		_, err = jobs.Succeed(ctx, tx, job.ID, res)
		return err
	})
	if errors.Is(err, jobs.ErrNotActive) {
		j.cleanup(ctx, []storage.StoredObject{stored})
		return nil
	}
	if err != nil {
		j.cleanup(ctx, []storage.StoredObject{stored})
		return err
	}
	j.release(ctx, jobs.TypeDocumentsShopeeReport, cmd)
	j.d.Log.Info().Str("job_id", job.ID.String()).Int("trips", res.TripCount).Int("photos", res.PhotoCount).
		Msg("documents.shopee-report")
	return nil
}
