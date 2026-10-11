package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// The admin standby backfill (POST /v1/standby/backfill, Appendix B §B.2.13; standby-backfill-dialog.tsx:283-357) and
// the admin incident report (POST /v1/incidents, §B.2.12; ReportIncidentModal.tsx:61-90). Both create a row in a
// tenant-stamped table: on the PostgreSQL path Stamp resolves its tenant in the request transaction before the insert
// (standby: task -> trip -> driver; incident: trip -> driver; T28), on the write-back path the document is created
// under a minted uuidv7 and the mirror stamps the row (R25).

// Routing keys of the records.
const (
	RouteStandbyCompleted = "standby.completed"
	RouteIncidentCreated  = "incident.created"
	standbyPhotoPurpose   = "standby_photo"
	incidentPhotoPurpose  = "incident_photo"
)

// standbyBackfillFields are the fields of POST /v1/standby/backfill.
var standbyBackfillFields = []string{"driverId", "taskId", "tripId", "startLocation", "endLocation", "startedAt",
	"endedAt", "note", "jobCategory", "truckId", "photos"}

// standbyPhotoIn is one standby photo of the backfill: customer_worksheet or site_photo, its uploaded key.
type standbyPhotoIn struct {
	typ, key string
	overlay  *string
}

// StandbyBackfillAPI is POST /v1/standby/backfill (operations:create_standby): a completed standby record for a past
// job, backfilled_by_admin, with its two optional photos; in the same transaction its task becomes completed and its
// trip standby, as the legacy batch did; the outbox gets standby.completed (billing.compute prices it, notify.line
// sends the card), task.updated and trip.updated. driverId, startLocation, endLocation, startedAt and endedAt are
// required, endedAt after startedAt. The answer (201) is the record as GET /v1/standby lists it.
func (s *Service) StandbyBackfillAPI(ctx context.Context, p *authz.Principal, rid string, body []byte) (WriteResult, error) {
	if err := requireCap(p, authz.OperationsCreateStandby); err != nil {
		return WriteResult{}, err
	}
	mode, err := s.mode()
	if err != nil {
		return WriteResult{}, err
	}
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return WriteResult{}, err
	}
	f.only(standbyBackfillFields...)
	in := standbyIn{driverID: f.id("driverId"), taskID: f.id("taskId"), tripID: f.id("tripId"),
		start: f.required("startLocation"), end: f.required("endLocation"), startedAt: f.instant("startedAt"),
		endedAt: f.instant("endedAt"), note: f.optText("note"), truckID: f.id("truckId")}
	if in.driverID == nil && !ck.hasField("driverId") {
		ck.add("driverId", "required")
	}
	for _, k := range []string{"startedAt", "endedAt"} {
		if !f.has(k) || f.isNull(k) {
			ck.add(k, "required")
		}
	}
	if in.startedAt != nil && in.endedAt != nil && !in.endedAt.After(*in.startedAt) {
		ck.add("endedAt", "before_start")
	}
	if f.has("jobCategory") {
		if v := f.optText("jobCategory"); v != nil {
			if c, ok := jobCategoryOf(*v); ok {
				in.jobCategory = &c
			} else {
				ck.add("jobCategory", "invalid")
			}
		}
	}
	in.photos = parseStandbyPhotos(f)
	if mode == modeWriteBack && len(in.photos) > 0 {
		ck.add("photos", ReasonWriteBackUnsupported)
	}
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.standbyBackfillWriteBack(ctx, p, rid, in)
	}
	var rec Standby
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		pl, err := s.planStandby(ctx, q, in)
		if err != nil {
			return err
		}
		res, err := Stamp(ctx, tx, p, TableStandby, Links{TaskID: in.taskID, TripID: in.tripID, DriverID: in.driverID})
		if err != nil {
			return err
		}
		ins := pl.insert
		ins.TenantID, ins.TenantSource = uuid.MustParse(res.TenantID), string(res.Source)
		if rec, _, err = CreateStandby(ctx, tx, NewStandby{StandbyInsert: ins}); err != nil {
			return err
		}
		for _, ph := range in.photos {
			if s.commits == nil {
				return invalid("photos.key", "not_found")
			}
			file, err := s.commits.Commit(ctx, tx, storage.CommitInput{Key: ph.key, Field: "photos.key",
				Purposes: []string{standbyPhotoPurpose}, OwnerID: rec.ID, UserID: p.UserID, TenantID: p.EffectiveTenant()})
			if err != nil {
				return err
			}
			if _, err := q.UpsertStandbyPhoto(ctx, operationsdb.UpsertStandbyPhotoParams{StandbyID: rec.ID, PhotoType: ph.typ,
				FileID: file.ID, OverlayText: ph.overlay}); err != nil {
				return MapError(err)
			}
		}
		evs, err := s.standbySideEffects(ctx, q, rec, pl)
		if err != nil {
			return err
		}
		return appendEvents(ctx, tx, rid, "", evs...)
	})
	if err != nil {
		return WriteResult{}, err
	}
	return s.standbyAnswer(ctx, p, rec.ID, http.StatusCreated)
}

