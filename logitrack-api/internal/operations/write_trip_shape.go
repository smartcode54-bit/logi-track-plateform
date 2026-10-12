package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The two edits of EditTripDetailsDialog.tsx that T31 left without a route (issue #54, the T31 handoff): the stop
// progress of a multi-drop trip (stops[] of PATCH /v1/trips/{id}: per stop its destination, status, delivery time and
// position; stops added, removed and re-pointed) and the job type an edited origin implies (a hub first_mile, a SOC
// line_haul). Both are mirrored on the trip's task with its route (EditTripDetailsDialog.tsx:569-606: the task's
// deliveryStops, isMultiDelivery, taskType, sourceHub with its hub's billing link, and destination), in the trip's
// FOR UPDATE transaction on the PostgreSQL path and in one Firestore commit over both documents on the write-back
// path. The task's "not past assigned" rule of PATCH /v1/tasks/{id} does not apply: this is an admin correction of
// what happened, not a new plan.

// MaxTripStops bounds the stops of one trip edit.
const MaxTripStops = 50

// tripStopIn is one stop of PATCH /v1/trips/{id} stops[] (its index is its position + 1): destination is required;
// status (pending | delivered | failed), deliveredAt, lat and lng are optional (absent: the stop at that index keeps
// its own; null clears).
type tripStopIn struct {
	destination    string
	status         *string
	deliveredAt    *time.Time
	deliveredAtSet bool
	lat, lng       *float64
	latSet, lngSet bool
}

// parseTripStops reads stops[]: absent = no stop edit; an array (empty included) is the whole new list. Violations are
// named stops.<field> with the stop's position (params.index, 1-based).
func parseTripStops(f fields) ([]tripStopIn, bool) {
	raw := f.raw("stops")
	if raw == nil {
		return nil, false
	}
	var items []json.RawMessage
	if isNull(raw) || json.Unmarshal(raw, &items) != nil {
		f.ck.add("stops", "invalid")
		return nil, false
	}
	if len(items) > MaxTripStops {
		f.ck.bad = append(f.ck.bad, httpx.FieldViolation{Field: "stops", Reason: "too_many", Params: map[string]any{"max": MaxTripStops}})
		return nil, false
	}
	out := make([]tripStopIn, 0, len(items))
	seen := map[string]bool{}
	for i, it := range items {
		var sub checker
		bad := func(field, reason string) {
			f.ck.bad = append(f.ck.bad, httpx.FieldViolation{Field: "stops." + field, Reason: reason,
				Params: map[string]any{"index": i + 1}})
		}
		sf, err := parseFields(it, &sub)
		if err != nil || !bytes.HasPrefix(bytes.TrimSpace(it), []byte("{")) {
			bad("destination", "invalid")
			continue
		}
		sf.only("destination", "status", "deliveredAt", "lat", "lng")
		st := tripStopIn{destination: normalizeDestination(sf.required("destination")), deliveredAt: sf.instant("deliveredAt"),
			deliveredAtSet: sf.has("deliveredAt"), lat: sf.number("lat"), lng: sf.number("lng"), latSet: sf.has("lat"),
			lngSet: sf.has("lng")}
		if sf.has("status") {
			switch v := sf.optText("status"); {
			case v == nil:
				sub.add("status", "required")
			case slices.Contains(StopStatuses, strings.ToLower(*v)):
				low := strings.ToLower(*v)
				st.status = &low
			default:
				sub.add("status", "invalid")
			}
		}
		for _, v := range sub.bad {
			f.ck.bad = append(f.ck.bad, httpx.FieldViolation{Field: "stops." + v.Field, Reason: v.Reason,
				Params: mergeParams(v.Params, map[string]any{"index": i + 1})})
		}
		if st.destination != "" {
			if seen[st.destination] {
				bad("destination", "duplicate") // UNIQUE (task_id, destination_raw) of the task's mirror
			}
			seen[st.destination] = true
		}
		out = append(out, st)
	}
	return out, true
}

func mergeParams(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// stopState is one stop of a trip's progress: a trip_delivery_stops row, a deliveryStopsProgress entry of the
// Firestore document (doc: the entry as read, so its photos and any other key are written back unchanged), or a stop
// of the edit.
type stopState struct {
	index        int32
	destination  string
	hubID        *uuid.UUID
	status       string
	deliveredAt  *time.Time
	lat, lng     *float64
	completedSeq *int32
	doc          map[string]any
}

func rowStops(rows []TripStop) []stopState {
	out := make([]stopState, len(rows))
	for i, r := range rows {
		out[i] = stopState{index: r.StopIndex, destination: r.DestinationRaw, hubID: r.DestinationHubID, status: r.Status,
			deliveredAt: r.DeliveredAt, lat: r.DeliveredLat, lng: r.DeliveredLng, completedSeq: r.CompletedSeq}
	}
	return out
}

// docStops reads deliveryStopsProgress[] of a trip document: its array position is the completion order (Q16, the
// loader's completed_seq).
func docStops(v any) []stopState {
	items, _ := v.([]any)
	out := make([]stopState, 0, len(items))
	for i, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		idx, ok := docInt(m["index"])
		if !ok || idx < 1 {
			continue
		}
		st := stopState{index: idx, destination: stringOf(m["destination"]), status: strings.ToLower(stringOf(m["status"])),
			deliveredAt: docTime(m["deliveredAt"]), lat: docFloat(m["deliveredLat"]), lng: docFloat(m["deliveredLng"]), doc: m}
		if !slices.Contains(StopStatuses, st.status) {
			st.status = "pending"
		}
		if st.status == "delivered" {
			seq := int32(i + 1)
			st.completedSeq = &seq
		}
		out = append(out, st)
	}
	return out
}

func docInt(v any) (int32, bool) {
	switch x := v.(type) {
	case int64:
		return int32(x), true
	case float64:
		if x == float64(int32(x)) {
			return int32(x), true
		}
	case int:
		return int32(x), true
	}
	return 0, false
}

func docFloat(v any) *float64 {
	switch x := v.(type) {
	case float64:
		return &x
	case int64:
		f := float64(x)
		return &f
	}
	return nil
}

func docTime(v any) *time.Time {
	switch x := v.(type) {
	case dump.Timestamp:
		t := x.UTC()
		return &t
	case time.Time:
		t := x.UTC()
		return &t
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return &t
		}
	}
	return nil
}

