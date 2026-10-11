package hubs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs/hubsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Field is an optional JSON field of a PATCH body (the type of the T19 administration): Set when the key is present,
// Null when its value is null.
type Field[T any] = iam.Field[T]

// Limits of the hub fields and of one import.
const (
	MaxSourceIDLen = 100
	MaxNameLen     = 200
	MaxImportRows  = 2000
)

// HubInput is the body of POST /v1/hubs and PATCH /v1/hubs/{id} (hubSchema of the web, camelCase): sourceId (stored
// trimmed and upper case, unique case-insensitively), sourceNameTh (required), sourceNameEn (the billing name),
// latitude and longitude (both or neither), stationType (HUB | SOC) and the link to a billing party: linkedPartyId,
// or linkedCustomerId with linkedCustomerKind (customer: a customers id, partner: a carrier tenant id; the legacy
// pair). On PATCH absent fields stay and null clears an optional one (a null link unlinks).
type HubInput struct {
	SourceID           Field[string]  `json:"sourceId"`
	SourceNameTh       Field[string]  `json:"sourceNameTh"`
	SourceNameEn       Field[string]  `json:"sourceNameEn"`
	Latitude           Field[float64] `json:"latitude"`
	Longitude          Field[float64] `json:"longitude"`
	StationType        Field[string]  `json:"stationType"`
	LinkedPartyID      Field[string]  `json:"linkedPartyId"`
	LinkedCustomerID   Field[string]  `json:"linkedCustomerId"`
	LinkedCustomerKind Field[string]  `json:"linkedCustomerKind"`
}

// checker collects the field violations of one request.
type checker struct{ bad []httpx.FieldViolation }

func (c *checker) add(field, reason string) { c.bad = append(c.bad, violation(field, reason)) }

func (c *checker) err() error {
	if len(c.bad) == 0 {
		return nil
	}
	return httpx.ErrInvalidArgument(c.bad...)
}

// text checks a free-text value: valid UTF-8 without control characters, at most max characters, required or not.
func (c *checker) text(field, v string, max int, required bool) {
	v = strings.TrimSpace(v)
	switch n := utf8.RuneCountInString(v); {
	case !utf8.ValidString(v) || strings.IndexFunc(v, unicode.IsControl) >= 0:
		c.add(field, "invalid")
	case n == 0 && required:
		c.add(field, "required")
	case n > max:
		c.bad = append(c.bad, httpx.FieldViolation{Field: field, Reason: "too_long", Params: map[string]any{"max": max}})
	}
}

func (c *checker) coordinate(field string, v float64, limit float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < -limit || v > limit {
		c.add(field, "out_of_range")
	}
}

// link is a requested billing-party link: unset (keep), clear, or a party to look up.
type link struct {
	set, clear bool
	party      *uuid.UUID // linkedPartyId
	customer   *uuid.UUID // linkedCustomerId of kind customer
	tenant     *uuid.UUID // linkedCustomerId of kind partner
	field      string     // the body field a missing party is reported on
}

// validate checks every field that is present (create: sourceId and sourceNameTh are required) and returns the link.
func (in *HubInput) validate(create bool) (link, error) {
	var ck checker
	if create || in.SourceID.Set {
		if in.SourceID.Null {
			ck.add("sourceId", "required")
		} else {
			ck.text("sourceId", in.SourceID.V, MaxSourceIDLen, true)
		}
		in.SourceID.V = NormalizeSourceID(in.SourceID.V)
	}
	if create || in.SourceNameTh.Set {
		if in.SourceNameTh.Null {
			ck.add("sourceNameTh", "required")
		} else {
			ck.text("sourceNameTh", in.SourceNameTh.V, MaxNameLen, true)
		}
	}
	if in.SourceNameEn.Set && !in.SourceNameEn.Null {
		ck.text("sourceNameEn", in.SourceNameEn.V, MaxNameLen, false)
	}
	if in.Latitude.Set && !in.Latitude.Null {
		ck.coordinate("latitude", in.Latitude.V, 90)
	}
	if in.Longitude.Set && !in.Longitude.Null {
		ck.coordinate("longitude", in.Longitude.V, 180)
	}
	if in.StationType.Set {
		st, ok := ParseStationType(in.StationType.V)
		if in.StationType.Null || !ok {
			ck.add("stationType", "invalid")
		}
		in.StationType.V = st
	}
	l, bad := parseLink(in.LinkedPartyID, in.LinkedCustomerID, in.LinkedCustomerKind)
	ck.bad = append(ck.bad, bad...)
	return l, ck.err()
}