// standbyIn is a parsed backfill.
type standbyIn struct {
	driverID, taskID, tripID, truckID *uuid.UUID
	start, end                        string
	startedAt, endedAt                *time.Time
	note, jobCategory                 *string
	photos                            []standbyPhotoIn
}

// standbyPlan is the record a backfill inserts with the rows it reads.
type standbyPlan struct {
	insert StandbyInsert
	driver *operationsdb.WriteDriverRow
	task   *Task
	trip   *Trip
	truck  *operationsdb.WriteTruckRow
}

// planStandby checks the links of a backfill under the caller's RLS (a driver, task, trip or truck it cannot read is
// 422 not_found; a trip of another task than the one named is 422 tripId task_mismatch) and builds the insert.
func (s *Service) planStandby(ctx context.Context, q *operationsdb.Queries, in standbyIn) (standbyPlan, error) {
	var ck checker
	var pl standbyPlan
	var err error
	if pl.driver, err = anyDriverRow(ctx, q, &ck, "driverId", *in.driverID); err != nil {
		return pl, err
	}
	if in.taskID != nil {
		t, err := q.LockTask(ctx, *in.taskID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			ck.add("taskId", "not_found")
		case err != nil:
			return pl, MapError(err)
		default:
			pl.task = &t
		}
	}
	if in.tripID != nil {
		t, err := q.LockTrip(ctx, *in.tripID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			ck.add("tripId", "not_found")
		case err != nil:
			return pl, MapError(err)
		default:
			pl.trip = &t
			if in.taskID != nil && t.TaskID != nil && *t.TaskID != *in.taskID {
				ck.add("tripId", "task_mismatch")
			}
		}
	}
	if in.truckID != nil {
		tr, err := q.WriteTruck(ctx, *in.truckID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			ck.add("truckId", "not_found")
		case err != nil:
			return pl, MapError(err)
		default:
			pl.truck = &tr
		}
	}
	if err := ck.err(); err != nil {
		return pl, err
	}
	start, end := in.start, in.end
	minutes := int32(math.Max(0, math.Round(in.endedAt.Sub(*in.startedAt).Minutes())))
	pl.insert = StandbyInsert{DriverID: in.driverID, TaskID: in.taskID, TripID: in.tripID, JobCategory: in.jobCategory,
		StartLocationRaw: &start, EndLocationRaw: &end, StartedAt: ptr(in.startedAt.UTC()), EndedAt: ptr(in.endedAt.UTC()),
		DurationMinutes: &minutes, Note: in.note, BackfilledByAdmin: true}
	if pl.truck != nil {
		plate := strings.TrimSpace(pl.truck.LicensePlate)
		pl.insert.TruckID, pl.insert.TruckLicensePlateSnapshot = &pl.truck.ID, &plate
	}
	return pl, nil
}

