package etl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// Domain events of the mirror (main spec §13.7, Appendix B §B.4.3, §B.5.2; T29). While Firestore writes the class C
// operations collections (tasks, trip_records, standby_records, incidentReport; until P7b) the FS->PG mirror is the
// producer of their events: each document a sweep or a listener applies is compared with its row before and after the
// apply, and what changed is appended to the outbox in the mirror's transaction, so the events commit with the rows
// (a held or rolled-back document emits nothing, a re-applied unchanged version emits nothing). They feed SSE (the
// tenant and dispatch topics of Appendix B §B.4.2) and billing.compute (trip.delivered, trip.delivered_at_changed,
// task.plan_date_changed, standby.completed, standby.customer_assigned). Every event carries the header
// origin=etl.sync and payload.source=mirror: the notify consumers ack them without a push or a LINE message,
// because the Firestore-side writer (the legacy callables, or the Go write-back handler, which emits its own event)
// owns the notification (internal/notify).
//
// The payload is thin (Appendix B §B.4.1): entity, id, tenantId, at (the document's Firestore update time, the moment of
// the write), legacyDocId, source, created, changed[] and the extras below; the relay keeps only the thin keys on the
// realtime topics and strips what a dispatcher may not read from dispatch:{partyId}:* (realtime.Project, T33). The
// dispatch topics are those of every party the row involves after the version and, for an update, before it (T33): a
// row whose party changed leaves the boards of the parties it no longer involves.
//
// What an applied version emits (at most one event per routing key, a row in the quarantine tenant never emits; a
// row that leaves it counts as created):
//   - tasks: task.created; else task.cancelled (status became cancelled), task.assigned (driver set), task.reassigned
//     (driver changed or removed), task.checked_in (status became checked_in), task.updated (anything else); plus
//     task.plan_date_changed when the Bangkok plan date moved;
//   - trip_records: trip.delivered (status became delivered, or a trip first seen delivered), trip.created,
//     trip.cancelled, trip.resubmitted (resubmittedAt moved), trip.updated (anything else, a rename included); plus
//     trip.delivered_at_changed when a delivered trip's deliveredTimestamp moved;
//   - standby_records (every record is completed, 0004): standby.completed for a new record or a moved start or end,
//     standby.customer_assigned when the customer changed; the inline billing snapshot is the pricer's (standby.priced)
//     and emits nothing;
//   - incidentReport: incident.created.

// opsTable is one operations collection of the event emission.
type opsTable struct {
	table, aggregate string
	// children digests the child rows the version replaced (stops, photos): a change there is a change of the row.
	children string
	// ignore are columns whose change is no event: generated columns and outputs other producers own.
	ignore []string
}

const childDigest = `md5(coalesce(string_agg((to_jsonb(x) - 'id' - 'created_at' - 'updated_at')::text, ',' ORDER BY (to_jsonb(x) - 'id' - 'created_at' - 'updated_at')::text), ''))`

// tripPhotoDigest digests a trip's photos with the plan index of the stop a photo belongs to instead of the stop's
// id: tripStops replaces the stops (new ids) on every apply and re-links the stop photos, so the id would differ every
// time and every write of a multi-drop trip would read as a change of its photos.
const tripPhotoDigest = `SELECT md5(coalesce(string_agg(p.e::text, ',' ORDER BY p.e::text), '')) FROM (
	SELECT (to_jsonb(x) - 'id' - 'created_at' - 'updated_at' - 'stop_id') || jsonb_build_object('stop_index', s.stop_index) AS e
	FROM trip_photos x LEFT JOIN trip_delivery_stops s ON s.id = x.stop_id WHERE x.trip_id = $1) p`

var opsTables = map[string]opsTable{
	"tasks": {table: "tasks", aggregate: "task",
		children: `SELECT ` + childDigest + ` FROM task_delivery_stops x WHERE x.task_id = $1`,
		ignore:   []string{"updated_at"}},
	"trip_records": {table: "trip_records", aggregate: "trip",
		children: `SELECT (` + tripPhotoDigest + `) || (SELECT ` + childDigest + ` FROM trip_delivery_stops x WHERE x.trip_id = $1)`,
		ignore:   []string{"updated_at", "billing_axis_date"}},
	"standby_records": {table: "standby_records", aggregate: "standby",
		ignore: []string{"updated_at", "billing_axis_date", "billing_estimate_thb", "billing_party_id", "billing_rate_source",
			"billing_rate_entry_id", "billing_effective_from_date", "billing_unpriced_reason", "billing_computed_at"}},
	"incidentReport": {table: "incident_reports", aggregate: "incident", ignore: []string{"updated_at"}},
}