// mergeStops is the stop list an edit leads to: stop i+1 of the body over the current stop of that index. A stop keeps
// its own status and, while it is not pending, its delivery time and position unless the body names them; a pending
// stop has none. A stop delivered before keeps its completion order, a newly delivered one comes after every other.
func (d directory) mergeStops(cur []stopState, in []tripStopIn) []stopState {
	byIndex := map[int32]stopState{}
	var maxSeq int32
	for _, c := range cur {
		byIndex[c.index] = c
		if c.completedSeq != nil && *c.completedSeq > maxSeq {
			maxSeq = *c.completedSeq
		}
	}
	out := make([]stopState, len(in))
	for i, s := range in {
		idx := int32(i + 1)
		c, had := byIndex[idx]
		n := stopState{index: idx, destination: s.destination, hubID: d.resolve(s.destination).hubID, status: "pending", doc: c.doc}
		if had {
			n.status = c.status
		}
		if s.status != nil {
			n.status = *s.status
		}
		if n.status != "pending" {
			if had && c.status != "pending" {
				n.deliveredAt, n.lat, n.lng = c.deliveredAt, c.lat, c.lng
			}
			if s.deliveredAtSet {
				n.deliveredAt = s.deliveredAt
			}
			if s.latSet {
				n.lat = s.lat
			}
			if s.lngSet {
				n.lng = s.lng
			}
		}
		if n.status == "delivered" {
			if had && c.status == "delivered" && c.completedSeq != nil {
				n.completedSeq = c.completedSeq
			} else {
				maxSeq++
				seq := maxSeq
				n.completedSeq = &seq
			}
		}
		out[i] = n
	}
	return out
}