// standbySideEffects completes the record's task and sets its trip to standby (the legacy batch), and returns the
// events: standby.completed with the trips topics, and the task's and trip's own events.
func (s *Service) standbySideEffects(ctx context.Context, q *operationsdb.Queries, rec Standby, pl standbyPlan) ([]opsEvent, error) {
	var extra []uuid.UUID
	for _, id := range []*uuid.UUID{rec.CustomerPartyID, rec.BillingPartyID} {
		if id != nil {
			extra = append(extra, *id)
		}
	}
	topics, err := tripTopics(ctx, q, rec.TenantID, rec.TripID, extra...)
	if err != nil {
		return nil, err
	}
	if rec.TaskID != nil {
		parties, err := q.TaskParties(ctx, *rec.TaskID)
		if err != nil {
			return nil, MapError(err)
		}
		more, err := tripTopics(ctx, q, rec.TenantID, nil, parties...)
		if err != nil {
			return nil, err
		}
		for _, t := range more {
			if !slices.Contains(topics, t) {
				topics = append(topics, t)
			}
		}
	}
	ev := opsEvent{route: RouteStandbyCompleted, agg: "standby", id: rec.ID, tenant: rec.TenantID, topics: topics,
		payload: map[string]any{"taskId": rec.TaskID, "tripId": rec.TripID, "customerPartyId": rec.CustomerPartyID,
			"changed": []string{}}}
	evs := []opsEvent{ev}
	if pl.task != nil && pl.task.Status != TaskCompleted && pl.task.Status != TaskCancelled {
		done, err := q.SetTaskStatus(ctx, operationsdb.SetTaskStatusParams{ID: pl.task.ID, Status: TaskCompleted})
		if err != nil {
			return nil, MapError(err)
		}
		tt, err := taskTopics(ctx, q, done)
		if err != nil {
			return nil, err
		}
		evs = append(evs, withTopics(taskEvent(RouteTaskUpdated, done, []string{"status"}), tt))
	}
	if pl.trip != nil && pl.trip.Status != TripStandby {
		trip, err := q.SetTripStatus(ctx, operationsdb.SetTripStatusParams{ID: pl.trip.ID, Status: TripStandby})
		if err != nil {
			return nil, MapError(err)
		}
		tt, err := tripTopics(ctx, q, trip.TenantID, &trip.ID)
		if err != nil {
			return nil, err
		}
		evs = append(evs, withTopics(tripEvent(RouteTripUpdated, trip, []string{"status"}), tt))
	}
	return evs, nil
}

// parseStandbyPhotos reads photos[]: [{type: customer_worksheet | site_photo, key, overlayText?}] (one per type).
func parseStandbyPhotos(f fields) []standbyPhotoIn {
	raw := f.raw("photos")
	if raw == nil || isNull(raw) {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		f.ck.add("photos", "invalid")
		return nil
	}
	var out []standbyPhotoIn
	for _, it := range items {
		pf, err := parseFields(it, f.ck)
		if err != nil || !bytes.HasPrefix(bytes.TrimSpace(it), []byte("{")) {
			f.ck.add("photos", "invalid")
			continue
		}
		pf.only("type", "key", "overlayText")
		ph := standbyPhotoIn{typ: pf.required("type"), key: pf.required("key"), overlay: pf.optText("overlayText")}
		if ph.typ != "" && !slices.Contains(StandbyPhotoKind, ph.typ) {
			f.ck.add("photos.type", "invalid")
		}
		if slices.ContainsFunc(out, func(o standbyPhotoIn) bool { return o.typ == ph.typ }) {
			f.ck.add("photos.type", "duplicate")
		}
		out = append(out, ph)
	}
	return out
}

