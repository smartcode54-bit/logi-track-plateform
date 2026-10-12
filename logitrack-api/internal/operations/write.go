package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The staff write routes of the operations domain (T31, issue #51; Appendix B §B.2.11-§B.2.13, main spec §12.1,
// §13.7-§13.8). Each route has two paths, chosen by PG_OWNED_DOMAINS (main spec §12.1: "write-back follows from
// PG_OWNED_DOMAINS"):
//
//   - operations PostgreSQL-owned (the staging VM, and every environment from P7b): the route writes PostgreSQL in
//     the caller's request transaction (db.WithPrincipal, so RLS decides every row), stamps the tenant of a new row
//     with Stamp (T28, Appendix C §C.3.10) and appends its domain events to the outbox in the same transaction: the
//     realtime topics of Appendix B §B.4.2 and the notify.fcm push of an assignment (exactly one per assignment);
//   - operations Firestore-owned (P2-P7b while installed 3.x APKs write the collections): the class C write goes to the
//     Firestore document first, in the legacy shape (legacy*.go, through the writeback.Writer port), never to
//     PostgreSQL next to it (§12.1: no dual master). The FS->PG mirror fills PostgreSQL and acknowledges the applied
//     update time in cache:mirror_ack (T29); the handler waits up to WriteBack.Wait (3 s) for that acknowledgement and
//     answers like the PostgreSQL path, else 202 with the document it wrote (WriteBackEcho). The notification is the
//     writer's: the handler appends its own notify event (header origin=writeback, no realtime topic), the mirror's
//     event of the same change (origin=etl.sync) feeds SSE and billing and pushes nothing.
//
// A Firestore-owned domain without a configured writer refuses the writes (503 unavailable, details.reason
// write_back_unconfigured): PostgreSQL is never written in its place.

// DefaultMirrorWait is how long a write-back handler waits for the mirror's acknowledgement (main spec §13.7, R53).
const DefaultMirrorWait = 3 * time.Second

// MirrorAcks waits for the mirror's read-your-writes acknowledgement (cache.Cache.WaitMirrorAck, R53): true once the
// mirror applied the document at or after want, false when the timeout passed first (or Redis is unreachable).
type MirrorAcks interface {
	WaitMirrorAck(ctx context.Context, collection, docID string, want time.Time, timeout time.Duration) (bool, error)
}

// RunOrders holds the queue positions the write-back hands out (cache.Cache.ReserveRunOrder, HoldRunOrder): a
// write-back's task reaches PostgreSQL only when the mirror loads it, so max(run_order) + 1 alone would give two
// assignments of one driver within the mirror's lag the same position.
type RunOrders interface {
	// ReserveRunOrder returns one more than the larger of floor (max(run_order), read under the driver's queue lock)
	// and the position held for the driver, and holds it.
	ReserveRunOrder(ctx context.Context, driverID string, floor int32) (int32, error)
	// HoldRunOrder raises the position held for the driver to at least pos.
	HoldRunOrder(ctx context.Context, driverID string, pos int32) error
}

// WriteBack is the Firestore-first path of the writes while the operations domain is not PostgreSQL-owned.
type WriteBack struct {
	// Writer writes the Firestore documents (writeback.Firestore over the REST client; writeback.Memory in tests).
	Writer writeback.Writer
	// Acks is the mirror acknowledgement; nil answers every write-back 202.
	Acks MirrorAcks
	// Wait bounds the wait for the acknowledgement; 0 = DefaultMirrorWait.
	Wait time.Duration
	// RunOrders holds the queue positions handed out; nil (or Redis unreachable, logged) = max(run_order) + 1 alone.
	RunOrders RunOrders
}

// nextRunOrder is the end of a driver's queue under the driver's queue lock (LockDriverQueue, held until the
// transaction ends): max(run_order) + 1 and, on the write-back path, past every position the write-back handed out
// that the mirror may not have loaded yet (WriteBack.RunOrders, T31 review).
func (s *Service) nextRunOrder(ctx context.Context, q *operationsdb.Queries, driver uuid.UUID) (int32, error) {
	if err := q.LockDriverQueue(ctx, driver); err != nil {
		return 0, MapError(err)
	}
	ro, err := q.NextRunOrder(ctx, driver)
	if err != nil {
		return 0, MapError(err)
	}
	if ros := s.runOrders(); ros != nil {
		n, err := ros.ReserveRunOrder(ctx, driver.String(), ro-1)
		if err != nil {
			s.log.Warn().Err(err).Msg("write-back: the queue position was not held (Redis); max(run_order) + 1 is used")
			return ro, nil
		}
		return n, nil
	}
	return ro, nil
}

// holdRunOrder records a position a write-back sets itself (the body's runOrder), so the next reservation passes it.
func (s *Service) holdRunOrder(ctx context.Context, driver uuid.UUID, pos int32) {
	if ros := s.runOrders(); ros != nil {
		if err := ros.HoldRunOrder(ctx, driver.String(), pos); err != nil {
			s.log.Warn().Err(err).Msg("write-back: the queue position was not held (Redis)")
		}
	}
}

func (s *Service) runOrders() RunOrders {
	if s.pgOwned || s.wb == nil || s.wb.Writer == nil {
		return nil
	}
	return s.wb.RunOrders
}

// Firestore collections of the write-back (legacy names).
const (
	CollTasks     = "tasks"
	CollTrips     = "trip_records"
	CollStandby   = "standby_records"
	CollIncidents = "incidentReport"
)

// ReasonWriteBackUnconfigured is details.reason of the 503 of a write while the operations domain is Firestore-owned
// and the api has no Firestore writer (ETL_FIRESTORE_PROJECT_ID, GOOGLE_APPLICATION_CREDENTIALS).
const ReasonWriteBackUnconfigured = "write_back_unconfigured"

// ReasonDocumentChanged is details.reason of the 409 of a write-back refused by a Firestore precondition: the
// document a create would write exists, the document an update names was deleted, or the document a
// read-modify-write read changed meanwhile.
const ReasonDocumentChanged = "document_changed"

// ReasonWriteBackUnsupported is the reason of a field the write-back path cannot carry (an evidence photo uploaded to
// Go's storage: the mirror links Firestore Storage URLs only; main spec §13.8).
const ReasonWriteBackUnsupported = "write_back_unsupported"

type writeMode int

const (
	modePG writeMode = iota + 1
	modeWriteBack
)

// mode is the path of the writes: PostgreSQL when the operations domain is PostgreSQL-owned, the write-back when a
// Firestore writer is configured, else 503.
func (s *Service) mode() (writeMode, error) {
	switch {
	case s.pgOwned:
		return modePG, nil
	case s.wb != nil && s.wb.Writer != nil:
		return modeWriteBack, nil
	}
	return 0, httpx.ErrUnavailable("the operations domain is Firestore-owned and the api has no Firestore writer").
		WithDetails(map[string]any{"reason": ReasonWriteBackUnconfigured})
}

// WriteResult is the answer of a write route: its status (200, 201, or 202 for a write-back the mirror has not
// acknowledged yet) and its data.
type WriteResult struct {
	Status int
	Data   any
}

// WriteBackEcho is the 202 data of a write-back the mirror did not acknowledge within the wait (main spec §11.10,
// §13.7): the PostgreSQL id the row will carry, the Firestore document it was written to, the update time of that
// write (the acknowledgement the client may poll for by reading the row) and the fields written, in the legacy shape.
type WriteBackEcho struct {
	Pending    bool           `json:"pending"`
	ID         uuid.UUID      `json:"id"`
	Collection string         `json:"collection"`
	DocID      string         `json:"docId"`
	UpdateTime time.Time      `json:"updateTime"`
	Document   map[string]any `json:"document"`
}

// --- request bodies ---------------------------------------------------------------------------------------------

// fields is a JSON object body read field by field: what is absent, null or a value is told apart (a PATCH changes
// only the fields it names; null clears an optional one). Type errors and unknown fields are collected in ck.
type fields struct {
	m  map[string]json.RawMessage
	ck *checker
}

// parseFields reads a JSON object body; an empty body is an empty object. Malformed JSON is 400.
func parseFields(body []byte, ck *checker) (fields, error) {
	f := fields{m: map[string]json.RawMessage{}, ck: ck}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return f, nil
	}
	if body[0] != '{' {
		return f, errBadBody()
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&f.m); err != nil || dec.More() {
		return f, httpx.ErrBadRequest("malformed JSON body")
	}
	return f, nil
}