// parseLink reads the three link fields: linkedPartyId wins; linkedCustomerId needs a kind (default customer) and a
// kind alone names nothing. Null on the id that is sent clears the link.
func parseLink(party, customer, kind Field[string]) (link, []httpx.FieldViolation) {
	var ck checker
	id := func(field string, f Field[string]) *uuid.UUID {
		v, err := uuid.Parse(strings.TrimSpace(f.V))
		if err != nil || v == uuid.Nil {
			ck.add(field, "invalid")
			return nil
		}
		return &v
	}
	var l link
	switch {
	case party.Set && !party.Null && strings.TrimSpace(party.V) != "":
		l = link{set: true, party: id("linkedPartyId", party), field: "linkedPartyId"}
	case customer.Set && !customer.Null && strings.TrimSpace(customer.V) != "":
		k := kindCustomer
		if kind.Set && !kind.Null && strings.TrimSpace(kind.V) != "" {
			k = strings.ToLower(strings.TrimSpace(kind.V))
		}
		l = link{set: true, field: "linkedCustomerId"}
		switch k {
		case kindCustomer:
			l.customer = id("linkedCustomerId", customer)
		case kindPartner:
			l.tenant = id("linkedCustomerId", customer)
		default:
			ck.add("linkedCustomerKind", "invalid")
		}
	case party.Set || customer.Set: // null or blank: unlink
		l = link{set: true, clear: true}
	case kind.Set && !kind.Null && strings.TrimSpace(kind.V) != "":
		ck.add("linkedCustomerId", "required")
	}
	return l, ck.bad
}

// party is a resolved billing party: its id and the code the network rule reads.
type party struct {
	id   *uuid.UUID
	code string
}

// resolveLink looks the link's billing party up in tx (RLS: parties are globally readable). A missing one is 422
// not_found on the field the request used.
func resolveLink(ctx context.Context, q *hubsdb.Queries, l link) (party, error) {
	if !l.set || l.clear {
		return party{}, nil
	}
	r, err := q.FindParty(ctx, hubsdb.FindPartyParams{PartyID: l.party, CustomerID: l.customer, TenantID: l.tenant})
	if errors.Is(err, pgx.ErrNoRows) {
		return party{}, httpx.ErrInvalidArgument(violation(l.field, "not_found"))
	}
	if err != nil {
		return party{}, err
	}
	id := r.ID
	return party{id: &id, code: trimmed(r.Code)}, nil
}