// standbyAnswer is the record as GET /v1/standby lists it (staff), read after the write.
func (s *Service) standbyAnswer(ctx context.Context, p *authz.Principal, id uuid.UUID, status int) (WriteResult, error) {
	view, photos, err := s.GetStandby(ctx, p, id)
	if err != nil {
		return WriteResult{}, err
	}
	r := view.Standby
	names, err := s.names(ctx, p, appendID(nil, r.DriverID))
	if err != nil {
		return WriteResult{}, err
	}
	files := make([]uuid.UUID, 0, len(photos))
	for _, ph := range photos {
		files = append(files, ph.FileID)
	}
	signed, err := s.sign(ctx, files)
	if err != nil {
		return WriteResult{}, err
	}
	b := StandbyBase{ID: r.ID, TenantID: r.TenantID, DriverID: r.DriverID, DriverName: nameOf(names, r.DriverID),
		TaskID: r.TaskID, TripID: r.TripID, JobCategory: r.JobCategory, StartLocation: r.StartLocationRaw,
		EndLocation: r.EndLocationRaw, StartedAt: r.StartedAt, EndedAt: r.EndedAt, DurationMinutes: r.DurationMinutes,
		Note: r.Note, Status: r.Status, Lat: r.Lat, Lng: r.Lng, TruckID: r.TruckID,
		TruckLicensePlate: r.TruckLicensePlateSnapshot, Photos: []StandbyPhotoRow{}, CreatedAt: r.CreatedAt}
	for _, ph := range photos {
		b.Photos = append(b.Photos, StandbyPhotoRow{Type: ph.PhotoType, OverlayText: ph.OverlayText, FileRef: *fileRef(signed, &ph.FileID)})
	}
	out := StandbyListView{StandbyBase: b, TenantSource: r.TenantSource, CustomerPartyID: r.CustomerPartyID,
		CustomerResolved: r.CustomerResolved, CustomerResolvedFrom: r.CustomerResolvedFrom,
		BackfilledByAdmin: r.BackfilledByAdmin, MigratedFromTripNo: r.MigratedFromTripNo, UpdatedAt: r.UpdatedAt}
	return WriteResult{Status: status, Data: out}, nil
}

// standbyBackfillWriteBack creates the standby document (minted uuidv7) and, in the same batch, completes its task
// document and sets its trip document to standby.
func (s *Service) standbyBackfillWriteBack(ctx context.Context, p *authz.Principal, rid string, in standbyIn) (WriteResult, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return WriteResult{}, err
	}
	var pl standbyPlan
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		var err error
		if pl, err = s.planStandby(ctx, q, in); err != nil {
			return err
		}
		_, err = Stamp(ctx, tx, p, TableStandby, Links{TaskID: in.taskID, TripID: in.tripID, DriverID: in.driverID})
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	ref := driverRef(pl.driver)
	if ref == "" {
		return WriteResult{}, notProjected("driverId")
	}
	now := s.now().UTC()
	ins := pl.insert
	doc := map[string]any{"driverId": ref, "taskId": nil, "tripId": nil, "startLocation": *ins.StartLocationRaw,
		"endLocation": *ins.EndLocationRaw, "startedAt": *ins.StartedAt, "endedAt": *ins.EndedAt,
		"durationMinutes": int64(*ins.DurationMinutes), "photos": []any{}, "note": optStr(ins.Note), "status": "completed",
		"lat": nil, "lng": nil, "createdAt": now, "updatedAt": now, "backfilledByAdmin": true}
	if ins.JobCategory != nil {
		doc["jobCategory"] = *ins.JobCategory
	}
	docs := []wbDoc{{collection: CollStandby, docID: id.String(), fields: doc, create: true}}
	if pl.task != nil {
		if pl.task.LegacyDocID == nil {
			return WriteResult{}, notProjected("taskId")
		}
		doc["taskId"] = *pl.task.LegacyDocID
		if pl.task.Status != TaskCompleted && pl.task.Status != TaskCancelled {
			docs = append(docs, wbDoc{collection: CollTasks, docID: *pl.task.LegacyDocID, mustExist: true,
				fields: map[string]any{"status": legacyTaskStatus[TaskCompleted], "updatedAt": now}})
		}
	}
	if pl.trip != nil {
		if pl.trip.LegacyDocID == nil {
			return WriteResult{}, notProjected("tripId")
		}
		doc["tripId"] = *pl.trip.LegacyDocID
		docs = append(docs, wbDoc{collection: CollTrips, docID: *pl.trip.LegacyDocID, mustExist: true,
			fields: map[string]any{"status": TripStandby, "updatedAt": now}})
	}
	if pl.truck != nil {
		if pl.truck.LegacyDocID == nil {
			return WriteResult{}, notProjected("truckId")
		}
		doc["truckId"], doc["truckLicensePlate"] = *pl.truck.LegacyDocID, *ins.TruckLicensePlateSnapshot
	}
	at, acked, err := s.writeBack(ctx, docs...)
	if err != nil {
		return WriteResult{}, err
	}
	tenant := uuid.Nil
	if pl.task != nil {
		tenant = pl.task.TenantID
	} else if pl.trip != nil {
		tenant = pl.trip.TenantID
	} else {
		tenant = pl.driver.TenantID
	}
	s.notifyWriteBack(ctx, p, rid, opsEvent{route: RouteStandbyCompleted, agg: "standby", id: id, tenant: tenant,
		payload: map[string]any{"taskId": in.taskID, "tripId": in.tripID, "changed": []string{}}})
	if !acked {
		return echo(id, docs[0], at), nil
	}
	res, err := s.standbyAnswer(ctx, p, id, http.StatusCreated)
	if err != nil {
		return echo(id, docs[0], at), nil //nolint:nilerr // written and acknowledged; a failed read answers the echo
	}
	return res, nil
}

