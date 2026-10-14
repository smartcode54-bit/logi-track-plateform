// Package hubs is the hub and SOC master data and the Hub <-> SOC distance matrix (T21, Appendix B §B.2.9):
// GET /v1/hubs (one DTO for every page, W8; the whole master, or keyset pages of a folded search for the pickers,
// UI2B), GET /v1/hubs/{id} (one hub by uuid or code), GET /v1/hubs/maps (nameToCode and codeToName, never merged), the
// steward writes POST /v1/hubs, PATCH /v1/hubs/{id} and POST /v1/hubs/import, the driver's hub (mobile:create_hub,
// route POST /v1/mobile/hubs at P7a), the distance reads GET /v1/distances[/{originId}/{destinationId}|/meta], and
// the distances.compute job: POST /v1/jobs/distances.compute in the api, the Google Distance Matrix consumer in the
// worker.
//
// Transactions (Appendix C §C.3.2): every request runs in db.WithPrincipal, so RLS decides (hubs, hub_name_aliases and
// hub_soc_distances are global-read-steward-write, R60, plus the driver hub insert); the steward keys
// operations:manage_sources and operations:calculate_distances decide the verbs first, and are only ever effective
// for own-fleet staff and platform_admin acting as a tenant. Two paths run in db.WithSystem, which puts this package
// on the analyzer's allow-list: the loads of the shared caches cache:hubs:all, cache:hubs:n2c and cache:hubs:c2n, in a
// READ ONLY transaction so every reader caches the same value (carrier names are not readable by every principal;
// each response then masks what the caller's RLS would hide), and the distances.compute consumer, which has no
// request principal and writes hub_soc_distances and settings.
//
// Events: every hub write appends outbox hubs.changed ({ids}, SSE global, Appendix B §B.4.3) in its transaction and
// drops the three hub keys after COMMIT (the relay drops them again); the distance job appends settings.changed
// (key distances_last_calculated, cache only) and job.updated for its owner.
package hubs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs/hubsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/searchtext"
)

// Outbox routing keys (Appendix B §B.5.2) and the realtime topic of hubs.changed (§B.4.2: every staff principal).
const (
	RouteHubsChanged     = "hubs.changed"
	RouteSettingsChanged = "settings.changed"
	TopicGlobal          = "global"
)

// SettingDistancesLastCalculated is the settings row of the last distance run (metadata/distances_last_calculated).
const SettingDistancesLastCalculated = "distances_last_calculated"

// CodeAlreadyExists is the 409 code of a taken hub code (Appendix B §B.1.5).
const CodeAlreadyExists = "already_exists"

// Party kinds of billing_parties and the linkedCustomerKind each maps to (hubSchema CUSTOMER_LINK_KIND_ENUM).
const (
	partyCustomer = "customer"
	partyTenant   = "tenant"
	kindCustomer  = "customer"
	kindPartner   = "partner"
)

// JobSubmitter submits jobs (jobs.Service implements it).
type JobSubmitter interface {
	Submit(ctx context.Context, in jobs.SubmitInput) (jobs.Job, error)
}

// Deps are the collaborators of the API service.
type Deps struct {
	// Pool is the logitrack_app pool.
	Pool db.Beginner
	// Cache holds cache:hubs:* and cache:settings:*; optional (without it every read loads from PostgreSQL).
	Cache *cache.Cache
	// Jobs submits distances.compute; optional (without it POST /v1/jobs/distances.compute answers 503).
	Jobs JobSubmitter
	// Now dates the monthly cap of the distance job (default time.Now).
	Now func() time.Time
	Log zerolog.Logger
}

// Service serves the hub and distance routes of the api.
type Service struct {
	pool  db.Beginner
	cache *cache.Cache
	jobs  JobSubmitter
	now   func() time.Time
	log   zerolog.Logger
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("hubs: the service needs a pool")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{pool: d.Pool, cache: d.Cache, jobs: d.Jobs, now: d.Now, log: d.Log}, nil
}

// tx runs fn in a request transaction of p: RLS decides every row it reads or writes.
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *hubsdb.Queries) error) error {
	return mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, hubsdb.New(tx)) }))
}

