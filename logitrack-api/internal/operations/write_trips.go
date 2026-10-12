package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Trip writes (Appendix B §B.2.12): PATCH /v1/trips/{id} (edit trip details), POST /v1/trips/{id}/deliver (the admin
// close of a stuck trip, deliveredVia admin_web), POST /v1/trips/{id}/cancel, POST /v1/trips/bulk-cancel and POST
// /v1/trips/{id}/rename. They replace the direct updateDoc writes of EditTripDetailsDialog.tsx (492-759, 682-807) and
// renameTripRecord (renameTripRecord.ts:51), behind operations:edit_trip_details.

// FileCommitter commits an uploaded evidence photo in the request transaction (storage.Service.Commit).
type FileCommitter interface {
	Commit(ctx context.Context, tx pgx.Tx, in storage.CommitInput) (storage.Committed, error)
}

// TripRenamer renames a trip (POST /v1/trips/{id}/rename) in one system transaction with its audit row
// trip_record_renamed (security_events is written in a system transaction only, Appendix C §C.4.13), after the
// caller's RLS showed the trip writable (iam.TripRenamer). AuditRename appends the same audit row after a write-back
// rename (the Firestore batch commits first; the legacy callable also appended after its batch).
type TripRenamer interface {
	RenameTrip(ctx context.Context, p *authz.Principal, rid string, tripID uuid.UUID, newNo string) (Trip, TripRename, error)
	AuditRename(ctx context.Context, p *authz.Principal, rid string, trip Trip, oldNo, newNo string, repointed int) error
}

// Reasons of the trip writes.
const (
	ReasonTripNotOpen      = "trip_not_open"
	ReasonTripDelivered    = "trip_delivered"
	ReasonTenantAdminOnly  = "tenant_admin_only"
	MaxBulkCancel          = 200
	ReasonNotInTransit     = "not_in_transit"
	tripPhotoPurpose       = "trip_photo"
	reviewPending          = "pending_review"
	deliveredViaAdminWeb   = "admin_web"
	RouteTripUpdated       = "trip.updated"
	RouteTripDelivered     = "trip.delivered"
	RouteTripCancelled     = "trip.cancelled"
	RouteTripDeliveredAtCh = "trip.delivered_at_changed"
)

// tripPatchFields are the fields of PATCH /v1/trips/{id}.
var tripPatchFields = []string{"sealCode", "partnerCode", "ocrData", "deliveredAt", "needsAdminReview", "reviewStatus",
	"reviewReason", "origin", "destination", "photos"}

// photoInput is one evidence photo of a trip write: its type (any non-empty text, R19), the uploaded key (committed in
// the write's transaction, purpose trip_photo) and the overlay's geocoding.
type photoInput struct {
	typ, key string
	lat, lng *float64
	address  *string
	at       *time.Time
}

// parsePhotos reads photos[]: [{type, key, lat?, lng?, address?, geocodedAt?}].
func parsePhotos(f fields) []photoInput {
	raw := f.raw("photos")
	if raw == nil || isNull(raw) {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		f.ck.add("photos", "invalid")
		return nil
	}
	var out []photoInput
	for _, it := range items {
		pf, err := parseFields(it, f.ck)
		if err != nil || !bytes.HasPrefix(bytes.TrimSpace(it), []byte("{")) {
			f.ck.add("photos", "invalid")
			continue
		}
		pf.only("type", "key", "lat", "lng", "address", "geocodedAt")
		ph := photoInput{typ: pf.required("type"), key: pf.required("key"), lat: pf.number("lat"), lng: pf.number("lng"),
			address: pf.optText("address"), at: pf.instant("geocodedAt")}
		out = append(out, ph)
	}
	return out
}

// putPhotos commits the photos of a trip write and merges them by type (last wins).
func (s *Service) putPhotos(ctx context.Context, tx pgx.Tx, p *authz.Principal, trip uuid.UUID, photos []photoInput) error {
	for _, ph := range photos {
		if s.commits == nil {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "photos.key", Reason: "not_found",
				Params: map[string]any{"key": ph.key}})
		}
		f, err := s.commits.Commit(ctx, tx, storage.CommitInput{Key: ph.key, Field: "photos.key",
			Purposes: []string{tripPhotoPurpose}, OwnerID: trip, UserID: p.UserID, TenantID: p.EffectiveTenant()})
		if err != nil {
			return err
		}
		if _, err := PutTripPhoto(ctx, tx, trip, PhotoInput{Type: ph.typ, FileID: f.ID, GeocodeLat: ph.lat,
			GeocodeLng: ph.lng, GeocodeAddress: ph.address, GeocodedAt: ph.at}); err != nil {
			return err
		}
	}
	return nil
}

