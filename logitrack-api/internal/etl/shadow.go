package etl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The PG->FS projection (developer-spec.md §13.8, firestore.shadow, R25, R71): while installed 3.x APKs and the
// Cloud Functions still read Firestore, every PostgreSQL write of a projected collection is copied to its Firestore
// document in the legacy shape. A projection:
//   - writes only the fields its encoder owns (ExportDoc.Mask, never a MobileOwnedFields entry), and of those only the
//     ones whose stored value differs: a legacy spelling that means the same value (a lower-case code, 'RETURN_CENTER'
//     for SOC, a yyyy-MM-dd string for a date, a download URL with its token) is kept, and a document that already
//     says what PostgreSQL says is not written at all, so the mirror sees no change from it (no echo loop);
//   - writes under a precondition on the update time it compared with (a concurrent writer makes it retry);
//   - mints the document id of a row born in PostgreSQL (R25): legacy_doc_id = id::text, or the natural key of a
//     distance pair ({hub}_{soc}, {soc}_{hub}: the 3.x app reads distances by that id,
//     hub_soc_distances_repository.dart, minted again when the hub's code changes); the minting transaction commits
//     before anything reaches Firestore, so a mirror sweep that reads the new document always finds its row, and the
//     document carries the updated_at the minting UPDATE left on the row (the reconciliation compares it). A minted id
//     whose write then fails is harmless: the next projection writes the same id;
//   - never writes a path that is not a valid document path (a '/' or a reserved name in a code): the row is skipped;
//   - skips the rows that are not PostgreSQL's to project: rows in the quarantine tenant (unresolved, Firestore keeps
//     the original), the hubs drivers created in Firestore (the mirror owns them), and the own fleet unless a
//     subcontractors document named it.

// ShadowTarget is one projection: rows of Collection by id, the rows that copy something of some hubs, customers or
// tenants (a distance pair names its hub's code, a hub its linked party's code and name, a driver its customers'
// codes), or every row.
type ShadowTarget struct {
	Collection string
	IDs        []uuid.UUID
	Hubs       []uuid.UUID // hub_soc_distances / soc_hub_distances: the pairs of these hubs
	Customers  []uuid.UUID // hubs linked to these customers; drivers holding codes of them
	Tenants    []uuid.UUID // hubs linked to these carriers
	All        bool
}

// selection is what an encoder reads.
type selection struct {
	all                           bool
	ids, hubs, customers, tenants []uuid.UUID
}

func (t ShadowTarget) selection() selection {
	return selection{all: t.All, ids: nonNilIDs(t.IDs), hubs: nonNilIDs(t.Hubs), customers: nonNilIDs(t.Customers),
		tenants: nonNilIDs(t.Tenants)}
}

// selectAll is the selection of every row (the reconciliation, export-back).
func selectAll() selection {
	return selection{all: true, ids: []uuid.UUID{}, hubs: []uuid.UUID{}, customers: []uuid.UUID{}, tenants: []uuid.UUID{}}
}

// selectIDs is the selection of rows by id.
func selectIDs(ids []uuid.UUID) selection {
	return selection{ids: nonNilIDs(ids), hubs: []uuid.UUID{}, customers: []uuid.UUID{}, tenants: []uuid.UUID{}}
}

func (s selection) empty() bool {
	return !s.all && len(s.ids)+len(s.hubs)+len(s.customers)+len(s.tenants) == 0
}

func nonNilIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

// The routing keys firestore.shadow binds (Appendix B §B.5.3).
const settingDistancesLastCalculated = "distances_last_calculated"

