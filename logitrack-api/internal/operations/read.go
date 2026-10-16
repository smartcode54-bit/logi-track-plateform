package operations

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// The read APIs of T30 (issue #50; Appendix B §B.2.11-§B.2.13, main spec §5.4): the task boards, the trips (list,
// detail, by number, photos), the driver monitor aggregate, the standby and incident lists. Every read runs in the
// caller's request transaction (db.WithPrincipal), so RLS decides; staff queries also name the reach (Reach,
// Appendix C §C.3.8). A dispatcher or a customer-scope principal reads the scope_* projections only (internal/scope,
// Appendix C §C.3.7, R85): its payloads carry no billing, party, review, evidence or HR column. Photos are signed after
// the referencing rows were read under the caller's rights ("a file is readable iff its referencing row is").

// PhotoTTL is the lifetime of a photo URL in a read payload (Appendix B §B.2.12: presigned 15 min; the purpose caps it).
const PhotoTTL = 15 * time.Minute

// MonitorLimit is the page size of GET /v1/trips/monitor (W8: pages of 200, the maximum too).
const MonitorLimit = 200

// Files signs the download URLs of the photos of a page (storage.Service.SignedFiles). It takes its own pool connection,
// so it is called after the request transaction closed.
type Files interface {
	SignedFiles(ctx context.Context, ids []uuid.UUID, ttl time.Duration) (map[uuid.UUID]storage.Signed, error)
}

// Hub is one hub or SOC of the master as the place keys and labels need it.
type Hub struct {
	ID                 uuid.UUID
	Code               string
	NameTh             string
	NameEn             *string
	LinkedCustomerName *string // as p may see it (hubs.Service masks it)
	// LinkedPartyID is the billing party the hub is linked to (the hub-derived customer link a task write copies,
	// sourceHubLinkedCustomerId / destinationLinkedCustomerId of the legacy forms, T31).
	LinkedPartyID *uuid.UUID
	// StationType is HUB or SOC (hubs.station_type): an origin at a SOC makes a trip line_haul (T34).
	StationType string
}

// Hubs lists the hub master for p (hubs.Service.List through an adapter of internal/app: the shared cache, global read).
type Hubs interface {
	Hubs(ctx context.Context, p *authz.Principal) ([]Hub, error)
}

// scoped reports whether p reads only the customer/dispatcher projections.
func scoped(p *authz.Principal) bool {
	return p.IsCustomerScope() || p.Dispatcher
}

// driverPrincipal reports a driver acting as itself (driver:self, R33).
func driverPrincipal(p *authz.Principal) bool {
	return p.EffectiveRole() == authz.Driver && p.DriverID != nil
}

// viewIncome reports whether the billing columns of a trip reach p's payload: billing.IncomeBillingVisible
// (accounting:view_income, never a dispatcher, Appendix B §B.2.12; the rule of the Income projection, T39), and never a
// scope principal.
func viewIncome(p *authz.Principal) bool {
	return billing.IncomeBillingVisible(p) && !scoped(p)
}

// taskTypesFor are the task types p may list: first_mile with operations:view_first_mile, line_haul with
// operations:view_line_haul, both with dispatch:view_operations.
func taskTypesFor(p *authz.Principal) []string {
	if p.Can(authz.DispatchViewOperations) {
		return []string{FirstMile, LineHaul}
	}
	var out []string
	if p.Can(authz.OperationsViewFirstMile) {
		out = append(out, FirstMile)
	}
	if p.Can(authz.OperationsViewLineHaul) {
		out = append(out, LineHaul)
	}
	return out
}

// taskTypeCap is the capability that reads a task type.
func taskTypeCap(taskType string) authz.Cap {
	if taskType == LineHaul {
		return authz.OperationsViewLineHaul
	}
	return authz.OperationsViewFirstMile
}

// mayReadTaskType reports whether p reads tasks of taskType (a driver reads its own, whatever the type).
func mayReadTaskType(p *authz.Principal, taskType string) bool {
	return driverPrincipal(p) || slices.Contains(taskTypesFor(p), taskType)
}

