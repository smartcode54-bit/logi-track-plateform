package notify

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify/notifydb"
)

// AdminWebCloseNote is the delivered card's หมายเหตุ when an admin closed the job from the web
// (trip_records.delivered_via = 'admin_web', fn:lineNotify.ts:52).
const AdminWebCloseNote = "ปิดงานโดยแอดมิน (ผ่านหน้าเว็บ)"

// noteJoin joins the delay note and the admin-close note (fn:lineNotify.ts:438).
const noteJoin = " · "

// lineCard is one record as its card needs it: the card's values, its target group, the flag, the evidence state and
// who may ask for it.
type lineCard struct {
	card       string
	recordID   uuid.UUID
	notifiedAt *time.Time
	owners     []*uuid.UUID // the record's driver and, through its task, the task's driver and helper
	// ready: the record holds what its card shows (a check-in time; a delivered status). A driver's request on a
	// record the mirror has not brought there yet waits (LineMirrorWait).
	ready bool
	group string // the LINE group (to); "" = none configured
	// evidence: the card may carry the gallery link (delivered, standby), from token / revokedAt and photos.
	evidence  bool
	token     *string
	revokedAt *time.Time
	photos    int
	trip      LineTripCard
	standby   LineStandbyCard
}

func (c lineCard) entity() string {
	switch c.card {
	case LineCheckin:
		return "task"
	case LineDelivered:
		return "trip"
	}
	return "standby"
}

func (c lineCard) ownedBy(driver uuid.UUID) bool {
	for _, o := range c.owners {
		if o != nil && *o == driver {
			return true
		}
	}
	return false
}

// message builds the card; link is the gallery URL ("" for none).
func (c lineCard) message(link string) FlexMessage {
	switch c.card {
	case LineCheckin:
		return BuildCheckinMessage(c.trip)
	case LineDelivered:
		t := c.trip
		if link != "" && c.photos > 0 {
			t.EvidenceURL, t.PhotoCount = link, c.photos
		}
		return BuildDeliveredMessage(t)
	}
	s := c.standby
	if link != "" && c.photos > 0 {
		s.EvidenceURL, s.PhotoCount = link, c.photos
	}
	return BuildStandbyMessage(s)
}

// lineParty is a billing party as the cards read it.
type lineParty struct {
	group, code string
	customerID  *uuid.UUID
}

// cardReader reads the values of one card within the consumer's transaction.
type cardReader struct {
	ctx     context.Context
	q       *notifydb.Queries
	parties map[uuid.UUID]lineParty
}