// ShadowTargets maps one firestore.shadow event onto its projections. Payloads are thin (main spec §7.1): the
// projection re-reads the rows. holidays.changed, broadcast.created, settings.changed of another key, trip.priced and
// standby.priced belong to later phases (T45, T48, T52, T38) and project nothing yet. A body that does not decode or
// an unknown routing key is an error the consumer acks as permanent.
func ShadowTargets(routingKey string, body []byte) ([]ShadowTarget, error) {
	var p struct {
		IDs      []string `json:"ids"`
		ID       string   `json:"id"`
		TruckID  string   `json:"truckId"`
		DriverID string   `json:"driverId"`
		TenantID string   `json:"tenantId"`
		Key      string   `json:"key"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("etl: %s payload: %w", routingKey, err)
		}
	}
	ids := func(raw ...string) ([]uuid.UUID, error) {
		out := []uuid.UUID{}
		for _, s := range raw {
			if s == "" {
				continue
			}
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, fmt.Errorf("etl: %s payload: %q is not a uuid", routingKey, s)
			}
			out = append(out, id)
		}
		return out, nil
	}
	one := func(coll string, raw ...string) ([]ShadowTarget, error) {
		list, err := ids(raw...)
		if err != nil {
			return nil, err
		}
		return []ShadowTarget{{Collection: coll, IDs: list}}, nil
	}
	switch routingKey {
	case "hubs.changed":
		list, err := ids(p.IDs...)
		if err != nil {
			return nil, err
		}
		// A hub's code is the hubId of its distance documents.
		return []ShadowTarget{{Collection: "hubs", IDs: list}, {Collection: "hub_soc_distances", Hubs: list},
			{Collection: "soc_hub_distances", Hubs: list}}, nil
	case "customers.changed":
		// A customer's code and name are copied onto the hubs linked to it (linkedCustomerName) and its code keys the
		// drivers' customerDriverIds.
		list, err := ids(p.IDs...)
		if err != nil {
			return nil, err
		}
		return []ShadowTarget{{Collection: "customers", IDs: list}, {Collection: "hubs", Customers: list},
			{Collection: "drivers", Customers: list}}, nil
	case "companies.changed":
		return one("companies", p.IDs...)
	case "truck.created", "truck.updated", "truck.renewed":
		return one("trucks", p.ID)
	case "driver.created", "driver.updated":
		return one("drivers", p.ID)
	case "assignment.created", "assignment.revoked":
		// The assignment, the truck's currentAssignments[] and the driver's currentAssignment.
		a, err := ids(p.ID)
		if err != nil {
			return nil, err
		}
		tr, err := ids(p.TruckID)
		if err != nil {
			return nil, err
		}
		dr, err := ids(p.DriverID)
		if err != nil {
			return nil, err
		}
		return []ShadowTarget{{Collection: "truckAssignment", IDs: a}, {Collection: "trucks", IDs: tr}, {Collection: "drivers", IDs: dr}}, nil
	case "tenant.created":
		return one("subcontractors", p.TenantID)
	case "tenant.updated":
		// A carrier's code and name are copied onto the hubs linked to it (linkedCustomerName).
		list, err := ids(p.TenantID)
		if err != nil {
			return nil, err
		}
		return []ShadowTarget{{Collection: "subcontractors", IDs: list}, {Collection: "hubs", Tenants: list}}, nil
	case "settings.changed":
		if p.Key != settingDistancesLastCalculated {
			return nil, nil
		}
		return []ShadowTarget{{Collection: "hub_soc_distances", All: true}, {Collection: "soc_hub_distances", All: true},
			{Collection: "metadata", All: true}}, nil
	case "holidays.changed", "broadcast.created", "trip.priced", "standby.priced":
		return nil, nil
	}
	return nil, fmt.Errorf("etl: firestore.shadow does not project %s", routingKey)
}

// encoder reads the rows of sel in the legacy shape; mint stores the minted document ids (the projection) instead of
// only computing them (the reconciliation, which never writes).
type encoder func(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error)

// projectors are the encoders of ProjectedCollections (shadow_encode.go).
var projectors = map[string]encoder{
	"subcontractors": encodeCarriers, "customers": encodeCustomerDocs, "companies": encodeCompanies,
	"hubs": encodeHubs, "hub_soc_distances": encodeDistances("hub_to_soc"), "soc_hub_distances": encodeDistances("soc_to_hub"),
	"metadata": encodeMetadata, "trucks": encodeTrucks, "drivers": encodeDrivers, "truckAssignment": encodeAssignments,
}

// ProjectStats count one projection.
type ProjectStats struct {
	Written   int `json:"written"`
	Unchanged int `json:"unchanged"`
	// Skipped are rows the encoder does not project (quarantined, driver-created hubs) or could not mint.
	Skipped int `json:"skipped"`
	// NotProjected is a target outside the projection plan (its domain is Firestore-owned, or the pin excludes it).
	NotProjected int `json:"notProjected"`
}

func (s *ProjectStats) add(o ProjectStats) {
	s.Written += o.Written
	s.Unchanged += o.Unchanged
	s.Skipped += o.Skipped
	s.NotProjected += o.NotProjected
}

// Projector writes projections. Tx is a system transaction on a pool that may write the projected tables (the worker:
// db.WithSystem on DATABASE_URL; cmd/etl: its ETL transaction); FS is the Firestore client (gcp.Firestore, the
// in-process gcptest stand-in in tests).
type Projector struct {
	Tx TxFunc
	FS FirestoreWriter
	// Collections are the collections this deployment projects (PlanProjection); other targets are counted
	// NotProjected and left alone.
	Collections []string
	// Owned are the PostgreSQL-owned domains (PG_OWNED_DOMAINS): the fields another, Firestore-owned domain still
	// writes (ForeignFields) are left out of every write.
	Owned map[string]bool
	Log   zerolog.Logger
}

// Projector returns a projector on the engine's transactions (`etl project`: the ETL login, which may write every
// projected table and mint their ids).
func (e *Engine) Projector(fs FirestoreWriter, collections []string, owned map[string]bool) *Projector {
	return &Projector{Tx: e.cfg.Tx, FS: fs, Collections: collections, Owned: owned, Log: e.cfg.Log}
}

// batchSize bounds one transaction of an All projection.
const batchSize = 200

// Project runs one target.
func (p *Projector) Project(ctx context.Context, t ShadowTarget) (ProjectStats, error) {
	var st ProjectStats
	enc, ok := projectors[t.Collection]
	if !ok {
		return st, fmt.Errorf("etl: %s is not a projected collection", t.Collection)
	}
	if !slices.Contains(p.Collections, t.Collection) {
		st.NotProjected++
		return st, nil
	}
	if !t.All || t.Collection == "metadata" { // metadata: one document
		sel := t.selection()
		if sel.empty() {
			return st, nil
		}
		return p.project(ctx, t.Collection, enc, sel)
	}
	// Every row, in batches of ids so no transaction stays open over thousands of Firestore round trips.
	table, idCol := projectedTable(t.Collection)
	var after uuid.UUID
	for {
		var batch []uuid.UUID
		if err := p.Tx(ctx, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT `+idCol+` FROM `+pgx.Identifier{table}.Sanitize()+` WHERE `+idCol+` > $1 ORDER BY 1 LIMIT $2`, after, batchSize)
			if err != nil {
				return err
			}
			batch, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			return err
		}); err != nil {
			return st, fmt.Errorf("etl: project %s: %w", t.Collection, err)
		}
		if len(batch) == 0 {
			return st, nil
		}
		s, err := p.project(ctx, t.Collection, enc, selectIDs(batch))
		st.add(s)
		if err != nil {
			return st, err
		}
		after = batch[len(batch)-1]
		if len(batch) < batchSize {
			return st, nil
		}
	}
}

