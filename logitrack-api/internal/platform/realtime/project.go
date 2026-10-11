package realtime

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The realtime copies of the operations events (T33, issue #53; main spec §8.3, Appendix B §B.4.1, §B.4.4). The relay
// writes each topic of an event with the payload that topic may carry, so no producer (the FS->PG mirror, the
// write-back handlers, the R13 reassignment, the later PostgreSQL-owned services) can put a billing, PII or cost field
// on a stream by mistake: the outbox row keeps its whole payload for its RabbitMQ consumers (notify.fcm reads the
// drivers of a task event, billing.compute and firestore.shadow their own keys), Redis gets the projection.
//
//   - Every topic of a task, trip, standby or incident event (tenant:{tid}:tasks|trips, dispatch:{partyId}:tasks|trips,
//     driver:{driverId}) carries the thin keys only (thinKeyList), each value a JSON scalar (changed: a list of strings).
//     The rest of the payload (legacyDocId, customerPartyId, a write-back's values, anything a producer adds) stays off
//     Redis.
//   - A driver's stream names no other driver: driverId and previousDriverId are left out of driver:{driverId} copies
//     (the app refetches its queue on tasks.changed).
//   - dispatch:{partyId}:* carries cross-tenant operational events of a party's work and nothing a scope principal may
//     not read (Appendix C §C.3.7): only the operational types (dispatchTypes; never trip.priced, trip.repriced,
//     standby.priced, trip.delivered_at_changed or standby.customer_assigned) on their own family (task events on
//     :tasks, trip, standby and incident events on :trips); changed[] keeps only the columns of the scope_* projection
//     of the entity (DispatchColumns, camelCase, plus the child markers); an event whose non-empty changed[] names
//     nothing a dispatcher sees is not sent there at all, unless it names a party column (*PartyId): the row may have
//     entered or left the dispatcher's scope, so its boards refetch.
//   - The party a row has just left (named in the payload's leftPartyIds, LeftPartiesKey) gets that one event on its
//     dispatch topic with the envelope only (entity, id, tenantId, at, version, changed: []), whatever changed[] names:
//     its boards refetch and drop the row, and its dispatchers read none of the row's new values, which RLS no longer
//     lets them read (Appendix B §B.4.2). A leftPartyIds the relay cannot read gives every dispatch topic of the event
//     that copy (fail closed).
//   - Every other event keeps its payload on every topic but dispatch:{partyId}:*, which carries operational events only.

// thinKeyList are the payload keys an operations event keeps on its realtime topics: the thin envelope of Appendix B
// §B.4.1 (entity, id, tenantId, at, version, changed) with the mirror's created and source, and the per-event extras of
// §B.4.3 and of the producers (main spec §13.7 as built T29, the R13 reassignment): taskType and planDate (task
// events), status, the task and trip numbers and links, the drivers of a task event, billingDateMoved (priced events).
var thinKeyList = []string{
	"entity", "id", "tenantId", "at", "version", "created", "source", "changed",
	"taskType", "planDate", "status", "tripNo", "previousTripNo", "taskId", "tripId", "driverId", "previousDriverId",
	"billingDateMoved",
}

// dispatchTypes are the event types dispatch:{partyId}:* carries, with the family of their topic (Appendix B §B.4.2,
// §B.4.3): the operational events, never a price.
var dispatchTypes = map[string]string{
	"task.created": FamilyTasks, "task.assigned": FamilyTasks, "task.updated": FamilyTasks, "task.checked_in": FamilyTasks,
	"task.cancelled": FamilyTasks, "task.reassigned": FamilyTasks, "task.plan_date_changed": FamilyTasks,
	"trip.created": FamilyTrips, "trip.updated": FamilyTrips, "trip.delivered": FamilyTrips, "trip.cancelled": FamilyTrips,
	"trip.resubmitted": FamilyTrips, "standby.completed": FamilyTrips, "incident.created": FamilyTrips,
}

// dispatchColumns are the names changed[] keeps on a dispatch topic, per entity: the columns of scope_tasks,
// scope_trips, scope_standby and scope_incidents (migration 0004, Appendix C §C.3.7) in camelCase as the mirror names
// them (main spec §13.7), and the child markers of the rows scope_task_stops, scope_trip_stops and scope_trip_photos
// show (0017). id is never a change. The sse integration test checks every name against the views and scope.IsHidden.
var dispatchColumns = map[string][]string{
	"task": {"tenantId", "taskNo", "taskType", "jobCategory", "status", "planAt", "planDate", "planTime", "actualPickupAt",
		"sourceHubRaw", "sourceHubId", "destinationRaw", "destinationHubId", "destinationSocKey", "truckId", "truckType",
		"licensePlateSnapshot", "driverId", "helperDriverId", "runOrder", "checkInAt", "checkInPhotoFileId",
		"checkInAppScreenshotFileId", "checkInLat", "checkInLng", "isMultiDelivery", "createdAt", "updatedAt", ChildDeliveryStops},
	"trip": {"tenantId", "tripNo", "status", "jobType", "jobCategory", "taskId", "driverId", "originRaw", "originHubId",
		"destinationRaw", "destinationHubId", "destinationSocKey", "sealCode", "partnerCode", "truckId",
		"truckLicensePlateSnapshot", "vehicleClass", "parcelCount", "totalWeightKg", "loadingLat", "loadingLng", "std", "sta",
		"ata", "deliveredAt", "deliveredLat", "deliveredLng", "isMultiDelivery", "createdAt", "updatedAt", ChildPhotosOrStops},
	"standby": {"tenantId", "driverId", "taskId", "tripId", "jobCategory", "startLocationRaw", "endLocationRaw", "startedAt",
		"endedAt", "durationMinutes", "note", "status", "lat", "lng", "truckId", "truckLicensePlateSnapshot", "createdAt"},
	"incident": {"tenantId", "tripId", "driverId", "delayCause", "description", "lat", "lng", "truckId",
		"truckLicensePlateSnapshot", "mapPhotoFileId", "situation1PhotoFileId", "situation2PhotoFileId", "reportedByKind",
		"createdAt"},
}

// The child markers of changed[] (main spec §13.7 as built T29): the planned stops of a task, the photos or stops of a
// trip.
const (
	ChildDeliveryStops = "deliveryStops"
	ChildPhotosOrStops = "photosOrStops"
)

// LeftPartiesKey is the payload key that names, as a list of uuid strings, the parties a row involved before the change
// and no longer does (the FS->PG mirror sets it, T33; any producer that moves a row between parties may): their dispatch
// copy is the envelope only (leftKeys). It is no thin key, so no topic carries it.
const LeftPartiesKey = "leftPartyIds"

// leftKeys are the keys of the copy a party the row left gets, besides an empty changed[]: the envelope of Appendix B
// §B.4.1.
var leftKeys = []string{"entity", "id", "tenantId", "at", "version"}

// partySuffix marks a party column in changed[] (billingPartyId, sourceLinkedPartyId, destinationLinkedPartyId,
// customerPartyId): the set of dispatch topics of the row may have changed with it.
const partySuffix = "PartyId"

var (
	thinKeys        = setOf(thinKeyList)
	dispatchChanged = map[string]map[string]bool{}
	// driverHidden are left out of driver:{driverId} copies: a driver's stream names no other driver.
	driverHidden = setOf([]string{"driverId", "previousDriverId"})
	// dispatchHidden are thin keys a dispatch topic never carries: the priced events' marker.
	dispatchHidden = setOf([]string{"billingDateMoved"})
)

func init() {
	for entity, names := range dispatchColumns {
		dispatchChanged[entity] = setOf(names)
	}
}

// ThinKeys are the keys an operations event keeps on its realtime topics (a copy).
func ThinKeys() []string { return append([]string(nil), thinKeyList...) }

// DispatchFamily is the family of the dispatch topic an event type travels on; ok is false for a type no dispatch
// topic carries.
func DispatchFamily(eventType string) (family string, ok bool) {
	family, ok = dispatchTypes[eventType]
	return family, ok
}

// DispatchColumns are the changed[] names a dispatch topic keeps for an entity (task, trip, standby, incident), a copy;
// nil for any other entity.
func DispatchColumns(entity string) []string {
	if names, ok := dispatchColumns[entity]; ok {
		return append([]string(nil), names...)
	}
	return nil
}

func setOf(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// OperationsEntity is the aggregate of an event type of the four operations collections (task, trip, standby,
// incident), "" for any other.
func OperationsEntity(eventType string) string {
	entity, _, ok := strings.Cut(eventType, ".")
	if !ok {
		return ""
	}
	switch entity {
	case "task", "trip", "standby", "incident":
		return entity
	}
	return ""
}

// Project returns the payload of an event as topic carries it (the rules of the file comment); ok is false when the
// topic does not carry the event at all. data is the outbox payload (a JSON object; empty = {}). A payload that is not
// a JSON object is passed through on a topic that does not project it and dropped from one that does.
func Project(topic, eventType string, data json.RawMessage) (json.RawMessage, bool) {
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	dispatch := strings.HasPrefix(topic, "dispatch:")
	entity := OperationsEntity(eventType)
	if dispatch {
		family, ok := dispatchTypes[eventType]
		if !ok || entity == "" || !strings.HasSuffix(topic, ":"+family) {
			return nil, false
		}
	}
	if entity == "" {
		return data, true
	}
	var in map[string]json.RawMessage
	if err := json.Unmarshal(data, &in); err != nil || in == nil {
		return nil, false
	}
	if dispatch {
		if left, ok := leftParty(topic, in); left || !ok {
			return leftCopy(in)
		}
	}
	driver := strings.HasPrefix(topic, "driver:")
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		switch {
		case !thinKeys[k], driver && driverHidden[k], dispatch && dispatchHidden[k]:
			continue
		case k == "changed":
			names, ok := stringList(v)
			if !ok {
				if dispatch {
					return nil, false // fail closed: a change list the relay cannot read may name anything
				}
				continue
			}
			if dispatch {
				kept, send := dispatchNames(entity, names)
				if !send {
					return nil, false
				}
				names = kept
			}
			b, err := json.Marshal(names)
			if err != nil {
				return nil, false
			}
			out[k] = b
		case scalar(v):
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return b, true
}

// leftParty reports whether the payload names the party of a dispatch topic (dispatch:{partyId}:{family}) among the
// parties the row left; ok is false when it holds a leftPartyIds that is not a list of strings.
func leftParty(topic string, in map[string]json.RawMessage) (left, ok bool) {
	v, has := in[LeftPartiesKey]
	if !has {
		return false, true
	}
	ids, ok := stringList(v)
	if !ok {
		return false, false
	}
	party, _, _ := strings.Cut(strings.TrimPrefix(topic, "dispatch:"), ":")
	for _, id := range ids {
		if strings.EqualFold(id, party) {
			return true, true
		}
	}
	return false, true
}

// leftCopy is the copy of an event a party the row left gets: the envelope's scalar values and an empty changed[].
func leftCopy(in map[string]json.RawMessage) (json.RawMessage, bool) {
	out := map[string]json.RawMessage{"changed": json.RawMessage("[]")}
	for _, k := range leftKeys {
		if v, ok := in[k]; ok && scalar(v) {
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return b, true
}

// dispatchNames keeps the names a dispatcher sees; send is false when a non-empty list names none of them and no
// party column.
func dispatchNames(entity string, names []string) (kept []string, send bool) {
	kept = []string{}
	party := false
	for _, n := range names {
		if dispatchChanged[entity][n] {
			kept = append(kept, n)
		}
		if strings.HasSuffix(n, partySuffix) {
			party = true
		}
	}
	return kept, len(names) == 0 || len(kept) > 0 || party
}

// stringList decodes a JSON array of strings (null = an empty list).
func stringList(v json.RawMessage) ([]string, bool) {
	var names []string
	if err := json.Unmarshal(v, &names); err != nil {
		return nil, false
	}
	if names == nil {
		names = []string{}
	}
	return names, true
}

// scalar reports a JSON string, number, boolean or null: an object or an array is never a thin value.
func scalar(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	return len(v) > 0 && v[0] != '{' && v[0] != '['
}