// --- POST /v1/incidents ---------------------------------------------------------------------------------------------

// incidentFields are the fields of POST /v1/incidents.
var incidentFields = []string{"driverId", "tripId", "delayCause", "description", "lat", "lng", "truckId", "photoKeys"}

// CreateIncidentAPI is POST /v1/incidents (operations:edit_trip_details): an admin-reported incident (reported_by_kind
// admin, the reporter the caller) on a trip or a driver (at least one: the tenant follows the trip, else the driver,
// T28), with its description (required), delay cause, position, truck (plate snapshotted) and up to three photos
// (photoKeys {map, situation1, situation2}, committed with the report as owner). The outbox gets incident.created.
// The answer (201) is the report as GET /v1/incidents lists it.
func (s *Service) CreateIncidentAPI(ctx context.Context, p *authz.Principal, rid string, body []byte) (WriteResult, error) {
	if err := requireCap(p, authz.OperationsEditTripDetails); err != nil {
		return WriteResult{}, err
	}
	mode, err := s.mode()
	if err != nil {
		return WriteResult{}, err
	}
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return WriteResult{}, err
	}
	f.only(incidentFields...)
	in := incidentIn{driverID: f.id("driverId"), tripID: f.id("tripId"), cause: f.optText("delayCause"),
		description: f.required("description"), lat: f.number("lat"), lng: f.number("lng"), truckID: f.id("truckId")}
	if in.driverID == nil && in.tripID == nil && !ck.hasField("driverId") && !ck.hasField("tripId") {
		ck.add("tripId", "required") // a report needs a trip or a driver for its tenant
	}
	if raw := f.raw("photoKeys"); raw != nil && !isNull(raw) {
		pf, err := parseFields(raw, &ck)
		if err != nil || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			ck.add("photoKeys", "invalid")
		} else {
			pf.only("map", "situation1", "situation2")
			in.photos = map[string]string{}
			for _, k := range []string{"map", "situation1", "situation2"} {
				if v := pf.optText(k); v != nil {
					in.photos[k] = *v
				}
			}
		}
	}
	if mode == modeWriteBack && len(in.photos) > 0 {
		ck.add("photoKeys", ReasonWriteBackUnsupported)
	}
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.createIncidentWriteBack(ctx, p, rid, in)
	}
	var inc Incident
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		pl, err := s.planIncident(ctx, q, p, in)
		if err != nil {
			return err
		}
		res, err := Stamp(ctx, tx, p, TableIncidents, Links{TripID: in.tripID, DriverID: in.driverID})
		if err != nil {
			return err
		}
		pl.insert.TenantID, pl.insert.TenantSource = uuid.MustParse(res.TenantID), string(res.Source)
		if inc, _, err = CreateIncident(ctx, tx, pl.insert); err != nil {
			return err
		}
		if len(in.photos) > 0 {
			ids := map[string]*uuid.UUID{}
			for k, key := range in.photos {
				if s.commits == nil {
					return invalid("photoKeys."+k, "not_found")
				}
				file, err := s.commits.Commit(ctx, tx, storage.CommitInput{Key: key, Field: "photoKeys." + k,
					Purposes: []string{incidentPhotoPurpose}, OwnerID: inc.ID, UserID: p.UserID, TenantID: p.EffectiveTenant()})
				if err != nil {
					return err
				}
				ids[k] = &file.ID
			}
			if inc, err = q.SetIncidentPhotos(ctx, operationsdb.SetIncidentPhotosParams{ID: inc.ID, MapPhotoFileID: ids["map"],
				Situation1PhotoFileID: ids["situation1"], Situation2PhotoFileID: ids["situation2"]}); err != nil {
				return MapError(err)
			}
		}
		topics, err := tripTopics(ctx, q, inc.TenantID, inc.TripID)
		if err != nil {
			return err
		}
		return appendEvents(ctx, tx, rid, "", opsEvent{route: RouteIncidentCreated, agg: "incident", id: inc.ID,
			tenant: inc.TenantID, topics: topics, payload: map[string]any{"tripId": inc.TripID, "changed": []string{}}})
	})
	if err != nil {
		return WriteResult{}, err
	}
	return s.incidentAnswer(ctx, p, inc.ID, inc.CreatedAt, http.StatusCreated)
}