// scopeRead runs fn with the scope queries of a request transaction of p.
func (s *Service) scopeRead(ctx context.Context, p *authz.Principal, fn func(q *scopedb.Queries) error) error {
	return s.Tx(ctx, p, func(tx pgx.Tx) error { return fn(scopedb.New(tx)) })
}

// --- query values ---------------------------------------------------------------------------------------------

// Bound parses a from / to value of a list: a Bangkok day yyyy-MM-dd (from: its midnight; to: the next midnight, so the
// day is included) or an RFC 3339 instant (as given; to is exclusive). "" is no bound; anything else is 422 <field>
// invalid.
func bound(ck *checker, field, v string, upper bool) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if d, err := time.ParseInLocation(time.DateOnly, v, clock.Bangkok); err == nil {
		if upper {
			d = d.AddDate(0, 0, 1)
		}
		return &d
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return &t
	}
	ck.add(field, "invalid")
	return nil
}

// planDay parses a Bangkok plan day yyyy-MM-dd as the date value of tasks.plan_date (midnight UTC).
func planDay(ck *checker, field, v string) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	d, err := time.Parse(time.DateOnly, v)
	if err != nil {
		ck.add(field, "invalid")
		return nil
	}
	return &d
}

// optUUID parses an optional uuid query value (422 <field> invalid).
func optUUID(ck *checker, field, v string) *uuid.UUID {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	id, err := uuid.Parse(v)
	if err != nil || id == uuid.Nil {
		ck.add(field, "invalid")
		return nil
	}
	return &id
}

// qText checks a free-text search: at most 100 characters, valid UTF-8 without control characters (the TW8 rule of
// /v1/truck-assignments).
func qText(ck *checker, v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len([]rune(v)) > 100 {
		ck.add("q", "too_long")
		return ""
	}
	if !validText(v) {
		ck.add("q", "invalid")
		return ""
	}
	return v
}

func validText(v string) bool {
	for _, r := range v {
		if r == 0xFFFD || r < 0x20 || r == 0x7F {
			return false
		}
	}
	return true
}

// trimChars is the white space JavaScript's String.prototype.trim strips (ECMAScript WhiteSpace and LineTerminator:
// tab, LF, VT, FF, CR, space, NBSP, the Zs spaces, LS, PS, BOM), the trim of the legacy place and plate keys
// (lib/placeFilter.ts clean, lib/truckPlate.ts clean). The monitor queries trim with the same set (btrim(x,
// trim_chars)), so a stored value, its row key, its facet option and the filter value sent back are one string.
const trimChars = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a" +
	"\u2028\u2029\u202f\u205f\u3000\ufeff"

// trimText trims trimChars from both ends of s (strings.TrimSpace differs: it also strips U+0085 and keeps U+FEFF).
func trimText(s string) string { return strings.Trim(s, trimChars) }

// echoText checks a filter value the facets may have listed (a raw place, an orphan plate, a partner code): the stored
// text verbatim, so control characters inside it are accepted; only what PostgreSQL text can never hold is refused
// (invalid UTF-8, NUL). There is no length bound beyond the request line's: a longer value is listed and must filter.
func echoText(v string) bool { return v != "" && utf8.ValidString(v) && !strings.ContainsRune(v, 0) }

