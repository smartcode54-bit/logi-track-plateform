package etl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// The legacy-shape encoders of the projected collections (main spec §13.8): each is the inverse of its loader
// (map_master.go, map_hubs.go, map_fleet.go, map_billing.go), so a document loaded at P1 and projected back says the
// same thing, and its mask is the set of fields that loader reads, minus the MobileOwnedFields.

// mintID is the document id of a row (R25): legacy_doc_id, else id::text, stored into legacy_doc_id when mint is set.
// The minting UPDATE moves the row's updated_at (trg_set_updated_at): updated, when given, takes the value it left, so
// the document says what the row says once the mint commits. table is a constant of the encoders, never document data.
func mintID(ctx context.Context, tx pgx.Tx, table string, id uuid.UUID, legacy *string, mint bool, updated *time.Time) (string, error) {
	if legacy != nil && *legacy != "" {
		return *legacy, nil
	}
	docID := id.String()
	if mint {
		var at time.Time
		err := tx.QueryRow(ctx, `UPDATE `+pgx.Identifier{table}.Sanitize()+` SET legacy_doc_id = $2 WHERE id = $1 AND legacy_doc_id IS NULL
			RETURNING updated_at`, id, docID).Scan(&at)
		switch {
		case errors.Is(err, pgx.ErrNoRows): // minted meanwhile by another projection
		case err != nil:
			return "", fmt.Errorf("etl: mint the document id of %s %s: %w", table, id, err)
		case updated != nil:
			*updated = at
		}
	}
	return docID, nil
}

func quarantinedRow(source string) bool { return source == string(tenancy.SourceQuarantine) }

// stringSet runs a query of one text column into a set (the references a Keep rule tests).
func stringSet(ctx context.Context, tx pgx.Tx, sql string, args ...any) (map[string]bool, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(list))
	for _, v := range list {
		out[v] = true
	}
	return out, nil
}

// --- hubs -------------------------------------------------------------------------------------------------------