// incidentIn is a parsed admin incident.
type incidentIn struct {
	driverID, tripID, truckID *uuid.UUID
	cause                     *string
	description               string
	lat, lng                  *float64
	photos                    map[string]string
}

// incidentPlan is the report an admin incident inserts with the rows it reads.
type incidentPlan struct {
	insert IncidentInsert
	driver *operationsdb.WriteDriverRow
	trip   *Trip
	truck  *operationsdb.WriteTruckRow
}

// planIncident checks the links under the caller's RLS (422 not_found) and builds the insert; a trip names its driver
// when the body names none.
func (s *Service) planIncident(ctx context.Context, q *operationsdb.Queries, p *authz.Principal, in incidentIn) (incidentPlan, error) {
	var ck checker
	var pl incidentPlan
	var err error
	if in.tripID != nil {
		t, err := q.LockTrip(ctx, *in.tripID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			ck.add("tripId", "not_found")
		case err != nil:
			return pl, MapError(err)
		default:
			pl.trip = &t
		}
	}
	drv := in.driverID
	if drv == nil && pl.trip != nil {
		drv = pl.trip.DriverID
	}
	if drv != nil {
		if pl.driver, err = anyDriverRow(ctx, q, &ck, "driverId", *drv); err != nil {
			return pl, err
		}
	}
	if in.truckID != nil {
		tr, err := q.WriteTruck(ctx, *in.truckID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			ck.add("truckId", "not_found")
		case err != nil:
			return pl, MapError(err)
		default:
			pl.truck = &tr
		}
	}
	if err := ck.err(); err != nil {
		return pl, err
	}
	desc := in.description
	var by *uuid.UUID
	if !p.IsMachine() {
		u := p.UserID
		by = &u
	}
	pl.insert = IncidentInsert{DriverID: drv, TripID: in.tripID, DelayCause: in.cause, Description: &desc, Lat: in.lat,
		Lng: in.lng, ReportedByKind: "admin", ReportedByUserID: by}
	if pl.truck != nil {
		plate := strings.TrimSpace(pl.truck.LicensePlate)
		pl.insert.TruckID, pl.insert.TruckLicensePlateSnapshot = &pl.truck.ID, &plate
	}
	return pl, nil
}