// projectedTable is the table (and id column) whose ids an All projection pages through.
func projectedTable(coll string) (string, string) {
	switch coll {
	case "subcontractors":
		return "tenants", "id"
	case "hub_soc_distances", "soc_hub_distances":
		return "hub_soc_distances", "id"
	case "truckAssignment":
		return "truck_assignments", "id"
	}
	return coll, "id"
}

// ProjectAll projects every row of the planned collections (`etl project`; the catch-up before the consumer is turned
// on, or after the kill switch was off).
func (p *Projector) ProjectAll(ctx context.Context) (map[string]ProjectStats, error) {
	out := map[string]ProjectStats{}
	for _, c := range ProjectedCollections {
		if !slices.Contains(p.Collections, c) {
			continue
		}
		st, err := p.Project(ctx, ShadowTarget{Collection: c, All: true})
		out[c] = st
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// project encodes the selection in one transaction, which mints the document ids of rows born in PostgreSQL and
// commits, then writes each document that differs: a sweep that reads a projected document finds its row.
func (p *Projector) project(ctx context.Context, coll string, enc encoder, sel selection) (ProjectStats, error) {
	var docs []ExportDoc
	if err := p.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		docs, err = enc(ctx, tx, sel, true)
		return err
	}); err != nil {
		return ProjectStats{}, err
	}
	return p.write(ctx, coll, docs)
}