// sameStops reports whether an edit leaves the stop list as it is.
func sameStops(cur, next []stopState) bool {
	if len(cur) != len(next) {
		return false
	}
	for i := range cur {
		a, b := cur[i], next[i]
		if a.index != b.index || a.destination != b.destination || !sameID(a.hubID, b.hubID) || a.status != b.status ||
			!sameTime(a.deliveredAt, b.deliveredAt) || !sameFloat(a.lat, b.lat) || !sameFloat(a.lng, b.lng) ||
			!sameInt32(a.completedSeq, b.completedSeq) {
			return false
		}
	}
	return true
}

func sameFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameInt32(a, b *int32) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// jobTypeOf is the job type an origin implies (EditTripDetailsDialog.tsx:365-370): a SOC key, or a hub of station type
// SOC, is line_haul; another hub first_mile; a place that resolves to no hub implies nothing (the type stays).
func (d directory) jobTypeOf(raw *string) (string, bool) {
	if raw == nil {
		return "", false
	}
	r := d.resolve(*raw)
	switch {
	case r.socKey != nil:
		return LineHaul, true
	case r.hubID != nil:
		if strings.EqualFold(trimText(d.byID[*r.hubID].StationType), "SOC") {
			return LineHaul, true
		}
		return FirstMile, true
	}
	return "", false
}

// tripShape is what an edit changes beyond the trip's own columns (UpdateTripDetails): the stop list, the multi-drop
// flag and the job type, with the type the origin implies for the task's mirror.
type tripShape struct {
	stops        []stopState
	stopsChanged bool
	multi        bool
	jobType      string
	derivedType  string
	changed      []string
}

func (d directory) planTripShape(cur Trip, curStops []stopState, pr operationsdb.UpdateTripDetailsParams, changed []string, ch tripChange) tripShape {
	sh := tripShape{multi: cur.IsMultiDelivery, jobType: cur.JobType}
	if ch.stopsSet {
		next := d.mergeStops(curStops, ch.stops)
		if !sameStops(curStops, next) {
			sh.stops, sh.stopsChanged = next, true
			sh.changed = append(sh.changed, "stops")
			if sh.multi = len(next) > 1; sh.multi != cur.IsMultiDelivery {
				sh.changed = append(sh.changed, "isMultiDelivery")
			}
		}
	}
	if slices.Contains(changed, "origin") {
		if jt, ok := d.jobTypeOf(pr.OriginRaw); ok {
			sh.derivedType = jt
			if jt != cur.JobType {
				sh.jobType = jt
				sh.changed = append(sh.changed, "jobType")
			}
		}
	}
	return sh
}

// taskMirror is the task side of a trip edit: the task's new route, flag and type, its new planned stops (when the
// trip's stops changed) and the changed payload fields of its task.updated.
type taskMirror struct {
	params  operationsdb.SetTaskFromTripParams
	stops   []operationsdb.CopyTaskStopParams
	changed []string
}