// readShared loads global master data for a shared cache: the system context in a READ ONLY transaction, so the value
// is the same whoever asks (cache.GetJSON's contract) and the load can write nothing. Callers authorise and mask.
func readShared(ctx context.Context, pool db.Beginner, fn func(q *hubsdb.Queries) error) error {
	return db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return err
		}
		return fn(hubsdb.New(tx))
	})
}

// emitHubsChanged appends hubs.changed {ids} in the change's transaction.
func emitHubsChanged(ctx context.Context, tx pgx.Tx, ids []uuid.UUID, rid string) error {
	if len(ids) == 0 {
		return nil
	}
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: RouteHubsChanged, AggregateType: "hub", AggregateID: ids[0].String(),
		Payload: map[string]any{"ids": idStrings(ids)}, Topics: []string{TopicGlobal}, Headers: headers,
	})
	return err
}

// invalidate drops the hub keys after COMMIT; the relay repeats it, so a Redis failure is only logged.
func (s *Service) invalidate(ctx context.Context, ids []uuid.UUID) {
	if s.cache == nil || len(ids) == 0 {
		return
	}
	payload, _ := json.Marshal(map[string]any{"ids": idStrings(ids)})
	if err := s.cache.OnEvent(ctx, cache.Event{RoutingKey: RouteHubsChanged, AggregateID: ids[0].String(), Payload: payload}); err != nil {
		s.log.Warn().Err(err).Str("event", RouteHubsChanged).Msg("cache invalidation failed; the outbox relay retries it")
	}
}

func idStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// --- the hub DTO --------------------------------------------------------------------------------------------