// write writes each document of docs that differs from Firestore's.
func (p *Projector) write(ctx context.Context, coll string, docs []ExportDoc) (ProjectStats, error) {
	var st ProjectStats
	foreign := ForeignFields(coll, p.Owned)
	for _, d := range docs {
		if d.Skip == "" && !validDocPath(d.Path) {
			d.Skip = "not a valid document path (a '/' or a reserved name in a code)"
			p.Log.Warn().Str("path", d.Path).Msg("firestore.shadow: a row whose document path is not valid is not projected")
		}
		if d.Skip != "" {
			st.Skipped++
			p.Log.Debug().Str("path", d.Path).Str("reason", d.Skip).Msg("firestore.shadow: not projected")
			continue
		}
		changed, err := writeDoc(ctx, p.FS, d, foreign)
		if err != nil {
			return st, err
		}
		if changed {
			st.Written++
		} else {
			st.Unchanged++
		}
	}
	return st, nil
}

// validDocID is a Firestore document id: not empty, no '/', not '.' or '..', not __name__-shaped, at most 1500 bytes.
func validDocID(id string) bool {
	return id != "" && id != "." && id != ".." && len(id) <= 1500 && !strings.Contains(id, "/") &&
		!(strings.HasPrefix(id, "__") && strings.HasSuffix(id, "__"))
}

// validDocPath is collection/id pairs of valid segments (an id with a '/' would name a collection or a nested document).
func validDocPath(path string) bool {
	segs := strings.Split(path, "/")
	if len(segs)%2 != 0 {
		return false
	}
	for _, s := range segs {
		if !validDocID(s) {
			return false
		}
	}
	return true
}

// writeDoc writes the fields of d that differ from the stored document, under a precondition on its update time
// (Exists=false when there is none); it reports whether anything was written. A Retired document that does not exist
// is not created. foreign (ForeignFields) are left out of a stored document, whose other writer owns them; a document
// created here gets them from PostgreSQL (a truck born in Go starts with its form's status and mileage, which the
// maintenance flow then keeps).
func writeDoc(ctx context.Context, fs FirestoreWriter, d ExportDoc, foreign []string) (bool, error) {
	cur, err := fs.Get(ctx, d.Path)
	var pre gcp.Precondition
	var curFields map[string]any
	switch {
	case errors.Is(err, gcp.ErrNotFound) && d.Retired:
		return false, nil
	case errors.Is(err, gcp.ErrNotFound):
		no := false
		pre.Exists = &no
	case err != nil:
		return false, fmt.Errorf("etl: project %s: %w", d.Path, err)
	default:
		pre.UpdateTime = cur.UpdateTime
		curFields = cur.Fields
		d = d.without(foreign)
	}
	fields, mask := d.Diff(curFields)
	if len(mask) == 0 && pre.Exists == nil {
		return false, nil
	}
	if _, err := fs.Patch(ctx, d.Path, fields, mask, pre); err != nil {
		return false, fmt.Errorf("etl: project %s: %w", d.Path, err)
	}
	return true, nil
}

// without drops fields from the document's mask (ForeignFields: another writer's).
func (d ExportDoc) without(fields []string) ExportDoc {
	if len(fields) == 0 {
		return d
	}
	d.Mask = slices.DeleteFunc(slices.Clone(d.Mask), func(f string) bool { return slices.Contains(fields, f) })
	return d
}

// Diff returns the fields of d whose stored value in cur is not equivalent, and the mask that writes them (a masked
// field absent from the result is deleted); the fields ExportDoc.Keep names are left alone, and a Merge field is
// compared and written as merged over the stored value. Equivalence: ExportDoc.Equiv when the encoder gives one, else
// legacyEqual; a field PostgreSQL leaves empty matches an absent, null, blank or empty stored value. A nil cur is a
// document that does not exist yet: every field with a value is written (an Equiv that reads an absent field as the
// loader's default only spares rewriting a stored document).
func (d ExportDoc) Diff(cur map[string]any) (map[string]any, []string) {
	fields := map[string]any{}
	var mask, keep []string
	if d.Keep != nil && cur != nil {
		keep = d.Keep(cur)
	}
	for _, f := range d.Mask {
		if slices.Contains(keep, f) {
			continue
		}
		want, has := d.Fields[f]
		c, curHas := cur[f]
		if m := d.Merge[f]; m != nil {
			want = m(c)
			has = want != nil
		}
		var same bool
		switch eq := d.Equiv[f]; {
		case cur == nil:
			same = !has || blank(want)
		case eq != nil:
			same = eq(c, curHas)
		case !has || blank(want):
			same = !curHas || blank(c)
		default:
			same = curHas && legacyEqual(want, c)
		}
		if same {
			continue
		}
		mask = append(mask, f)
		if has && !blank(want) {
			fields[f] = want
		}
	}
	return fields, mask
}