// encodeHubs: hubs (+ the linked party) -> hubs/{id} (§A.3.3 inverse). The network and the aliases are PostgreSQL's
// own (not in the legacy document); a hub a driver created in Firestore is the mirror's (skipped). The selection also
// takes the hubs linked to some customers or carriers (their code and name are copied into linkedCustomerName).
func encodeHubs(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT h.id, h.legacy_doc_id, h.source_id::text, h.name_th, h.name_en, h.latitude, h.longitude,
		  h.station_type, h.created_by_driver, bp.kind, coalesce(cu.legacy_doc_id, cu.id::text, t.legacy_doc_id, t.id::text),
		  coalesce(cu.code::text, t.code::text), coalesce(cu.name, t.name_th), u.legacy_auth_uid, h.created_at, h.updated_at
		FROM hubs h LEFT JOIN billing_parties bp ON bp.id = h.linked_party_id LEFT JOIN customers cu ON cu.id = bp.customer_id
		LEFT JOIN tenants t ON t.id = bp.tenant_id LEFT JOIN users u ON u.id = h.created_by
		WHERE $1 OR h.id = ANY($2) OR bp.customer_id = ANY($3) OR bp.tenant_id = ANY($4) ORDER BY h.id`,
		sel.all, sel.ids, sel.customers, sel.tenants)
	if err != nil {
		return nil, fmt.Errorf("etl: encode hubs: %w", err)
	}
	type hub struct {
		id                                    uuid.UUID
		legacy, nameEn                        *string
		code, nameTh, station                 string
		lat, lng                              *float64
		byDriver                              bool
		kind, partyDoc, partyCode, party, uid *string
		created, updated                      time.Time
	}
	var hs []hub
	for rows.Next() {
		var h hub
		if err := rows.Scan(&h.id, &h.legacy, &h.code, &h.nameTh, &h.nameEn, &h.lat, &h.lng, &h.station, &h.byDriver, &h.kind,
			&h.partyDoc, &h.partyCode, &h.party, &h.uid, &h.created, &h.updated); err != nil {
			rows.Close()
			return nil, err
		}
		hs = append(hs, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	parties, err := stringSet(ctx, tx, `SELECT coalesce(legacy_doc_id, id::text) FROM customers
		UNION SELECT coalesce(legacy_doc_id, id::text) FROM tenants`)
	if err != nil {
		return nil, err
	}
	var out []ExportDoc
	for _, h := range hs {
		if h.byDriver && h.legacy != nil && *h.legacy != h.id.String() {
			out = append(out, newDoc("hubs/"+*h.legacy).skip("a hub a driver created in Firestore: the mirror's").doc())
			continue
		}
		docID, err := mintID(ctx, tx, "hubs", h.id, h.legacy, mint, &h.updated)
		if err != nil {
			return nil, err
		}
		code, station := h.code, h.station
		b := newDoc("hubs/"+docID).
			set("source_id", code).eq("source_id", func(cur any, has bool) bool {
			s, ok := cur.(string)
			return has && ok && hubs.NormalizeSourceID(s) == code
		}).
			set("source_name_th", h.nameTh).set("source_name_en", h.nameEn).set("latitude", h.lat).set("longitude", h.lng).
			set("station_type", station).eq("station_type", func(cur any, has bool) bool {
			s, _ := cur.(string)
			if !has || strings.TrimSpace(s) == "" {
				return station == hubs.StationHub // the form's default
			}
			v, ok := hubs.ParseStationType(s)
			return ok && v == station
		}).
			set("createdByDriver", h.byDriver).eq("createdByDriver", sameBool(h.byDriver, false)).
			created(h.created).set("updatedAt", h.updated)
		if h.kind != nil {
			kind := map[string]string{"customer": "customer", "tenant": "partner"}[*h.kind]
			label := linkedPartyLabel(deref(h.partyCode, ""), deref(h.party, ""))
			b.set("linkedCustomerId", h.partyDoc).set("customerLinkKind", kind).set("linkedCustomerName", label).
				eq("customerLinkKind", func(cur any, has bool) bool {
					s, _ := cur.(string)
					// The loader resolves the party without the kind (customers first): an absent kind of a customer is the same.
					return (has && s == kind) || (kind == "customer" && blank(cur))
				}).
				eq("linkedCustomerName", sameLinkedPartyLabel(deref(h.partyCode, ""), deref(h.party, "")))
		} else {
			// No link in PostgreSQL: an unlinked hub, or a link the load could not resolve (party_unresolved), which stays.
			b.set("linkedCustomerId", nil).set("customerLinkKind", nil).set("linkedCustomerName", nil).
				keepIf("linkedCustomerId", "", unknownTo(parties), "linkedCustomerId", "customerLinkKind", "linkedCustomerName")
		}
		b.uid("createdBy", h.uid)
		out = append(out, b.doc())
	}
	return out, nil
}

// linkedPartyLabel is the denormalised linkedCustomerName the hub form and the Go hubs API write: "CODE — Name", or
// the part there is (internal/hubs displayName; hub-dialog.tsx).
func linkedPartyLabel(code, name string) string {
	code, name = strings.TrimSpace(code), strings.TrimSpace(name)
	switch {
	case code != "" && name != "":
		return code + " — " + name
	case name != "":
		return name
	}
	return code
}

// sameLinkedPartyLabel: a stored label says the party's current code and name ("code — Name" with the code in any
// case, as the legacy form copied the customers document's spelling), or its current name alone (older documents);
// after a rename or a code change neither matches and the label is rewritten.
func sameLinkedPartyLabel(code, name string) func(cur any, has bool) bool {
	code, name = strings.TrimSpace(code), strings.TrimSpace(name)
	return func(cur any, has bool) bool {
		s, ok := cur.(string)
		if !has || !ok {
			return code == "" && name == ""
		}
		s = strings.TrimSpace(s)
		if s == name && name != "" || s == linkedPartyLabel(code, name) {
			return true
		}
		c, n, found := strings.Cut(s, " — ")
		return found && code != "" && strings.EqualFold(strings.TrimSpace(c), code) && strings.TrimSpace(n) == name
	}
}

// --- hub <-> SOC distances --------------------------------------------------------------------------------------

// encodeDistances: hub_soc_distances(direction) -> hub_soc_distances/{hub}_{soc} or soc_hub_distances/{soc}_{hub}. The
// 3.x app reads a pair by that natural id, built from the hubs document's source_id and the SOC key
// (hub_soc_distances_repository.dart:60-69), so a pair is projected under it, not under its uuid:
//   - a pair whose legacy_doc_id still names it (any split {H}_{S} with H the hub's code and S the SOC key, the loaded
//     spelling of either kept) keeps that id;
//   - a pair computed in Go is minted with the natural id, the hub part spelled as the hub's loaded pairs spell it (a
//     legacy lower-case code stays lower-case, as its hubs document does), else as the code;
//   - a pair whose hub's code changed (PATCH /v1/hubs/{id}) is minted again under the new natural id, and its former
//     document loses its distance and duration (Retired), so a hub that later takes the old code is never served this
//     pair's figures;
//   - a natural id another pair of the direction still holds, or one that is not a valid document id (a '/' in a
//     code), leaves the pair under its current id, or unprojected when it has none.
func encodeDistances(direction string) encoder {
	coll := map[string]string{hubs.DirectionHubToSOC: "hub_soc_distances", hubs.DirectionSOCToHub: "soc_hub_distances"}[direction]
	return func(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
		rows, err := tx.Query(ctx, `SELECT d.id, d.hub_id, d.legacy_doc_id, h.source_id::text, d.soc_key, d.network, d.distance_m, d.distance_km::float8,
			  d.duration_s, d.duration_min::float8, d.hub_lat, d.hub_lng, d.soc_lat, d.soc_lng, cu.legacy_auth_uid, uu.legacy_auth_uid,
			  d.created_at, d.updated_at
			FROM hub_soc_distances d JOIN hubs h ON h.id = d.hub_id LEFT JOIN users cu ON cu.id = d.created_by
			LEFT JOIN users uu ON uu.id = d.updated_by
			WHERE d.direction = $1 AND ($2 OR d.id = ANY($3) OR d.hub_id = ANY($4)) ORDER BY d.id`, direction, sel.all, sel.ids, sel.hubs)
		if err != nil {
			return nil, fmt.Errorf("etl: encode %s: %w", coll, err)
		}
		type pair struct {
			id, hubID                         uuid.UUID
			legacy, network, createdBy, updBy *string
			hub, soc                          string
			meters, seconds                   int32
			km, minutes, hubLat, hubLng       float64
			socLat, socLng                    float64
			created, updated                  time.Time
		}
		var ps []pair
		for rows.Next() {
			var p pair
			if err := rows.Scan(&p.id, &p.hubID, &p.legacy, &p.hub, &p.soc, &p.network, &p.meters, &p.km, &p.seconds, &p.minutes, &p.hubLat,
				&p.hubLng, &p.socLat, &p.socLng, &p.createdBy, &p.updBy, &p.created, &p.updated); err != nil {
				rows.Close()
				return nil, err
			}
			ps = append(ps, p)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		hubIDs := make([]uuid.UUID, 0, len(ps))
		codes := map[uuid.UUID]string{}
		for _, p := range ps {
			if _, ok := codes[p.hubID]; !ok {
				hubIDs = append(hubIDs, p.hubID)
				codes[p.hubID] = p.hub
			}
		}
		spelling, err := hubSpellings(ctx, tx, hubIDs, codes)
		if err != nil {
			return nil, err
		}
		natural := func(p pair) string {
			if direction == hubs.DirectionSOCToHub {
				return p.soc + "_" + spelling[p.hubID]
			}
			return spelling[p.hubID] + "_" + p.soc
		}
		// A pair whose id moves goes first: a pair of the same batch may take the id it frees.
		slices.SortStableFunc(ps, func(a, b pair) int {
			am, bm := a.legacy != nil && !namesPair(*a.legacy, direction, a.hub, a.soc), b.legacy != nil && !namesPair(*b.legacy, direction, b.hub, b.soc)
			switch {
			case am && !bm:
				return -1
			case bm && !am:
				return 1
			}
			return 0
		})
		var out []ExportDoc
		for _, p := range ps {
			legacy := deref(p.legacy, "")
			docID, retire := legacy, ""
			if legacy == "" || !namesPair(legacy, direction, p.hub, p.soc) {
				nat := natural(p)
				var held bool
				if validDocID(nat) {
					if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hub_soc_distances WHERE legacy_doc_id = $1 AND direction = $2 AND id <> $3)`,
						nat, direction, p.id).Scan(&held); err != nil {
						return nil, fmt.Errorf("etl: distance %s: %w", p.id, err)
					}
				}
				switch {
				case !validDocID(nat) || held:
					if legacy == "" {
						reason := "another pair of this direction holds the document id"
						if !validDocID(nat) {
							reason = "the natural id is not a valid document id (a '/' or a reserved name in the hub or SOC code)"
						}
						out = append(out, newDoc(coll+"/"+nat).skip(reason).doc())
						continue
					}
					// The pair stays under its current id until the natural one is free.
				case mint:
					var at time.Time
					err := tx.QueryRow(ctx, `UPDATE hub_soc_distances SET legacy_doc_id = $2 WHERE id = $1 AND legacy_doc_id IS NOT DISTINCT FROM $3
						AND NOT EXISTS (SELECT 1 FROM hub_soc_distances o WHERE o.legacy_doc_id = $2 AND o.direction = $4 AND o.id <> $1)
						RETURNING updated_at`, p.id, nat, p.legacy, direction).Scan(&at)
					switch {
					case errors.Is(err, pgx.ErrNoRows): // minted meanwhile, or the id was taken: the next projection settles it
						if legacy == "" {
							out = append(out, newDoc(coll+"/"+nat).skip("another pair of this direction holds the document id").doc())
							continue
						}
					case err != nil:
						return nil, fmt.Errorf("etl: mint the document id of distance %s: %w", p.id, err)
					default:
						docID, retire, p.updated = nat, legacy, at
					}
				default: // the reconciliation: what the projection would write
					docID, retire = nat, legacy
				}
			}
			if retire != "" && validDocID(retire) {
				r := newDoc(coll+"/"+retire).set("distanceMeters", nil).set("distanceKm", nil).set("durationSeconds", nil).
					set("durationMinutes", nil)
				r.d.Retired = true
				out = append(out, r.doc())
			}
			hub, soc := p.hub, p.soc
			b := newDoc(coll+"/"+docID).
				set("hubId", hub).eq("hubId", func(cur any, has bool) bool {
				s, ok := cur.(string)
				return has && ok && hubs.NormalizeSourceID(s) == hub
			}).
				set("socId", soc).eq("socId", func(cur any, has bool) bool {
				s, ok := cur.(string)
				return has && ok && hubs.NormalizeSocKey(s) == soc
			}).
				set("network", p.network).set("distanceMeters", int64(p.meters)).set("distanceKm", jsNumber(p.km)).
				set("durationSeconds", int64(p.seconds)).set("durationMinutes", jsNumber(p.minutes)).
				set("hubLat", jsNumber(p.hubLat)).set("hubLng", jsNumber(p.hubLng)).set("socLat", jsNumber(p.socLat)).
				set("socLng", jsNumber(p.socLng)).created(p.created).set("updatedAt", p.updated)
			b.uid("createdBy", p.createdBy).uid("updatedBy", p.updBy)
			out = append(out, b.doc())
		}
		return out, nil
	}
}

// namesPair: a document id that splits into {H}_{S} (hub_to_soc) or {S}_{H} (soc_to_hub), H being the hub's code and
// S the SOC key in any spelling the loader folds to them (a code may itself hold '_', so every split is tried).
func namesPair(id, direction, code, soc string) bool {
	for i := 0; i < len(id); i++ {
		if id[i] != '_' {
			continue
		}
		left, right := id[:i], id[i+1:]
		if direction == hubs.DirectionSOCToHub {
			left, right = right, left
		}
		if hubs.NormalizeSourceID(left) == code && hubs.NormalizeSocKey(right) == soc {
			return true
		}
	}
	return false
}