// statusList checks a status filter against vocab; an empty list is no filter (nil).
func statusList(ck *checker, field string, in []string, vocab []string) []string {
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !slices.Contains(vocab, v) {
			ck.add(field, "invalid")
			return nil
		}
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// --- names and places -----------------------------------------------------------------------------------------

// driverName is the display name of a driver: the Thai full name, else first and last name (lib/driverName.ts without
// the e-mail fallback; drivers.displayName).
func driverName(fullTh, first, last *string) *string {
	if fullTh != nil {
		if v := strings.Trim(*fullTh, " "); v != "" {
			return &v
		}
	}
	var f, l string
	if first != nil {
		f = *first
	}
	if last != nil {
		l = *last
	}
	if v := strings.Trim(f+" "+l, " "); v != "" {
		return &v
	}
	return nil
}

// names reads the display names of the given drivers under p's rights (staff: drivers; a scope principal:
// scope_drivers). A driver p cannot read keeps no name.
func (s *Service) names(ctx context.Context, p *authz.Principal, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return out, nil
	}
	if scoped(p) {
		err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
			rows, err := q.ScopeDriversByID(ctx, ids)
			for _, r := range rows {
				if n := driverName(r.FullNameTh, &r.FirstName, &r.LastName); n != nil {
					out[r.ID] = *n
				}
			}
			return err
		})
		return out, err
	}
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		rows, err := q.DriverNamesOf(ctx, ids)
		for _, r := range rows {
			if n := driverName(r.FullNameTh, &r.FirstName, &r.LastName); n != nil {
				out[r.ID] = *n
			}
		}
		return err
	})
	return out, err
}

func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != uuid.Nil && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func appendID(ids []uuid.UUID, id *uuid.UUID) []uuid.UUID {
	if id != nil {
		return append(ids, *id)
	}
	return ids
}

func nameOf(names map[uuid.UUID]string, id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	if n, ok := names[*id]; ok {
		return &n
	}
	return nil
}

// Place filter values (ADR 0006): code:<CODE> (a hub or SOC code), raw:<text> (a value that resolved to nothing, its
// own bucket), __none__ (no value). hub:<uuid> names a hub the directory does not know (no code to show).
const (
	PlaceNone  = "__none__"
	placeCode  = "code:"
	placeRaw   = "raw:"
	placeHub   = "hub:"
	plateID    = "id:"
	plateText  = "plate:"
	BucketNone = "__none__"
)

// socLabels are the SOC display names of a SOC key no hub row carries (SOC_DESTINATIONS, validate/taskSchema.ts).
var socLabels = map[string]string{"SOCE": "SOCE (บัวโรย)", "SOCN": "SOCN (วังน้อย)", "SOCW": "SOCW (สมุทรสาคร)"}

// socKey is the SOC key of a raw place: normalizeSocIdToKey (validate/taskSchema.ts) restricted to SOCE, SOCN and
// SOCW (the key itself, or followed by a space or "("), the rule the monitor queries apply in SQL; else "".
func socKey(raw string) string {
	u := strings.ToUpper(trimText(raw))
	for _, k := range []string{"SOCE", "SOCN", "SOCW"} {
		if u == k || strings.HasPrefix(u, k+" ") || strings.HasPrefix(u, k+"(") {
			return k
		}
	}
	return ""
}

// directory is the hub master of one request: the code and label of a hub id, the hub ids of a code.
type directory struct {
	byID   map[uuid.UUID]Hub
	byCode map[string][]uuid.UUID
}

func (s *Service) directory(ctx context.Context, p *authz.Principal) (directory, error) {
	d := directory{byID: map[uuid.UUID]Hub{}, byCode: map[string][]uuid.UUID{}}
	if s.hubs == nil {
		return d, nil
	}
	list, err := s.hubs.Hubs(ctx, p)
	if err != nil {
		return d, err
	}
	for _, h := range list {
		d.byID[h.ID] = h
		code := strings.ToUpper(trimText(h.Code))
		d.byCode[code] = append(d.byCode[code], h.ID)
	}
	return d, nil
}

// label is buildHubCodeToDisplayMapFromEntries + resolveHubOrSocDisplay (lib/hubDisplay.ts): Thai name, English name,
// the linked customer's name (each only when it is not the code itself), else the code; a SOC key no hub carries
// shows its SOC_DESTINATIONS name.
func (d directory) label(code string) string {
	if ids := d.byCode[strings.ToUpper(code)]; len(ids) > 0 { // hubs.source_id is unique: one hub per code
		h := d.byID[ids[0]]
		for _, v := range []*string{&h.NameTh, h.NameEn, h.LinkedCustomerName} {
			if v != nil {
				if s := strings.TrimSpace(*v); s != "" && s != strings.TrimSpace(h.Code) {
					return s
				}
			}
		}
		return strings.TrimSpace(h.Code)
	}
	if l, ok := socLabels[code]; ok {
		return l
	}
	return code
}

