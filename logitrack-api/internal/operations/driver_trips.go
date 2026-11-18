package operations

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Driver trip constants (T55).
const (
	// RouteTripCreated is the routing key of a loading submit (Appendix B §B.5.2).
	RouteTripCreated = "trip.created"
	// ReasonTaskNotCheckedIn, ReasonMultiDropNotSupported and ReasonTaskHasOpenTrip are the 409s of a loading submit.
	ReasonTaskNotCheckedIn      = "task_not_checked_in"
	ReasonMultiDropNotSupported = "multi_drop_not_supported"
	ReasonTaskHasOpenTrip       = "task_has_open_trip"
	// DeliveredViaMobile is trip_records.delivered_via of the driver's own delivery.
	DeliveredViaMobile = "mobile"
	// checkinAppPhotoType is the trip photo the check-in's customer-app screenshot becomes at loading (ADR 0019).
	checkinAppPhotoType = "checkin_app"
)

// MobileTripView is a trip as the driver app reads it (GET/POST /v1/mobile/trips*, T55): no billing, review or OCR
// fields; photos list the types the trip holds.
type MobileTripView struct {
	ID           uuid.UUID         `json:"id"`
	TripNo       string            `json:"tripNo"`
	Status       string            `json:"status"`
	TaskID       *uuid.UUID        `json:"taskId"`
	JobType      string            `json:"jobType"`
	JobCategory  *string           `json:"jobCategory"`
	Origin       *string           `json:"origin"`
	Destination  *string           `json:"destination"`
	SealCode     *string           `json:"sealCode"`
	TruckID      *uuid.UUID        `json:"truckId"`
	LicensePlate *string           `json:"licensePlate"`
	CreatedAt    time.Time         `json:"createdAt"`
	DeliveredAt  *time.Time        `json:"deliveredAt"`
	IsMultiDrop  bool              `json:"isMultiDelivery"`
	Photos       []MobileTripPhoto `json:"photos"`
}

// MobileTripPhoto is the type of one evidence photo of a trip.
type MobileTripPhoto struct {
	Type string `json:"type"`
}

func mobileTripView(t Trip, photos []operationsdb.TripPhoto) MobileTripView {
	v := MobileTripView{ID: t.ID, TripNo: t.TripNo, Status: t.Status, TaskID: t.TaskID, JobType: t.JobType,
		JobCategory: t.JobCategory, Origin: t.OriginRaw, Destination: t.DestinationRaw, SealCode: t.SealCode,
		TruckID: t.TruckID, LicensePlate: t.TruckLicensePlateSnapshot, CreatedAt: t.CreatedAt, DeliveredAt: t.DeliveredAt,
		IsMultiDrop: t.IsMultiDelivery, Photos: make([]MobileTripPhoto, 0, len(photos))}
	for _, ph := range photos {
		v.Photos = append(v.Photos, MobileTripPhoto{Type: ph.PhotoType})
	}
	return v
}

// mobileTrip reads the trip id under the driver's RLS with its photos.
func (s *Service) mobileTrip(ctx context.Context, p *authz.Principal, id uuid.UUID) (MobileTripView, error) {
	var v MobileTripView
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		me := *p.DriverID
		t, err := q.GetTrip(ctx, operationsdb.GetTripParams{ID: id, ReachDriver: &me})
		if err != nil {
			return notFound(err)
		}
		photos, err := q.TripPhotos(ctx, id)
		if err != nil {
			return err
		}
		v = mobileTripView(t, photos)
		return nil
	})
	return v, err
}