// planTaskMirror mirrors an edit on the trip's task: the origin (with its hub's billing link, when the hub has one)
// and the destination as the trip now holds them, the type the origin implies, and the stops (each with the fields of
// the task's old stop at the same destination, else its hub's billing link, a fresh sequence and now).
func (d directory) planTaskMirror(task Task, old []TaskStop, pr operationsdb.UpdateTripDetailsParams, tripChanged []string, sh tripShape, now time.Time) taskMirror {
	m := taskMirror{params: operationsdb.SetTaskFromTripParams{ID: task.ID, SourceHubRaw: task.SourceHubRaw,
		SourceHubID: task.SourceHubID, SourceLinkedPartyID: task.SourceLinkedPartyID, DestinationRaw: task.DestinationRaw,
		DestinationHubID: task.DestinationHubID, DestinationSocKey: task.DestinationSocKey,
		IsMultiDelivery: task.IsMultiDelivery, TaskType: task.TaskType}}
	if slices.Contains(tripChanged, "origin") && pr.OriginRaw != nil && *pr.OriginRaw != task.SourceHubRaw {
		r := d.resolve(*pr.OriginRaw)
		m.params.SourceHubRaw, m.params.SourceHubID = *pr.OriginRaw, r.hubID
		if r.party != nil {
			m.params.SourceLinkedPartyID = r.party
		}
		m.changed = append(m.changed, "sourceHub")
	}
	if slices.Contains(tripChanged, "destination") && pr.DestinationRaw != nil && *pr.DestinationRaw != task.DestinationRaw {
		m.params.DestinationRaw, m.params.DestinationHubID = *pr.DestinationRaw, pr.DestinationHubID
		m.params.DestinationSocKey = pr.DestinationSocKey
		m.changed = append(m.changed, "destination")
	}
	if sh.derivedType != "" && sh.derivedType != task.TaskType {
		m.params.TaskType = sh.derivedType
		m.changed = append(m.changed, "taskType")
	}
	if sh.stopsChanged {
		byDest := map[string]TaskStop{}
		for _, o := range old {
			byDest[normalizeDestination(o.DestinationRaw)] = o
		}
		for i, s := range sh.stops {
			p := operationsdb.CopyTaskStopParams{TaskID: task.ID, StopIndex: s.index, DestinationRaw: s.destination,
				DestinationHubID: s.hubID, Status: s.status, DeliveredAt: s.deliveredAt, DeliveredLat: s.lat, DeliveredLng: s.lng}
			if o, ok := byDest[s.destination]; ok {
				p.DestinationLinkedPartyID, p.EstimatedRateThb, p.DropFeeThb = o.DestinationLinkedPartyID, o.EstimatedRateThb, o.DropFeeThb
				p.Sequence, p.AddedAt, p.SourceID, p.IsCustom = o.Sequence, o.AddedAt, o.SourceID, o.IsCustom
			} else {
				if s.hubID != nil {
					p.DestinationLinkedPartyID = d.byID[*s.hubID].LinkedPartyID
				}
				seq := int32(i)
				p.Sequence, p.AddedAt = &seq, &now
			}
			m.stops = append(m.stops, p)
		}
		m.changed = append(m.changed, "deliveryStops")
		if m.params.IsMultiDelivery = len(sh.stops) > 1; m.params.IsMultiDelivery != task.IsMultiDelivery {
			m.changed = append(m.changed, "isMultiDelivery")
		}
	}
	return m
}