// lockTrip locks a trip under the caller's RLS (FOR UPDATE: a trip the caller cannot update is 404).
func lockTrip(ctx context.Context, q *operationsdb.Queries, id uuid.UUID) (Trip, error) {
	t, err := q.LockTrip(ctx, id)
	if err != nil {
		return Trip{}, MapError(notFound(err))
	}
	return t, nil
}

// tripView is the answer of a trip write: the trip as GET /v1/trips/{id} reads it.
func (s *Service) tripView(ctx context.Context, p *authz.Principal, id uuid.UUID, status int) (WriteResult, error) {
	v, err := s.GetTripView(ctx, p, id)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: status, Data: v}, nil
}

// --- PATCH /v1/trips/{id} -------------------------------------------------------------------------------------------

// tripChange is a parsed PATCH of a trip.
type tripChange struct {
	f      fields
	photos []photoInput
}

// PatchTripAPI is PATCH /v1/trips/{id} (operations:edit_trip_details): the seal and partner codes, the OCR data, the
// delivery time, the review flags, the route (origin, destination: resolved to their hubs as at load) and evidence
// photos (merged by type). A deliveredAt change of a delivered trip reads the period lock of its billing party in the
// transaction, for the old and the new instant (409 billing_period_locked with blockedInvoiceNumber, R17), and emits
// trip.delivered_at_changed (billing restamps) besides trip.updated. Photos need the PostgreSQL path (write-back: 422
// write_back_unsupported).
func (s *Service) PatchTripAPI(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, body []byte) (WriteResult, error) {
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
	f.only(tripPatchFields...)
	ch := tripChange{f: f, photos: parsePhotos(f)}
	if f.has("reviewStatus") {
		if v := f.optText("reviewStatus"); v != nil && *v != reviewPending {
			ck.add("reviewStatus", "invalid")
		}
	}
	if f.has("ocrData") && !f.isNull("ocrData") {
		if raw := bytes.TrimSpace(f.raw("ocrData")); len(raw) == 0 || raw[0] != '{' {
			ck.add("ocrData", "invalid")
		}
	}
	if mode == modeWriteBack && len(ch.photos) > 0 {
		ck.add("photos", ReasonWriteBackUnsupported)
	}
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.patchTripWriteBack(ctx, p, rid, d, id, ch)
	}
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		cur, err := lockTrip(ctx, q, id)
		if err != nil {
			return err
		}
		params, changed, err := s.mergeTrip(ctx, tx, d, cur, ch)
		if err != nil {
			return err
		}
		if len(changed) > 0 {
			if _, err := q.UpdateTripDetails(ctx, params); err != nil {
				return tripNoError(err, cur.TripNo)
			}
		}
		if len(ch.photos) > 0 {
			if err := s.putPhotos(ctx, tx, p, id, ch.photos); err != nil {
				return err
			}
			changed = append(changed, "photos")
		}
		if len(changed) == 0 {
			return nil
		}
		next, err := q.LockTrip(ctx, id)
		if err != nil {
			return MapError(err)
		}
		topics, err := tripTopics(ctx, q, next.TenantID, &next.ID)
		if err != nil {
			return err
		}
		evs := []opsEvent{withTopics(tripEvent(RouteTripUpdated, next, changed), topics)}
		if slices.Contains(changed, "deliveredAt") && cur.Status == TripDelivered {
			evs = append(evs, tripEvent(RouteTripDeliveredAtCh, next, []string{"deliveredAt"})) // billing only: no topic
		}
		return appendEvents(ctx, tx, rid, "", evs...)
	})
	if err != nil {
		return WriteResult{}, err
	}
	return s.tripView(ctx, p, id, http.StatusOK)
}