// CreateTripForDriver is POST /v1/mobile/trips ✱ {tripNo, taskId, sealCode?, partnerCode?, ocrData?, truckId?,
// parcelCount?, sealTime?, totalWeight?, lat?, lng?, photos[]} (mobile:submit_trip, driver:self; spec R23), the
// loading submit of a single-stop job. In one transaction under the driver's RLS: the task (the driver's or its
// helper's) must be checked in, not multi-drop and without an open trip; CheckSeal refuses a seal another trip holds
// (409 duplicate_seal, the holder named only within the driver's reach); the trip is stamped on the task's tenant and
// takes the task's job type, category, route, truck and billing party; created_at and std are now; the photos are
// committed (purpose trip_photo, owner the trip) and the check-in's customer-app screenshot becomes its checkin_app
// photo (ADR 0019); the task becomes in_transit; the outbox gets trip.created and task.updated. A taken trip number is
// 409 duplicate_trip_id. origin, destination, distance and isMultiDelivery are refused in this slice (422
// not_supported): the route is the task's.
func (s *Service) CreateTripForDriver(ctx context.Context, p *authz.Principal, rid, _ string, body []byte) (WriteResult, error) {
	if err := s.requireDriver(p, authz.MobileSubmitTrip); err != nil {
		return WriteResult{}, err
	}
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return WriteResult{}, err
	}
	f.only("tripNo", "taskId", "sealCode", "partnerCode", "ocrData", "truckId", "parcelCount", "sealTime",
		"totalWeight", "lat", "lng", "photos")
	tripNo := f.required("tripNo")
	taskRef := f.required("taskId")
	seal, partner := f.optText("sealCode"), f.optText("partnerCode")
	truckID := f.id("truckId")
	parcels, sealTime, weight := f.integer("parcelCount"), f.instant("sealTime"), f.number("totalWeight")
	lat, lng := f.number("lat"), f.number("lng")
	var ocr []byte
	if raw := f.raw("ocrData"); raw != nil && !isNull(raw) {
		ocr = raw
	}
	photos := parsePhotos(f)
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	taskID, err := s.driverTask(ctx, p, taskRef)
	if err != nil {
		return WriteResult{}, err
	}
	me := *p.DriverID
	var trip Trip
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		task, err := q.LockTask(ctx, taskID)
		if err != nil {
			return MapError(notFound(err))
		}
		switch {
		case task.Status != TaskCheckedIn:
			return errPrecondition(ReasonTaskNotCheckedIn, "the task must be checked in before loading").
				WithDetails(map[string]any{"status": task.Status})
		case task.IsMultiDelivery:
			return errPrecondition(ReasonMultiDropNotSupported, "a multi-drop job is not loaded by this app yet")
		}
		if truckID != nil && (task.TruckID == nil || *truckID != *task.TruckID) {
			return httpx.ErrInvalidArgument(violation("truckId", "must_match_task"))
		}
		if open, err := q.TripsOfTasks(ctx, operationsdb.TripsOfTasksParams{TaskIds: []uuid.UUID{task.ID},
			ReachDriver: &me}); err != nil {
			return MapError(err)
		} else {
			for _, t := range open {
				if t.Status == TripInTransit || t.Status == TripIncident {
					return errPrecondition(ReasonTaskHasOpenTrip, "the task already has an open trip").
						WithDetails(map[string]any{"tripNo": t.TripNo})
				}
			}
		}
		if seal != nil {
			if err := CheckSeal(ctx, tx, ReachOf(p), *seal, nil); err != nil {
				return err
			}
		}
		res, err := Stamp(ctx, tx, p, TableTrips, Links{TaskID: &task.ID, DriverID: &me})
		if err != nil {
			return err
		}
		now := s.now().UTC()
		origin, dest := strings.TrimSpace(task.SourceHubRaw), strings.TrimSpace(task.DestinationRaw)
		ins := TripInsert{TenantID: uuid.MustParse(res.TenantID), TenantSource: string(res.Source),
			TripNo: strings.TrimSpace(tripNo), Status: TripInTransit, JobType: task.TaskType, JobCategory: task.JobCategory,
			TaskID: &task.ID, DriverID: &me, OriginRaw: &origin, OriginHubID: task.SourceHubID, DestinationRaw: &dest,
			DestinationHubID: task.DestinationHubID, DestinationSocKey: task.DestinationSocKey, SealCode: seal,
			PartnerCode: partner, OcrData: ocr, TruckID: task.TruckID, TruckLicensePlateSnapshot: task.LicensePlateSnapshot,
			VehicleClass: task.TruckType, ParcelCount: parcels, SealTime: sealTime, TotalWeightKg: weight, LoadingLat: lat,
			LoadingLng: lng, Std: &now, BillingPartyID: task.BillingPartyID, CreatedAt: &now}
		trip, err = CreateTrip(ctx, tx, NewTrip{TripInsert: ins})
		if err != nil {
			return err
		}
		if err := s.putPhotos(ctx, tx, p, trip.ID, photos); err != nil {
			return err
		}
		if task.CheckInAppScreenshotFileID != nil {
			if _, err := PutTripPhoto(ctx, tx, trip.ID, PhotoInput{Type: checkinAppPhotoType,
				FileID: *task.CheckInAppScreenshotFileID}); err != nil {
				return err
			}
		}
		moved, err := q.SetTaskStatus(ctx, operationsdb.SetTaskStatusParams{ID: task.ID, Status: TaskInTransit})
		if err != nil {
			return MapError(err)
		}
		topics, err := tripTopics(ctx, q, trip.TenantID, &trip.ID)
		if err != nil {
			return err
		}
		tt, err := taskTopics(ctx, q, moved)
		if err != nil {
			return err
		}
		return appendEvents(ctx, tx, rid, "",
			withTopics(tripEvent(RouteTripCreated, trip, []string{"status"}), topics),
			withTopics(taskEvent(RouteTaskUpdated, moved, []string{"status"}), tt))
	})
	if err != nil {
		return WriteResult{}, err
	}
	v, err := s.mobileTrip(ctx, p, trip.ID)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: http.StatusCreated, Data: v}, nil
}

