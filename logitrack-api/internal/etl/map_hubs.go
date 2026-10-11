package etl

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
)

// Hubs, aliases and the Hub-SOC distances (Appendix A §A.3.3). Hubs are a global master (no tenant).

var (
	thaiText        = regexp.MustCompile(`\p{Thai}`)
	unknownHubID    = regexp.MustCompile(`^UNKNOWN_[0-9]+$`)
	hubSourceFields = []string{"source_id", "hubId", "hubCode"}
)

// hubSourceID is source_id <- hubId <- hubCode, upper and trimmed (sources/page.tsx:76-100, Q8).
func hubSourceID(f map[string]any) string {
	for _, k := range hubSourceFields {
		if s, ok := f[k].(string); ok && strings.TrimSpace(s) != "" {
			return hubs.NormalizeSourceID(s)
		}
	}
	return ""
}

// nonEmpty counts the fields that hold a value: the completeness measure that picks the winner of a duplicated
// source_id (reimport-hubs-dev.js:45-79).
func nonEmpty(f map[string]any) int {
	n := 0
	for _, v := range f {
		if present(v) {
			n++
		}
	}
	return n
}

// hubNames are the Thai and English names of a hub document: source_name_th <- hubTHName <- hub_th_name <-
// station_name_th and source_name_en <- hubName <- station_name_en; Thai text in the English name with an empty Thai
// name moves to the Thai name (hub-dialog.tsx:95-104). A document without a Thai name cannot load (hubs.name_th).
func hubNames(c *docCtx) (th, en *string) {
	th = c.text("source_name_th", "hubTHName", "hub_th_name", "station_name_th")
	en = c.text("source_name_en", "hubName", "station_name_en")
	if th == nil && en != nil && thaiText.MatchString(*en) {
		th, en = en, nil
	}
	return th, en
}

// hubPrep is the winner of every source_id: among the documents that can load (a Thai name after the TH/EN repair),
// the one with the most non-empty fields, ties to the lower doc id (doc-id order); the row id the winner's row has or
// will take, so a loser loaded first already maps to it; and the documents that take a row over from a winner
// deleted at source.
type hubPrep struct {
	winner   map[string]string    // doc path -> the winning doc path of its source_id
	loadable map[string]bool      // source_id -> its winner can load (otherwise no document of it can)
	id       map[string]uuid.UUID // source_id -> hubs.id
	takeover map[string]bool      // doc paths that win now, were rejected before and must be applied even if unchanged
}