// Place is a resolved origin or destination: key the filter value it belongs to, label the display name (the raw text
// when it resolved to nothing), resolved whether it matched the hub master or a SOC.
type Place struct {
	Raw      *string `json:"raw"`
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Resolved bool    `json:"resolved"`
}

// place resolves one stored place: the hub stored at write, else the SOC key (stored, or of the raw text), else the
// trimmed raw text, else none.
func (d directory) place(raw *string, hubID *uuid.UUID, soc *string) Place {
	out := Place{Raw: raw}
	text := ""
	if raw != nil {
		text = trimText(*raw)
	}
	switch {
	case hubID != nil:
		if h, ok := d.byID[*hubID]; ok {
			code := trimText(h.Code)
			out.Key, out.Label, out.Resolved = placeCode+code, d.label(code), true
		} else {
			out.Key, out.Label, out.Resolved = placeHub+hubID.String(), text, true
		}
	case soc != nil && *soc != "":
		out.Key, out.Label, out.Resolved = placeCode+*soc, d.label(*soc), true
	case socKey(text) != "":
		k := socKey(text)
		out.Key, out.Label, out.Resolved = placeCode+k, d.label(k), true
	case text != "":
		out.Key, out.Label = placeRaw+text, text
	default:
		out.Key = PlaceNone
	}
	return out
}

// placeFilter is a parsed place filter value as the monitor queries take it.
type placeFilter struct {
	on   bool
	hubs []uuid.UUID
	soc  *string
	raw  *string
	none bool
}

// parsePlace reads a place filter value: code:<CODE> (the hubs of that code and, for SOCE/SOCN/SOCW, the SOC key),
// hub:<uuid>, raw:<text>, __none__; "" or all is no filter. A code no hub or SOC carries matches nothing.
func (d directory) parsePlace(ck *checker, field, v string) placeFilter {
	v = trimText(v)
	switch {
	case v == "" || v == "all":
		return placeFilter{}
	case v == PlaceNone:
		return placeFilter{on: true, hubs: []uuid.UUID{}, none: true}
	case strings.HasPrefix(v, placeCode):
		code := strings.ToUpper(trimText(strings.TrimPrefix(v, placeCode)))
		if code == "" {
			break
		}
		f := placeFilter{on: true, hubs: slices.Clone(d.byCode[code])}
		if f.hubs == nil {
			f.hubs = []uuid.UUID{}
		}
		if k := socKey(code); k != "" && k == code {
			f.soc = &k
		}
		return f
	case strings.HasPrefix(v, placeHub):
		id, err := uuid.Parse(strings.TrimPrefix(v, placeHub))
		if err != nil {
			break
		}
		return placeFilter{on: true, hubs: []uuid.UUID{id}}
	case strings.HasPrefix(v, placeRaw):
		raw := trimText(strings.TrimPrefix(v, placeRaw))
		if !echoText(raw) {
			break
		}
		return placeFilter{on: true, hubs: []uuid.UUID{}, raw: &raw}
	}
	ck.add(field, "invalid")
	return placeFilter{}
}

// --- photos ---------------------------------------------------------------------------------------------------

// Photo is one evidence photo as a read payload carries it (Appendix B §B.2.12): the type, the object key and a URL
// signed for PhotoTTL (absent when the file is missing or not committed), the geocoding of the overlay and the ADR 0018
// workflow rank the list is sorted by.
type Photo struct {
	ID        uuid.UUID  `json:"id"`
	Type      string     `json:"type"`
	Known     bool       `json:"known"`
	StopID    *uuid.UUID `json:"stopId"`
	FileID    uuid.UUID  `json:"fileId"`
	Key       *string    `json:"key"`
	URL       *string    `json:"url"`
	Geocoding *Geocoding `json:"geocoding"`
	Rank      int        `json:"rank"`
	CreatedAt time.Time  `json:"createdAt"`
}