// mergeTrip merges a PATCH into the locked trip: the UpdateTripDetails parameters and the changed payload fields. The
// delivery-time rule reads the period lock in tx.
func (s *Service) mergeTrip(ctx context.Context, tx pgx.Tx, d directory, cur Trip, ch tripChange) (operationsdb.UpdateTripDetailsParams, []string, error) {
	f := ch.f
	var ck checker
	f.ck = &ck
	pr := operationsdb.UpdateTripDetailsParams{ID: cur.ID, SealCode: cur.SealCode, PartnerCode: cur.PartnerCode,
		OcrData: cur.OcrData, OriginRaw: cur.OriginRaw, OriginHubID: cur.OriginHubID, DestinationRaw: cur.DestinationRaw,
		DestinationHubID: cur.DestinationHubID, DestinationSocKey: cur.DestinationSocKey, TruckID: cur.TruckID,
		TruckLicensePlateSnapshot: cur.TruckLicensePlateSnapshot, TruckTypeRaw: cur.TruckTypeRaw, VehicleClass: cur.VehicleClass,
		DistanceKm: cur.DistanceKm, ParcelCount: cur.ParcelCount, SealTime: cur.SealTime, TotalWeightKg: cur.TotalWeightKg,
		Std: cur.Std, Sta: cur.Sta, Ata: cur.Ata, DurationMinutes: cur.DurationMinutes, DeliveredAt: cur.DeliveredAt,
		NeedsAdminReview: cur.NeedsAdminReview, ReviewStatus: cur.ReviewStatus, ReviewReason: cur.ReviewReason}
	var changed []string
	mark := func(k string) { changed = append(changed, k) }
	if f.has("sealCode") {
		if v := f.optText("sealCode"); !sameStr(v, cur.SealCode) {
			pr.SealCode = v
			mark("sealCode")
		}
	}
	if f.has("partnerCode") {
		if v := f.optText("partnerCode"); !sameStr(v, cur.PartnerCode) {
			pr.PartnerCode = v
			mark("partnerCode")
		}
	}
	if f.has("ocrData") {
		var raw []byte
		if !f.isNull("ocrData") {
			raw = bytes.TrimSpace(f.raw("ocrData"))
		}
		if !jsonEqual(raw, cur.OcrData) {
			pr.OcrData = raw
			mark("ocrData")
		}
	}
	if f.has("needsAdminReview") {
		if b := f.boolean("needsAdminReview"); b != nil && *b != cur.NeedsAdminReview {
			pr.NeedsAdminReview = *b
			mark("needsAdminReview")
		} else if b == nil {
			ck.add("needsAdminReview", "invalid")
		}
	}
	if f.has("reviewStatus") {
		if v := f.optText("reviewStatus"); !sameStr(v, cur.ReviewStatus) {
			pr.ReviewStatus = v
			mark("reviewStatus")
		}
	}
	if f.has("reviewReason") {
		if v := f.optText("reviewReason"); !sameStr(v, cur.ReviewReason) {
			pr.ReviewReason = v
			mark("reviewReason")
		}
	}
	if f.has("origin") {
		v := f.optText("origin")
		if !sameStr(v, cur.OriginRaw) {
			pr.OriginRaw, pr.OriginHubID = v, nil
			if v != nil {
				pr.OriginHubID = d.resolve(*v).hubID
			}
			mark("origin")
		}
	}
	if f.has("destination") {
		v := f.optText("destination")
		if !sameStr(v, cur.DestinationRaw) {
			pr.DestinationRaw, pr.DestinationHubID, pr.DestinationSocKey = v, nil, nil
			if v != nil {
				r := d.resolve(*v)
				pr.DestinationHubID, pr.DestinationSocKey = r.hubID, r.socKey
			}
			mark("destination")
		}
	}
	if f.has("deliveredAt") {
		at := f.instant("deliveredAt")
		if !sameTime(at, cur.DeliveredAt) {
			if at == nil && cur.Status == TripDelivered {
				ck.add("deliveredAt", "required") // a delivered trip keeps a delivery time (the billing axis, Q13)
			}
			pr.DeliveredAt = at
			mark("deliveredAt")
		}
	}
	if err := ck.err(); err != nil {
		return pr, nil, err
	}
	if slices.Contains(changed, "deliveredAt") && cur.Status == TripDelivered && cur.BillingPartyID != nil {
		var old, next time.Time
		if cur.DeliveredAt != nil {
			old = *cur.DeliveredAt
		}
		if pr.DeliveredAt != nil {
			next = *pr.DeliveredAt
		}
		if l, locked, err := billing.PeriodLock(ctx, tx, *cur.BillingPartyID, old, next); err != nil {
			return pr, nil, MapError(err)
		} else if locked {
			return pr, nil, billing.ErrPeriodLocked(l)
		}
	}
	return pr, changed, nil
}