// applyTripShape writes the stop list, the trip's flag and type and the task's mirror in the trip's transaction (the
// trip is locked; UpdateTripDetails ran). It returns the trip's further changed fields and the task's events.
func (s *Service) applyTripShape(ctx context.Context, q *operationsdb.Queries, d directory, cur Trip,
	pr operationsdb.UpdateTripDetailsParams, changed []string, ch tripChange) ([]string, []opsEvent, error) {
	routeChanged := slices.Contains(changed, "origin") || slices.Contains(changed, "destination")
	if !ch.stopsSet && !routeChanged {
		return nil, nil, nil
	}
	var curStops []TripStop
	if ch.stopsSet {
		var err error
		if curStops, err = q.TripStops(ctx, cur.ID); err != nil {
			return nil, nil, MapError(err)
		}
	}
	sh := d.planTripShape(cur, rowStops(curStops), pr, changed, ch)
	if sh.stopsChanged {
		for _, st := range sh.stops {
			if _, err := q.PutTripStop(ctx, operationsdb.PutTripStopParams{TripID: cur.ID, StopIndex: st.index,
				DestinationRaw: st.destination, DestinationHubID: st.hubID, Status: st.status, DeliveredAt: st.deliveredAt,
				DeliveredLat: st.lat, DeliveredLng: st.lng, CompletedSeq: st.completedSeq}); err != nil {
				return nil, nil, MapError(err)
			}
		}
		if _, err := q.DeleteTripStopsFrom(ctx, operationsdb.DeleteTripStopsFromParams{TripID: cur.ID,
			LastIndex: int32(len(sh.stops))}); err != nil {
			return nil, nil, MapError(err)
		}
	}
	if sh.multi != cur.IsMultiDelivery || sh.jobType != cur.JobType {
		if _, err := q.SetTripShape(ctx, operationsdb.SetTripShapeParams{ID: cur.ID, IsMultiDelivery: sh.multi,
			JobType: sh.jobType}); err != nil {
			return nil, nil, MapError(err)
		}
	}
	if cur.TaskID == nil || (!sh.stopsChanged && !routeChanged) {
		return sh.changed, nil, nil
	}
	task, err := q.LockTask(ctx, *cur.TaskID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return sh.changed, nil, nil // a task outside the caller's write reach stays as it is
	case err != nil:
		return nil, nil, MapError(err)
	}
	var old []TaskStop
	if sh.stopsChanged {
		if old, err = q.TaskStops(ctx, task.ID); err != nil {
			return nil, nil, MapError(err)
		}
	}
	m := d.planTaskMirror(task, old, pr, changed, sh, s.now().UTC())
	if len(m.changed) == 0 {
		return sh.changed, nil, nil
	}
	if sh.stopsChanged {
		if _, err := q.DeleteTaskStops(ctx, task.ID); err != nil {
			return nil, nil, MapError(err)
		}
		for _, p := range m.stops {
			if _, err := q.CopyTaskStop(ctx, p); err != nil {
				return nil, nil, MapError(err)
			}
		}
	}
	next, err := q.SetTaskFromTrip(ctx, m.params)
	if err != nil {
		return nil, nil, MapError(err)
	}
	topics, err := taskTopics(ctx, q, next)
	if err != nil {
		return nil, nil, err
	}
	return sh.changed, []opsEvent{withTopics(taskEvent(RouteTaskUpdated, next, m.changed), topics)}, nil
}

// --- write-back ---------------------------------------------------------------------------------------------------

// wbShape is the write-back plan of a trip edit's stops, type and task mirror, read under the trip's lock: the task
// with its document id, and the Firestore side of the billing parties the mirror names (partyDoc).
type wbShape struct {
	task    *Task
	parties map[uuid.UUID]operationsdb.WritePartyRow
}

// planShapeWriteBack reads what the write-back of the stops and the task mirror needs under the caller's RLS: the
// trip's task (outside the caller's write reach: no mirror; without a document: 409 not_projected) and the parties of
// the hubs the edit names.
func (s *Service) planShapeWriteBack(ctx context.Context, q *operationsdb.Queries, d directory, cur Trip,
	pr operationsdb.UpdateTripDetailsParams, changed []string, ch tripChange) (wbShape, error) {
	out := wbShape{parties: map[uuid.UUID]operationsdb.WritePartyRow{}}
	routeChanged := slices.Contains(changed, "origin") || slices.Contains(changed, "destination")
	if cur.TaskID == nil || (!ch.stopsSet && !routeChanged) {
		return out, nil
	}
	task, err := q.LockTask(ctx, *cur.TaskID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return out, nil
	case err != nil:
		return out, MapError(err)
	}
	if task.LegacyDocID == nil || *task.LegacyDocID == "" {
		return out, notProjected("taskId")
	}
	out.task = &task
	var hubs []uuid.UUID
	if slices.Contains(changed, "origin") && pr.OriginRaw != nil {
		if r := d.resolve(*pr.OriginRaw); r.party != nil {
			hubs = append(hubs, *r.party)
		}
	}
	for _, st := range ch.stops {
		if r := d.resolve(st.destination); r.party != nil {
			hubs = append(hubs, *r.party)
		}
	}
	var ck checker
	for _, id := range hubs {
		if _, err := s.party(ctx, q, &ck, "stops.destination", id, out.parties); err != nil {
			return out, err
		}
	}
	return out, ck.err()
}