// only refuses every field outside allowed (422 not_supported: refused, never silently dropped).
func (f fields) only(allowed ...string) {
	keys := make([]string, 0, len(f.m))
	for k := range f.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !slices.Contains(allowed, k) {
			f.ck.add(k, "not_supported")
		}
	}
}

func (f fields) has(k string) bool { _, ok := f.m[k]; return ok }

func (f fields) isNull(k string) bool { v, ok := f.m[k]; return ok && isNull(v) }

// text reads a string field: absent or null = nil; a string is trimmed (JavaScript's trim set) and must be valid
// UTF-8 without NUL; another JSON type is invalid.
func (f fields) text(k string) *string {
	v, ok := f.m[k]
	if !ok || isNull(v) {
		return nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		f.ck.add(k, "invalid")
		return nil
	}
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		f.ck.add(k, "invalid_encoding")
		return nil
	}
	s = trimText(s)
	return &s
}

// required is text that must be present and non-empty.
func (f fields) required(k string) string {
	v := f.text(k)
	if v == nil || *v == "" {
		if !f.has(k) || f.isNull(k) || v != nil {
			f.ck.add(k, "required")
		}
		return ""
	}
	return *v
}

// optText is text where "" is no value (nil).
func (f fields) optText(k string) *string {
	v := f.text(k)
	if v == nil || *v == "" {
		return nil
	}
	return v
}