func jsonEqual(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}

// --- POST /v1/trips/{id}/deliver ------------------------------------------------------------------------------------

// DeliverTripAPI is POST /v1/trips/{id}/deliver {deliveredAt?, lat?, lng?, photos[]?} (operations:edit_trip_details,
// the admin close of a stuck trip, EditTripDetailsDialog.tsx:688-747): in one transaction the in-transit (or incident)
// trip becomes delivered with deliveredVia admin_web (deliveredAt: the body's, else the trip's own, else now), the
// delivery photos are merged, its task is completed and the driver's active truck is cleared when it still names that
// task; the outbox gets trip.delivered (billing.compute prices it, notify.line sends the delivered card) and the task's
// task.updated. A delivered or cancelled trip is 409 trip_not_open.
func (s *Service) DeliverTripAPI(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, body []byte) (WriteResult, error) {
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
	f.only("deliveredAt", "lat", "lng", "photos")
	at, lat, lng := f.instant("deliveredAt"), f.number("lat"), f.number("lng")
	photos := parsePhotos(f)
	if mode == modeWriteBack && len(photos) > 0 {
		ck.add("photos", ReasonWriteBackUnsupported)
	}
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.deliverTripWriteBack(ctx, p, rid, id, at, lat, lng)
	}
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		cur, err := lockTrip(ctx, q, id)
		if err != nil {
			return err
		}
		if cur.Status != TripInTransit && cur.Status != TripIncident {
			return errPrecondition(ReasonTripNotOpen, "only an in-transit or incident trip is delivered").
				WithDetails(map[string]any{"status": cur.Status})
		}
		when := deliveryTime(at, cur.DeliveredAt, s.now())
		next, err := q.DeliverTrip(ctx, operationsdb.DeliverTripParams{ID: id, DeliveredAt: when, Lat: lat, Lng: lng,
			DeliveredVia: deliveredViaAdminWeb})
		if err != nil {
			return MapError(notFound(err))
		}
		if err := s.putPhotos(ctx, tx, p, id, photos); err != nil {
			return err
		}
		topics, err := tripTopics(ctx, q, next.TenantID, &next.ID)
		if err != nil {
			return err
		}
		evs := []opsEvent{withTopics(tripEvent(RouteTripDelivered, next, []string{"status", "deliveredAt"}), topics)}
		if next.TaskID != nil {
			task, err := q.LockTask(ctx, *next.TaskID)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				// a task outside the caller's write reach stays as it is
			case err != nil:
				return MapError(err)
			case task.Status != TaskCompleted && task.Status != TaskCancelled:
				done, err := q.SetTaskStatus(ctx, operationsdb.SetTaskStatusParams{ID: task.ID, Status: TaskCompleted})
				if err != nil {
					return MapError(err)
				}
				if task.DriverID != nil {
					if _, err := q.ClearDriverActiveTask(ctx, operationsdb.ClearDriverActiveTaskParams{DriverID: *task.DriverID,
						TaskID: &task.ID}); err != nil {
						return MapError(err)
					}
				}
				tt, err := taskTopics(ctx, q, done)
				if err != nil {
					return err
				}
				evs = append(evs, withTopics(taskEvent(RouteTaskUpdated, done, []string{"status"}), tt))
			}
		}
		return appendEvents(ctx, tx, rid, "", evs...)
	})
	if err != nil {
		return WriteResult{}, err
	}
	return s.tripView(ctx, p, id, http.StatusOK)
}