// anyDriverRow reads a driver the caller can see, inactive included (a record of past work: 422 <field> not_found).
func anyDriverRow(ctx context.Context, q *operationsdb.Queries, ck *checker, field string, id uuid.UUID) (*operationsdb.WriteDriverRow, error) {
	dr, err := q.WriteDriver(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		ck.add(field, "not_found")
		return nil, nil
	case err != nil:
		return nil, MapError(err)
	}
	return &dr, nil
}

// incidentAnswer is the report as GET /v1/incidents lists it (staff), read after the write.
func (s *Service) incidentAnswer(ctx context.Context, p *authz.Principal, id uuid.UUID, at time.Time, status int) (WriteResult, error) {
	items, _, err := s.ListIncidentsView(ctx, p, IncidentListInput{From: at.Add(-time.Second).UTC().Format(time.RFC3339Nano),
		To: at.Add(time.Second).UTC().Format(time.RFC3339Nano), Limit: MaxLimit})
	if err != nil {
		return WriteResult{}, err
	}
	switch list := items.(type) {
	case []IncidentView:
		for _, v := range list {
			if v.ID == id {
				return WriteResult{Status: status, Data: v}, nil
			}
		}
	case []IncidentBase:
		for _, v := range list {
			if v.ID == id {
				return WriteResult{Status: status, Data: v}, nil
			}
		}
	}
	return WriteResult{Status: status, Data: map[string]any{"id": id}}, nil
}

// createIncidentWriteBack creates the incidentReport document under a minted uuidv7 (the legacy modal's shape).
func (s *Service) createIncidentWriteBack(ctx context.Context, p *authz.Principal, rid string, in incidentIn) (WriteResult, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return WriteResult{}, err
	}
	var pl incidentPlan
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		var err error
		if pl, err = s.planIncident(ctx, q, p, in); err != nil {
			return err
		}
		_, err = Stamp(ctx, tx, p, TableIncidents, Links{TripID: in.tripID, DriverID: in.driverID})
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	now := s.now().UTC()
	doc := map[string]any{"driverId": nil, "tripId": nil, "delayCause": optStr(in.cause), "description": in.description,
		"lat": nil, "lng": nil, "reportedBy": "admin", "reportedByUid": p.UserID.String(), "truckId": nil,
		"truckPlate": nil, "createdAt": now, "updatedAt": now}
	if in.lat != nil {
		doc["lat"] = *in.lat
	}
	if in.lng != nil {
		doc["lng"] = *in.lng
	}
	if pl.driver != nil {
		ref := driverRef(pl.driver)
		if ref == "" {
			return WriteResult{}, notProjected("driverId")
		}
		doc["driverId"] = ref
	}
	if pl.trip != nil {
		if pl.trip.LegacyDocID == nil {
			return WriteResult{}, notProjected("tripId")
		}
		doc["tripId"] = *pl.trip.LegacyDocID
	}
	if pl.truck != nil {
		if pl.truck.LegacyDocID == nil {
			return WriteResult{}, notProjected("truckId")
		}
		doc["truckId"], doc["truckPlate"] = *pl.truck.LegacyDocID, *pl.insert.TruckLicensePlateSnapshot
	}
	w := wbDoc{collection: CollIncidents, docID: id.String(), fields: doc, create: true}
	at, acked, err := s.writeBack(ctx, w)
	if err != nil {
		return WriteResult{}, err
	}
	if !acked {
		return echo(id, w, at), nil
	}
	res, err := s.incidentAnswer(ctx, p, id, now, http.StatusCreated)
	if err != nil {
		return echo(id, w, at), nil //nolint:nilerr // written and acknowledged; a failed read answers the echo
	}
	return res, nil
}