// id reads a uuid field (absent or null = nil).
func (f fields) id(k string) *uuid.UUID {
	v, ok := f.m[k]
	if !ok || isNull(v) {
		return nil
	}
	id, ok := parseID(v)
	if !ok {
		f.ck.add(k, "invalid")
		return nil
	}
	return &id
}

// boolean reads a bool field (absent or null = nil).
func (f fields) boolean(k string) *bool {
	v, ok := f.m[k]
	if !ok || isNull(v) {
		return nil
	}
	var b bool
	if json.Unmarshal(v, &b) != nil {
		f.ck.add(k, "invalid")
		return nil
	}
	return &b
}

// number reads a finite JSON number (absent or null = nil).
func (f fields) number(k string) *float64 {
	v, ok := f.m[k]
	if !ok || isNull(v) {
		return nil
	}
	var n float64
	if json.Unmarshal(v, &n) != nil {
		f.ck.add(k, "invalid")
		return nil
	}
	return &n
}

// integer reads a whole JSON number in int32 range (absent or null = nil).
func (f fields) integer(k string) *int32 {
	n := f.number(k)
	if n == nil {
		return nil
	}
	if *n != float64(int64(*n)) || *n < -(1<<31) || *n > (1<<31-1) {
		f.ck.add(k, "invalid")
		return nil
	}
	i := int32(*n)
	return &i
}

// instant reads an RFC 3339 instant (absent or null = nil).
func (f fields) instant(k string) *time.Time {
	s := f.text(k)
	if s == nil || *s == "" {
		if s != nil {
			f.ck.add(k, "invalid")
		}
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		f.ck.add(k, "invalid")
		return nil
	}
	return &t
}

// planInstant reads a plan date: a Bangkok day yyyy-MM-dd (its Bangkok midnight, the legacy picker's local midnight)
// or an RFC 3339 instant (kept as given; its Bangkok day is the plan day).
func (f fields) planInstant(k string) *time.Time {
	s := f.text(k)
	if s == nil || *s == "" {
		if s != nil {
			f.ck.add(k, "required")
		}
		return nil
	}
	if d, ok := clock.MidnightFromDateString(*s); ok && d.Format(time.DateOnly) == *s {
		return &d
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		f.ck.add(k, "invalid")
		return nil
	}
	return &t
}

// planTime reads HH:MM (tasks.plan_time CHECK).
func (f fields) planTime(k string) *string {
	s := f.optText(k)
	if s == nil {
		return nil
	}
	if t, err := time.Parse("15:04", *s); err != nil || t.Format("15:04") != *s {
		f.ck.add(k, "invalid")
		return nil
	}
	return s
}