// deliveryTime is the delivery instant of an admin close: the body's, else the trip's own (the driver may have stamped
// it), else now (EditTripDetailsDialog.tsx:697-701).
func deliveryTime(body, cur *time.Time, now time.Time) time.Time {
	switch {
	case body != nil:
		return body.UTC()
	case cur != nil:
		return cur.UTC()
	}
	return now.UTC()
}

// --- POST /v1/trips/{id}/cancel, POST /v1/trips/bulk-cancel ---------------------------------------------------------

// CancelTripAPI is POST /v1/trips/{id}/cancel (operations:edit_trip_details; EditTripDetailsDialog.tsx:748-766): the
// trip becomes cancelled (no income) and its open task cancelled with it; trip.cancelled and task.cancelled. A
// cancelled trip is answered as it is; a delivered trip is 409 trip_delivered (its income is the billing's: reprice
// or credit it instead).
func (s *Service) CancelTripAPI(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, body []byte) (WriteResult, error) {
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
	f.only()
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.cancelTripWriteBack(ctx, p, rid, id)
	}
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		cur, err := lockTrip(ctx, q, id)
		if err != nil {
			return err
		}
		switch cur.Status {
		case TripCancelled:
			return nil
		case TripDelivered:
			return errPrecondition(ReasonTripDelivered, "a delivered trip is not cancelled").WithDetails(map[string]any{"status": cur.Status})
		}
		next, err := q.SetTripStatus(ctx, operationsdb.SetTripStatusParams{ID: id, Status: TripCancelled})
		if err != nil {
			return MapError(err)
		}
		evs, err := s.cancelTripTask(ctx, q, next)
		if err != nil {
			return err
		}
		return appendEvents(ctx, tx, rid, "", evs...)
	})
	if err != nil {
		return WriteResult{}, err
	}
	return s.tripView(ctx, p, id, http.StatusOK)
}

// cancelTripTask cancels the open task of a cancelled trip and returns the events of both.
func (s *Service) cancelTripTask(ctx context.Context, q *operationsdb.Queries, trip Trip) ([]opsEvent, error) {
	topics, err := tripTopics(ctx, q, trip.TenantID, &trip.ID)
	if err != nil {
		return nil, err
	}
	evs := []opsEvent{withTopics(tripEvent(RouteTripCancelled, trip, []string{"status"}), topics)}
	if trip.TaskID == nil {
		return evs, nil
	}
	task, err := q.LockTask(ctx, *trip.TaskID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return evs, nil
	case err != nil:
		return nil, MapError(err)
	case task.Status == TaskCancelled || task.Status == TaskCompleted:
		return evs, nil
	}
	done, err := q.CancelTask(ctx, operationsdb.CancelTaskParams{ID: task.ID, CancelledAt: s.now().UTC(),
		Reason: ptr("trip " + trip.TripNo + " cancelled")})
	if err != nil {
		return nil, MapError(err)
	}
	tt, err := taskTopics(ctx, q, done)
	if err != nil {
		return nil, err
	}
	return append(evs, withTopics(taskEvent(RouteTaskCancelled, done, []string{"status"}), tt)), nil
}

// BulkCancelResult is the answer of POST /v1/trips/bulk-cancel.
type BulkCancelResult struct {
	Cancelled []uuid.UUID         `json:"cancelled"`
	Failed    []BulkCancelFailure `json:"failed"`
	// Pending is set on the 202 of a write-back the mirror did not acknowledge within the wait.
	Pending bool `json:"pending,omitempty"`
}

// BulkCancelFailure names a trip the bulk cancel left as it was: not_found (outside the caller's write reach) or
// not_in_transit.
type BulkCancelFailure struct {
	ID     uuid.UUID `json:"id"`
	Reason string    `json:"reason"`
	Status string    `json:"status,omitempty"`
}