// OpsCollections are the collections whose mirrored writes emit domain events (and that the listeners poll).
var OpsCollections = []string{"tasks", "trip_records", "standby_records", "incidentReport"}

// opsState is a row of an operations table as one version of its document left it.
type opsState struct {
	id     uuid.UUID
	tenant uuid.UUID
	row    map[string]json.RawMessage
	digest string
	// parties are the billing parties the row involved before the apply (opsBefore only): its dispatch topics.
	parties []uuid.UUID
}

func (s opsState) exists() bool { return s.id != uuid.Nil }

// opsBefore reads the row the document is about to update (none for a new document, or one outside the operations
// collections) and the parties it involves before the apply. A renamed trip's row still holds its former id: the
// rename is an update of that row (renamedFrom).
func opsBefore(c *docCtx) (opsState, error) {
	t, ok := opsTables[c.coll]
	if !ok {
		return opsState{}, nil
	}
	var id uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id FROM `+pgx.Identifier{t.table}.Sanitize()+` WHERE legacy_doc_id = $1`, c.doc.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) && c.coll == "trip_records" {
		if old := c.str("renamedFromTripId"); old != "" {
			err = c.tx.QueryRow(c.ctx, `SELECT id FROM trip_records WHERE legacy_doc_id = $1`, old).Scan(&id)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return opsState{}, nil
	}
	if err != nil {
		return opsState{}, fmt.Errorf("etl: %s: the row before: %w", c.doc.Path, err)
	}
	s, err := readOps(c, t, id)
	if err != nil || s.tenant == c.e.quar {
		return s, err // a row in the quarantine tenant is on no dispatcher's board (RLS never shows it)
	}
	s.parties, err = opsParties(c, c.coll, s)
	return s, err
}

func readOps(c *docCtx, t opsTable, id uuid.UUID) (opsState, error) {
	var raw []byte
	s := opsState{id: id}
	if err := c.tx.QueryRow(c.ctx, `SELECT to_jsonb(r), r.tenant_id FROM `+pgx.Identifier{t.table}.Sanitize()+` r WHERE r.id = $1`, id).
		Scan(&raw, &s.tenant); err != nil {
		return opsState{}, fmt.Errorf("etl: %s: read %s: %w", c.doc.Path, t.table, err)
	}
	if err := json.Unmarshal(raw, &s.row); err != nil {
		return opsState{}, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	if t.children != "" {
		if err := c.tx.QueryRow(c.ctx, t.children, id).Scan(&s.digest); err != nil {
			return opsState{}, fmt.Errorf("etl: %s: child rows: %w", c.doc.Path, err)
		}
	}
	return s, nil
}

// changedColumns lists the columns (camelCase, sorted) whose value differs, and "children" when the child rows did.
func changedColumns(t opsTable, before, after opsState) []string {
	var out []string
	keys := map[string]bool{}
	for k := range before.row {
		keys[k] = true
	}
	for k := range after.row {
		keys[k] = true
	}
	for k := range keys {
		if slices.Contains(t.ignore, k) || bytes.Equal(before.row[k], after.row[k]) {
			continue
		}
		out = append(out, camel(k))
	}
	if before.digest != after.digest {
		switch t.table {
		case "tasks":
			out = append(out, "deliveryStops")
		case "trip_records":
			out = append(out, "photosOrStops")
		}
	}
	slices.Sort(out)
	return out
}

func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// str is a text column of the row ("" when NULL or absent).
func (s opsState) str(col string) string {
	var v *string
	if raw, ok := s.row[col]; ok && json.Unmarshal(raw, &v) == nil && v != nil {
		return *v
	}
	return ""
}

func (s opsState) uuidCol(col string) *uuid.UUID {
	if v := s.str(col); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			return &id
		}
	}
	return nil
}

// emitOpsEvents appends the events of one applied document (c after mirrorApply) and returns how many.
func emitOpsEvents(c *docCtx, before opsState) (int, error) {
	t, ok := opsTables[c.coll]
	if !ok || c.outcome != Loaded || c.table != t.table || c.target == "" {
		return 0, nil
	}
	id, err := uuid.Parse(c.target)
	if err != nil {
		return 0, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	after, err := readOps(c, t, id)
	if err != nil {
		return 0, err
	}
	quarantine := c.e.quar
	if after.tenant == quarantine {
		return 0, nil
	}
	created := !before.exists() || before.id != after.id || before.tenant == quarantine
	var changed []string
	if !created {
		if changed = changedColumns(t, before, after); len(changed) == 0 {
			return 0, nil
		}
	}
	keys := opsRoutes(c.coll, created, before, after, changed)
	if len(keys) == 0 {
		return 0, nil
	}
	parties, err := opsParties(c, c.coll, after)
	if err != nil {
		return 0, err
	}
	parties = unionParties(parties, before.parties)
	for _, key := range keys {
		payload := map[string]any{"entity": t.aggregate, "id": after.id, "tenantId": after.tenant, "legacyDocId": c.doc.ID,
			"at": c.doc.UpdateTime.UTC(), "source": "mirror", "created": created, "changed": nonNil(changed)}
		opsPayload(c, payload, before, after)
		ev := outbox.Event{RoutingKey: key, AggregateType: t.aggregate, AggregateID: after.id.String(), TenantID: &after.tenant,
			Payload: payload, Topics: opsTopics(key, after, before, parties), Headers: map[string]string{outbox.HeaderOrigin: outbox.OriginMirror}}
		if _, err := outbox.Append(c.ctx, c.tx, ev); err != nil {
			return 0, fmt.Errorf("etl: %s: %s: %w", c.doc.Path, key, err)
		}
	}
	return len(keys), nil
}

// unionParties adds the parties of before that after does not hold, sorted like opsParties.
func unionParties(after, before []uuid.UUID) []uuid.UUID {
	for _, p := range before {
		if !slices.Contains(after, p) {
			after = append(after, p)
		}
	}
	slices.SortFunc(after, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return after
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// opsRoutes picks the routing keys of one applied version (the rules of the file comment).
func opsRoutes(coll string, created bool, before, after opsState, changed []string) []string {
	has := func(col string) bool { return slices.Contains(changed, camel(col)) }
	status := after.str("status")
	switch coll {
	case "tasks":
		if created {
			return []string{"task.created"}
		}
		var keys []string
		switch {
		case has("status") && status == "cancelled":
			keys = append(keys, "task.cancelled")
		case has("driver_id") && before.str("driver_id") == "":
			keys = append(keys, "task.assigned")
		case has("driver_id"):
			keys = append(keys, "task.reassigned")
		case has("status") && status == "checked_in":
			keys = append(keys, "task.checked_in")
		case slices.ContainsFunc(changed, func(k string) bool { return k != "planDate" && k != "planAt" }) || (has("plan_at") && !has("plan_date")):
			keys = append(keys, "task.updated") // a plan time moved within its day is an update; a moved date has its own key
		}
		if has("plan_date") {
			keys = append(keys, "task.plan_date_changed")
		}
		return keys
	case "trip_records":
		delivered := status == "delivered"
		switch {
		case created && delivered:
			return []string{"trip.delivered"}
		case created:
			return []string{"trip.created"}
		case has("status") && delivered:
			return []string{"trip.delivered"}
		}
		var keys []string
		switch {
		case has("status") && status == "cancelled":
			keys = append(keys, "trip.cancelled")
		case has("resubmitted_at") && after.str("resubmitted_at") != "":
			keys = append(keys, "trip.resubmitted")
		default:
			keys = append(keys, "trip.updated")
		}
		if delivered && before.str("status") == "delivered" && has("delivered_at") {
			keys = append(keys, "trip.delivered_at_changed")
		}
		return keys
	case "standby_records":
		if created {
			return []string{"standby.completed"}
		}
		var keys []string
		if has("ended_at") || has("started_at") {
			keys = append(keys, "standby.completed")
		}
		if has("customer_party_id") {
			keys = append(keys, "standby.customer_assigned")
		}
		return keys
	case "incidentReport":
		if created {
			return []string{"incident.created"}
		}
	}
	return nil
}

// opsPayload adds the thin fields Appendix B §B.4.3 names per event (consumers re-read the row).
func opsPayload(c *docCtx, p map[string]any, before, after opsState) {
	switch c.coll {
	case "tasks":
		p["taskType"], p["planDate"], p["status"] = after.str("task_type"), after.str("plan_date"), after.str("status")
		p["driverId"], p["previousDriverId"] = after.uuidCol("driver_id"), before.uuidCol("driver_id")
	case "trip_records":
		p["status"], p["tripNo"], p["taskId"] = after.str("status"), after.str("trip_no"), after.uuidCol("task_id")
		if c.renamed != "" || (before.exists() && before.str("trip_no") != after.str("trip_no")) {
			p["previousTripNo"] = before.str("trip_no")
		}
	case "standby_records":
		p["taskId"], p["tripId"], p["customerPartyId"] = after.uuidCol("task_id"), after.uuidCol("trip_id"), after.uuidCol("customer_party_id")
	case "incidentReport":
		p["tripId"] = after.uuidCol("trip_id")
	}
}

// opsParties are the billing parties a row involves (the set app_task_in_scope / app_trip_in_scope check, Appendix C
// §C.3.3): the dispatch:{partyId}:* topics of its events.
func opsParties(c *docCtx, coll string, s opsState) ([]uuid.UUID, error) {
	var direct []*uuid.UUID
	var task, trip *uuid.UUID
	switch coll {
	case "tasks":
		task = &s.id
	case "trip_records":
		direct, task = []*uuid.UUID{s.uuidCol("billing_party_id")}, s.uuidCol("task_id")
	case "standby_records":
		direct = []*uuid.UUID{s.uuidCol("customer_party_id"), s.uuidCol("billing_party_id")}
		task, trip = s.uuidCol("task_id"), s.uuidCol("trip_id")
	case "incidentReport":
		trip = s.uuidCol("trip_id")
	}
	if trip != nil {
		var party, tripTask *uuid.UUID
		err := c.tx.QueryRow(c.ctx, `SELECT billing_party_id, task_id FROM trip_records WHERE id = $1`, *trip).Scan(&party, &tripTask)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("etl: %s: the trip's parties: %w", c.doc.Path, err)
		}
		direct = append(direct, party)
		if task == nil {
			task = tripTask
		}
	}
	out := []uuid.UUID{}
	add := func(id *uuid.UUID) {
		if id != nil && *id != uuid.Nil && !slices.Contains(out, *id) {
			out = append(out, *id)
		}
	}
	for _, p := range direct {
		add(p)
	}
	if task != nil {
		rows, err := c.tx.Query(c.ctx, `SELECT p FROM (
			  SELECT unnest(ARRAY[t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id]) AS p FROM tasks t WHERE t.id = $1
			  UNION ALL SELECT s.destination_linked_party_id FROM task_delivery_stops s WHERE s.task_id = $1) x
			WHERE p IS NOT NULL`, *task)
		if err != nil {
			return nil, fmt.Errorf("etl: %s: the task's parties: %w", c.doc.Path, err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, fmt.Errorf("etl: %s: the task's parties: %w", c.doc.Path, err)
		}
		for _, id := range ids {
			add(&id)
		}
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return out, nil
}

// opsTopics are the realtime topics of one event (Appendix B §B.4.2): the tenant's family and the dispatch topic of
// every party (after and before the version) for the operational events, the drivers' queues for task events; the
// billing-only keys (trip.delivered_at_changed, standby.customer_assigned) have no topic. What each topic carries of
// the payload is the relay's projection (realtime.Project).
func opsTopics(key string, after, before opsState, parties []uuid.UUID) []string {
	tenant := after.tenant.String()
	var topics []string
	family := ""
	switch {
	case key == "trip.delivered_at_changed" || key == "standby.customer_assigned":
		return nil
	case strings.HasPrefix(key, "task."):
		family = realtime.FamilyTasks
	default:
		family = realtime.FamilyTrips
	}
	topics = append(topics, realtime.TenantTopic(tenant, family))
	for _, p := range parties {
		topics = append(topics, realtime.DispatchTopic(p.String(), family))
	}
	if family == realtime.FamilyTasks {
		for _, d := range []*uuid.UUID{after.uuidCol("driver_id"), before.uuidCol("driver_id"), after.uuidCol("helper_driver_id")} {
			if d != nil && !slices.Contains(topics, realtime.DriverTopic(d.String())) {
				topics = append(topics, realtime.DriverTopic(d.String()))
			}
		}
	}
	return topics
}