// linkFields are the legacy billing-link fields of a party (destinationLinkedCustomer*, sourceHubLinkedCustomer*);
// a party without a Firestore document is 409 not_projected.
func linkFields(prefix, kindKey string, p operationsdb.WritePartyRow, field string) (map[string]any, error) {
	id, code, name, kind := partyDoc(p)
	if id == "" {
		return nil, notProjected(field)
	}
	return map[string]any{prefix + "Id": id, prefix + "Name": name, prefix + "Code": code, kindKey: kind}, nil
}

// tripStopDocs encodes the stop list as deliveryStopsProgress[] (the loader's keys): each stop keeps the keys of the
// document's own entry at its index (its photos above all).
func tripStopDocs(stops []stopState) []any {
	out := make([]any, len(stops))
	for i, st := range stops {
		m := map[string]any{}
		for k, v := range st.doc {
			m[k] = v
		}
		if st.doc != nil && normalizeDestination(stringOf(st.doc["destination"])) != st.destination {
			delete(m, "sourceId") // re-pointed: the picked place is no longer the driver's
		}
		m["index"], m["destination"], m["status"] = int64(st.index), st.destination, st.status
		m["deliveredAt"], m["deliveredLat"], m["deliveredLng"] = optTime(st.deliveredAt), optFloat(st.lat), optFloat(st.lng)
		if _, ok := m["photos"]; !ok {
			m["photos"] = []any{}
		}
		out[i] = m
	}
	return out
}

func optFloat(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

// taskStopDocs encodes the task's deliveryStops[] after a trip's stop edit: each stop keeps the keys of the task
// document's own stop at the same destination (its billing link above all), else carries its hub's billing link, a
// fresh sequence and now.
func taskStopDocs(d directory, docStops any, stops []stopState, parties map[uuid.UUID]operationsdb.WritePartyRow, now time.Time) ([]any, error) {
	items, _ := docStops.([]any)
	byDest := map[string]map[string]any{}
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			byDest[normalizeDestination(stringOf(m["destination"]))] = m
		}
	}
	out := make([]any, len(stops))
	for i, st := range stops {
		m := map[string]any{}
		if o, ok := byDest[st.destination]; ok {
			for k, v := range o {
				m[k] = v
			}
		} else {
			m["sequence"], m["addedAt"] = int64(i), now
			if st.hubID != nil {
				if pid := d.byID[*st.hubID].LinkedPartyID; pid != nil {
					if p, ok := parties[*pid]; ok {
						lf, err := linkFields("destinationLinkedCustomer", "destinationCustomerLinkKind", p, "stops.destination")
						if err != nil {
							return nil, err
						}
						for k, v := range lf {
							m[k] = v
						}
					}
				}
			}
		}
		m["index"], m["destination"], m["status"] = int64(st.index), st.destination, st.status
		m["deliveredAt"], m["deliveredLat"], m["deliveredLng"] = optTime(st.deliveredAt), optFloat(st.lat), optFloat(st.lng)
		out[i] = m
	}
	return out, nil
}