// BulkCancelTripsAPI is POST /v1/trips/bulk-cancel {ids[]} (operations:edit_trip_details; the "clear the driver's other
// stuck trips" option, EditTripDetailsDialog.tsx:771-800): the in-transit trips among ids (at most 200) are cancelled
// with their open tasks in one transaction; the others are listed in failed[].
func (s *Service) BulkCancelTripsAPI(ctx context.Context, p *authz.Principal, rid string, body []byte) (WriteResult, error) {
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
	f.only("ids")
	ids := parseIDs(f, "ids")
	switch {
	case len(ids) == 0 && !ck.hasField("ids"):
		ck.add("ids", "required")
	case len(ids) > MaxBulkCancel:
		ck.add("ids", "too_many")
	}
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.bulkCancelWriteBack(ctx, p, rid, ids)
	}
	out := BulkCancelResult{Cancelled: []uuid.UUID{}, Failed: []BulkCancelFailure{}}
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		rows, err := q.CancelInTransitTrips(ctx, ids)
		if err != nil {
			return MapError(err)
		}
		var evs []opsEvent
		done := map[uuid.UUID]bool{}
		for _, r := range rows {
			done[r.ID] = true
			out.Cancelled = append(out.Cancelled, r.ID)
			trip, err := q.LockTrip(ctx, r.ID)
			if err != nil {
				return MapError(err)
			}
			te, err := s.cancelTripTask(ctx, q, trip)
			if err != nil {
				return err
			}
			evs = append(evs, te...)
		}
		r := ReachOf(p)
		for _, id := range ids {
			if done[id] {
				continue
			}
			t, err := q.GetTrip(ctx, operationsdb.GetTripParams{ID: id, AllTenants: r.All, TenantIds: r.tenants(), ReachDriver: r.Driver})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				out.Failed = append(out.Failed, BulkCancelFailure{ID: id, Reason: "not_found"})
			case err != nil:
				return MapError(err)
			default:
				out.Failed = append(out.Failed, BulkCancelFailure{ID: id, Reason: ReasonNotInTransit, Status: t.Status})
			}
		}
		return appendEvents(ctx, tx, rid, "", evs...)
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: http.StatusOK, Data: out}, nil
}

// parseIDs reads a list of uuids (duplicates dropped, order kept).
func parseIDs(f fields, k string) []uuid.UUID {
	raw := f.raw(k)
	if raw == nil || isNull(raw) {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		f.ck.add(k, "invalid")
		return nil
	}
	var out []uuid.UUID
	for _, it := range items {
		id, ok := parseID(it)
		if !ok {
			f.ck.add(k, "invalid")
			return nil
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// --- POST /v1/trips/{id}/rename -------------------------------------------------------------------------------------

// RenameTripAPI is POST /v1/trips/{id}/rename {newTripNo} (operations:edit_trip_details as tenant_admin, or a platform
// admin acting through X-Act-On-Tenant; renameTripRecord was admin-only): the number of a delivered trip changes in
// place with one trip_no_history row and trip_record_renamed in the same transaction; the outbox gets trip.updated
// with previousTripNo. While Firestore owns the collection the rename stays copy + re-point + delete in one Firestore
// batch (until T58), which the mirror applies as the same rename.
func (s *Service) RenameTripAPI(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, body []byte) (WriteResult, error) {
	if err := requireCap(p, authz.OperationsEditTripDetails); err != nil {
		return WriteResult{}, err
	}
	if p.EffectiveRole() != authz.TenantAdmin {
		return WriteResult{}, authz.ErrPermissionDenied("a trip is renamed by a tenant admin").
			WithDetails(map[string]any{"reason": ReasonTenantAdminOnly})
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
	f.only("newTripNo")
	newNo := f.required("newTripNo")
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.renameTripWriteBack(ctx, p, rid, id, newNo)
	}
	if s.renamer == nil {
		return WriteResult{}, errors.New("operations: no trip renamer is wired")
	}
	// The caller's RLS must let it update the trip (404 otherwise) before the system transaction renames it.
	if err := s.Tx(ctx, p, func(tx pgx.Tx) error {
		_, err := lockTrip(ctx, operationsdb.New(tx), id)
		return err
	}); err != nil {
		return WriteResult{}, err
	}
	if _, _, err := s.renamer.RenameTrip(ctx, p, rid, id, strings.TrimSpace(newNo)); err != nil {
		return WriteResult{}, err
	}
	return s.tripView(ctx, p, id, http.StatusOK)
}