func (l *LINE) load(ctx context.Context, q *notifydb.Queries, card string, id uuid.UUID) (lineCard, bool, error) {
	r := &cardReader{ctx: ctx, q: q, parties: map[uuid.UUID]lineParty{}}
	var c lineCard
	var err error
	switch card {
	case LineCheckin:
		c, err = r.checkin(id)
	case LineDelivered:
		c, err = r.delivered(id)
	default:
		c, err = r.standbyCard(id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return lineCard{}, false, nil
	}
	return c, err == nil, err
}

// checkin is the check-in card of a task (fn:lineNotify.ts:300-329).
func (r *cardReader) checkin(id uuid.UUID) (lineCard, error) {
	t, err := r.q.LineTask(r.ctx, id)
	if err != nil {
		return lineCard{}, err
	}
	src, err := r.party(t.SourceLinkedPartyID)
	if err != nil {
		return lineCard{}, err
	}
	dst, err := r.party(t.DestinationLinkedPartyID)
	if err != nil {
		return lineCard{}, err
	}
	target := firstParty(t.SourceLinkedPartyID, src, t.DestinationLinkedPartyID, dst)
	drv, err := r.driver(t.DriverID)
	if err != nil {
		return lineCard{}, err
	}
	code, err := r.driverCode(t.DriverID, target)
	if err != nil {
		return lineCard{}, err
	}
	labels, err := r.hubLabels(t.SourceHubRaw, t.DestinationRaw)
	if err != nil {
		return lineCard{}, err
	}
	truckType, err := r.truckType(t.TruckID, t.TruckType)
	if err != nil {
		return lineCard{}, err
	}
	date := t.PlanAt
	if t.CheckInAt != nil {
		date = *t.CheckInAt
	}
	return lineCard{
		card: LineCheckin, recordID: t.ID, notifiedAt: t.LineCheckinNotifiedAt,
		owners: []*uuid.UUID{t.DriverID, t.HelperDriverID}, ready: t.CheckInAt != nil, group: target.group,
		trip: LineTripCard{
			DateLine:         FormatBuddhistShortDate(date),
			OriginLabel:      labels(t.SourceHubRaw),
			DestinationLabel: labels(t.DestinationRaw),
			TripNo:           or(jsTrim(t.TaskNo), t.Ref),
			// The legacy fallback was the task's denormalised driverName, which PostgreSQL does not keep.
			DriverNameTh: ResolveDriverNameTh(drv.name(), ""),
			DriverCode:   code,
			Plate:        or(jsTrim(t.LicensePlate), drv.plate()),
			Phone:        drv.phone(),
			Partner:      or(src.code, dst.code),
			TruckType:    truckType,
			CheckInHm:    FormatBangkokHm(t.CheckInAt),
		},
	}, nil
}

// delivered is the delivered card of a trip (fn:lineNotify.ts:392-466).
func (r *cardReader) delivered(id uuid.UUID) (lineCard, error) {
	tr, err := r.q.LineTrip(r.ctx, id)
	if err != nil {
		return lineCard{}, err
	}
	src, err := r.party(tr.TaskSourcePartyID)
	if err != nil {
		return lineCard{}, err
	}
	dst, err := r.party(tr.TaskDestinationPartyID)
	if err != nil {
		return lineCard{}, err
	}
	billing, err := r.party(tr.BillingPartyID)
	if err != nil {
		return lineCard{}, err
	}
	// The task's linked party first (source, else destination); the trip's billing party only when the task names
	// none (resolveLineTarget).
	target := firstParty(tr.TaskSourcePartyID, src, tr.TaskDestinationPartyID, dst)
	if tr.TaskSourcePartyID == nil && tr.TaskDestinationPartyID == nil {
		target = billing
	}
	drv, err := r.driver(tr.DriverID)
	if err != nil {
		return lineCard{}, err
	}
	code, err := r.driverCode(tr.DriverID, target)
	if err != nil {
		return lineCard{}, err
	}
	origin, destination := deref(tr.TaskSourceHubRaw), deref(tr.TaskDestinationRaw)
	if tr.OriginRaw != nil {
		origin = *tr.OriginRaw
	}
	if tr.DestinationRaw != nil {
		destination = *tr.DestinationRaw
	}
	labels, err := r.hubLabels(origin, destination)
	if err != nil {
		return lineCard{}, err
	}
	truckType, err := r.truckType(tr.TruckID, tr.TruckTypeRaw)
	if err != nil {
		return lineCard{}, err
	}
	photos, err := r.q.LineTripPhotoCount(r.ctx, tr.ID)
	if err != nil {
		return lineCard{}, err
	}
	causes, err := r.q.LineTripDelayCauses(r.ctx, tr.ID)
	if err != nil {
		return lineCard{}, err
	}
	var notes []string
	if len(causes) > 0 {
		notes = append(notes, BuildDelayNote(causes))
	}
	if jsTrim(tr.DeliveredVia) == "admin_web" {
		notes = append(notes, AdminWebCloseNote)
	}
	date := tr.CreatedAt
	if tr.Std != nil {
		date = *tr.Std
	}
	parcels := ""
	if tr.ParcelCount != nil {
		parcels = strconv.Itoa(int(*tr.ParcelCount))
	}
	return lineCard{
		card: LineDelivered, recordID: tr.ID, notifiedAt: tr.LineDeliveredNotifiedAt,
		owners: []*uuid.UUID{tr.DriverID, tr.TaskDriverID, tr.TaskHelperDriverID}, ready: tr.Status == "delivered",
		group: target.group, evidence: true, token: tr.EvidenceToken, revokedAt: tr.EvidenceTokenRevokedAt, photos: int(photos),
		trip: LineTripCard{
			DateLine:         FormatBuddhistShortDate(date),
			OriginLabel:      labels(origin),
			DestinationLabel: labels(destination),
			TripNo:           or(jsTrim(tr.OcrTripID), tr.TripNo),
			DriverNameTh:     ResolveDriverNameTh(drv.name(), tr.LegacyDriverRef),
			DriverCode:       code,
			Plate:            or(jsTrim(tr.TruckPlate), drv.plate()),
			Phone:            drv.phone(),
			Partner:          or(jsTrim(tr.PartnerCode), src.code),
			TruckType:        truckType,
			Parcels:          parcels,
			CheckInHm:        FormatBangkokHm(tr.TaskCheckInAt),
			DepartHm:         FormatBangkokHm(tr.Std),
			ArriveHm:         FormatBangkokHm(tr.Ata),
			DoneHm:           FormatBangkokHm(tr.DeliveredAt),
			Notes:            strings.Join(notes, noteJoin),
		},
	}, nil
}

// standbyCard is the standby card (fn:lineNotify.ts:331-390): the record's customer party only (ADR 0008).
func (r *cardReader) standbyCard(id uuid.UUID) (lineCard, error) {
	s, err := r.q.LineStandby(r.ctx, id)
	if err != nil {
		return lineCard{}, err
	}
	target, err := r.party(s.CustomerPartyID)
	if err != nil {
		return lineCard{}, err
	}
	drv, err := r.driver(s.DriverID)
	if err != nil {
		return lineCard{}, err
	}
	code, err := r.driverCode(s.DriverID, target)
	if err != nil {
		return lineCard{}, err
	}
	start, end := deref(s.StartLocationRaw), deref(s.EndLocationRaw)
	labels, err := r.hubLabels(start, end)
	if err != nil {
		return lineCard{}, err
	}
	photos, err := r.q.LineStandbyPhotoCount(r.ctx, s.ID)
	if err != nil {
		return lineCard{}, err
	}
	date := s.CreatedAt
	switch {
	case s.EndedAt != nil:
		date = *s.EndedAt
	case s.StartedAt != nil:
		date = *s.StartedAt
	}
	duration := "-"
	if s.DurationMinutes != nil {
		duration = strconv.Itoa(int(*s.DurationMinutes)) + " นาที"
	}
	return lineCard{
		card: LineStandby, recordID: s.ID, notifiedAt: s.LineNotifiedAt,
		owners: []*uuid.UUID{s.DriverID, s.TaskDriverID, s.TaskHelperDriverID}, ready: true,
		group: target.group, evidence: true, token: s.EvidenceToken, revokedAt: s.EvidenceTokenRevokedAt, photos: int(photos),
		standby: LineStandbyCard{
			DateLine:     FormatBuddhistShortDate(date),
			StartLabel:   labels(start),
			EndLabel:     labels(end),
			DriverNameTh: ResolveDriverNameTh(drv.name(), s.LegacyDriverRef),
			DriverCode:   code,
			Plate:        or(jsTrim(s.TruckPlate), drv.plate()),
			Phone:        drv.phone(),
			Partner:      target.code,
			StartedHm:    FormatBangkokHm(s.StartedAt),
			EndedHm:      FormatBangkokHm(s.EndedAt),
			DurationText: duration,
			Notes:        jsTrim(s.Note),
		},
	}, nil
}

// party reads a billing party once per card; nil is no party.
func (r *cardReader) party(id *uuid.UUID) (lineParty, error) {
	if id == nil {
		return lineParty{}, nil
	}
	if p, ok := r.parties[*id]; ok {
		return p, nil
	}
	row, err := r.q.LineParty(r.ctx, *id)
	if errors.Is(err, pgx.ErrNoRows) {
		return lineParty{}, nil
	}
	if err != nil {
		return lineParty{}, err
	}
	p := lineParty{group: jsTrim(row.LineGroupID), code: jsTrim(row.Code), customerID: row.CustomerID}
	r.parties[*id] = p
	return p, nil
}

// firstParty is resolveLinkedEntity: the source party when the task names one, else the destination party.
func firstParty(srcID *uuid.UUID, src lineParty, dstID *uuid.UUID, dst lineParty) lineParty {
	if srcID != nil {
		return src
	}
	if dstID != nil {
		return dst
	}
	return lineParty{}
}

// lineDriver is a driver as the cards read it; the zero value is a driver that could not be read.
type lineDriver struct {
	row   notifydb.LineDriverRow
	found bool
}

func (r *cardReader) driver(id *uuid.UUID) (lineDriver, error) {
	if id == nil {
		return lineDriver{}, nil
	}
	row, err := r.q.LineDriver(r.ctx, *id)
	if errors.Is(err, pgx.ErrNoRows) {
		return lineDriver{}, nil
	}
	if err != nil {
		return lineDriver{}, err
	}
	return lineDriver{row: row, found: true}, nil
}

func (d lineDriver) name() *LineDriverName {
	if !d.found {
		return nil
	}
	return &LineDriverName{FullNameTh: d.row.FullNameTh, FirstName: d.row.FirstName, LastName: d.row.LastName, Email: d.row.Email}
}

// plate is plateFromDriver: the truck the driver is responsible for now, else the truck of its current assignment.
func (d lineDriver) plate() string {
	return or(jsTrim(d.row.ActiveTruckPlate), jsTrim(d.row.AssignmentPlate))
}

func (d lineDriver) phone() string { return jsTrim(d.row.Mobile) }

// driverCode is the id the target customer keeps for the driver ("-" for a carrier party or none kept); never the
// national ID card or the licence.
func (r *cardReader) driverCode(driver *uuid.UUID, target lineParty) (string, error) {
	if driver == nil || target.customerID == nil || target.code == "" {
		return "-", nil
	}
	rows, err := r.q.LineDriverCodes(r.ctx, notifydb.LineDriverCodesParams{DriverID: *driver, CustomerID: *target.customerID})
	if err != nil {
		return "", err
	}
	entries := make([]LineCodeEntry, len(rows))
	for i, e := range rows {
		entries[i] = LineCodeEntry{Key: e.IDTypeKey, Value: e.Value}
	}
	return ResolveDriverCustomerCode(entries), nil
}

// hubLabels is hubLabel for the places of one card: a value that is a hub code (exact) shows the hub's Thai name,
// else its English name; anything else shows as it is, "-" when blank.
func (r *cardReader) hubLabels(raws ...string) (func(string) string, error) {
	var codes []string
	for _, v := range raws {
		if s := jsTrim(v); s != "" {
			codes = append(codes, s)
		}
	}
	names := map[string]string{}
	if len(codes) > 0 {
		rows, err := r.q.LineHubNames(r.ctx, codes)
		if err != nil {
			return nil, err
		}
		for _, h := range rows {
			if n := or(jsTrim(h.NameTh), jsTrim(h.NameEn)); n != "" {
				names[jsTrim(h.Code)] = n
			}
		}
	}
	return func(raw string) string {
		v := jsTrim(raw)
		if v == "" {
			return "-"
		}
		if n, ok := names[v]; ok {
			return n
		}
		return v
	}, nil
}

// truckType is loadTruckType: the truck's type as the fleet master spells it, else the record's own value.
func (r *cardReader) truckType(id *uuid.UUID, fallback string) (string, error) {
	if id != nil {
		t, err := r.q.LineTruckType(r.ctx, *id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if s := jsTrim(t); s != "" {
			return s, nil
		}
	}
	return or(jsTrim(fallback), "-"), nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