// hubSpellings is, per hub, the spelling of its code in the ids of its loaded pairs (the hubs document's source_id
// the legacy writer built them from, which the 3.x app builds its reads from), else the code itself.
func hubSpellings(ctx context.Context, tx pgx.Tx, hubIDs []uuid.UUID, codes map[uuid.UUID]string) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(codes))
	for id, code := range codes {
		out[id] = code
	}
	if len(hubIDs) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT hub_id, direction, legacy_doc_id, soc_key FROM hub_soc_distances
		WHERE hub_id = ANY($1) AND legacy_doc_id IS NOT NULL ORDER BY hub_id, legacy_doc_id`, hubIDs)
	if err != nil {
		return nil, fmt.Errorf("etl: distance ids: %w", err)
	}
	defer rows.Close()
	counts := map[uuid.UUID]map[string]int{}
	for rows.Next() {
		var hub uuid.UUID
		var dir, id, soc string
		if err := rows.Scan(&hub, &dir, &id, &soc); err != nil {
			return nil, err
		}
		code := codes[hub]
		for i := 0; i < len(id); i++ {
			if id[i] != '_' {
				continue
			}
			h, sk := id[:i], id[i+1:]
			if dir == hubs.DirectionSOCToHub {
				h, sk = sk, h
			}
			if hubs.NormalizeSourceID(h) == code && hubs.NormalizeSocKey(sk) == soc {
				if counts[hub] == nil {
					counts[hub] = map[string]int{}
				}
				counts[hub][h]++
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for hub, c := range counts {
		best, n := out[hub], 0
		for sp, k := range c {
			if k > n || (k == n && sp < best) {
				best, n = sp, k
			}
		}
		out[hub] = best
	}
	return out, nil
}

// encodeMetadata: settings('distances_last_calculated') -> metadata/distances_last_calculated {timestamp} (the shape of
// computeHubSocDistances, functions/src/distances.ts:382); the instant is the Go job's `at`, or the loaded document's
// timestamp / calculatedAt.
func encodeMetadata(ctx context.Context, tx pgx.Tx, _ selection, _ bool) ([]ExportDoc, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, settingDistancesLastCalculated).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("etl: encode metadata: %w", err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("etl: settings %s: %w", settingDistancesLastCalculated, err)
	}
	for _, k := range []string{"at", "timestamp", "calculatedAt"} {
		if t, ok := toTime(v[k]); ok {
			return []ExportDoc{newDoc("metadata/"+settingDistancesLastCalculated).set("timestamp", t).doc()}, nil
		}
	}
	return nil, nil
}

// --- trucks -----------------------------------------------------------------------------------------------------

// truckFileField maps truck_files.kind to its single-URL field (truckFileFields, inverted).
var truckFileField = func() map[string]string {
	m := map[string]string{}
	for _, f := range truckFileFields {
		m[f[2]] = f[0]
	}
	return m
}()

// truckStatusLiteral is the legacy truckStatus of a row: the raw legacy value while it still folds to the status, else
// the form's spelling (insurance_claim -> insurance-claim).
func truckStatusLiteral(status string, legacy *string) string {
	if legacy != nil && truckStatusCanon(*legacy) == status {
		return *legacy
	}
	return strings.ReplaceAll(status, "_", "-")
}

// truckStatusCanon is the loader's fold of truckStatus ('Available' is active).
func truckStatusCanon(s string) string {
	if v := canon(s); v != "available" {
		return v
	}
	return "active"
}

// encodeTrucks: trucks + truck_files + status_history + the active assignments -> trucks/{id} (§A.3.4 inverse). The
// maintenance flow's fields (ForeignFields) are removed by the projector while billing is Firestore-owned.
func encodeTrucks(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT t.id, t.legacy_doc_id, t.tenant_source, tn.kind, coalesce(tn.legacy_doc_id, tn.id::text),
		  t.ownership_type, t.license_plate, t.province, t.vin, t.engine_number, t.gps_vehicle_id, t.status, t.legacy_status, t.brand,
		  t.model, t.year, t.color, t.type_raw, t.seats, t.fuel_type, t.engine_capacity::float8, t.fuel_capacity::float8,
		  t.max_load_weight_kg::float8, t.registration_date, t.buying_date, t.notes, t.tax_expiry_date, t.tax_expense_thb::float8,
		  t.tax_responsible, t.tax_renewal_status, t.maintenance_responsible, t.last_service_date, t.next_service_date,
		  t.next_service_mileage, t.pm_interval_km, t.current_mileage, t.last_alert_mileage, coalesce(m.legacy_doc_id, m.id::text),
		  t.insurance_policy_id, t.insurance_policy_number, t.insurance_company, t.insurance_type, t.insurance_start_date,
		  t.insurance_expiry_date, t.insurance_premium_thb::float8, t.insurance_notes, t.insurance_renewal_status, t.payment_method,
		  t.created_at, t.updated_at
		FROM trucks t JOIN tenants tn ON tn.id = t.tenant_id LEFT JOIN maintenance_records m ON m.id = t.active_maintenance_id
		WHERE $1 OR t.id = ANY($2) ORDER BY t.id`, sel.all, sel.ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode trucks: %w", err)
	}
	type truck struct {
		id                                                      uuid.UUID
		legacy, vin, engine, gps, legacyStatus, fuelType, notes *string
		source, tenantKind, tenantDoc, own, plate, province     string
		status, brand, model, color, typeRaw                    string
		year                                                    int16
		seats                                                   *int16
		engineCap, fuelCap, maxLoad, taxExpense, premium        *float64
		regDate, buyDate, taxExpiry, lastService, nextService   *time.Time
		insStart, insExpiry                                     *time.Time
		taxResp, maintResp                                      string
		taxRenewal, insRenewal, insType, payment                *string
		nextMileage, pm, current, lastAlert                     *int32
		activeMaint                                             *string
		insPolicyID, insPolicyNo, insCompany, insNotes          *string
		created, updated                                        time.Time
	}
	var ts []truck
	for rows.Next() {
		var t truck
		if err := rows.Scan(&t.id, &t.legacy, &t.source, &t.tenantKind, &t.tenantDoc, &t.own, &t.plate, &t.province, &t.vin, &t.engine,
			&t.gps, &t.status, &t.legacyStatus, &t.brand, &t.model, &t.year, &t.color, &t.typeRaw, &t.seats, &t.fuelType, &t.engineCap,
			&t.fuelCap, &t.maxLoad, &t.regDate, &t.buyDate, &t.notes, &t.taxExpiry, &t.taxExpense, &t.taxResp, &t.taxRenewal,
			&t.maintResp, &t.lastService, &t.nextService, &t.nextMileage, &t.pm, &t.current, &t.lastAlert, &t.activeMaint,
			&t.insPolicyID, &t.insPolicyNo, &t.insCompany, &t.insType, &t.insStart, &t.insExpiry, &t.premium, &t.insNotes,
			&t.insRenewal, &t.payment, &t.created, &t.updated); err != nil {
			rows.Close()
			return nil, err
		}
		ts = append(ts, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(ts))
	for i, t := range ts {
		ids[i] = t.id
	}
	files, err := entityFiles(ctx, tx, `SELECT tf.truck_id, tf.kind, f.legacy_url FROM truck_files tf JOIN file_objects f ON f.id = tf.file_id
		WHERE tf.truck_id = ANY($1) ORDER BY tf.truck_id, tf.position, f.id`, ids)
	if err != nil {
		return nil, err
	}
	history, err := statusHistory(ctx, tx, "truck", ids)
	if err != nil {
		return nil, err
	}
	current, err := activeAssignments(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	gps, err := stringSet(ctx, tx, `SELECT gps_vehicle_id FROM trucks WHERE gps_vehicle_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	ownDoc, err := ownFleetDoc(ctx, tx)
	if err != nil {
		return nil, err
	}
	var out []ExportDoc
	for _, t := range ts {
		if quarantinedRow(t.source) {
			out = append(out, newDoc("trucks/"+deref(t.legacy, t.id.String())).skip("in the quarantine tenant").doc())
			continue
		}
		docID, err := mintID(ctx, tx, "trucks", t.id, t.legacy, mint, &t.updated)
		if err != nil {
			return nil, err
		}
		status, legacyStatus := t.status, t.legacyStatus
		b := newDoc("trucks/"+docID).
			set("ownershipType", t.own).eq("ownershipType", sameCanon(t.own, "own", nil)).
			set("licensePlate", t.plate).set("province", t.province).set("vin", t.vin).
			set("engineNumber", t.engine).set("GPSVehicleId", t.gps).
			set("truckStatus", truckStatusLiteral(status, legacyStatus)).eq("truckStatus", func(cur any, has bool) bool {
			s, ok := cur.(string)
			return has && ok && truckStatusCanon(s) == status
		}).
			set("brand", t.brand).set("model", t.model).
			set("year", numericString(int64(t.year))).eq("year", sameNumber(float64(t.year))).
			set("color", t.color).set("type", t.typeRaw).set("fuelType", t.fuelType).
			set("engineCapacity", t.engineCap).set("fuelCapacity", t.fuelCap).set("maxLoadWeight", t.maxLoad).
			set("notes", t.notes).set("taxExpense", t.taxExpense).
			set("taxResponsible", t.taxResp).eq("taxResponsible", sameOrDefault(t.taxResp, "Operation Admin")).
			set("maintenanceResponsible", t.maintResp).eq("maintenanceResponsible", sameOrDefault(t.maintResp, "Driver")).set("nextServiceMileage", t.nextMileage).set("pmIntervalKm", t.pm).
			set("currentMileage", t.current).set("lastAlertMileage", t.lastAlert).set("activeMaintenanceId", t.activeMaint).
			set("insurancePolicyId", t.insPolicyID).set("insurancePolicyNumber", t.insPolicyNo).set("insuranceCompany", t.insCompany).
			set("insuranceType", t.insType).set("insurancePremium", t.premium).set("insuranceNotes", t.insNotes).
			created(t.created).set("updatedAt", t.updated)
		if t.seats != nil {
			b.set("seats", numericString(int64(*t.seats))).eq("seats", sameNumber(float64(*t.seats)))
		} else {
			b.set("seats", nil)
		}
		// A carrier's truck names its subcontractors document; an own truck has none (the form's "").
		if t.own == "subcontractor" && t.tenantKind == "carrier" {
			b.set("subcontractorId", t.tenantDoc)
		} else {
			b.set("subcontractorId", nil).eq("subcontractorId", sameOwnFleet(ownDoc))
		}
		if t.gps == nil { // a GPS id another truck holds (duplicate_natural_key) stays on this document
			b.keepIf("GPSVehicleId", "", func(v string) bool { return gps[v] }, "GPSVehicleId")
		}
		for _, f := range []struct {
			field string
			v     *string
		}{{"taxRenewalStatus", t.taxRenewal}, {"insuranceRenewalStatus", t.insRenewal}} {
			b.set(f.field, f.v)
			if f.v != nil {
				b.eq(f.field, sameCanon(*f.v, "", nil))
			}
		}
		if t.payment != nil {
			lit := map[string]string{"cash": "Cash", "transfer": "Transfer", "company_credit": "Company Credit"}[*t.payment]
			b.set("paymentMethod", lit).eq("paymentMethod", sameCanon(*t.payment, "", nil))
		} else {
			b.set("paymentMethod", nil)
		}
		for _, d := range []struct {
			field string
			v     *time.Time
		}{{"registrationDate", t.regDate}, {"buyingDate", t.buyDate}, {"taxExpiryDate", t.taxExpiry}, {"lastServiceDate", t.lastService},
			{"nextServiceDate", t.nextService}, {"insuranceStartDate", t.insStart}, {"insuranceExpiryDate", t.insExpiry}} {
			if d.v == nil {
				b.set(d.field, nil)
				continue
			}
			b.set(d.field, ymd(*d.v)).eq(d.field, sameDay(*d.v))
		}
		// Files: the single-URL fields, insuranceDocuments[] and images[] carry the legacy URL of objects copied at P1;
		// a Go upload has none, so its field is empty in Firestore (§13.8).
		single := map[string]string{}
		var insurance, images []string
		for _, f := range files[t.id] {
			switch {
			case f.kind == "insurance_document":
				insurance = append(insurance, f.url)
			case f.kind == "legacy_image":
				images = append(images, f.url)
			case truckFileField[f.kind] != "":
				if _, seen := single[truckFileField[f.kind]]; !seen {
					single[truckFileField[f.kind]] = f.url
				}
			}
		}
		for _, f := range truckFileFields {
			if u, ok := single[f[0]]; ok {
				b.set(f[0], u).eq(f[0], sameFile(u))
			} else {
				b.set(f[0], nil)
			}
		}
		b.set("insuranceDocuments", anyStrings(insurance)).eq("insuranceDocuments", sameFiles(insurance))
		b.set("images", anyStrings(images)).eq("images", sameFiles(images))
		h := history[t.id]
		b.set("statusHistory", truckHistoryDoc(h)).eq("statusHistory", sameTruckHistory(h))
		as := current[t.id]
		b.set("currentAssignments", truckAssignmentsDoc(as)).eq("currentAssignments", sameTruckAssignments(as)).
			set("currentAssignment", nil)
		out = append(out, b.doc())
	}
	return out, nil
}

// ownFleetDoc is the subcontractors document settings/tenancy named as the own fleet, if any.
func ownFleetDoc(ctx context.Context, tx pgx.Tx) (*string, error) {
	var doc *string
	err := tx.QueryRow(ctx, `SELECT legacy_doc_id FROM tenants WHERE kind = 'own_fleet'`).Scan(&doc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return doc, err
}

// sameOwnFleet: an own-fleet row's subcontractorId is empty, or the own fleet's own subcontractors document.
func sameOwnFleet(ownDoc *string) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		s, _ := cur.(string)
		return !has || blank(cur) || (ownDoc != nil && s == *ownDoc)
	}
}

func deref(p *string, def string) string {
	if p != nil && *p != "" {
		return *p
	}
	return def
}

// fileRow is one file of an entity: its kind and the legacy URL (only objects copied from Firebase Storage have one).
type fileRow struct{ kind, url string }

func entityFiles(ctx context.Context, tx pgx.Tx, sql string, ids []uuid.UUID) (map[uuid.UUID][]fileRow, error) {
	rows, err := tx.Query(ctx, sql, ids)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]fileRow{}
	for rows.Next() {
		var id uuid.UUID
		var kind string
		var url *string
		if err := rows.Scan(&id, &kind, &url); err != nil {
			rows.Close()
			return nil, err
		}
		if url != nil && *url != "" {
			out[id] = append(out[id], fileRow{kind, *url})
		}
	}
	return out, rows.Err()
}

// historyRow is one status_history entry.
type historyRow struct {
	status                           string
	previous, label, reason, by, uid *string
	at                               time.Time
}

func statusHistory(ctx context.Context, tx pgx.Tx, entity string, ids []uuid.UUID) (map[uuid.UUID][]historyRow, error) {
	rows, err := tx.Query(ctx, `SELECT s.entity_id, s.status, s.previous_status, s.changed_by_label, s.reason, u.email::text,
		  u.legacy_auth_uid, s.changed_at
		FROM status_history s LEFT JOIN users u ON u.id = s.changed_by_user_id
		WHERE s.entity_type = $1 AND s.entity_id = ANY($2) ORDER BY s.entity_id, s.changed_at, s.id`, entity, ids)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]historyRow{}
	for rows.Next() {
		var id uuid.UUID
		var h historyRow
		if err := rows.Scan(&id, &h.status, &h.previous, &h.label, &h.reason, &h.by, &h.uid, &h.at); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = append(out[id], h)
	}
	return out, rows.Err()
}

// truckHistoryDoc is trucks statusHistory[] {status, date ISO, changedBy, notes?} (Q18).
func truckHistoryDoc(hs []historyRow) []any {
	out := make([]any, 0, len(hs))
	for _, h := range hs {
		m := map[string]any{"status": h.status, "date": legacyISO(h.at), "changedBy": truckChangedBy(h)}
		if h.reason != nil && *h.reason != "" {
			m["notes"] = *h.reason
		}
		out = append(out, m)
	}
	return out
}

func truckChangedBy(h historyRow) string {
	if h.label != nil && *h.label != "" {
		return *h.label
	}
	return deref(h.by, "")
}

// loadableEntries are the entries of a stored status history the loader reads (a status and a parseable time): an
// entry it could not load (bad_timestamp) is kept in the document as long as the rest says what PostgreSQL holds.
func loadableEntries(cur any, timeField string) []map[string]any {
	list, _ := cur.([]any)
	var out []map[string]any
	for _, e := range list {
		m, _ := e.(map[string]any)
		s, _ := m["status"].(string)
		if _, ok := toTime(m[timeField]); ok && strings.TrimSpace(s) != "" {
			out = append(out, m)
		}
	}
	return out
}

// sameOrDefault: the stored value, or an absent one where the form's default is the value (trucks taxResponsible).
func sameOrDefault(want, def string) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		s, ok := cur.(string)
		return (has && ok && s == want) || (!has && want == def)
	}
}

// sameTruckHistory: the stored entries say the same statuses, instants, authors and notes.
func sameTruckHistory(hs []historyRow) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		list := loadableEntries(cur, "date")
		if len(list) != len(hs) {
			return false
		}
		for i, h := range hs {
			m := list[i]
			at, ok := toTime(m["date"])
			by, _ := m["changedBy"].(string)
			notes, _ := m["notes"].(string)
			if m["status"] != h.status || !ok || !at.Truncate(time.Microsecond).Equal(h.at) || by != truckChangedBy(h) ||
				strings.TrimSpace(notes) != strings.TrimSpace(deref(h.reason, "")) {
				return false
			}
		}
		return true
	}
}

// assignmentRow is one active truck assignment.
type assignmentRow struct {
	truck, driver                   uuid.UUID
	doc, truckDoc, driverDoc, plate string
	model, first, last              *string
	at                              time.Time
}

func activeAssignments(ctx context.Context, tx pgx.Tx, trucks []uuid.UUID) (map[uuid.UUID][]assignmentRow, error) {
	rows, err := tx.Query(ctx, `SELECT a.truck_id, a.driver_id, coalesce(a.legacy_doc_id, a.id::text), coalesce(t.legacy_doc_id, t.id::text),
		  coalesce(d.legacy_doc_id, d.id::text), t.license_plate, t.model, d.first_name, d.last_name, a.created_at
		FROM truck_assignments a JOIN trucks t ON t.id = a.truck_id JOIN drivers d ON d.id = a.driver_id
		WHERE a.status = 'active' AND a.truck_id = ANY($1) ORDER BY a.truck_id, a.created_at, a.id`, trucks)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]assignmentRow{}
	for rows.Next() {
		var a assignmentRow
		if err := rows.Scan(&a.truck, &a.driver, &a.doc, &a.truckDoc, &a.driverDoc, &a.plate, &a.model, &a.first, &a.last, &a.at); err != nil {
			rows.Close()
			return nil, err
		}
		out[a.truck] = append(out[a.truck], a)
	}
	return out, rows.Err()
}

func driverName(first, last *string) string {
	return strings.TrimSpace(deref(first, "") + " " + deref(last, ""))
}

// truckAssignmentsDoc is trucks currentAssignments[] {driverId, driverName, assignedAt, assignmentId}
// (truck-assignment/actions.client.ts:112-117), derived from the active rows (R14).
func truckAssignmentsDoc(as []assignmentRow) []any {
	out := make([]any, 0, len(as))
	for _, a := range as {
		out = append(out, map[string]any{"driverId": a.driverDoc, "driverName": driverName(a.first, a.last),
			"assignedAt": dump.Timestamp{Time: a.at.UTC()}, "assignmentId": a.doc})
	}
	return out
}

// sameTruckAssignments: the stored array names the same assignments and drivers (names and instants are snapshots).
func sameTruckAssignments(as []assignmentRow) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		list, _ := cur.([]any)
		if len(list) != len(as) {
			return len(as) == 0 && blank(cur)
		}
		for i, a := range as {
			m, _ := list[i].(map[string]any)
			if m["assignmentId"] != a.doc || m["driverId"] != a.driverDoc {
				return false
			}
		}
		return true
	}
}

// --- drivers ----------------------------------------------------------------------------------------------------

// encodeDrivers: drivers + files + status_history + customer codes + the home assignment -> drivers/{id} (§A.3.4
// inverse). The 3.x app's fields (MobileOwnedFields: fcmToken, activeTruck, currentTruckId, truckId, updatedAt) are
// never written. authId, by which the 3.x app finds its driver and the Firestore rules grant a driver its own data,
// follows the login PostgreSQL links (driverAuthID). The selection also takes the drivers holding codes of some
// customers (customerDriverIds is keyed by the customer's code).
func encodeDrivers(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT d.id, d.legacy_doc_id, d.tenant_source, tn.kind, coalesce(tn.legacy_doc_id, tn.id::text),
		  d.user_id IS NOT NULL, u.legacy_auth_uid, d.legacy_auth_uid,
		  EXISTS (SELECT 1 FROM users x WHERE x.legacy_auth_uid = d.legacy_auth_uid AND x.id IS DISTINCT FROM d.user_id),
		  d.first_name, d.last_name, d.full_name_th, d.mobile, d.email::text, d.birth_date,
		  d.id_card, d.id_card_expired_date, d.truck_license_id, d.license_type, d.license_expired_date, d.employment_type,
		  d.contract_years::float8, d.hire_date, d.probation_passed, d.status, d.assign_to_project, pf.legacy_url, cf.legacy_url,
		  lf.legacy_url, coalesce(a.legacy_doc_id, a.id::text), coalesce(tr.legacy_doc_id, tr.id::text), tr.license_plate, tr.model,
		  a.created_at, d.created_at
		FROM drivers d JOIN tenants tn ON tn.id = d.tenant_id LEFT JOIN users u ON u.id = d.user_id
		LEFT JOIN file_objects pf ON pf.id = d.profile_file_id LEFT JOIN file_objects cf ON cf.id = d.id_card_file_id
		LEFT JOIN file_objects lf ON lf.id = d.license_file_id
		LEFT JOIN truck_assignments a ON a.id = d.current_assignment_id LEFT JOIN trucks tr ON tr.id = a.truck_id
		WHERE $1 OR d.id = ANY($2) OR d.id IN (SELECT dc.driver_id FROM driver_customer_codes dc WHERE dc.customer_id = ANY($3))
		ORDER BY d.id`, sel.all, sel.ids, sel.customers)
	if err != nil {
		return nil, fmt.Errorf("etl: encode drivers: %w", err)
	}
	type driver struct {
		id                                                 uuid.UUID
		linked, legacyUIDHeld                              bool
		userUID, legacyUID                                 *string
		legacy, fullTh, email, idCard, licenseID           *string
		licenseType, project, profile, idImg, licImg       *string
		source, tenantKind, tenantDoc, first, last, mobile string
		employment, status                                 string
		birth, idExpiry, licExpiry, hire                   *time.Time
		contract                                           *float64
		probation                                          bool
		asgDoc, asgTruck, asgPlate, asgModel               *string
		asgAt                                              *time.Time
		created                                            time.Time
	}
	var ds []driver
	for rows.Next() {
		var d driver
		if err := rows.Scan(&d.id, &d.legacy, &d.source, &d.tenantKind, &d.tenantDoc, &d.linked, &d.userUID, &d.legacyUID,
			&d.legacyUIDHeld, &d.first, &d.last, &d.fullTh, &d.mobile,
			&d.email, &d.birth, &d.idCard, &d.idExpiry, &d.licenseID, &d.licenseType, &d.licExpiry, &d.employment, &d.contract,
			&d.hire, &d.probation, &d.status, &d.project, &d.profile, &d.idImg, &d.licImg, &d.asgDoc, &d.asgTruck, &d.asgPlate,
			&d.asgModel, &d.asgAt, &d.created); err != nil {
			rows.Close()
			return nil, err
		}
		ds = append(ds, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(ds))
	for i, d := range ds {
		ids[i] = d.id
	}
	history, err := statusHistory(ctx, tx, "driver", ids)
	if err != nil {
		return nil, err
	}
	codes, err := driverCodes(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	assignments, err := stringSet(ctx, tx, `SELECT coalesce(legacy_doc_id, id::text) FROM truck_assignments`)
	if err != nil {
		return nil, err
	}
	ownDoc, err := ownFleetDoc(ctx, tx)
	if err != nil {
		return nil, err
	}
	known, err := stringSet(ctx, tx, `SELECT code::text FROM customers`)
	if err != nil {
		return nil, err
	}
	var uids *driverUIDs // read once, when a driver needs it
	var out []ExportDoc
	for _, d := range ds {
		if quarantinedRow(d.source) {
			out = append(out, newDoc("drivers/"+deref(d.legacy, d.id.String())).skip("in the quarantine tenant").doc())
			continue
		}
		docID, err := mintID(ctx, tx, "drivers", d.id, d.legacy, mint, nil) // updatedAt is the 3.x app's
		if err != nil {
			return nil, err
		}
		status, employment := d.status, d.employment
		b := newDoc("drivers/"+docID).
			set("firstName", d.first).set("lastName", d.last).set("fullNameTh", d.fullTh).set("mobile", d.mobile).
			set("email", d.email).set("idCard", d.idCard).set("truckLicenseId", d.licenseID).set("licenseType", d.licenseType).
			set("contractYears", d.contract).set("assignToProject", d.project).
			set("probationPassed", d.probation).eq("probationPassed", sameBool(d.probation, false)).
			set("status", map[string]string{"active": "Active", "inactive": "Inactive", "on_duty": "On-Duty"}[status]).
			eq("status", sameCanon(status, "active", nil)).
			set("employmentType", strings.ToUpper(employment)).eq("employmentType", sameCanon(employment, "full_time", nil)).
			created(d.created)
		for _, f := range []struct {
			field string
			v     *time.Time
		}{{"birthDate", d.birth}, {"idCardExpiredDate", d.idExpiry}, {"truckLicenseExpiredDate", d.licExpiry}, {"hireDate", d.hire}} {
			if f.v == nil {
				b.set(f.field, nil)
				continue
			}
			b.set(f.field, bangkokMidnight(*f.v)).eq(f.field, sameDay(*f.v))
		}
		switch {
		case d.linked && d.userUID != nil && *d.userUID != "":
			b.set("authId", *d.userUID) // the linked login's Firebase uid
		case d.linked || (d.legacyUID != nil && d.legacyUIDHeld):
			// A login without a Firebase uid yet (it gets one, and an event, when the bridge gives it one), or a P1 uid
			// whose login is no longer linked: no account finds the driver.
			b.set("authId", nil)
		case d.legacyUID != nil:
			b.uid("authId", d.legacyUID) // a uid no login PostgreSQL knows holds (user_unresolved): kept, never deleted
		default:
			if uids == nil {
				if uids, err = readDriverUIDs(ctx, tx); err != nil {
					return nil, err
				}
			}
			// No link and no P1 uid: a stored uid of a login PostgreSQL knows is a former link and goes; one it does
			// not know, or another driver's P1 uid (a duplicate the steward resolves), stays as Firestore has it.
			b.set("authId", nil).keepIf("authId", "", func(v string) bool { return !uids.users[v] || uids.drivers[v] }, "authId")
		}
		if d.tenantKind == "carrier" {
			b.set("subcontractorId", d.tenantDoc)
		} else {
			b.set("subcontractorId", nil).eq("subcontractorId", sameOwnFleet(ownDoc))
		}
		for _, f := range []struct {
			field string
			v     *string
		}{{"profileImage", d.profile}, {"idCardImage", d.idImg}, {"truckLicenseImage", d.licImg}} {
			b.set(f.field, f.v)
			if f.v != nil {
				b.eq(f.field, sameFile(*f.v))
			}
		}
		h := history[d.id]
		b.set("statusHistory", driverHistoryDoc(h)).eq("statusHistory", sameDriverHistory(h))
		c := codes[d.id]
		b.set("customerDriverIds", c)
		b.d.Merge["customerDriverIds"] = mergeCodes(c, known)
		if d.asgDoc != nil {
			ca := map[string]any{"assignmentId": *d.asgDoc, "truckId": deref(d.asgTruck, ""), "truckPlate": deref(d.asgPlate, ""),
				"truckModel": deref(d.asgModel, "")}
			if d.asgAt != nil {
				ca["assignedAt"] = dump.Timestamp{Time: d.asgAt.UTC()}
			}
			asg, truck := *d.asgDoc, deref(d.asgTruck, "")
			b.set("currentAssignment", ca).eq("currentAssignment", func(cur any, has bool) bool {
				m, ok := cur.(map[string]any)
				return has && ok && m["assignmentId"] == asg && m["truckId"] == truck
			})
		} else {
			// No home truck in PostgreSQL: none, or an assignment that did not load (it stays as Firestore has it).
			b.set("currentAssignment", nil).keepIf("currentAssignment", "assignmentId", unknownTo(assignments), "currentAssignment")
		}
		out = append(out, b.doc())
	}
	return out, nil
}

// driverHistoryDoc is drivers statusHistory[] {status, changedAt, changedBy uid|'system'|label, changedByName?,
// reason?, previousStatus?}.
func driverHistoryDoc(hs []historyRow) []any {
	out := make([]any, 0, len(hs))
	for _, h := range hs {
		m := map[string]any{"status": h.status, "changedAt": dump.Timestamp{Time: h.at.UTC()}, "changedBy": driverChangedBy(h)}
		if h.uid != nil && h.label != nil && *h.label != "" {
			m["changedByName"] = *h.label
		}
		if h.reason != nil && *h.reason != "" {
			m["reason"] = *h.reason
		}
		if h.previous != nil && *h.previous != "" {
			m["previousStatus"] = *h.previous
		}
		out = append(out, m)
	}
	return out
}

func driverChangedBy(h historyRow) string {
	if h.uid != nil && *h.uid != "" {
		return *h.uid
	}
	return deref(h.label, deref(h.by, ""))
}

// sameDriverHistory: the stored entries say the same statuses, instants, reasons and previous statuses (the author's
// spelling is the loader's: a resolved uid, else 'system' or the name).
func sameDriverHistory(hs []historyRow) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		list := loadableEntries(cur, "changedAt")
		if len(list) != len(hs) {
			return false
		}
		for i, h := range hs {
			m := list[i]
			at, ok := toTime(m["changedAt"])
			reason, _ := m["reason"].(string)
			prev, _ := m["previousStatus"].(string)
			if m["status"] != h.status || !ok || !at.Truncate(time.Microsecond).Equal(h.at) ||
				strings.TrimSpace(reason) != deref(h.reason, "") || strings.TrimSpace(prev) != deref(h.previous, "") {
				return false
			}
		}
		return true
	}
}

// driverCodes is customerDriverIds {customerCode: {idTypeKey: value}} of each driver.
func driverCodes(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID]map[string]any, error) {
	rows, err := tx.Query(ctx, `SELECT dc.driver_id, cu.code::text, dc.id_type_key, dc.value FROM driver_customer_codes dc
		JOIN customers cu ON cu.id = dc.customer_id WHERE dc.driver_id = ANY($1) ORDER BY 1, 2, 3`, ids)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var code, key, value string
		if err := rows.Scan(&id, &code, &key, &value); err != nil {
			rows.Close()
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]any{}
		}
		m, _ := out[id][code].(map[string]any)
		if m == nil {
			m = map[string]any{}
			out[id][code] = m
		}
		m[key] = value
	}
	return out, rows.Err()
}