// shapeDocs builds the Firestore writes of a trip edit's stops, type and task mirror over the trip document's scalar
// changes (doc): a read-modify-write of each document whose arrays it rewrites (under the update time it read), so the
// stop photos and the progress the 3.x app wrote are kept. It returns the writes (the trip first) and the task's
// changed fields (none: no task write).
func (s *Service) shapeDocs(ctx context.Context, d directory, cur Trip, pr operationsdb.UpdateTripDetailsParams,
	changed []string, ch tripChange, plan wbShape, doc map[string]any, now time.Time) ([]wbDoc, []string, bool, error) {
	trip := map[string]any{}
	for k, v := range doc {
		trip[k] = v
	}
	tripW := wbDoc{collection: CollTrips, docID: *cur.LegacyDocID, mustExist: true}
	sh := tripShape{jobType: cur.JobType}
	if ch.stopsSet {
		fields, updated, ok, err := s.wb.Writer.Get(ctx, CollTrips, *cur.LegacyDocID)
		if err != nil {
			return nil, nil, false, writeback.ErrUnavailable(err)
		}
		if !ok {
			return nil, nil, false, notFound(pgx.ErrNoRows)
		}
		curStops := docStops(fields["deliveryStopsProgress"])
		next := d.mergeStops(curStops, ch.stops)
		if !sameStops(curStops, next) {
			sh.stops, sh.stopsChanged = next, true
			trip["deliveryStopsProgress"] = tripStopDocs(next)
			trip["isMultiDelivery"], trip["totalDeliveryStops"] = len(next) > 1, int64(len(next))
			tripW.updateTime, tripW.mustExist = updated, false
		}
	}
	if slices.Contains(changed, "origin") {
		if jt, ok := d.jobTypeOf(pr.OriginRaw); ok {
			sh.derivedType = jt
			if jt != cur.JobType {
				trip["jobType"] = jt // the trip document's own literal (first_mile | line_haul)
			}
		}
	}
	var docs []wbDoc
	var taskChanged []string
	if t := plan.task; t != nil {
		task := map[string]any{}
		taskW := wbDoc{collection: CollTasks, docID: *t.LegacyDocID, mustExist: true}
		if slices.Contains(changed, "origin") && pr.OriginRaw != nil && *pr.OriginRaw != t.SourceHubRaw {
			task["sourceHub"] = *pr.OriginRaw
			if r := d.resolve(*pr.OriginRaw); r.party != nil {
				if p, ok := plan.parties[*r.party]; ok {
					lf, err := linkFields("sourceHubLinkedCustomer", "sourceHubCustomerLinkKind", p, "origin")
					if err != nil {
						return nil, nil, false, err
					}
					for k, v := range lf {
						task[k] = v
					}
				}
			}
			taskChanged = append(taskChanged, "sourceHub")
		}
		if slices.Contains(changed, "destination") && pr.DestinationRaw != nil && *pr.DestinationRaw != t.DestinationRaw {
			task["destination"] = *pr.DestinationRaw
			taskChanged = append(taskChanged, "destination")
		}
		if sh.derivedType != "" && sh.derivedType != t.TaskType {
			task["taskType"] = legacyTaskType[sh.derivedType]
			taskChanged = append(taskChanged, "taskType")
		}
		if sh.stopsChanged {
			fields, updated, ok, err := s.wb.Writer.Get(ctx, CollTasks, *t.LegacyDocID)
			if err != nil {
				return nil, nil, false, writeback.ErrUnavailable(err)
			}
			if !ok {
				return nil, nil, false, notFound(pgx.ErrNoRows)
			}
			stops, err := taskStopDocs(d, fields["deliveryStops"], sh.stops, plan.parties, now)
			if err != nil {
				return nil, nil, false, err
			}
			task["deliveryStops"], task["isMultiDelivery"] = stops, len(sh.stops) > 1
			taskW.updateTime, taskW.mustExist = updated, false
			taskChanged = append(taskChanged, "deliveryStops")
		}
		if len(taskChanged) > 0 {
			task["updatedAt"] = now
			taskW.fields = task
			docs = append(docs, taskW)
		}
	}
	if len(trip) == 0 && len(docs) == 0 {
		return nil, nil, false, nil
	}
	trip["updatedAt"] = now
	tripW.fields = trip
	return append([]wbDoc{tripW}, docs...), taskChanged, true, nil
}