// blank is an empty legacy value: null, a blank string, an empty array or map.
func blank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// legacyEqual compares an encoded value with a stored one: numbers by value (a stored integer, double or numeric string),
// times by instant at microseconds (a Timestamp, an ISO string or epoch milliseconds), arrays element-wise, maps per
// key with blank-equal absences.
func legacyEqual(want, cur any) bool {
	switch w := want.(type) {
	case nil:
		return blank(cur)
	case string:
		c, ok := cur.(string)
		return ok && c == w
	case bool:
		c, ok := cur.(bool)
		return ok && c == w
	case int64:
		c, ok := toFloat(cur)
		return ok && c == float64(w)
	case float64:
		c, ok := toFloat(cur)
		return ok && (c == w || math.Abs(c-w) < 1e-9*math.Max(1, math.Abs(w)))
	case dump.Timestamp:
		c, ok := toTime(cur)
		return ok && c.Truncate(time.Microsecond).Equal(w.Truncate(time.Microsecond))
	case []any:
		c, ok := cur.([]any)
		if !ok {
			return len(w) == 0 && blank(cur)
		}
		if len(c) != len(w) {
			return false
		}
		for i := range w {
			if !legacyEqual(w[i], c[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		c, ok := cur.(map[string]any)
		if !ok {
			return len(w) == 0 && blank(cur)
		}
		for k, wv := range w {
			if !legacyEqual(wv, c[k]) {
				return false
			}
		}
		for k, cv := range c {
			if _, ok := w[k]; !ok && !blank(cv) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(want, cur)
}

// --- value helpers of the encoders -----------------------------------------------------------------------------

// jsNumber is a number as the JavaScript SDK stored it: an integral value is an integerValue, any other a doubleValue.
func jsNumber(f float64) any {
	if f == math.Trunc(f) && math.Abs(f) < 1<<53 {
		return int64(f)
	}
	return f
}

// legacyISO is Date.prototype.toISOString: UTC with milliseconds.
func legacyISO(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// ymd is a date column as the web forms store it (yyyy-MM-dd).
func ymd(d time.Time) string { return d.Format("2006-01-02") }

// bangkokMidnight is a date column as a Timestamp: the Bangkok midnight a browser in Asia/Bangkok stores for the day.
func bangkokMidnight(d time.Time) dump.Timestamp {
	return dump.Timestamp{Time: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, clock.Bangkok).UTC()}
}

// sameDay: a stored date (yyyy-MM-dd, an instant on its Bangkok day) is the day d (the cast of docCtx.date).
func sameDay(d time.Time) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		if !has {
			return false
		}
		got := (&docCtx{f: map[string]any{"v": cur}}).date("v")
		return got != nil && got.Equal(time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC))
	}
}

// sameCanon: a stored vocabulary literal whose canonical form (canon, mapped by fold when given) is want; an absent
// value matches absent, which is what the loader reads as that value.
func sameCanon(want string, absent string, fold func(string) string) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		s, ok := cur.(string)
		if !has || !ok || strings.TrimSpace(s) == "" {
			return want == absent
		}
		v := canon(s)
		if fold != nil {
			v = fold(v)
		}
		return v == want
	}
}

// sameNumber: a stored number or numeric string with the value of want.
func sameNumber(want float64) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		c, ok := toFloat(cur)
		return has && ok && c == want
	}
}

// sameBool: a stored boolean, absent being absent (the loader's default).
func sameBool(want, absent bool) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		b, ok := cur.(bool)
		if !has || !ok {
			return want == absent
		}
		return b == want
	}
}

// keepSnapshot: a denormalised string the legacy writer copied at the time (an assignment's plate or driver name) is
// filled when missing and otherwise kept.
func keepSnapshot(cur any, has bool) bool {
	s, ok := cur.(string)
	return has && ok && strings.TrimSpace(s) != ""
}

// sameFile: a stored download URL of the same object (its token kept) as want (file_objects.legacy_url).
func sameFile(want string) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		s, ok := cur.(string)
		if !has || !ok {
			return false
		}
		return sameURL(want, s)
	}
}

