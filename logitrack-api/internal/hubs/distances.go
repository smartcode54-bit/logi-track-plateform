package hubs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs/hubsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Paging of GET /v1/distances (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Distance is one row of hub_soc_distances: hubId is the HUB's uuid and hubSourceId its code, socKey the SOC's
// normalised key (SOCE, SOCN, SOCW or the upper code), direction hub_to_soc or soc_to_hub.
type Distance struct {
	ID              uuid.UUID `json:"id"`
	HubID           uuid.UUID `json:"hubId"`
	HubSourceID     string    `json:"hubSourceId"`
	SocKey          string    `json:"socKey"`
	Direction       string    `json:"direction"`
	Network         *string   `json:"network"`
	DistanceMeters  int32     `json:"distanceMeters"`
	DistanceKm      float64   `json:"distanceKm"`
	DurationSeconds int32     `json:"durationSeconds"`
	DurationMinutes float64   `json:"durationMinutes"`
	HubLat          float64   `json:"hubLat"`
	HubLng          float64   `json:"hubLng"`
	SocLat          float64   `json:"socLat"`
	SocLng          float64   `json:"socLng"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// ListDistancesInput are the query parameters of GET /v1/distances.
type ListDistancesInput struct {
	HubID     string // uuid or code of a HUB (its rows) or of a SOC (the rows of its key)
	Direction string
	Limit     int
	Cursor    string
}

type distCursor struct {
	ID uuid.UUID `json:"id"`
}

func (c distCursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeDistCursor(s string) (distCursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return distCursor{}, false
	}
	var c distCursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil {
		return distCursor{}, false
	}
	return c, true
}

// hubRef is a hub a path or query names.
type hubRef struct {
	id      uuid.UUID
	code    string
	station string
}

// findHubs resolves references (a uuid, a code, or a SOC key such as SOCE) to hubs, in p's transaction. Unknown
// references are absent from the map.
func findHubs(ctx context.Context, q *hubsdb.Queries, refs ...string) (map[string]hubRef, error) {
	ids, codes := []uuid.UUID{}, []string{}
	for _, r := range refs {
		if id, err := uuid.Parse(r); err == nil {
			ids = append(ids, id)
		} else {
			codes = append(codes, NormalizeSourceID(r))
		}
	}
	rows, err := q.FindHubsByRef(ctx, hubsdb.FindHubsByRefParams{Ids: ids, Codes: codes})
	if err != nil {
		return nil, err
	}
	out := map[string]hubRef{}
	for _, ref := range refs {
		for _, h := range rows {
			if h.ID.String() == strings.ToLower(ref) || strings.EqualFold(h.SourceID, NormalizeSourceID(ref)) {
				out[ref] = hubRef{id: h.ID, code: h.SourceID, station: h.StationType}
				break
			}
		}
	}
	var socs []hubsdb.ListSocsRow
	for _, ref := range refs {
		if _, ok := out[ref]; ok {
			continue
		}
		if socs == nil {
			if socs, err = q.ListSocs(ctx); err != nil {
				return nil, err
			}
		}
		key := NormalizeSocKey(ref)
		for _, s := range socs {
			if NormalizeSocKey(s.SourceID) == key {
				out[ref] = hubRef{id: s.ID, code: s.SourceID, station: s.StationType}
				break
			}
		}
	}
	return out, nil
}

// ListDistances is GET /v1/distances (authenticated; HubDistancePanel.tsx:30): the rows of one HUB (hubId), of one
// SOC's key (hubId naming a SOC), or of every pair, keyset on id; direction narrows to one direction. An unknown
// hubId has no rows.
func (s *Service) ListDistances(ctx context.Context, p *authz.Principal, in ListDistancesInput) ([]Distance, string, error) {
	var ck checker
	var dir *string
	if v := strings.TrimSpace(in.Direction); v != "" {
		if v != DirectionHubToSOC && v != DirectionSOCToHub {
			ck.add("direction", "invalid")
		}
		dir = &v
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		ck.add("limit", "out_of_range")
	}
	var after *uuid.UUID
	if in.Cursor != "" {
		c, ok := decodeDistCursor(in.Cursor)
		if !ok {
			ck.add("cursor", "invalid")
		}
		after = &c.ID
	}
	ref := strings.TrimSpace(in.HubID)
	if len([]rune(ref)) > MaxSourceIDLen {
		ck.add("hubId", "too_long")
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	var rows []hubsdb.ListDistancesRow
	err := s.tx(ctx, p, func(_ pgx.Tx, q *hubsdb.Queries) error {
		params := hubsdb.ListDistancesParams{Direction: dir, AfterID: after, RowLimit: int32(limit) + 1}
		if ref != "" {
			found, err := findHubs(ctx, q, ref)
			if err != nil {
				return err
			}
			h, ok := found[ref]
			if !ok {
				return nil
			}
			if h.station == StationSOC {
				key := NormalizeSocKey(h.code)
				params.SocKey = &key
			} else {
				params.HubID = &h.id
			}
		}
		var err error
		rows, err = q.ListDistances(ctx, params)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = distCursor{ID: rows[limit-1].ID}.encode()
	}
	out := make([]Distance, len(rows))
	for i, r := range rows {
		out[i] = Distance{ID: r.ID, HubID: r.HubID, HubSourceID: r.HubSourceID, SocKey: r.SocKey, Direction: r.Direction,
			Network: r.Network, DistanceMeters: r.DistanceM, DistanceKm: r.DistanceKm, DurationSeconds: r.DurationS,
			DurationMinutes: r.DurationMin, HubLat: r.HubLat, HubLng: r.HubLng, SocLat: r.SocLat, SocLng: r.SocLng,
			UpdatedAt: r.UpdatedAt.UTC()}
	}
	return out, next, nil
}

// Pair is the answer of GET /v1/distances/{originId}/{destinationId}.
type Pair struct {
	DistanceKm      float64 `json:"distanceKm"`
	DurationMinutes float64 `json:"durationMinutes"`
	Direction       string  `json:"direction"`
}

// GetPair is GET /v1/distances/{originId}/{destinationId} (authenticated; the staff copy of fetchHubSocDistance,
// mob:hub_soc_distances_repository.dart:50-80): origin and destination are uuids or codes (a SOC also by its key, such
// as SOCE); the direction comes from their station types: HUB -> SOC is hub_to_soc, SOC -> HUB soc_to_hub. Any other
// pair (HUB -> HUB, SOC -> SOC, the same point), an unknown point or a pair never computed is 404.
func (s *Service) GetPair(ctx context.Context, p *authz.Principal, origin, destination string) (*Pair, error) {
	origin, destination = strings.TrimSpace(origin), strings.TrimSpace(destination)
	if origin == "" || destination == "" || len([]rune(origin)) > MaxSourceIDLen || len([]rune(destination)) > MaxSourceIDLen {
		return nil, httpx.ErrNotFound()
	}
	var out *Pair
	err := s.tx(ctx, p, func(_ pgx.Tx, q *hubsdb.Queries) error {
		found, err := findHubs(ctx, q, origin, destination)
		if err != nil {
			return err
		}
		o, ok1 := found[origin]
		d, ok2 := found[destination]
		if !ok1 || !ok2 || o.id == d.id {
			return httpx.ErrNotFound()
		}
		var params hubsdb.GetDistanceParams
		switch {
		case o.station == StationHub && d.station == StationSOC:
			params = hubsdb.GetDistanceParams{HubID: o.id, SocKey: NormalizeSocKey(d.code), Direction: DirectionHubToSOC}
		case o.station == StationSOC && d.station == StationHub:
			params = hubsdb.GetDistanceParams{HubID: d.id, SocKey: NormalizeSocKey(o.code), Direction: DirectionSOCToHub}
		default:
			return httpx.ErrNotFound()
		}
		r, err := q.GetDistance(ctx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		out = &Pair{DistanceKm: r.DistanceKm, DurationMinutes: r.DurationMin, Direction: r.Direction}
		return nil
	})
	return out, err
}

// Meta is GET /v1/distances/meta: when the distance matrix was last computed (settings distances_last_calculated,
// cache:settings:{key}), null before the first run.
type Meta struct {
	LastCalculatedAt *time.Time `json:"lastCalculatedAt"`
	Pairs            *int       `json:"pairs,omitempty"`
}

// lastCalculated is the value of the settings row: {at, pairs, ...} (written by the job; the seed writes the same
// shape), or the legacy {timestamp} of metadata/distances_last_calculated.
type lastCalculated struct {
	At        *time.Time `json:"at"`
	Timestamp *time.Time `json:"timestamp"`
	Pairs     *int       `json:"pairs"`
}

// DistancesMeta is GET /v1/distances/meta (authenticated).
func (s *Service) DistancesMeta(ctx context.Context, p *authz.Principal) (Meta, error) {
	load := func(ctx context.Context) (Meta, error) {
		var m Meta
		err := s.tx(ctx, p, func(_ pgx.Tx, q *hubsdb.Queries) error {
			r, err := q.GetSetting(ctx, SettingDistancesLastCalculated)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			var v lastCalculated
			_ = json.Unmarshal(r.Value, &v) // an unreadable value still dates the row
			at := v.At
			if at == nil {
				at = v.Timestamp
			}
			if at == nil {
				t := r.UpdatedAt
				at = &t
			}
			u := at.UTC()
			m = Meta{LastCalculatedAt: &u, Pairs: v.Pairs}
			return nil
		})
		return m, err
	}
	if s.cache == nil {
		return load(ctx)
	}
	// settings is RLS-exempt: the value is the same whoever loads it.
	return cache.GetJSON(ctx, s.cache, s.cache.Keys().Settings(SettingDistancesLastCalculated), s.cache.TTLs().Settings, load)
}