// raw is the JSON of a field (absent = nil).
func (f fields) raw(k string) json.RawMessage { return f.m[k] }

// taskTypeOf reads a task type: first_mile | line_haul, the legacy FIRST_MILE | LINE_HAUL accepted.
func taskTypeOf(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case FirstMile:
		return FirstMile, true
	case LineHaul:
		return LineHaul, true
	}
	return "", false
}

// jobCategoryOf reads a job category: PRIMARY | SUPPLEMENTARY in any case (an import cell goes through
// compute.JobCategoryFromCell instead).
func jobCategoryOf(v string) (string, bool) {
	c, ok := compute.ExplicitJobCategory(strings.ToUpper(strings.TrimSpace(v)))
	return string(c), ok
}

// --- authorization ------------------------------------------------------------------------------------------------

// ReasonActOnTenantRequired is details.reason of the 403 of a platform admin writing an operations row without
// X-Act-On-Tenant: the ordinary writes run under the caller's RLS, as that tenant's tenant_admin (Appendix C §C.3.9,
// audited per request); only the R13 reassignment takes platform:cross_tenant_write alone.
const ReasonActOnTenantRequired = "act_on_tenant_required"

// requireCap checks a write capability: 403 permission_denied naming it; a platform principal without
// X-Act-On-Tenant also gets details.reason act_on_tenant_required. An API key never writes these rows.
func requireCap(p *authz.Principal, c authz.Cap) error {
	if p.IsMachine() {
		return authz.ErrPermissionDenied("an API key does not write operations rows", c)
	}
	if p.Can(c) {
		return nil
	}
	e := authz.ErrPermissionDenied("missing capability", c)
	if p.IsPlatform() && p.ActOnTenant == nil {
		e = e.WithDetails(map[string]any{"reason": ReasonActOnTenantRequired})
	}
	return e
}

// --- places -------------------------------------------------------------------------------------------------------

// resolvedPlace is the hub (or SOC key) of a raw place a write stores, the ETL's resolution (etl docCtx.hub,
// main spec §13.4): the SOC keys first, then the hub codes the text may carry (the whole text, the code part of
// "CODE - Name"), else nothing (the raw text stays its own bucket, ADR 0006).
type resolvedPlace struct {
	hubID  *uuid.UUID
	socKey *string
	party  *uuid.UUID // the hub's linked billing party (the hub-derived customer link of the legacy forms)
}

func (d directory) resolve(raw string) resolvedPlace {
	raw = trimText(raw)
	if raw == "" {
		return resolvedPlace{}
	}
	key := compute.NormalizeDestinationCode(raw)
	if key == "SOCE" || key == "SOCN" || key == "SOCW" {
		return resolvedPlace{socKey: &key}
	}
	up := strings.ToUpper(raw)
	candidates := []string{up, compute.ExtractHubID(raw)}
	if i := strings.IndexByte(up, '-'); i > 0 {
		candidates = append(candidates, strings.TrimSpace(up[:i]))
	}
	for _, code := range candidates {
		if ids := d.byCode[strings.ToUpper(trimText(code))]; code != "" && len(ids) > 0 {
			id := ids[0]
			return resolvedPlace{hubID: &id, party: d.byID[id].LinkedPartyID}
		}
	}
	return resolvedPlace{}
}

// hubByID is the place of a hub id a body names (sourceHubId, destinationHubId): 422 not_found when the master has
// no such hub.
func (d directory) hubByID(ck *checker, field string, id uuid.UUID) (Hub, bool) {
	h, ok := d.byID[id]
	if !ok {
		ck.add(field, "not_found")
	}
	return h, ok
}

// --- events -------------------------------------------------------------------------------------------------------

// opsEvent is one domain event of a write (Appendix B §B.4.3, §B.5.2): thin payloads, consumers re-read the row.
type opsEvent struct {
	route   string
	agg     string
	id      uuid.UUID
	tenant  uuid.UUID
	payload map[string]any
	topics  []string
	doc     string // write-back: the Firestore document the write committed (payload legacyDocId)
}