func sameURL(want, cur string) bool {
	if cur == want {
		return true
	}
	_, _, legacy, ok := ParseStorageURL(cur)
	return ok && legacy == want
}

// sameFiles: element-wise sameURL.
func sameFiles(want []string) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		list, _ := cur.([]any)
		if !has || len(list) != len(want) {
			return len(want) == 0 && (!has || blank(cur))
		}
		for i, w := range want {
			s, ok := list[i].(string)
			if !ok || !sameURL(w, s) {
				return false
			}
		}
		return true
	}
}

// strings as []any for a document field.
func anyStrings(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// docBuilder accumulates one ExportDoc.
type docBuilder struct{ d ExportDoc }

func newDoc(path string) *docBuilder {
	return &docBuilder{d: ExportDoc{Path: path, Fields: map[string]any{}, Equiv: map[string]func(any, bool) bool{},
		Merge: map[string]func(any) any{}}}
}

// set adds field to the mask with value v (nil, a nil pointer or a blank string: the field is empty and deleted).
func (b *docBuilder) set(field string, v any) *docBuilder {
	b.d.Mask = append(b.d.Mask, field)
	switch x := v.(type) {
	case nil:
	case *string:
		if x != nil && strings.TrimSpace(*x) != "" {
			b.d.Fields[field] = *x
		}
	case *time.Time:
		if x != nil {
			b.d.Fields[field] = dump.Timestamp{Time: x.UTC()}
		}
	case time.Time:
		b.d.Fields[field] = dump.Timestamp{Time: x.UTC()}
	case *float64:
		if x != nil {
			b.d.Fields[field] = jsNumber(*x)
		}
	case *int32:
		if x != nil {
			b.d.Fields[field] = int64(*x)
		}
	case *int16:
		if x != nil {
			b.d.Fields[field] = int64(*x)
		}
	case *int64:
		if x != nil {
			b.d.Fields[field] = *x
		}
	case *bool:
		if x != nil {
			b.d.Fields[field] = *x
		}
	case string:
		if strings.TrimSpace(x) != "" {
			b.d.Fields[field] = x
		}
	default:
		b.d.Fields[field] = v
	}
	return b
}

// created sets createdAt: written on a new document, kept as stored, never added to a legacy document that has none
// (the row's created_at is then the load's time, not the document's).
func (b *docBuilder) created(t time.Time) *docBuilder {
	want := dump.Timestamp{Time: t.UTC()}
	return b.set("createdAt", t).eq("createdAt", func(cur any, has bool) bool { return !has || legacyEqual(want, cur) })
}

// eq sets the equivalence of field.
func (b *docBuilder) eq(field string, fn func(any, bool) bool) *docBuilder {
	b.d.Equiv[field] = fn
	return b
}

// keepIf leaves fields alone when the stored value of ref (a string, or the string at ref.sub of a stored map) makes
// pred true: PostgreSQL holds no value there because the load could not resolve the legacy one (an unknown linked
// party, a GPS id or carrier code another row holds, an assignment that did not load), so deleting it would lose it.
func (b *docBuilder) keepIf(ref, sub string, pred func(string) bool, fields ...string) *docBuilder {
	prev := b.d.Keep
	b.d.Keep = func(cur map[string]any) []string {
		var out []string
		if prev != nil {
			out = prev(cur)
		}
		v := cur[ref]
		if sub != "" {
			m, _ := v.(map[string]any)
			v = m[sub]
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" && pred(strings.TrimSpace(s)) {
			out = append(out, fields...)
		}
		return out
	}
	return b
}

// unknownTo is the keepIf predicate "names nothing in known".
func unknownTo(known map[string]bool) func(string) bool {
	return func(v string) bool { return !known[v] }
}

func (b *docBuilder) skip(reason string) *docBuilder {
	b.d.Skip = reason
	return b
}

func (b *docBuilder) doc() ExportDoc { return b.d }

// uidOrNil is a Firebase uid column (a users.legacy_auth_uid): set in the mask only when known, so a legacy value the
// loader could not resolve (user_unresolved) is never deleted.
func (b *docBuilder) uid(field string, v *string) *docBuilder {
	if v != nil && *v != "" {
		b.set(field, *v)
	}
	return b
}

// int64Of formats a smallint/int column as the web form's string ("2020" for a year).
func numericString(v int64) string { return strconv.FormatInt(v, 10) }