// prepareHubs decides the winners over the run's documents and the hubs documents loaded before (etl.source_docs), so
// a later load that brings a more complete duplicate moves the row to it. With full (a load: docs is the whole
// collection as Firestore holds it now), a stored document docs lacks was deleted at source and is no candidate, so
// the surviving duplicate takes the row over; a stored candidate only counts without full (one retried document).
// A document that cannot load never wins over one that can, so a richer English-only duplicate never keeps a
// loadable one out of the table.
func prepareHubs(c *docCtx, docs []dump.Doc, full bool) error {
	type cand struct {
		path, id string
		score    int
		loadable bool
	}
	bySource := map[string][]cand{}
	seen := map[string]bool{}
	add := func(id, path string, f map[string]any) {
		if seen[path] {
			return
		}
		seen[path] = true
		if s := hubSourceID(f); s != "" {
			th, _ := hubNames(&docCtx{f: f})
			bySource[s] = append(bySource[s], cand{path: path, id: id, score: nonEmpty(f), loadable: th != nil})
		}
	}
	inRun := map[string]bool{}
	for _, d := range docs {
		inRun[d.Path] = true
		add(d.ID, d.Path, d.Fields)
	}
	rows, err := c.tx.Query(c.ctx, `SELECT doc_id, doc_path, raw, status FROM etl.source_docs WHERE collection = 'hubs'`)
	if err != nil {
		return fmt.Errorf("etl: hubs: %w", err)
	}
	type stored struct {
		id, path, status string
		raw              []byte
	}
	var olds []stored
	for rows.Next() {
		var s stored
		if err := rows.Scan(&s.id, &s.path, &s.raw, &s.status); err != nil {
			rows.Close()
			return err
		}
		olds = append(olds, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	status := map[string]string{}
	for _, s := range olds {
		status[s.path] = s.status
		if full && !inRun[s.path] {
			continue // deleted at source: kept in etl.source_docs (reconcile reports it), never a candidate
		}
		f, err := dump.UnmarshalFields(s.raw)
		if err != nil {
			return fmt.Errorf("etl: %s: %w", s.path, err)
		}
		add(s.id, s.path, f)
	}
	p := &hubPrep{winner: map[string]string{}, loadable: map[string]bool{}, id: map[string]uuid.UUID{}, takeover: map[string]bool{}}
	for src, cs := range bySource {
		slices.SortFunc(cs, func(a, b cand) int {
			if a.loadable != b.loadable {
				if a.loadable {
					return -1
				}
				return 1
			}
			if a.score != b.score {
				return b.score - a.score
			}
			return strings.Compare(a.id, b.id)
		})
		for _, x := range cs {
			p.winner[x.path] = cs[0].path
		}
		p.loadable[src] = cs[0].loadable
		// A loadable winner that an earlier load rejected (it lost to a duplicate that is gone now) is applied even
		// when Firestore did not change it, so the row stops carrying the deleted document.
		if w := cs[0]; w.loadable && inRun[w.path] && status[w.path] == string(Rejected) {
			p.takeover[w.path] = true
		}
		var id uuid.UUID
		err := c.tx.QueryRow(c.ctx, `SELECT id FROM hubs WHERE source_id = $1`, src).Scan(&id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if id, err = uuid.NewV7(); err != nil {
				return err
			}
		case err != nil:
			return fmt.Errorf("etl: hubs %s: %w", src, err)
		}
		p.id[src] = id
	}
	c.run.prep["hubs"] = p
	return nil
}

// hubTakesOver is the reapply rule of hubs: a duplicate that wins now and lost before.
func hubTakesOver(r *runState, path string) bool {
	p, _ := r.prep["hubs"].(*hubPrep)
	return p != nil && p.takeover[path]
}

// loadHub: hubs/{id} -> hubs + hub_name_aliases (§A.3.3). Duplicates per source_id keep the most complete doc that
// can load (prepareHubs); the losers are rejected with duplicate_hub_source_id and their doc ids map to the winner's
// row in etl.source_docs, so legacy references still resolve. Thai text in source_name_en with an empty Thai name moves to name_th
// (hub-dialog.tsx:95-104); `unknown_<n>` ids of import-hubs.ts load flagged hub_unresolved. The network is the
// hubDistanceNetworkGroup rule (hubs.NetworkGroup) over the code and the linked customer's code. Aliases are name ->
// code only (source_name_th, source_name_en, legacy hubName; blank, equal-to-code and other hubs' codes skipped; the
// first writer in doc-id order keeps a name).
func loadHub(c *docCtx) error {
	sid := hubSourceID(c.f)
	if sid == "" {
		return c.reject("source_id", ReasonMissingRequired, "hubs.source_id is NOT NULL (source_id, hubId, hubCode)", nil)
	}
	p, _ := c.run.prep["hubs"].(*hubPrep)
	if p == nil {
		// One document outside a load (a retry, the mirror): every stored document stays a candidate.
		if err := prepareHubs(c, []dump.Doc{c.doc}, false); err != nil {
			return err
		}
		p = c.run.prep["hubs"].(*hubPrep)
	}
	hubID := p.id[sid]
	// A loser maps to the winner's row only when the winner can load: when no document of the source_id has a Thai
	// name each is rejected on its own (missing_required) and none points at a row that will not exist.
	if w := p.winner[c.doc.Path]; w != "" && w != c.doc.Path && p.loadable[sid] {
		if err := c.reject("source_id", ReasonDuplicateHubSourceID, "another document of this source_id ("+w+") is more complete; this doc maps to its row", sid); err != nil {
			return err
		}
		c.table, c.target = "hubs", hubID.String()
		return nil
	}
	th, en := hubNames(c)
	if th == nil {
		return c.reject("source_name_th", ReasonMissingRequired, "hubs.name_th is NOT NULL", nil)
	}
	lat, lng := c.float("latitude"), c.float("longitude")
	if lat == nil {
		lat = c.float("lat")
	}
	if lng == nil {
		lng = c.float("lng")
	}
	station := hubs.StationHub
	if s := c.str("station_type"); s != "" {
		v, ok := hubs.ParseStationType(s)
		if !ok {
			c.find("station_type", ReasonStatusOutOfVocab, "not HUB, SOC, FM_HUB, LH_HUB or RETURN_CENTER; HUB kept", s)
		} else {
			station = v
		}
	}
	party, err := c.party("linkedCustomerId")
	if err != nil {
		return err
	}
	code := ""
	if party != nil {
		if err := c.tx.QueryRow(c.ctx, `SELECT coalesce(cu.code::text, t.code::text, '') FROM billing_parties bp
			LEFT JOIN customers cu ON cu.id = bp.customer_id LEFT JOIN tenants t ON t.id = bp.tenant_id WHERE bp.id = $1`, *party).Scan(&code); err != nil {
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	if unknownHubID.MatchString(sid) {
		c.find("source_id", ReasonHubUnresolved, "an unknown_<n> placeholder id of import-hubs.ts", sid)
	}
	by, err := c.user("createdBy", c.str("createdBy"))
	if err != nil {
		return err
	}
	// A doc whose source_id changed leaves its old row: that row keeps its code, without this legacy id.
	if _, err := c.tx.Exec(c.ctx, `UPDATE hubs SET legacy_doc_id = NULL WHERE legacy_doc_id = $1 AND source_id <> $2`, c.doc.ID, sid); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	r := newRow("hubs").set("id", hubID).set("legacy_doc_id", c.doc.ID).set("source_id", sid).set("name_th", th).set("name_en", en).
		set("latitude", lat).set("longitude", lng).set("station_type", station).set("network", hubs.NetworkGroup(sid, code)).
		set("linked_party_id", party).set("created_by_driver", c.isTrue("createdByDriver")).set("created_by", by)
	c.times(r)
	id, err := upsert(c.ctx, c.tx, r, "(source_id)", "source_id", "id")
	if err != nil {
		return fmt.Errorf("%s: %w", c.doc.Path, err)
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM hub_name_aliases WHERE hub_id = $1 AND source IN ('name_th', 'name_en', 'legacy_hubName')`, id); err != nil {
		return fmt.Errorf("etl: %s: aliases: %w", c.doc.Path, err)
	}
	var names, sources []string
	add := func(name *string, source string) {
		if name == nil {
			return
		}
		n := strings.TrimSpace(*name)
		if n == "" || strings.EqualFold(n, sid) || slices.ContainsFunc(names, func(x string) bool { return strings.EqualFold(x, n) }) {
			return
		}
		names, sources = append(names, n), append(sources, source)
	}
	add(th, "name_th")
	add(en, "name_en")
	add(c.text("hubName"), "legacy_hubName")
	for i, n := range names {
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO hub_name_aliases (alias, hub_id, source) SELECT $1, $2, $3
			WHERE NOT EXISTS (SELECT 1 FROM hubs o WHERE o.source_id = $1::citext) ON CONFLICT (alias) DO NOTHING`, n, id, sources[i]); err != nil {
			return fmt.Errorf("etl: %s: alias: %w", c.doc.Path, err)
		}
	}
	c.done("hubs", id)
	return nil
}

// loadDistance: hub_soc_distances/{hub}_{soc} and soc_hub_distances/{soc}_{hub} -> one table with direction (R14):
// hubId is a source_id -> hubs.id (no hub: rejected + hub_unresolved, the hub is a key column), socId normalised
// (SOCE/SOCN/SOCW, else the upper code). A second document of one (hub, SOC, direction) is rejected with
// duplicate_natural_key.
func loadDistance(c *docCtx) error {
	dir := hubs.DirectionHubToSOC
	if c.coll == "soc_hub_distances" {
		dir = hubs.DirectionSOCToHub
	}
	code, soc := hubs.NormalizeSourceID(c.str("hubId")), c.str("socId")
	if code == "" || soc == "" {
		return c.reject("hubId", ReasonMissingRequired, "hub_id and soc_key are NOT NULL", nil)
	}
	var hubID uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id FROM hubs WHERE source_id = $1`, code).Scan(&hubID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.reject("hubId", ReasonHubUnresolved, "no hub with this source_id; hub_id is NOT NULL", code)
	}
	if err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	key := hubs.NormalizeSocKey(soc)
	nums := map[string]*float64{}
	for _, f := range []string{"distanceMeters", "distanceKm", "durationSeconds", "durationMinutes", "hubLat", "hubLng", "socLat", "socLng"} {
		if !c.has(f) {
			return c.reject(f, ReasonMissingRequired, "a NOT NULL distance column has no value", nil)
		}
		if nums[f] = c.float(f); nums[f] == nil {
			return c.reject(f, ReasonBadNumber, "a NOT NULL distance column is not a number", c.get(f))
		}
	}
	if *nums["distanceMeters"] < 0 || *nums["durationSeconds"] < 0 {
		return c.reject("distanceMeters", ReasonBadNumber, "distance and duration are >= 0", c.get("distanceMeters"))
	}
	var network *string
	if s := strings.ToUpper(c.str("network")); s != "" {
		if s == hubs.NetworkSPX || s == hubs.NetworkSPK {
			network = &s
		} else {
			c.find("network", ReasonStatusOutOfVocab, "neither SPX nor SPK", c.get("network"))
		}
	}
	var other string
	err = c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM hub_soc_distances WHERE hub_id = $1 AND soc_key = $2
		AND direction = $3 AND legacy_doc_id IS DISTINCT FROM $4`, hubID, key, dir, c.doc.ID).Scan(&other)
	if err == nil {
		return c.reject("socId", ReasonDuplicateNaturalKey, "the pair and direction are already "+other, soc)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	createdBy, err := c.user("createdBy", c.str("createdBy"))
	if err != nil {
		return err
	}
	updatedBy, err := c.user("updatedBy", c.str("updatedBy"))
	if err != nil {
		return err
	}
	r := newRow("hub_soc_distances").set("legacy_doc_id", c.doc.ID).set("hub_id", hubID).set("soc_key", key).set("direction", dir).
		set("network", network).set("distance_m", int64(*nums["distanceMeters"])).set("distance_km", *nums["distanceKm"]).
		set("duration_s", int64(*nums["durationSeconds"])).set("duration_min", *nums["durationMinutes"]).
		set("hub_lat", *nums["hubLat"]).set("hub_lng", *nums["hubLng"]).set("soc_lat", *nums["socLat"]).set("soc_lng", *nums["socLng"]).
		set("created_by", createdBy).set("updated_by", updatedBy)
	c.times(r)
	id, err := upsert(c.ctx, c.tx, r, "(hub_id, soc_key, direction)", "hub_id", "soc_key", "direction")
	if err != nil {
		return fmt.Errorf("%s: %w", c.doc.Path, err)
	}
	c.done("hub_soc_distances", id)
	return nil
}