// driverTripID resolves a driver trip route's {id}: the uuid or the trip number (the app keeps the number of its
// running trip, D10), under the driver's RLS. A trip that is not the driver's own is 403 not_task_owner, none 404.
func (s *Service) driverTripID(ctx context.Context, p *authz.Principal, ref string) (uuid.UUID, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return uuid.Nil, httpx.ErrNotFound()
	}
	me := *p.DriverID
	var id uuid.UUID
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		var t Trip
		var err error
		if u, perr := uuid.Parse(ref); perr == nil {
			t, err = q.GetTrip(ctx, operationsdb.GetTripParams{ID: u, ReachDriver: &me})
		} else {
			t, err = q.TripByNo(ctx, operationsdb.TripByNoParams{TripNo: ref, ReachDriver: &me})
		}
		if err != nil {
			return notFound(err)
		}
		if !sameID(t.DriverID, &me) {
			return httpx.NewError(http.StatusForbidden, CodeNotTaskOwner, "the trip belongs to another driver")
		}
		id = t.ID
		return nil
	})
	return id, err
}

// DeliverTripForDriver is POST /v1/mobile/trips/{id}/deliver ✱ {deliveredAt, lat?, lng?, photos[]}
// (mobile:submit_trip, driver:self; spec R24): the driver's own single-stop delivery, deliverTripTx with deliveredVia
// mobile: the in-transit trip becomes delivered at deliveredAt (required), the photos are merged, the task is
// completed, the driver's active truck cleared, and the outbox gets trip.delivered (billing.compute prices it,
// notify.line sends the delivered card). {id} is the uuid or the trip number. A trip already delivered by the driver at
// the same deliveredAt answers 200 (a replay after IDEMPOTENCY_TTL); any other closed trip is 409 trip_not_open.
func (s *Service) DeliverTripForDriver(ctx context.Context, p *authz.Principal, rid, ref string, body []byte) (WriteResult, error) {
	if err := s.requireDriver(p, authz.MobileSubmitTrip); err != nil {
		return WriteResult{}, err
	}
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return WriteResult{}, err
	}
	f.only("deliveredAt", "lat", "lng", "photos")
	at, lat, lng := f.instant("deliveredAt"), f.number("lat"), f.number("lng")
	if at == nil && !f.has("deliveredAt") {
		ck.add("deliveredAt", "required")
	}
	photos := parsePhotos(f)
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	id, err := s.driverTripID(ctx, p, ref)
	if err != nil {
		return WriteResult{}, err
	}
	when := at.UTC()
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		cur, err := lockTrip(ctx, operationsdb.New(tx), id)
		if err != nil {
			return err
		}
		if cur.Status == TripDelivered && cur.DeliveredVia != nil && *cur.DeliveredVia == DeliveredViaMobile &&
			cur.DeliveredAt != nil && cur.DeliveredAt.Equal(when) {
			return errReplayed
		}
		if cur.Status != TripInTransit {
			return errPrecondition(ReasonTripNotOpen, "only an in-transit trip is delivered").
				WithDetails(map[string]any{"status": cur.Status})
		}
		if cur.IsMultiDelivery {
			return errPrecondition(ReasonMultiDropNotSupported, "a multi-drop trip is not delivered by this app yet")
		}
		_, err = s.deliverTripTx(ctx, tx, p, rid, cur, when, lat, lng, photos, DeliveredViaMobile)
		return err
	})
	if err != nil && !errors.Is(err, errReplayed) {
		return WriteResult{}, err
	}
	v, err := s.mobileTrip(ctx, p, id)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: http.StatusOK, Data: v}, nil
}

// PendingTripForDriver is GET /v1/mobile/trips/pending-in-transit (spec R25): the driver's newest in-transit trip, or
// nil, to resume a job the app lost (trip_records_repository.dart:195-201).
func (s *Service) PendingTripForDriver(ctx context.Context, p *authz.Principal) (*MobileTripView, error) {
	if !driverPrincipal(p) {
		return nil, authz.ErrPermissionDenied("the route acts for a driver (driver:self)")
	}
	var id *uuid.UUID
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		t, err := q.PendingInTransitTrip(ctx, *p.DriverID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		id = &t.ID
		return nil
	})
	if err != nil || id == nil {
		return nil, err
	}
	v, err := s.mobileTrip(ctx, p, *id)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// TripForDriver is GET /v1/mobile/trips/by-no/{tripNo} (spec R25): the driver's own trip by number (the app checks a
// trip it still holds: delivered or cancelled clears it, in transit keeps it, 404 clears it).
func (s *Service) TripForDriver(ctx context.Context, p *authz.Principal, tripNo string) (MobileTripView, error) {
	if !driverPrincipal(p) {
		return MobileTripView{}, authz.ErrPermissionDenied("the route acts for a driver (driver:self)")
	}
	id, err := s.driverTripID(ctx, p, tripNo)
	if err != nil {
		return MobileTripView{}, err
	}
	return s.mobileTrip(ctx, p, id)
}