// driverUIDs are the Firebase uids PostgreSQL knows: of every login, and the P1 uids of the drivers.
type driverUIDs struct{ users, drivers map[string]bool }

func readDriverUIDs(ctx context.Context, tx pgx.Tx) (*driverUIDs, error) {
	users, err := stringSet(ctx, tx, `SELECT legacy_auth_uid FROM users WHERE legacy_auth_uid IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	drivers, err := stringSet(ctx, tx, `SELECT legacy_auth_uid FROM drivers WHERE legacy_auth_uid IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	return &driverUIDs{users: users, drivers: drivers}, nil
}

// foldCode is a customerDriverIds key as the loader reads it (customers.code is upper case and trimmed).
func foldCode(k string) string { return strings.ToUpper(strings.TrimSpace(k)) }

// mergeCodes builds customerDriverIds over the stored map (known: every customers.code). Cloud Functions look an
// entry up by the customers document's code exactly (resolveDriverCustomerCode, lineMessage.ts:93-113), and the
// customers projection keeps a legacy lower-case code, so:
//   - an entry whose key folds to a code PostgreSQL knows says what PostgreSQL holds for that customer, under the
//     stored key (its non-blank values PostgreSQL no longer holds go, its blank ones stay as the loader skipped them);
//     an entry whose every value went is removed;
//   - a code PostgreSQL holds and the map lacks is added under the code; an entry of no known code holding exactly
//     those values is the same entry under the customer's former code (changed in Go) and goes;
//   - any other entry (a code no customer has: customer_unresolved at the load) stays as it is.
func mergeCodes(want map[string]any, known map[string]bool) func(cur any) any {
	return func(cur any) any {
		stored, _ := cur.(map[string]any)
		out := make(map[string]any, len(stored)+len(want))
		present := map[string]bool{}
		var unknown []string
		for k, v := range stored {
			f := foldCode(k)
			if !known[f] {
				out[k] = v
				unknown = append(unknown, k)
				continue
			}
			pg, _ := want[f].(map[string]any)
			entry, _ := v.(map[string]any)
			merged := map[string]any{}
			for ik, iv := range entry {
				if s, ok := iv.(string); ok && strings.TrimSpace(s) != "" && strings.TrimSpace(ik) != "" && pg[ik] == nil {
					continue // removed in PostgreSQL
				}
				merged[ik] = iv
			}
			for ik, iv := range pg {
				merged[ik] = iv
			}
			if hasValue(merged) || !hasValue(entry) {
				out[k] = merged
			}
			present[f] = true
		}
		slices.Sort(unknown)
		for _, code := range slices.Sorted(maps.Keys(want)) {
			if present[code] {
				continue
			}
			for i, u := range unknown {
				if u != "" && sameValues(out[u], want[code]) {
					delete(out, u)
					unknown[i] = ""
					break
				}
			}
			out[code] = want[code]
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
}

// hasValue: an entry holds a non-blank string value (what the loader keeps).
func hasValue(m map[string]any) bool {
	for k, v := range m {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" && strings.TrimSpace(k) != "" {
			return true
		}
	}
	return false
}

// sameValues: a stored entry's non-blank values are exactly the values PostgreSQL holds for a customer.
func sameValues(stored, want any) bool {
	s, _ := stored.(map[string]any)
	w, _ := want.(map[string]any)
	n := 0
	for k, v := range s {
		if sv, ok := v.(string); ok && strings.TrimSpace(sv) != "" && strings.TrimSpace(k) != "" {
			if w[k] != sv {
				return false
			}
			n++
		}
	}
	return n > 0 && n == len(w)
}

// --- truck assignments ------------------------------------------------------------------------------------------

// encodeAssignments: truck_assignments -> truckAssignment/{id} (§A.3.4 inverse). The truck plate and model and the
// driver name are snapshots the legacy writer took at assignment time: filled when missing, kept otherwise.
func encodeAssignments(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT a.id, a.legacy_doc_id, a.tenant_source, coalesce(t.legacy_doc_id, t.id::text), t.license_plate, t.model,
		  coalesce(d.legacy_doc_id, d.id::text), d.first_name, d.last_name, a.status, u.legacy_auth_uid, a.admin_label, a.created_at,
		  a.updated_at, a.revoked_at
		FROM truck_assignments a JOIN trucks t ON t.id = a.truck_id JOIN drivers d ON d.id = a.driver_id
		LEFT JOIN users u ON u.id = a.admin_user_id
		WHERE $1 OR a.id = ANY($2) ORDER BY a.id`, sel.all, sel.ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode assignments: %w", err)
	}
	type asg struct {
		id                                          uuid.UUID
		legacy, model, first, last, admin           *string
		source, truck, plate, driver, status, label string
		created, updated                            time.Time
		revoked                                     *time.Time
	}
	var as []asg
	for rows.Next() {
		var a asg
		if err := rows.Scan(&a.id, &a.legacy, &a.source, &a.truck, &a.plate, &a.model, &a.driver, &a.first, &a.last, &a.status, &a.admin,
			&a.label, &a.created, &a.updated, &a.revoked); err != nil {
			rows.Close()
			return nil, err
		}
		as = append(as, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []ExportDoc
	for _, a := range as {
		if quarantinedRow(a.source) {
			out = append(out, newDoc("truckAssignment/"+deref(a.legacy, a.id.String())).skip("in the quarantine tenant").doc())
			continue
		}
		docID, err := mintID(ctx, tx, "truck_assignments", a.id, a.legacy, mint, &a.updated)
		if err != nil {
			return nil, err
		}
		label := a.label
		b := newDoc("truckAssignment/"+docID).
			set("id", docID).eq("id", keepSnapshot).
			set("truckId", a.truck).set("driverId", a.driver).set("status", a.status).eq("status", sameCanon(a.status, "", nil)).
			set("truckPlate", a.plate).eq("truckPlate", keepSnapshot).
			set("truckModel", a.model).eq("truckModel", keepSnapshot).
			set("driverName", driverName(a.first, a.last)).eq("driverName", keepSnapshot).
			set("adminName", label).eq("adminName", func(cur any, has bool) bool {
			s, _ := cur.(string)
			return (has && s == label) || (label == "System" && blank(cur))
		}).
			created(a.created).set("updatedAt", a.updated).set("revokedAt", a.revoked)
		b.uid("adminId", a.admin)
		out = append(out, b.doc())
	}
	return out, nil
}

// --- carrier profiles (subcontractors) --------------------------------------------------------------------------

// encodeCarriers: tenants(kind carrier) + billing party + tenant_files -> subcontractors/{id} (§A.3.2 inverse), and the
// own fleet only when a subcontractors document named it (settings/tenancy): no document id is ever minted for it.
func encodeCarriers(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT t.id, t.legacy_doc_id, t.kind, t.code::text, t.name_th, t.legal_type, t.id_card_number, t.tax_id,
		  t.contact_person, t.phone, t.email::text, t.website, t.address, t.designation, t.fleet_size, t.dispatch_center,
		  t.service_regions, t.vehicle_types, t.line_group_id, t.status, bp.billing_date_basis, t.created_at, t.updated_at
		FROM tenants t LEFT JOIN billing_parties bp ON bp.tenant_id = t.id
		WHERE (t.kind = 'carrier' OR (t.kind = 'own_fleet' AND t.legacy_doc_id IS NOT NULL)) AND ($1 OR t.id = ANY($2)) ORDER BY t.id`,
		sel.all, sel.ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode subcontractors: %w", err)
	}
	type carrier struct {
		id                                                           uuid.UUID
		legacy, code, legal, idCard, tax, contact, phone, email, web *string
		address, designation, dispatch, line, basis                  *string
		kind, name, status                                           string
		fleet                                                        int32
		regions, vehicles                                            []string
		created, updated                                             time.Time
	}
	var cs []carrier
	for rows.Next() {
		var c carrier
		if err := rows.Scan(&c.id, &c.legacy, &c.kind, &c.code, &c.name, &c.legal, &c.idCard, &c.tax, &c.contact, &c.phone, &c.email,
			&c.web, &c.address, &c.designation, &c.fleet, &c.dispatch, &c.regions, &c.vehicles, &c.line, &c.status, &c.basis,
			&c.created, &c.updated); err != nil {
			rows.Close()
			return nil, err
		}
		cs = append(cs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(cs))
	for i, c := range cs {
		ids[i] = c.id
	}
	files, err := entityFiles(ctx, tx, `SELECT tf.tenant_id, tf.kind, f.legacy_url FROM tenant_files tf JOIN file_objects f ON f.id = tf.file_id
		WHERE tf.tenant_id = ANY($1) ORDER BY tf.tenant_id, tf.position, f.id`, ids)
	if err != nil {
		return nil, err
	}
	codes, err := stringSet(ctx, tx, `SELECT code::text FROM tenants WHERE code IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	var out []ExportDoc
	for _, c := range cs {
		docID, err := mintID(ctx, tx, "tenants", c.id, c.legacy, mint && c.kind == "carrier", &c.updated)
		if err != nil {
			return nil, err
		}
		status, basis := c.status, deref(c.basis, "delivered")
		b := newDoc("subcontractors/"+docID).
			set("name", c.name).set("idCardNumber", c.idCard).set("taxId", c.tax).set("contactPerson", c.contact).
			set("phone", c.phone).set("email", c.email).set("website", c.web).set("address", c.address).
			set("designation", c.designation).set("dispatchCenter", c.dispatch).set("lineGroupId", c.line).set("code", c.code).
			set("fleetSize", int64(c.fleet)).eq("fleetSize", func(cur any, has bool) bool {
			f, ok := toFloat(cur)
			return (has && ok && f == float64(c.fleet)) || (c.fleet == 0 && !has)
		}).
			set("serviceRegions", anyStrings(c.regions)).set("vehicleTypes", anyStrings(c.vehicles)).
			set("status", status).eq("status", sameCanon(status, "active", nil)).
			set("billingDateBasis", basis).eq("billingDateBasis", sameCanon(basis, "delivered", nil)).
			created(c.created).set("updatedAt", c.updated)
		if c.legal != nil {
			b.set("type", *c.legal).eq("type", sameCanon(*c.legal, "individual", nil))
		}
		if c.code == nil { // a code another tenant holds (duplicate_natural_key) stays on this document
			b.keepIf("code", "", func(v string) bool { return codes[strings.ToUpper(v)] || codes[v] }, "code")
		}
		var docs []string
		for _, f := range files[c.id] {
			docs = append(docs, f.url)
		}
		b.set("documents", anyStrings(docs)).eq("documents", sameFiles(docs))
		out = append(out, b.doc())
	}
	return out, nil
}

// --- customers --------------------------------------------------------------------------------------------------

// encodeCustomerDocs: customers + billing party + driver id types + the logo -> customers/{id} (§A.3.2 inverse; the
// encoder of export-back with the projection's equivalences and minting).
func encodeCustomerDocs(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT c.id, c.legacy_doc_id, c.code::text, c.name, c.description, f.legacy_url, c.address, c.tax_id,
		  c.branch_type, c.branch_number, c.contact_name, c.contact_phone, c.billing_email::text, c.payment_terms_days, c.invoice_note,
		  bp.billing_date_basis, c.line_group_id, c.created_at, c.updated_at
		FROM customers c LEFT JOIN billing_parties bp ON bp.customer_id = c.id LEFT JOIN file_objects f ON f.id = c.logo_file_id
		WHERE $1 OR c.id = ANY($2) ORDER BY c.id`, sel.all, sel.ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode customers: %w", err)
	}
	type cust struct {
		id                                                                    uuid.UUID
		legacy, desc, logo, addr, tax, branch, branchNo, cname, cphone, email *string
		note, basis, line                                                     *string
		code, name                                                            string
		terms                                                                 *int32
		created, updated                                                      time.Time
	}
	var cs []cust
	for rows.Next() {
		var c cust
		if err := rows.Scan(&c.id, &c.legacy, &c.code, &c.name, &c.desc, &c.logo, &c.addr, &c.tax, &c.branch, &c.branchNo, &c.cname,
			&c.cphone, &c.email, &c.terms, &c.note, &c.basis, &c.line, &c.created, &c.updated); err != nil {
			rows.Close()
			return nil, err
		}
		cs = append(cs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []ExportDoc
	for _, c := range cs {
		docID, err := mintID(ctx, tx, "customers", c.id, c.legacy, mint, &c.updated)
		if err != nil {
			return nil, err
		}
		code, basis := c.code, deref(c.basis, "delivered")
		b := newDoc("customers/"+docID).
			set("code", code).eq("code", func(cur any, has bool) bool {
			s, ok := cur.(string)
			return has && ok && strings.ToUpper(strings.TrimSpace(s)) == code
		}).
			set("name", c.name).set("description", c.desc).set("address", c.addr).set("taxId", c.tax).
			set("branchNumber", c.branchNo).set("contactName", c.cname).set("contactPhone", c.cphone).set("billingEmail", c.email).
			set("paymentTermsDays", c.terms).set("invoiceNote", c.note).set("lineGroupId", c.line).
			set("billingDateBasis", basis).eq("billingDateBasis", sameCanon(basis, "delivered", nil)).
			created(c.created).set("updatedAt", c.updated)
		b.set("logoUrl", c.logo)
		if c.logo != nil {
			b.eq("logoUrl", sameFile(*c.logo))
		}
		if c.branch != nil {
			lit := map[string]string{"hq": "สำนักงานใหญ่", "branch": "สาขา"}[*c.branch]
			branch := *c.branch
			b.set("branchType", lit).eq("branchType", func(cur any, has bool) bool {
				s, _ := cur.(string)
				return has && (s == lit || s == branch)
			})
		} else {
			b.set("branchType", nil)
		}
		types := []any{}
		trows, err := tx.Query(ctx, `SELECT key, label FROM customer_driver_id_types WHERE customer_id = $1 ORDER BY position, key`, c.id)
		if err != nil {
			return nil, err
		}
		for trows.Next() {
			var k, l string
			if err := trows.Scan(&k, &l); err != nil {
				trows.Close()
				return nil, err
			}
			types = append(types, map[string]any{"key": k, "label": l})
		}
		if err := trows.Err(); err != nil {
			return nil, err
		}
		// driverIdTypes is written even when empty (the schema's default).
		b.d.Mask = append(b.d.Mask, "driverIdTypes")
		b.d.Fields["driverIdTypes"] = types
		out = append(out, b.doc())
	}
	return out, nil
}

// --- companies --------------------------------------------------------------------------------------------------

// encodeCompanies: companies + the three files -> companies/{id} (§A.3.2 inverse).
func encodeCompanies(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT c.id, c.legacy_doc_id, c.tenant_source, c.name_th, c.name_en, c.short_name, c.tax_id, c.branch_type,
		  c.branch_number, c.address, c.phone, c.email::text, c.signatory_name, c.bank_name, c.account_number, c.account_name,
		  c.withholding_tax_rate::float8, c.company_type, c.is_active, c.max_trucks, c.max_drivers, lf.legacy_url, sf.legacy_url,
		  gf.legacy_url, c.created_at, c.updated_at
		FROM companies c LEFT JOIN file_objects lf ON lf.id = c.logo_file_id LEFT JOIN file_objects sf ON sf.id = c.stamp_file_id
		LEFT JOIN file_objects gf ON gf.id = c.signature_file_id
		WHERE $1 OR c.id = ANY($2) ORDER BY c.id`, sel.all, sel.ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode companies: %w", err)
	}
	type company struct {
		id                                                             uuid.UUID
		legacy, nameEn, short, branchNo, phone, email, signatory, bank *string
		account, accountName, logo, stamp, signature                   *string
		source, nameTh, tax, branch, address, ctype                    string
		wht                                                            float64
		active                                                         bool
		maxTrucks, maxDrivers                                          *int32
		created, updated                                               time.Time
	}
	var cs []company
	for rows.Next() {
		var c company
		if err := rows.Scan(&c.id, &c.legacy, &c.source, &c.nameTh, &c.nameEn, &c.short, &c.tax, &c.branch, &c.branchNo, &c.address,
			&c.phone, &c.email, &c.signatory, &c.bank, &c.account, &c.accountName, &c.wht, &c.ctype, &c.active, &c.maxTrucks,
			&c.maxDrivers, &c.logo, &c.stamp, &c.signature, &c.created, &c.updated); err != nil {
			rows.Close()
			return nil, err
		}
		cs = append(cs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []ExportDoc
	for _, c := range cs {
		if quarantinedRow(c.source) {
			out = append(out, newDoc("companies/"+deref(c.legacy, c.id.String())).skip("in the quarantine tenant").doc())
			continue
		}
		docID, err := mintID(ctx, tx, "companies", c.id, c.legacy, mint, &c.updated)
		if err != nil {
			return nil, err
		}
		branch, ctype, wht := c.branch, c.ctype, c.wht
		b := newDoc("companies/"+docID).
			set("nameTh", c.nameTh).set("nameEn", c.nameEn).set("shortName", c.short).set("taxId", c.tax).
			set("branchType", branch).eq("branchType", func(cur any, has bool) bool {
			s, _ := cur.(string) // the loader's fold
			switch strings.TrimSpace(s) {
			case "", "headquarters", "hq", "สำนักงานใหญ่":
				return branch == "headquarters"
			case "branch", "สาขา":
				return branch == "branch"
			}
			return false
		}).
			set("branchNumber", c.branchNo).set("address", c.address).set("phone", c.phone).set("email", c.email).
			set("signatoryName", c.signatory).set("bankName", c.bank).set("accountNumber", c.account).set("accountName", c.accountName).
			set("withholdingTaxRate", jsNumber(wht)).eq("withholdingTaxRate", func(cur any, has bool) bool {
			f, ok := toFloat(cur)
			return (has && ok && f == wht) || (!has && wht == 3)
		}).
			set("companyType", ctype).eq("companyType", sameCanon(ctype, "", nil)).
			set("isActive", c.active).eq("isActive", sameBool(c.active, true)).
			set("maxTrucks", c.maxTrucks).set("maxDrivers", c.maxDrivers).
			created(c.created).set("updatedAt", c.updated)
		for _, f := range []struct {
			field string
			v     *string
		}{{"logoUrl", c.logo}, {"stampUrl", c.stamp}, {"signatureUrl", c.signature}} {
			b.set(f.field, f.v)
			if f.v != nil {
				b.eq(f.field, sameFile(*f.v))
			}
		}
		out = append(out, b.doc())
	}
	return out, nil
}