// Hub is the single DTO of GET /v1/hubs (W8, Appendix B §B.2.9), also the payload of every hub write. linkedCustomerId
// is the customer (kind customer) or carrier tenant (kind partner) the hub's billing party bills (the legacy
// linkedCustomerId); linkedPartyId is that billing party (R6); network is the stored SPX/SPK group.
type Hub struct {
	ID                 uuid.UUID  `json:"id"`
	SourceID           string     `json:"sourceId"`
	SourceNameTh       string     `json:"sourceNameTh"`
	SourceNameEn       *string    `json:"sourceNameEn"`
	Latitude           *float64   `json:"latitude"`
	Longitude          *float64   `json:"longitude"`
	StationType        string     `json:"stationType"`
	Network            string     `json:"network"`
	LinkedPartyID      *uuid.UUID `json:"linkedPartyId"`
	LinkedCustomerID   *uuid.UUID `json:"linkedCustomerId"`
	LinkedCustomerName *string    `json:"linkedCustomerName"`
	LinkedCustomerKind *string    `json:"linkedCustomerKind"` // customer | partner
	CreatedByDriver    bool       `json:"createdByDriver"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// cachedHub is one entry of cache:hubs:all: the DTO plus what the per-request mask needs (the carrier tenant of a
// partner link).
type cachedHub struct {
	Hub
	PartyKind     string     `json:"partyKind,omitempty"`
	PartyTenantID *uuid.UUID `json:"partyTenantId,omitempty"`
}

func hubFromRow(r hubsdb.ListHubRowsRow) cachedHub {
	h := cachedHub{Hub: Hub{ID: r.ID, SourceID: r.SourceID, SourceNameTh: r.NameTh, SourceNameEn: r.NameEn,
		Latitude: r.Latitude, Longitude: r.Longitude, StationType: r.StationType, Network: r.Network,
		LinkedPartyID: r.LinkedPartyID, CreatedByDriver: r.CreatedByDriver, UpdatedAt: r.UpdatedAt.UTC()}}
	if r.PartyKind == nil {
		return h
	}
	h.PartyKind = *r.PartyKind
	switch h.PartyKind {
	case partyCustomer:
		k := kindCustomer
		h.LinkedCustomerKind, h.LinkedCustomerID = &k, r.PartyCustomerID
		h.LinkedCustomerName = displayName(r.CustomerCode, r.CustomerName)
	case partyTenant:
		k := kindPartner
		h.LinkedCustomerKind, h.LinkedCustomerID, h.PartyTenantID = &k, r.PartyTenantID, r.PartyTenantID
		h.LinkedCustomerName = displayName(r.TenantCode, r.TenantName)
	}
	return h
}

// displayName is the legacy denormalised linkedCustomerName, "CODE — Name" (hub-dialog.tsx), or the part there is.
func displayName(code, name *string) *string {
	c, n := trimmed(code), trimmed(name)
	switch {
	case c != "" && n != "":
		v := c + " — " + n
		return &v
	case n != "":
		return &n
	case c != "":
		return &c
	}
	return nil
}

func trimmed(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// forPrincipal is the DTO as p may see it, equal to what the RLS-backed Get answers: the hub row is global (every
// authenticated principal reads hubs, linkedPartyId included), but the customer or carrier behind its billing party is
// shown only where p's RLS reads that row. A customer-scope principal reads only its own billing parties, of any kind
// (0012), so for any other party it sees no linkedCustomerId, linkedCustomerName or linkedCustomerKind (the party row
// and its kind are hidden too); other principals read every party, and tenants.p_read decides a carrier's name (own
// tenant and contractor reach, stewards, dispatchers).
func (h cachedHub) forPrincipal(p *authz.Principal) Hub {
	out := h.Hub
	if h.LinkedPartyID == nil {
		return out
	}
	link, party := linkVisible(p, h)
	if link {
		return out
	}
	out.LinkedCustomerID, out.LinkedCustomerName = nil, nil
	if !party {
		out.LinkedCustomerKind = nil // billing_parties.p_read (0012) hides the party row itself
	}
	return out
}

// linkVisible says whether p's RLS reads the customer or carrier behind h's billing party (link) and the billing party
// row itself (party, which carries the kind).
func linkVisible(p *authz.Principal, h cachedHub) (link, party bool) {
	r := p.RLS()
	if r.Bypass || r.Steward {
		return true, true
	}
	if r.Role == string(authz.ScopeCustomer) { // 0012: billing_parties.p_read keeps the customer role to its own parties
		own := slices.Contains(r.CustomerIDs, *h.LinkedPartyID)
		return own, own
	}
	switch h.PartyKind {
	case partyCustomer:
		return true, true
	case partyTenant:
		if r.Dispatcher { // app_is_scope(): a dispatcher's app.role is its membership role, so it reads every party
			return true, true
		}
		return h.PartyTenantID != nil && r.TenantID != nil &&
			(*r.TenantID == *h.PartyTenantID || slices.Contains(r.SubtenantIDs, *h.PartyTenantID)), true
	}
	return false, true
}

// --- reads --------------------------------------------------------------------------------------------------

// ListInput are the filters and the page of GET /v1/hubs.
type ListInput struct {
	StationType string // HUB | SOC (legacy spellings accepted)
	// Q: every folded word of it is in the code or a name, in any order (searchtext, the web's lib/searchText.ts).
	Q string
	// Page asks for one keyset page (?limit or ?cursor given): Limit rows (0 = DefaultLimit) after Cursor. Without it
	// the whole master answers in one response.
	Page   bool
	Limit  int
	Cursor string
}

// hubCursor is the opaque cursor of a GET /v1/hubs page: the code and id of its last row.
type hubCursor struct {
	Code string    `json:"c"`
	ID   uuid.UUID `json:"id"`
}

func (c hubCursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeHubCursor(s string) (hubCursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return hubCursor{}, false
	}
	var c hubCursor
	if json.Unmarshal(b, &c) != nil || c.Code == "" || c.ID == uuid.Nil {
		return hubCursor{}, false
	}
	return c, true
}

// compareHubKeys is the order of GET /v1/hubs: the stored (upper-case) code in byte order, then the id. It is total
// and computed here, not taken from the database's collation, so a keyset cursor resumes exactly.
func compareHubKeys(codeA string, idA uuid.UUID, codeB string, idB uuid.UUID) int {
	if c := strings.Compare(codeA, codeB); c != 0 {
		return c
	}
	return bytes.Compare(idA[:], idB[:])
}

// List is GET /v1/hubs: the hubs and SOCs ordered by code (compareHubKeys) from cache:hubs:all (Appendix B §B.6.2),
// filtered and masked for p. Without in.Page it answers the whole master (a few hundred rows) and no cursor: the
// master-data pages, the maps and the distance panels read it so. With in.Page it answers one keyset page and the
// cursor of the next ("" on the last page): the pickers search with q one page at a time (UI2B).
func (s *Service) List(ctx context.Context, p *authz.Principal, in ListInput) ([]Hub, string, error) {
	var ck checker
	var station string
	if v := strings.TrimSpace(in.StationType); v != "" {
		st, ok := ParseStationType(v)
		if !ok {
			ck.add("stationType", "invalid")
		}
		station = st
	}
	q := strings.TrimSpace(in.Q)
	if utf8.RuneCountInString(q) > searchtext.MaxQueryLen {
		ck.add("q", "too_long")
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if in.Page && (limit < 1 || limit > MaxLimit) {
		ck.add("limit", "out_of_range")
	}
	var after *hubCursor
	if in.Cursor != "" {
		c, ok := decodeHubCursor(in.Cursor)
		if !ok {
			ck.add("cursor", "invalid")
		}
		after = &c
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	all, err := s.allHubs(ctx)
	if err != nil {
		return nil, "", err
	}
	terms := searchtext.Terms(q)
	out := make([]Hub, 0, len(all))
	for _, h := range all {
		if station != "" && h.StationType != station {
			continue
		}
		if len(terms) > 0 && !matches(h.Hub, terms) {
			continue
		}
		if after != nil && compareHubKeys(h.SourceID, h.ID, after.Code, after.ID) <= 0 {
			continue
		}
		out = append(out, h.forPrincipal(p))
	}
	slices.SortFunc(out, func(a, b Hub) int { return compareHubKeys(a.SourceID, a.ID, b.SourceID, b.ID) })
	if !in.Page || len(out) <= limit {
		return out, "", nil
	}
	out = out[:limit]
	last := out[limit-1]
	return out, hubCursor{Code: last.SourceID, ID: last.ID}.encode(), nil
}

// matches says whether every term is in the hub's folded code and names.
func matches(h Hub, terms []string) bool {
	text := h.SourceID + " " + h.SourceNameTh
	if h.SourceNameEn != nil {
		text += " " + *h.SourceNameEn
	}
	return searchtext.Matches(searchtext.Fold(text), terms)
}

// Lookup is GET /v1/hubs/{ref}: one hub or SOC by its uuid or its code (case-insensitive), else the SOC whose
// normalised key the reference names (SOCE for "SOCE (บัวโรย)", normalizeSocIdToKey; the first in code order), from
// cache:hubs:all and masked for p as the list is. A picker labels the value it holds with it when the current page
// does not hold it (UI2B). Nothing matching is 404.
func (s *Service) Lookup(ctx context.Context, p *authz.Principal, ref string) (*Hub, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || utf8.RuneCountInString(ref) > MaxSourceIDLen {
		return nil, httpx.ErrNotFound()
	}
	all, err := s.allHubs(ctx)
	if err != nil {
		return nil, err
	}
	id, idErr := uuid.Parse(ref)
	code := NormalizeSourceID(ref)
	var soc *cachedHub
	for i := range all {
		h := &all[i]
		if (idErr == nil && h.ID == id) || strings.EqualFold(h.SourceID, code) {
			out := h.forPrincipal(p)
			return &out, nil
		}
		if h.StationType == StationSOC && NormalizeSocKey(h.SourceID) == NormalizeSocKey(ref) &&
			(soc == nil || compareHubKeys(h.SourceID, h.ID, soc.SourceID, soc.ID) < 0) {
			soc = h
		}
	}
	if soc == nil {
		return nil, httpx.ErrNotFound()
	}
	out := soc.forPrincipal(p)
	return &out, nil
}

// allHubs reads the master through cache:hubs:all (TTL CACHE_TTL_HUBS).
func (s *Service) allHubs(ctx context.Context) ([]cachedHub, error) {
	load := func(ctx context.Context) ([]cachedHub, error) { return loadHubs(ctx, s.pool) }
	if s.cache == nil {
		return load(ctx)
	}
	return cache.GetJSON(ctx, s.cache, s.cache.Keys().HubsAll(), s.cache.TTLs().Hubs, load)
}

func loadHubs(ctx context.Context, pool db.Beginner) ([]cachedHub, error) {
	var out []cachedHub
	err := readShared(ctx, pool, func(q *hubsdb.Queries) error {
		rows, err := q.ListHubRows(ctx, nil)
		if err != nil {
			return err
		}
		out = make([]cachedHub, len(rows))
		for i, r := range rows {
			out[i] = hubFromRow(r)
		}
		return nil
	})
	return out, err
}

// Get is one hub as p sees it, read in p's transaction (the payload of the writes).
func (s *Service) Get(ctx context.Context, p *authz.Principal, id uuid.UUID) (*Hub, error) {
	var h cachedHub
	err := s.tx(ctx, p, func(_ pgx.Tx, q *hubsdb.Queries) error {
		rows, err := q.ListHubRows(ctx, &id)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return httpx.ErrNotFound()
		}
		h = hubFromRow(rows[0])
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := h.forPrincipal(p)
	return &out, nil
}

// Maps is GET /v1/hubs/maps: nameToCode and codeToName from cache:hubs:n2c and cache:hubs:c2n, two Redis hashes
// written and dropped together but never merged (Appendix B §B.6.2). Display and filter use only (R53).
func (s *Service) Maps(ctx context.Context) (Maps, error) {
	load := func(ctx context.Context) (cache.HubMaps, error) {
		m, err := loadMaps(ctx, s.pool)
		return cache.HubMaps{NameToCode: m.NameToCode, CodeToName: m.CodeToName}, err
	}
	if s.cache == nil {
		m, err := load(ctx)
		return Maps{NameToCode: m.NameToCode, CodeToName: m.CodeToName}, err
	}
	m, err := s.cache.HubMaps(ctx, load)
	if err != nil {
		return Maps{}, err
	}
	return Maps{NameToCode: m.NameToCode, CodeToName: m.CodeToName}, nil
}

func loadMaps(ctx context.Context, pool db.Beginner) (Maps, error) {
	m := Maps{NameToCode: map[string]string{}, CodeToName: map[string]string{}}
	err := readShared(ctx, pool, func(q *hubsdb.Queries) error {
		names, err := q.ListNameAliases(ctx)
		if err != nil {
			return err
		}
		for _, r := range names {
			name, code := strings.TrimSpace(r.Name), strings.TrimSpace(r.Code)
			if name == "" || code == "" {
				continue
			}
			if _, taken := m.NameToCode[name]; !taken {
				m.NameToCode[name] = code
			}
		}
		codes, err := q.ListCodeNames(ctx)
		if err != nil {
			return err
		}
		for _, r := range codes {
			code, name := strings.TrimSpace(r.Code), strings.TrimSpace(r.Name)
			if code == "" || name == "" {
				continue
			}
			if _, taken := m.CodeToName[code]; !taken {
				m.CodeToName[code] = name
			}
		}
		return nil
	})
	return m, err
}

// --- errors -------------------------------------------------------------------------------------------------

func violation(field, reason string) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason}
}

// mapDBError turns the database refusals a request can still meet into the API's codes: the unique hub code (409),
// checks and foreign keys (422), and RLS (42501: a write outside the caller's reach, 403); anything else is returned as
// is.
func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "23505":
		field := ""
		if pe.ConstraintName == "hubs_source_id" {
			field = "sourceId"
		}
		return httpx.NewError(http.StatusConflict, CodeAlreadyExists, "already exists").
			WithDetails(map[string]any{"field": field, "reason": "taken"}).Wrap(err)
	case "23503":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "not_found")).Wrap(err)
	case "23514":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "invalid")).Wrap(err)
	case "42501":
		return authz.ErrPermissionDenied("row-level security refused the write").Wrap(err)
	}
	return err
}