// Geocoding is the location stamped on a photo.
type Geocoding struct {
	Lat     *float64   `json:"lat"`
	Lng     *float64   `json:"lng"`
	Address *string    `json:"address"`
	At      *time.Time `json:"at"`
}

func geocoding(lat, lng *float64, address *string, at *time.Time) *Geocoding {
	if lat == nil && lng == nil && address == nil && at == nil {
		return nil
	}
	return &Geocoding{Lat: lat, Lng: lng, Address: address, At: at}
}

// sign signs the files of a page (after the request transaction closed). Without a signer every URL is absent.
func (s *Service) sign(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]storage.Signed, error) {
	ids = uniqueIDs(ids)
	if s.files == nil || len(ids) == 0 {
		return map[uuid.UUID]storage.Signed{}, nil
	}
	return s.files.SignedFiles(ctx, ids, PhotoTTL)
}

// FileRef is a signed file of a payload (incident and standby photos): its id, object key and URL.
type FileRef struct {
	FileID uuid.UUID `json:"fileId"`
	Key    *string   `json:"key"`
	URL    *string   `json:"url"`
}

func fileRef(signed map[uuid.UUID]storage.Signed, id *uuid.UUID) *FileRef {
	if id == nil {
		return nil
	}
	ref := &FileRef{FileID: *id}
	if sg, ok := signed[*id]; ok {
		ref.Key, ref.URL = &sg.Key, &sg.URL
	}
	return ref
}

func photoOf(signed map[uuid.UUID]storage.Signed, ph photoRow) Photo {
	out := Photo{ID: ph.id, Type: ph.typ, Known: ph.known != nil && *ph.known, StopID: ph.stopID, FileID: ph.fileID,
		Geocoding: geocoding(ph.lat, ph.lng, ph.address, ph.at), Rank: PhotoRank(ph.typ), CreatedAt: ph.createdAt}
	if sg, ok := signed[ph.fileID]; ok {
		out.Key, out.URL = &sg.Key, &sg.URL
	}
	return out
}

// photoRow is a trip photo of either read path.
type photoRow struct {
	id        uuid.UUID
	typ       string
	known     *bool
	stopID    *uuid.UUID
	fileID    uuid.UUID
	lat, lng  *float64
	address   *string
	at        *time.Time
	position  int32
	createdAt time.Time
}

func photoRows(staff []TripPhoto, scope []scopedb.ScopeTripPhoto) []photoRow {
	out := make([]photoRow, 0, len(staff)+len(scope))
	for _, p := range staff {
		out = append(out, photoRow{id: p.ID, typ: p.PhotoType, known: p.PhotoTypeKnown, stopID: p.StopID, fileID: p.FileID,
			lat: p.GeocodeLat, lng: p.GeocodeLng, address: p.GeocodeAddress, at: p.GeocodedAt, position: p.Position,
			createdAt: p.CreatedAt})
	}
	for _, p := range scope {
		out = append(out, photoRow{id: p.ID, typ: p.PhotoType, known: p.PhotoTypeKnown, stopID: p.StopID, fileID: p.FileID,
			lat: p.GeocodeLat, lng: p.GeocodeLng, address: p.GeocodeAddress, at: p.GeocodedAt, position: p.Position,
			createdAt: p.CreatedAt})
	}
	slices.SortStableFunc(out, func(a, b photoRow) int { return PhotoRank(a.typ) - PhotoRank(b.typ) })
	return out
}

// errTaskType is the 403 of a task of a type the caller does not read.
func errTaskType(taskType string) error {
	return authz.ErrPermissionDenied("missing capability", taskTypeCap(taskType))
}

// notAvailable is the 422 of a filter the caller's projection cannot apply (a scope principal's billing filters).
func notAvailable(ck *checker, field string) { ck.add(field, "not_available") }