// partyCode is the code of the party a hub already links to (for the network rule of a PATCH that keeps the link).
func partyCode(ctx context.Context, q *hubsdb.Queries, id *uuid.UUID) (string, error) {
	if id == nil {
		return "", nil
	}
	r, err := q.FindParty(ctx, hubsdb.FindPartyParams{PartyID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return trimmed(r.Code), nil
}

// coordinatesRule: a hub has both coordinates or neither (check-in geofencing needs the pair).
func coordinatesRule(lat, lng *float64) error {
	if (lat == nil) != (lng == nil) {
		field := "latitude"
		if lng == nil {
			field = "longitude"
		}
		return httpx.ErrInvalidArgument(violation(field, "required"))
	}
	return nil
}

func optText(f Field[string]) *string {
	if !f.Set || f.Null {
		return nil
	}
	if v := strings.TrimSpace(f.V); v != "" {
		return &v
	}
	return nil
}

func optFloat(f Field[float64]) *float64 {
	if !f.Set || f.Null {
		return nil
	}
	v := f.V
	return &v
}

func actor(p *authz.Principal) *uuid.UUID {
	if p.IsMachine() || p.UserID == uuid.Nil {
		return nil // an API key's id is no users row
	}
	id := p.UserID
	return &id
}

// Create is POST /v1/hubs (operations:manage_sources, a steward key: R60): the hub, its name aliases and outbox
// hubs.changed in one transaction; a code that exists (case-insensitively) is 409 already_exists.
func (s *Service) Create(ctx context.Context, p *authz.Principal, rid string, in HubInput) (*Hub, error) {
	l, err := in.validate(true)
	if err != nil {
		return nil, err
	}
	lat, lng := optFloat(in.Latitude), optFloat(in.Longitude)
	if err := coordinatesRule(lat, lng); err != nil {
		return nil, err
	}
	station := StationHub
	if in.StationType.Set {
		station = in.StationType.V
	}
	var id uuid.UUID
	err = s.tx(ctx, p, func(tx pgx.Tx, q *hubsdb.Queries) error {
		pt, err := resolveLink(ctx, q, l)
		if err != nil {
			return err
		}
		nameTh, nameEn := strings.TrimSpace(in.SourceNameTh.V), optText(in.SourceNameEn)
		id, err = q.InsertHub(ctx, hubsdb.InsertHubParams{SourceID: in.SourceID.V, NameTh: nameTh, NameEn: nameEn,
			Latitude: lat, Longitude: lng, StationType: station, Network: NetworkGroup(in.SourceID.V, pt.code),
			LinkedPartyID: pt.id, CreatedBy: actor(p)})
		if err != nil {
			return err
		}
		if err := syncAliases(ctx, q, id, in.SourceID.V, nameTh, nameEn, true); err != nil {
			return err
		}
		return emitHubsChanged(ctx, tx, []uuid.UUID{id}, rid)
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, []uuid.UUID{id})
	return s.Get(ctx, p, id)
}

// Patch is PATCH /v1/hubs/{id} (operations:manage_sources): the merged row (network recomputed from the code and the
// link), the name aliases and hubs.changed in one transaction; a request that changes nothing writes nothing.
func (s *Service) Patch(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in HubInput) (*Hub, error) {
	l, err := in.validate(false)
	if err != nil {
		return nil, err
	}
	changed := false
	err = s.tx(ctx, p, func(tx pgx.Tx, q *hubsdb.Queries) error {
		cur, err := q.LockHub(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		m, ch, err := merge(ctx, q, cur, in, l)
		if err != nil {
			return err
		}
		if !ch {
			return nil
		}
		changed = true
		if err := q.UpdateHub(ctx, m); err != nil {
			return err
		}
		if err := syncAliases(ctx, q, id, m.SourceID, m.NameTh, m.NameEn, false); err != nil {
			return err
		}
		return emitHubsChanged(ctx, tx, []uuid.UUID{id}, rid)
	})
	if err != nil {
		return nil, err
	}
	if changed {
		s.invalidate(ctx, []uuid.UUID{id})
	}
	return s.Get(ctx, p, id)
}

// merge applies a validated PATCH (or an import row) to the locked row: the merged parameters and whether anything
// changed. The network is recomputed when the code or the link changes.
func merge(ctx context.Context, q *hubsdb.Queries, cur hubsdb.Hub, in HubInput, l link) (hubsdb.UpdateHubParams, bool, error) {
	m := hubsdb.UpdateHubParams{ID: cur.ID, SourceID: cur.SourceID, NameTh: cur.NameTh, NameEn: cur.NameEn,
		Latitude: cur.Latitude, Longitude: cur.Longitude, StationType: cur.StationType, Network: cur.Network,
		LinkedPartyID: cur.LinkedPartyID}
	changed := false
	if in.SourceID.Set && in.SourceID.V != cur.SourceID {
		m.SourceID, changed = in.SourceID.V, true
	}
	if in.SourceNameTh.Set && strings.TrimSpace(in.SourceNameTh.V) != cur.NameTh {
		m.NameTh, changed = strings.TrimSpace(in.SourceNameTh.V), true
	}
	if v := optText(in.SourceNameEn); in.SourceNameEn.Set && !eq(v, cur.NameEn) {
		m.NameEn, changed = v, true
	}
	if v := optFloat(in.Latitude); in.Latitude.Set && !eq(v, cur.Latitude) {
		m.Latitude, changed = v, true
	}
	if v := optFloat(in.Longitude); in.Longitude.Set && !eq(v, cur.Longitude) {
		m.Longitude, changed = v, true
	}
	if err := coordinatesRule(m.Latitude, m.Longitude); err != nil && (in.Latitude.Set || in.Longitude.Set) {
		return m, false, err
	}
	if in.StationType.Set && in.StationType.V != cur.StationType {
		m.StationType, changed = in.StationType.V, true
	}
	code := ""
	if l.set {
		pt, err := resolveLink(ctx, q, l)
		if err != nil {
			return m, false, err
		}
		if !eq(pt.id, cur.LinkedPartyID) {
			m.LinkedPartyID, changed = pt.id, true
		}
		code = pt.code
	} else {
		c, err := partyCode(ctx, q, m.LinkedPartyID)
		if err != nil {
			return m, false, err
		}
		code = c
	}
	if n := NetworkGroup(m.SourceID, code); n != cur.Network {
		m.Network, changed = n, true
	}
	return m, changed, nil
}

func eq[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// aliasNames are the name -> code aliases a hub carries: its Thai and English names, trimmed, without blanks and
// without a name equal to its own code (buildHubMaps).
func aliasNames(code, nameTh string, nameEn *string) (aliases, sources []string) {
	add := func(name, source string) {
		name = strings.TrimSpace(name)
		if name == "" || strings.EqualFold(name, code) {
			return
		}
		for _, a := range aliases {
			if strings.EqualFold(a, name) {
				return
			}
		}
		aliases, sources = append(aliases, name), append(sources, source)
	}
	add(nameTh, "name_th")
	if nameEn != nil {
		add(*nameEn, "name_en")
	}
	return aliases, sources
}

// syncAliases keeps hub_name_aliases equal to the hub's names (Appendix A §A.3.3: name -> code only, first writer
// wins): the names it no longer carries are dropped and handed to the next hub that carries them, its current names
// are added where no other hub holds them. fresh skips the drop for a new hub.
func syncAliases(ctx context.Context, q *hubsdb.Queries, hubID uuid.UUID, code, nameTh string, nameEn *string, fresh bool) error {
	aliases, sources := aliasNames(code, nameTh, nameEn)
	if !fresh {
		current := make([]string, 0, len(aliases))
		for _, a := range aliases {
			current = append(current, strings.ToLower(a))
		}
		freed, err := q.DeleteStaleNameAliases(ctx, hubsdb.DeleteStaleNameAliasesParams{HubID: hubID, Current: current})
		if err != nil {
			return err
		}
		if len(freed) > 0 {
			if err := q.ReclaimNameAliases(ctx, freed); err != nil {
				return err
			}
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return q.InsertNameAliases(ctx, hubsdb.InsertNameAliasesParams{HubID: hubID, Aliases: aliases, Sources: sources})
}

// --- the driver's hub ----------------------------------------------------------------------------------------

// DriverHubInput is the driver's subset (addHubByDriver, mob:hubs_repository.dart:208-246): a code, one name (Thai and
// English alike, as the APK stores it), the station type and optional coordinates.
type DriverHubInput struct {
	SourceID    string   `json:"sourceId"`
	SourceName  string   `json:"sourceName"`
	StationType string   `json:"stationType"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
}

// CreateDriverHub is the service of POST /v1/mobile/hubs (mobile:create_hub + driver:self; the route is mounted at
// P7a, Appendix B §B.2.21): a hub with created_by_driver = true, written by the driver itself under RLS
// (p_driver_hub_insert, p_driver_hub_alias_insert), its alias and hubs.changed in one transaction. A taken code is 409.
func (s *Service) CreateDriverHub(ctx context.Context, p *authz.Principal, rid string, in DriverHubInput) (*Hub, error) {
	if p.EffectiveRole() != authz.Driver || p.DriverID == nil {
		return nil, authz.ErrPermissionDenied("a driver hub is created by a driver")
	}
	if !p.Can(authz.MobileCreateHub) {
		return nil, authz.ErrPermissionDenied("missing capability", authz.MobileCreateHub)
	}
	var ck checker
	ck.text("sourceId", in.SourceID, MaxSourceIDLen, true)
	ck.text("sourceName", in.SourceName, MaxNameLen, true)
	station := StationHub
	if strings.TrimSpace(in.StationType) != "" {
		st, ok := ParseStationType(in.StationType)
		if !ok {
			ck.add("stationType", "invalid")
		}
		station = st
	}
	if in.Latitude != nil {
		ck.coordinate("latitude", *in.Latitude, 90)
	}
	if in.Longitude != nil {
		ck.coordinate("longitude", *in.Longitude, 180)
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	if err := coordinatesRule(in.Latitude, in.Longitude); err != nil {
		return nil, err
	}
	code, name := NormalizeSourceID(in.SourceID), strings.TrimSpace(in.SourceName)
	var id uuid.UUID
	err := s.tx(ctx, p, func(tx pgx.Tx, q *hubsdb.Queries) error {
		var err error
		id, err = q.InsertHub(ctx, hubsdb.InsertHubParams{SourceID: code, NameTh: name, NameEn: &name, Latitude: in.Latitude,
			Longitude: in.Longitude, StationType: station, Network: NetworkGroup(code, ""), CreatedByDriver: true,
			CreatedBy: actor(p)})
		if err != nil {
			return err
		}
		if err := syncAliases(ctx, q, id, code, name, &name, true); err != nil {
			return err
		}
		return emitHubsChanged(ctx, tx, []uuid.UUID{id}, rid)
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, []uuid.UUID{id})
	return s.Get(ctx, p, id)
}

// --- import ------------------------------------------------------------------------------------------------

// ImportInput is the body of POST /v1/hubs/import: rows the web parsed from the pickup-location workbook
// (pickup-import-dialog.tsx; XLSX stays a browser concern) or a JSON file (scripts/import-hubs.ts).
type ImportInput struct {
	Rows []ImportRow `json:"rows"`
}

// ImportRow is one hub upserted by sourceId: an existing hub takes the fields the row carries (a missing or null
// field keeps its value; clearing is a PATCH), a new hub needs a name (sourceNameTh, else sourceNameEn as the legacy
// import did) and defaults to HUB.
type ImportRow struct {
	SourceID           string   `json:"sourceId"`
	SourceNameTh       *string  `json:"sourceNameTh"`
	SourceNameEn       *string  `json:"sourceNameEn"`
	Latitude           *float64 `json:"latitude"`
	Longitude          *float64 `json:"longitude"`
	StationType        *string  `json:"stationType"`
	LinkedPartyID      *string  `json:"linkedPartyId"`
	LinkedCustomerID   *string  `json:"linkedCustomerId"`
	LinkedCustomerKind *string  `json:"linkedCustomerKind"`
}

// ImportItem is the outcome of one row.
type ImportItem struct {
	Index    int       `json:"index"`
	ID       uuid.UUID `json:"id"`
	SourceID string    `json:"sourceId"`
	Outcome  string    `json:"outcome"` // created | updated | unchanged
}

// ImportResult is the answer of POST /v1/hubs/import.
type ImportResult struct {
	Created   int          `json:"created"`
	Updated   int          `json:"updated"`
	Unchanged int          `json:"unchanged"`
	Items     []ImportItem `json:"items"`
}

// Import outcomes.
const (
	OutcomeCreated   = "created"
	OutcomeUpdated   = "updated"
	OutcomeUnchanged = "unchanged"
)

func fieldOf[T any](v *T) Field[T] {
	if v == nil {
		return Field[T]{}
	}
	return Field[T]{Set: true, V: *v}
}

// input turns a row into the HubInput of the shared validation (present fields only).
func (r ImportRow) input() HubInput {
	return HubInput{SourceID: Field[string]{Set: true, V: r.SourceID}, SourceNameTh: fieldOf(r.SourceNameTh),
		SourceNameEn: fieldOf(r.SourceNameEn), Latitude: fieldOf(r.Latitude), Longitude: fieldOf(r.Longitude),
		StationType: fieldOf(r.StationType), LinkedPartyID: fieldOf(r.LinkedPartyID),
		LinkedCustomerID: fieldOf(r.LinkedCustomerID), LinkedCustomerKind: fieldOf(r.LinkedCustomerKind)}
}

// prefix renames the fields of a row's 422 to rows[i].field; any other error is returned as it is.
func prefix(i int, err error) ([]httpx.FieldViolation, error) {
	e, ok := errors.AsType[*httpx.Error](err)
	if !ok || e.Code != httpx.CodeInvalidArgument {
		return nil, err
	}
	fs, _ := e.Details["fields"].([]httpx.FieldViolation)
	out := make([]httpx.FieldViolation, len(fs))
	for k, f := range fs {
		f.Field = fmt.Sprintf("rows[%d].%s", i, f.Field)
		out[k] = f
	}
	return out, nil
}

// Import is POST /v1/hubs/import (operations:manage_sources): every row upserted by sourceId in one transaction with
// its aliases and one hubs.changed for the hubs it created or changed. Any invalid row refuses the whole import (422,
// fields rows[i].*), as does a code twice in the request; nothing is written then.
func (s *Service) Import(ctx context.Context, p *authz.Principal, rid string, in ImportInput) (*ImportResult, error) {
	var bad []httpx.FieldViolation
	switch n := len(in.Rows); {
	case n == 0:
		return nil, httpx.ErrInvalidArgument(violation("rows", "required"))
	case n > MaxImportRows:
		return nil, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "rows", Reason: "too_many", Params: map[string]any{"max": MaxImportRows}})
	}
	inputs := make([]HubInput, len(in.Rows))
	links := make([]link, len(in.Rows))
	seen := map[string]int{}
	codes := make([]string, 0, len(in.Rows))
	for i, r := range in.Rows {
		inputs[i] = r.input()
		l, err := inputs[i].validate(false)
		if err != nil {
			fs, err := prefix(i, err)
			if err != nil {
				return nil, err
			}
			bad = append(bad, fs...)
			continue
		}
		links[i] = l
		code := inputs[i].SourceID.V
		if code == "" {
			bad = append(bad, violation(fmt.Sprintf("rows[%d].sourceId", i), "required"))
			continue
		}
		if _, dup := seen[code]; dup {
			bad = append(bad, violation(fmt.Sprintf("rows[%d].sourceId", i), "duplicate"))
			continue
		}
		seen[code] = i
		codes = append(codes, code)
	}
	if len(bad) > 0 {
		return nil, httpx.ErrInvalidArgument(bad...)
	}
	res := &ImportResult{Items: make([]ImportItem, 0, len(in.Rows))}
	var changedIDs []uuid.UUID
	err := s.tx(ctx, p, func(tx pgx.Tx, q *hubsdb.Queries) error {
		existing, err := q.LockHubsBySourceID(ctx, codes)
		if err != nil {
			return err
		}
		byCode := make(map[string]hubsdb.Hub, len(existing))
		for _, h := range existing {
			byCode[NormalizeSourceID(h.SourceID)] = h
		}
		for i := range inputs {
			item, err := s.importRow(ctx, q, p, inputs[i], links[i], byCode)
			if err != nil {
				fs, err := prefix(i, err)
				if err != nil {
					return err
				}
				return httpx.ErrInvalidArgument(fs...)
			}
			item.Index = i
			res.Items = append(res.Items, item)
			switch item.Outcome {
			case OutcomeCreated:
				res.Created++
			case OutcomeUpdated:
				res.Updated++
			default:
				res.Unchanged++
			}
			if item.Outcome != OutcomeUnchanged {
				changedIDs = append(changedIDs, item.ID)
			}
		}
		return emitHubsChanged(ctx, tx, changedIDs, rid)
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, changedIDs)
	return res, nil
}

func (s *Service) importRow(ctx context.Context, q *hubsdb.Queries, p *authz.Principal, in HubInput, l link,
	byCode map[string]hubsdb.Hub) (ImportItem, error) {
	code := in.SourceID.V
	if cur, ok := byCode[code]; ok {
		in.SourceID.Set = false // the key of the upsert, never a rename
		m, changed, err := merge(ctx, q, cur, in, l)
		if err != nil {
			return ImportItem{}, err
		}
		if !changed {
			return ImportItem{ID: cur.ID, SourceID: cur.SourceID, Outcome: OutcomeUnchanged}, nil
		}
		if err := q.UpdateHub(ctx, m); err != nil {
			return ImportItem{}, err
		}
		if err := syncAliases(ctx, q, cur.ID, m.SourceID, m.NameTh, m.NameEn, false); err != nil {
			return ImportItem{}, err
		}
		return ImportItem{ID: cur.ID, SourceID: m.SourceID, Outcome: OutcomeUpdated}, nil
	}
	nameTh, nameEn := optText(in.SourceNameTh), optText(in.SourceNameEn)
	if nameTh == nil {
		nameTh = nameEn // the legacy organic rule: a Thai name in the English column (Appendix A §A.3.3)
	}
	if nameTh == nil {
		return ImportItem{}, httpx.ErrInvalidArgument(violation("sourceNameTh", "required"))
	}
	lat, lng := optFloat(in.Latitude), optFloat(in.Longitude)
	if err := coordinatesRule(lat, lng); err != nil {
		return ImportItem{}, err
	}
	station := StationHub
	if in.StationType.Set {
		station = in.StationType.V
	}
	pt, err := resolveLink(ctx, q, l)
	if err != nil {
		return ImportItem{}, err
	}
	id, err := q.InsertHub(ctx, hubsdb.InsertHubParams{SourceID: code, NameTh: *nameTh, NameEn: nameEn, Latitude: lat,
		Longitude: lng, StationType: station, Network: NetworkGroup(code, pt.code), LinkedPartyID: pt.id, CreatedBy: actor(p)})
	if err != nil {
		return ImportItem{}, err
	}
	if err := syncAliases(ctx, q, id, code, *nameTh, nameEn, true); err != nil {
		return ImportItem{}, err
	}
	return ImportItem{ID: id, SourceID: code, Outcome: OutcomeCreated}, nil
}