// appendEvents appends the events of a write in tx. origin "" is a PostgreSQL-path event (with its realtime topics);
// outbox.OriginWriteBack marks the notify event of a write-back (no topic: the mirror's event carries them).
func appendEvents(ctx context.Context, tx pgx.Tx, rid, origin string, evs ...opsEvent) error {
	for _, e := range evs {
		headers := map[string]string{}
		if rid != "" {
			headers["requestId"] = rid
		}
		topics := e.topics
		if origin != "" {
			headers[outbox.HeaderOrigin] = origin
			topics = nil
		}
		tenant := e.tenant
		if e.payload == nil {
			e.payload = map[string]any{}
		}
		e.payload["entity"], e.payload["id"], e.payload["tenantId"] = e.agg, e.id, e.tenant
		if origin == outbox.OriginWriteBack {
			e.payload["source"] = "writeback"
		} else {
			e.payload["source"] = "api"
		}
		if _, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: e.route, AggregateType: e.agg,
			AggregateID: e.id.String(), TenantID: &tenant, Payload: e.payload, Topics: topics, Headers: headers}); err != nil {
			return err
		}
	}
	return nil
}

// taskTopics are the realtime topics of a task event: the tenant's tasks family, the dispatch topic of every party of
// the task, and the queues of the drivers concerned (the driver, the helper, a previous driver).
func taskTopics(ctx context.Context, q *operationsdb.Queries, t Task, drivers ...*uuid.UUID) ([]string, error) {
	parties, err := q.TaskParties(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	topics := []string{realtime.TenantTopic(t.TenantID.String(), realtime.FamilyTasks)}
	for _, p := range parties {
		topics = append(topics, realtime.DispatchTopic(p.String(), realtime.FamilyTasks))
	}
	for _, d := range append([]*uuid.UUID{t.DriverID, t.HelperDriverID}, drivers...) {
		if d != nil && !slices.Contains(topics, realtime.DriverTopic(d.String())) {
			topics = append(topics, realtime.DriverTopic(d.String()))
		}
	}
	return topics, nil
}

// tripTopics are the realtime topics of a trip, standby or incident event: the tenant's trips family and the dispatch
// topic of every party of the trip and its task.
func tripTopics(ctx context.Context, q *operationsdb.Queries, tenant uuid.UUID, trip *uuid.UUID, extra ...uuid.UUID) ([]string, error) {
	topics := []string{realtime.TenantTopic(tenant.String(), realtime.FamilyTrips)}
	parties := slices.Clone(extra)
	if trip != nil {
		ps, err := q.TripParties(ctx, *trip)
		if err != nil {
			return nil, err
		}
		parties = append(parties, ps...)
	}
	for _, p := range parties {
		if t := realtime.DispatchTopic(p.String(), realtime.FamilyTrips); p != uuid.Nil && !slices.Contains(topics, t) {
			topics = append(topics, t)
		}
	}
	return topics, nil
}

// taskEvent builds a task event with the thin fields of Appendix B §B.4.3.
func taskEvent(route string, t Task, changed []string) opsEvent {
	if changed == nil {
		changed = []string{}
	}
	return opsEvent{route: route, agg: "task", id: t.ID, tenant: t.TenantID, payload: map[string]any{
		"taskType": t.TaskType, "planDate": dayString(t.PlanDate), "status": t.Status, "changed": changed,
	}}
}

// tripEvent builds a trip event.
func tripEvent(route string, r Trip, changed []string) opsEvent {
	if changed == nil {
		changed = []string{}
	}
	return opsEvent{route: route, agg: "trip", id: r.ID, tenant: r.TenantID, payload: map[string]any{
		"status": r.Status, "tripNo": r.TripNo, "taskId": r.TaskID, "changed": changed,
	}}
}

// --- write-back ---------------------------------------------------------------------------------------------------

// wbDoc is one Firestore document a write-back writes: its collection and id, and the fields (an update mask: the
// fields not named keep their values; nil removes a field). create writes only a document that does not exist yet (a
// minted id), mustExist only one that does (an update never resurrects a deleted document), updateTime only one that
// still carries that update time (a read-modify-write); replace writes the fields as the whole document (a renamed
// trip's copy), del removes the document.
type wbDoc struct {
	collection, docID string
	fields            map[string]any
	create, mustExist bool
	updateTime        time.Time
	replace, del      bool
}

// writeBack writes the documents of one request to Firestore (one commit: all or none), then waits for the mirror to
// acknowledge the first one (the row the answer reads). It returns the update time of the write and whether the
// mirror acknowledged it.
func (s *Service) writeBack(ctx context.Context, docs ...wbDoc) (time.Time, bool, error) {
	at, err := s.commitWriteBack(ctx, docs...)
	if err != nil {
		return time.Time{}, false, err
	}
	return at, s.waitAck(ctx, docs[0].collection, docs[0].docID, at), nil
}

// commitWriteBack is writeBack without the wait for the mirror: the one commit and its update time.
func (s *Service) commitWriteBack(ctx context.Context, docs ...wbDoc) (time.Time, error) {
	writes := make([]writeback.DocWrite, 0, len(docs))
	for _, d := range docs {
		writes = append(writes, writeback.DocWrite{Collection: d.collection, DocID: d.docID, Fields: d.fields,
			MustNotExist: d.create, MustExist: d.mustExist, UpdateTime: d.updateTime, Replace: d.replace, Delete: d.del})
	}
	at, err := s.wb.Writer.Commit(ctx, writes...)
	switch {
	case errors.Is(err, writeback.ErrConflict):
		return time.Time{}, fmt.Errorf("%w: %w", writeback.ErrConflict,
			errPrecondition(ReasonDocumentChanged, "the Firestore document changed or does not exist; reload and retry"))
	case err != nil:
		var he *httpx.Error
		if errors.As(err, &he) {
			return time.Time{}, err
		}
		return time.Time{}, writeback.ErrUnavailable(err)
	}
	return at, nil
}

// waitAck waits for the mirror's acknowledgement of one written document (false after the wait, or without Redis).
func (s *Service) waitAck(ctx context.Context, collection, docID string, at time.Time) bool {
	if s.wb.Acks == nil {
		return false
	}
	wait := s.wb.Wait
	if wait <= 0 {
		wait = DefaultMirrorWait
	}
	ok, err := s.wb.Acks.WaitMirrorAck(ctx, collection, docID, at, wait)
	if err != nil {
		s.log.Warn().Err(err).Str("collection", collection).Msg("write-back: mirror acknowledgement unreadable; answering 202")
		return false
	}
	return ok
}

// echo is the 202 answer of an unacknowledged write-back.
func echo(id uuid.UUID, d wbDoc, at time.Time) WriteResult {
	doc := map[string]any{}
	for k, v := range d.fields {
		if v != nil {
			doc[k] = v
		}
	}
	return WriteResult{Status: http.StatusAccepted, Data: WriteBackEcho{Pending: true, ID: id, Collection: d.collection,
		DocID: d.docID, UpdateTime: at.UTC(), Document: doc}}
}

// notifyWriteBack appends the writer's notify events of a write-back in a request transaction of its own, after the
// Firestore write succeeded (header origin=writeback, no topic). Each event names the document it tells of and the
// update time at of that commit (payload legacyDocId, writeUpdateTime): notify.fcm holds its push until the mirror
// acknowledged that time (cache:mirror_ack), so it never plans from the row as it was before the write. A failure here
// is logged, not returned: the document is written, and the client must not retry a write that happened.
func (s *Service) notifyWriteBack(ctx context.Context, p *authz.Principal, rid string, at time.Time, evs ...opsEvent) {
	if len(evs) == 0 {
		return
	}
	for i := range evs {
		if evs[i].doc == "" {
			continue
		}
		if evs[i].payload == nil {
			evs[i].payload = map[string]any{}
		}
		evs[i].payload["legacyDocId"] = evs[i].doc
		evs[i].payload["writeUpdateTime"] = at.UTC().Format(time.RFC3339Nano)
	}
	if err := s.Tx(ctx, p, func(tx pgx.Tx) error { return appendEvents(ctx, tx, rid, outbox.OriginWriteBack, evs...) }); err != nil {
		s.log.Error().Err(err).Str("route", evs[0].route).Msg("write-back: the notify event was not appended")
	}
}
