package billing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/vehicleclass"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// RateEntry is a route rate card (customer_rate_entries): an announcement row, void only (ADR 0009 §1). customerId and
// billingPartyId are both the billing party's id (billing_parties.id); tenantId is the billing carrier that owns it.
type RateEntry struct {
	ID                   uuid.UUID  `json:"id"`
	TenantID             uuid.UUID  `json:"tenantId"`
	BillingPartyID       uuid.UUID  `json:"billingPartyId"`
	CustomerID           uuid.UUID  `json:"customerId"`
	ImportID             string     `json:"importId"`
	HubID                string     `json:"hubId"` // hub_code, the extractHubId form
	RawHubName           *string    `json:"rawHubName"`
	DestinationCode      string     `json:"destinationCode"`
	VehicleClass         string     `json:"vehicleClass"`
	RateTHB              float64    `json:"rateThb"`
	DistanceKm           *float64   `json:"distanceKm"`
	JobCategory          string     `json:"jobCategory"`
	EffectiveFrom        time.Time  `json:"effectiveFrom"`
	EffectiveFromDateStr string     `json:"effectiveFromDateStr"`
	ImportedAt           *time.Time `json:"importedAt"`
	CreatedBy            *uuid.UUID `json:"createdBy"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	Voided               bool       `json:"voided"`
	VoidedAt             *time.Time `json:"voidedAt"`
	VoidedBy             *uuid.UUID `json:"voidedBy"`
	VoidedReason         *string    `json:"voidedReason"`
	LegacyDocID          *string    `json:"legacyDocId,omitempty"`
}

func rateEntryOf(r billingdb.GetRateEntryRow) RateEntry {
	return RateEntry{ID: r.ID, TenantID: r.TenantID, BillingPartyID: r.BillingPartyID, CustomerID: r.BillingPartyID,
		ImportID: r.ImportID, HubID: r.HubCode, RawHubName: r.RawHubName, DestinationCode: r.DestinationCode,
		VehicleClass: r.VehicleClass, RateTHB: r.RateThb, DistanceKm: r.DistanceKm, JobCategory: r.JobCategory,
		EffectiveFrom: r.EffectiveFromAt.UTC(), EffectiveFromDateStr: r.EffectiveFromDate.Format(time.DateOnly),
		ImportedAt: utc(r.ImportedAt), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
		Voided: r.Voided, VoidedAt: utc(r.VoidedAt), VoidedBy: r.VoidedBy, VoidedReason: r.VoidedReason,
		LegacyDocID: r.LegacyDocID}
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// FuelAdjustment is a fuel round (customer_fuel_rate_adjustments): an announcement row, void only. The multiplier and
// add were computed once at creation (main spec §6.6); fuelBand is the band of the reference price, for display.
type FuelAdjustment struct {
	ID                            uuid.UUID  `json:"id"`
	TenantID                      uuid.UUID  `json:"tenantId"`
	BillingPartyID                uuid.UUID  `json:"billingPartyId"`
	CustomerID                    uuid.UUID  `json:"customerId"`
	EffectiveFrom                 time.Time  `json:"effectiveFrom"`
	EffectiveFromDateStr          string     `json:"effectiveFromDateStr"`
	RateMultiplier                float64    `json:"rateMultiplier"`
	AddTHBPerTrip                 float64    `json:"addThbPerTrip"`
	ReferenceFuelPriceTHBPerLitre *float64   `json:"referenceFuelPriceThbPerLitre"`
	FuelBand                      *FuelBand  `json:"fuelBand"`
	AnnouncementNote              string     `json:"announcementNote"`
	FuelBandEnabled               bool       `json:"fuelBandEnabled"`
	FuelBandBaselineFuelFloor     *int32     `json:"fuelBandBaselineFuelFloor"`
	FuelBandTHBPerBaht            *float64   `json:"fuelBandThbPerBaht"`
	CreatedBy                     *uuid.UUID `json:"createdBy"`
	CreatedAt                     time.Time  `json:"createdAt"`
	UpdatedAt                     time.Time  `json:"updatedAt"`
	Voided                        bool       `json:"voided"`
	VoidedAt                      *time.Time `json:"voidedAt"`
	VoidedBy                      *uuid.UUID `json:"voidedBy"`
	VoidedReason                  *string    `json:"voidedReason"`
	LegacyDocID                   *string    `json:"legacyDocId,omitempty"`
}

// FuelBand is the (lower, upper] band of a reference price (compute.FuelBandRange, ADR 0009 §3).
type FuelBand struct {
	LowerTHB float64 `json:"lowerThb"`
	UpperTHB float64 `json:"upperThb"`
}

func fuelAdjustmentOf(r billingdb.GetFuelAdjustmentRow) FuelAdjustment {
	a := FuelAdjustment{ID: r.ID, TenantID: r.TenantID, BillingPartyID: r.BillingPartyID, CustomerID: r.BillingPartyID,
		EffectiveFrom: r.EffectiveFromAt.UTC(), EffectiveFromDateStr: r.EffectiveFromDate.Format(time.DateOnly),
		RateMultiplier: r.RateMultiplier, AddTHBPerTrip: r.AddThbPerTrip, ReferenceFuelPriceTHBPerLitre: r.ReferenceFuelPriceThb,
		AnnouncementNote: r.AnnouncementNote, FuelBandEnabled: r.FuelBandEnabled, FuelBandBaselineFuelFloor: r.FuelBandBaselineFloor,
		FuelBandTHBPerBaht: r.FuelBandThbPerBaht, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(), Voided: r.Voided, VoidedAt: utc(r.VoidedAt), VoidedBy: r.VoidedBy,
		VoidedReason: r.VoidedReason, LegacyDocID: r.LegacyDocID}
	if r.ReferenceFuelPriceThb != nil {
		if lo, hi, ok := compute.FuelBandRange(*r.ReferenceFuelPriceThb); ok {
			a.FuelBand = &FuelBand{LowerTHB: lo, UpperTHB: hi}
		}
	}
	return a
}

// ServiceFee is a customer_service_fees row (mutable, deletable; one per party and fee type after D5).
type ServiceFee struct {
	ID             uuid.UUID `json:"id"`
	TenantID       uuid.UUID `json:"tenantId"`
	BillingPartyID uuid.UUID `json:"billingPartyId"`
	CustomerID     uuid.UUID `json:"customerId"`
	FeeType        string    `json:"feeType"`
	CustomTypeName *string   `json:"customTypeName"`
	AmountTHB      float64   `json:"amountThb"`
	Unit           string    `json:"unit"`
	Note           *string   `json:"note"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	LegacyDocID    *string   `json:"legacyDocId,omitempty"`
}

func serviceFeeOf(r billingdb.GetServiceFeeRow) ServiceFee {
	return ServiceFee{ID: r.ID, TenantID: r.TenantID, BillingPartyID: r.BillingPartyID, CustomerID: r.BillingPartyID,
		FeeType: r.FeeType, CustomTypeName: r.CustomTypeName, AmountTHB: r.AmountThb, Unit: r.Unit, Note: r.Note,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), LegacyDocID: r.LegacyDocID}
}

// StandbyRate is a standby_rate_entries row: a fixed price per standby event, mutable, soft-deleted by a void (R20).
type StandbyRate struct {
	ID                   uuid.UUID  `json:"id"`
	TenantID             uuid.UUID  `json:"tenantId"`
	BillingPartyID       uuid.UUID  `json:"billingPartyId"`
	CustomerID           uuid.UUID  `json:"customerId"`
	RateTHB              float64    `json:"rateThb"`
	EffectiveFrom        time.Time  `json:"effectiveFrom"`
	EffectiveFromDateStr string     `json:"effectiveFromDateStr"`
	Note                 *string    `json:"note"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	Voided               bool       `json:"voided"`
	VoidedAt             *time.Time `json:"voidedAt"`
	VoidedBy             *uuid.UUID `json:"voidedBy"`
	VoidedReason         *string    `json:"voidedReason"`
	LegacyDocID          *string    `json:"legacyDocId,omitempty"`
}

func standbyRateOf(r billingdb.GetStandbyRateRow) StandbyRate {
	return StandbyRate{ID: r.ID, TenantID: r.TenantID, BillingPartyID: r.BillingPartyID, CustomerID: r.BillingPartyID,
		RateTHB: r.RateThb, EffectiveFrom: r.EffectiveFromAt.UTC(), EffectiveFromDateStr: r.EffectiveFromDate.Format(time.DateOnly),
		Note: r.Note, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Voided: r.VoidedAt != nil,
		VoidedAt: utc(r.VoidedAt), VoidedBy: r.VoidedBy, VoidedReason: r.VoidedReason, LegacyDocID: r.LegacyDocID}
}

// --- the rate-card bundle (cache:ratecard:{partyId}, UI only) ------------------------------------------

// bundle is one party's four rate tables, every tenant and voided rows too, in list order.
type bundle struct {
	Entries []RateEntry      `json:"entries"`
	Fuel    []FuelAdjustment `json:"fuel"`
	Fees    []ServiceFee     `json:"fees"`
	Standby []StandbyRate    `json:"standby"`
}

// partyBundle reads the bundle through cache:ratecard:{partyId} (CACHE_TTL_RATECARD; dropped by ratecard.changed).
// The rate-card page reads it; no money path ever does (R17).
func (s *Service) partyBundle(ctx context.Context, party uuid.UUID) (bundle, error) {
	load := func(ctx context.Context) (bundle, error) {
		var b bundle
		err := readShared(ctx, s.pool, func(q *billingdb.Queries) error {
			entries, err := q.PartyRateEntries(ctx, party)
			if err != nil {
				return err
			}
			fuel, err := q.PartyFuelAdjustments(ctx, party)
			if err != nil {
				return err
			}
			fees, err := q.PartyServiceFees(ctx, party)
			if err != nil {
				return err
			}
			standby, err := q.PartyStandbyRates(ctx, party)
			if err != nil {
				return err
			}
			b = bundle{Entries: make([]RateEntry, len(entries)), Fuel: make([]FuelAdjustment, len(fuel)),
				Fees: make([]ServiceFee, len(fees)), Standby: make([]StandbyRate, len(standby))}
			for i, r := range entries {
				b.Entries[i] = rateEntryOf(billingdb.GetRateEntryRow(r))
			}
			for i, r := range fuel {
				b.Fuel[i] = fuelAdjustmentOf(billingdb.GetFuelAdjustmentRow(r))
			}
			for i, r := range fees {
				b.Fees[i] = serviceFeeOf(billingdb.GetServiceFeeRow(r))
			}
			for i, r := range standby {
				b.Standby[i] = standbyRateOf(billingdb.GetStandbyRateRow(r))
			}
			return nil
		})
		return b, err
	}
	if s.cache == nil {
		return load(ctx)
	}
	return cache.GetJSON(ctx, s.cache, s.cache.Keys().RateCard(party.String()), s.cache.TTLs().RateCard, load)
}

// resolveParty finds the billing party a request names (billing_parties.id, or the customer or carrier it bills) under
// the caller's RLS; ok is false when there is none.
func resolveParty(ctx context.Context, q *billingdb.Queries, id uuid.UUID) (billingdb.ResolvePartyRow, bool, error) {
	r, err := q.ResolveParty(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// listParty resolves ?customerId for a list: (party, true) or (uuid.Nil, false) when the id names no party (an empty
// list).
func (s *Service) listParty(ctx context.Context, p *authz.Principal, raw string) (uuid.UUID, bool, error) {
	var ck checker
	id := ck.party("customerId", raw)
	if err := ck.err(); err != nil {
		return uuid.Nil, false, err
	}
	var party uuid.UUID
	found := false
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		r, ok, err := resolveParty(ctx, q, id)
		party, found = r.ID, ok
		return err
	})
	return party, found, err
}

// writeParty resolves the billing party of a write: 422 customerId not_found when there is none.
func writeParty(ctx context.Context, q *billingdb.Queries, id uuid.UUID) (billingdb.ResolvePartyRow, error) {
	r, ok, err := resolveParty(ctx, q, id)
	if err != nil {
		return r, err
	}
	if !ok {
		return r, httpx.ErrInvalidArgument(violation("customerId", "not_found"))
	}
	return r, nil
}

// ListInput are the query parameters of the rate-table lists.
type ListInput struct {
	CustomerID    string
	IncludeVoided bool
	Limit         int
	Cursor        string
}

// --- rate entries -----------------------------------------------------------------------------------------

// rateEntryBefore reports whether e sorts after the cursor position in the list order (effective_from_at, import_id by
// bytes, id) DESC: the rows of the next page.
func rateEntryAfter(e RateEntry, c cursor) bool {
	if !e.EffectiveFrom.Equal(*c.T) {
		return e.EffectiveFrom.Before(*c.T)
	}
	if e.ImportID != *c.S {
		return e.ImportID < *c.S
	}
	return bytes.Compare(e.ID[:], c.ID[:]) < 0
}

// ListRateEntries is GET /v1/billing/rate-entries (accounting:view_rate_card): newest effective date first, then
// import id, keyset paged; with ?customerId served from the party's bundle (cache:ratecard:{partyId}), without it from
// PostgreSQL. Voided rows only with ?includeVoided=true. Rows outside the caller's tenant reach never appear.
func (s *Service) ListRateEntries(ctx context.Context, p *authz.Principal, in ListInput) ([]RateEntry, string, error) {
	var ck checker
	limit := ck.limitOf(in.Limit, DefaultLimit)
	var after cursor
	if in.Cursor != "" {
		var ok bool
		if after, ok = decodeCursor(in.Cursor); !ok || after.T == nil || after.S == nil || after.ID == uuid.Nil {
			ck.add("cursor", "invalid")
		}
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	all, tenants := p.TenantReach()
	var out []RateEntry
	if strings.TrimSpace(in.CustomerID) != "" {
		party, found, err := s.listParty(ctx, p, in.CustomerID)
		if err != nil || !found {
			return []RateEntry{}, "", err
		}
		b, err := s.partyBundle(ctx, party)
		if err != nil {
			return nil, "", err
		}
		for _, e := range b.Entries {
			if !inReach(all, tenants, e.TenantID) || (e.Voided && !in.IncludeVoided) || (after.T != nil && !rateEntryAfter(e, after)) {
				continue
			}
			out = append(out, e)
			if len(out) > limit {
				break
			}
		}
	} else {
		err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
			rows, err := q.ListRateEntries(ctx, billingdb.ListRateEntriesParams{AllTenants: all, TenantIds: tenants,
				IncludeVoided: in.IncludeVoided, AfterAt: after.T, AfterImport: deref(after.S), AfterID: after.ID,
				RowLimit: int32(limit) + 1})
			for _, r := range rows {
				out = append(out, rateEntryOf(billingdb.GetRateEntryRow(r)))
			}
			return err
		})
		if err != nil {
			return nil, "", err
		}
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		t, s := last.EffectiveFrom, last.ImportID
		next = cursor{T: &t, S: &s, ID: last.ID}.encode()
	}
	if out == nil {
		out = []RateEntry{}
	}
	return out, next, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// RateRowInput is one row of an import, or the body of a manual add (CustomerRateEntryInput of the web).
type RateRowInput struct {
	HubID           string   `json:"hubId"`
	RawHubName      string   `json:"rawHubName"`
	DestinationCode string   `json:"destinationCode"`
	VehicleClass    string   `json:"vehicleClass"`
	RateTHB         *float64 `json:"rateThb"`
	DistanceKm      *float64 `json:"distanceKm"`
	JobCategory     string   `json:"jobCategory"` // a cell: blank = PRIMARY, หลัก/เสริม/primary/supplementary..., else refused
}

// ImportInput is the body of POST /v1/billing/rate-entries/import.
type ImportInput struct {
	CustomerID           string         `json:"customerId"`
	EffectiveFromDateStr string         `json:"effectiveFromDateStr"`
	Rows                 []RateRowInput `json:"rows"`
}

// CreateRateEntryInput is the body of POST /v1/billing/rate-entries (one manual row).
type CreateRateEntryInput struct {
	CustomerID           string `json:"customerId"`
	EffectiveFromDateStr string `json:"effectiveFromDateStr"`
	RateRowInput
}

// ImportResult is the answer of an import.
type ImportResult struct {
	ImportID string `json:"importId"`
	Count    int    `json:"count"`
}

// rateRows are the normalised columns of the rows of one write.
type rateRows struct {
	hubs, raw, dests, classes, rates, distances, cats []string
}

// rateRow normalises one row as the web wrote it (billing.ts:131-209, main spec §6.12): hub_code = ExtractHubID,
// destination_code = NormalizeDestinationCode (the SPK dash collapse kept, §6.18 #1), vehicle_class through the one
// mapper (a blank or unknown class is refused, R15, never the legacy 4WJ default), job_category from the import cell
// (an unrecognised cell rejects the row, never PRIMARY), the rate at most two decimals. prefix names the fields.
func (ck *checker) rateRow(prefix string, r RateRowInput, manual bool, out *rateRows) {
	hub := compute.ExtractHubID(r.HubID)
	switch {
	case hub == "":
		ck.add(prefix+"hubId", "required")
	case len([]rune(hub)) > 100:
		ck.addParams(prefix+"hubId", "too_long", map[string]any{"max": 100})
	}
	raw := ck.text(prefix+"rawHubName", r.RawHubName, 200, false)
	if raw == "" && manual {
		raw = hub // createCustomerRateEntry: rawHubName.trim() || hubId
	}
	dest := compute.NormalizeDestinationCode(r.DestinationCode)
	switch {
	case dest == "":
		ck.add(prefix+"destinationCode", "required")
	case len([]rune(dest)) > 100:
		ck.addParams(prefix+"destinationCode", "too_long", map[string]any{"max": 100})
	}
	class := ""
	if c := vehicleclass.Of(r.VehicleClass); c != nil {
		class = *c
	} else if strings.TrimSpace(r.VehicleClass) == "" {
		ck.add(prefix+"vehicleClass", "required")
	} else {
		ck.addParams(prefix+"vehicleClass", "invalid", map[string]any{"allowed": vehicleclass.Classes})
	}
	rate, _ := ck.money(prefix+"rateThb", r.RateTHB, 0, true)
	distance := ""
	if r.DistanceKm != nil {
		if d, ok := ck.money(prefix+"distanceKm", r.DistanceKm, 0, false); ok {
			if d >= 1e7 {
				ck.addParams(prefix+"distanceKm", "out_of_range", map[string]any{"max": 1e7})
			}
			distance = decimalText(d, 2)
		}
	}
	cat, ok := compute.JobCategoryFromCell(r.JobCategory)
	if !ok {
		ck.addParams(prefix+"jobCategory", "invalid", map[string]any{"allowed": []string{"PRIMARY", "SUPPLEMENTARY"}})
	}
	out.hubs = append(out.hubs, hub)
	out.raw = append(out.raw, raw)
	out.dests = append(out.dests, dest)
	out.classes = append(out.classes, class)
	out.rates = append(out.rates, decimalText(rate, 2))
	out.distances = append(out.distances, distance)
	out.cats = append(out.cats, string(cat))
}

// ImportRateEntries is POST /v1/billing/rate-entries/import (accounting:edit_rate_card): every row of the file under
// one import id rc_<ms>, all or nothing (a row that breaks a rule is a 422 naming rows[i].field), the billing tenant
// the caller's; outbox ratecard.changed.
func (s *Service) ImportRateEntries(ctx context.Context, p *authz.Principal, rid string, in ImportInput) (ImportResult, error) {
	var ck checker
	partyID := ck.party("customerId", in.CustomerID)
	day := ck.date("effectiveFromDateStr", in.EffectiveFromDateStr)
	var rows rateRows
	switch {
	case len(in.Rows) == 0:
		ck.add("rows", "required")
	case len(in.Rows) > MaxImportRows:
		ck.addParams("rows", "too_many", map[string]any{"max": MaxImportRows})
	default:
		for i, r := range in.Rows {
			ck.rateRow(fmt.Sprintf("rows[%d].", i), r, false, &rows)
			if len(ck.bad) >= 100 {
				break // enough to fix the file
			}
		}
	}
	if err := ck.err(); err != nil {
		return ImportResult{}, err
	}
	tenant, err := tenantOf(p)
	if err != nil {
		return ImportResult{}, err
	}
	now := s.now().UTC()
	importID := fmt.Sprintf("rc_%d", now.UnixMilli())
	var party uuid.UUID
	count := 0
	err = s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		pr, err := writeParty(ctx, q, partyID)
		if err != nil {
			return err
		}
		party = pr.ID
		ids, err := q.InsertRateEntries(ctx, billingdb.InsertRateEntriesParams{TenantID: tenant, PartyID: party,
			ImportID: importID, HubCodes: rows.hubs, RawHubNames: rows.raw, DestinationCodes: rows.dests,
			VehicleClasses: rows.classes, Rates: rows.rates, Distances: rows.distances, JobCategories: rows.cats,
			EffectiveFromDate: day, ImportedAt: now, CreatedBy: actor(p)})
		if err != nil {
			return err
		}
		count = len(ids)
		return rateCardChanged(ctx, tx, tenant, party, TableEntries, rid)
	})
	if err != nil {
		return ImportResult{}, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, party)
	return ImportResult{ImportID: importID, Count: count}, nil
}

// CreateRateEntry is POST /v1/billing/rate-entries (accounting:edit_rate_card): one manual row under import id
// manual_<ms>.
func (s *Service) CreateRateEntry(ctx context.Context, p *authz.Principal, rid string, in CreateRateEntryInput) (*RateEntry, error) {
	var ck checker
	partyID := ck.party("customerId", in.CustomerID)
	day := ck.date("effectiveFromDateStr", in.EffectiveFromDateStr)
	var rows rateRows
	ck.rateRow("", in.RateRowInput, true, &rows)
	if err := ck.err(); err != nil {
		return nil, err
	}
	tenant, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	importID := fmt.Sprintf("manual_%d", now.UnixMilli())
	var out RateEntry
	err = s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		pr, err := writeParty(ctx, q, partyID)
		if err != nil {
			return err
		}
		ids, err := q.InsertRateEntries(ctx, billingdb.InsertRateEntriesParams{TenantID: tenant, PartyID: pr.ID,
			ImportID: importID, HubCodes: rows.hubs, RawHubNames: rows.raw, DestinationCodes: rows.dests,
			VehicleClasses: rows.classes, Rates: rows.rates, Distances: rows.distances, JobCategories: rows.cats,
			EffectiveFromDate: day, ImportedAt: now, CreatedBy: actor(p)})
		if err != nil {
			return err
		}
		if len(ids) != 1 {
			return fmt.Errorf("billing: a manual add wrote %d rows", len(ids))
		}
		row, err := q.GetRateEntry(ctx, ids[0])
		if err != nil {
			return err
		}
		out = rateEntryOf(row)
		return rateCardChanged(ctx, tx, tenant, pr.ID, TableEntries, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, out.BillingPartyID)
	return &out, nil
}

// VoidInput is the body of the void routes.
type VoidInput struct {
	Reason string `json:"reason"`
}

// voidReason checks the reason of a void (required: "A reason is required to void an announcement").
func voidReason(in VoidInput) (string, error) {
	var ck checker
	r := ck.text("reason", in.Reason, 500, true)
	return r, ck.err()
}

// VoidRateEntry is POST /v1/billing/rate-entries/{id}/void (accounting:edit_rate_card): the only change an
// announcement row takes (trg_announcement_void_only); a voided row is 409 announcement_immutable (already_voided).
func (s *Service) VoidRateEntry(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in VoidInput) (*RateEntry, error) {
	reason, err := voidReason(in)
	if err != nil {
		return nil, err
	}
	by := actor(p)
	if by == nil {
		return nil, authz.ErrPermissionDenied("a void needs a user")
	}
	var out RateEntry
	err = s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		cur, err := q.LockRateEntry(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if cur.Voided {
			return errAnnouncement("already_voided")
		}
		if err := q.VoidRateEntry(ctx, billingdb.VoidRateEntryParams{ID: id, VoidedAt: ptr(s.now().UTC()), VoidedBy: by,
			Reason: &reason}); err != nil {
			return err
		}
		row, err := q.GetRateEntry(ctx, id)
		if err != nil {
			return err
		}
		out = rateEntryOf(row)
		return rateCardChanged(ctx, tx, cur.TenantID, cur.BillingPartyID, TableEntries, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, out.BillingPartyID)
	return &out, nil
}

// --- fuel adjustments ---------------------------------------------------------------------------------------

// Fuel adjustment modes of POST /v1/billing/fuel-adjustments (main spec §6.6).
const (
	FuelModePercent = "percent"
	FuelModeFixed   = "fixed_thb"
	FuelModeBand    = "band"
)

// Defaults of the band mode (parseInt(...) || 41, parseFloat(...) || 10 of the rate-card page).
const (
	DefaultBandBaselineFloor = 41
	DefaultBandTHBPerBaht    = 10
)

// FuelAdjustmentInput is the body of POST /v1/billing/fuel-adjustments.
type FuelAdjustmentInput struct {
	CustomerID                    string   `json:"customerId"`
	EffectiveFromDateStr          string   `json:"effectiveFromDateStr"`
	Mode                          string   `json:"mode"`
	Pct                           *float64 `json:"pct"`
	AddTHBPerTrip                 *float64 `json:"addThbPerTrip"`
	ReferenceFuelPriceTHBPerLitre *float64 `json:"referenceFuelPriceThbPerLitre"`
	FuelBandBaselineFuelFloor     *float64 `json:"fuelBandBaselineFuelFloor"`
	FuelBandTHBPerBaht            *float64 `json:"fuelBandThbPerBaht"`
	AnnouncementNote              string   `json:"announcementNote"`
}

// fuelValues are the stored columns of a fuel round.
type fuelValues struct {
	mult, add         float64
	reference         string
	bandEnabled       bool
	baseline          *int32
	perBaht           string
	multText, addText string
}

// fuelRound computes the stored multiplier and add of a request, once (handleCreateAdjustment of the rate-card page,
// rate-card/page.tsx:659-711): percent -> mult = 1 + pct/100, add 0; fixed_thb -> mult 1, add as typed; band -> mult 1,
// add = FuelSurchargeTHB(reference, baseline || 41, perBaht || 10), signed and never clamped. The multiplier must be
// finite and above 0; pct has at most four decimals so the multiplier fits NUMERIC(10,6) exactly.
func (ck *checker) fuelRound(in FuelAdjustmentInput) fuelValues {
	v := fuelValues{mult: 1}
	ref, hasRef := 0.0, false
	if in.ReferenceFuelPriceTHBPerLitre != nil {
		if x, ok := ck.money("referenceFuelPriceThbPerLitre", in.ReferenceFuelPriceTHBPerLitre, 0, false); ok {
			if x >= 1e6 {
				ck.addParams("referenceFuelPriceThbPerLitre", "out_of_range", map[string]any{"max": 1e6})
			} else {
				ref, hasRef = x, true
				v.reference = decimalText(x, 2)
			}
		}
	}
	switch in.Mode {
	case FuelModePercent:
		if in.Pct == nil {
			ck.add("pct", "required")
			break
		}
		pct := *in.Pct
		switch {
		case pct != pct || pct <= -100 || pct >= 100000:
			ck.addParams("pct", "out_of_range", map[string]any{"min": -100, "max": 100000})
		case !decimals(pct, 4):
			ck.addParams("pct", "precision", map[string]any{"decimals": 4})
		default:
			v.mult = 1 + pct/100
		}
	case FuelModeFixed:
		if x, ok := ck.money("addThbPerTrip", in.AddTHBPerTrip, -1e12, true); ok {
			v.add = x
		}
	case FuelModeBand:
		if !hasRef && in.ReferenceFuelPriceTHBPerLitre == nil {
			ck.add("referenceFuelPriceThbPerLitre", "required")
		}
		baseline := float64(DefaultBandBaselineFloor)
		if b := in.FuelBandBaselineFuelFloor; b != nil && *b != 0 {
			if *b != float64(int32(*b)) || *b < 0 || *b > 1000 {
				ck.add("fuelBandBaselineFuelFloor", "invalid")
			} else {
				baseline = *b
			}
		}
		perBaht := float64(DefaultBandTHBPerBaht)
		if x := in.FuelBandTHBPerBaht; x != nil && *x != 0 {
			if got, ok := ck.money("fuelBandThbPerBaht", x, 0, true); ok {
				if got >= 1e8 {
					ck.addParams("fuelBandThbPerBaht", "out_of_range", map[string]any{"max": 1e8})
				}
				perBaht = got
			}
		}
		if hasRef {
			if v.add = compute.FuelSurchargeTHB(ref, baseline, perBaht); math.Abs(v.add) >= 1e12 {
				ck.addParams("fuelBandThbPerBaht", "out_of_range", map[string]any{"max": 1e8})
			}
		}
		b := int32(baseline)
		v.bandEnabled, v.baseline, v.perBaht = true, &b, decimalText(perBaht, 2)
	case "":
		ck.add("mode", "required")
	default:
		ck.addParams("mode", "invalid", map[string]any{"allowed": []string{FuelModePercent, FuelModeFixed, FuelModeBand}})
	}
	v.multText, v.addText = decimalText(v.mult, 6), decimalText(v.add, 2)
	return v
}

// ListFuelAdjustments is GET /v1/billing/fuel-adjustments (accounting:view_rate_card): newest round first, every
// round of the party (or of the caller's reach without ?customerId); voided rounds only with ?includeVoided=true.
func (s *Service) ListFuelAdjustments(ctx context.Context, p *authz.Principal, in ListInput) ([]FuelAdjustment, error) {
	all, tenants := p.TenantReach()
	out := []FuelAdjustment{}
	if strings.TrimSpace(in.CustomerID) != "" {
		party, found, err := s.listParty(ctx, p, in.CustomerID)
		if err != nil || !found {
			return out, err
		}
		b, err := s.partyBundle(ctx, party)
		if err != nil {
			return nil, err
		}
		for _, a := range b.Fuel {
			if inReach(all, tenants, a.TenantID) && (in.IncludeVoided || !a.Voided) {
				out = append(out, a)
			}
		}
		return out, nil
	}
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		rows, err := q.ListFuelAdjustments(ctx, billingdb.ListFuelAdjustmentsParams{AllTenants: all, TenantIds: tenants,
			IncludeVoided: in.IncludeVoided})
		for _, r := range rows {
			out = append(out, fuelAdjustmentOf(billingdb.GetFuelAdjustmentRow(r)))
		}
		return err
	})
	return out, err
}

// CreateFuelAdjustment is POST /v1/billing/fuel-adjustments (accounting:edit_rate_card): always a new row
// (announcements are immutable, ADR 0009 §1), effective from Bangkok midnight of the day.
func (s *Service) CreateFuelAdjustment(ctx context.Context, p *authz.Principal, rid string, in FuelAdjustmentInput) (*FuelAdjustment, error) {
	var ck checker
	partyID := ck.party("customerId", in.CustomerID)
	day := ck.date("effectiveFromDateStr", in.EffectiveFromDateStr)
	note := ck.text("announcementNote", in.AnnouncementNote, 1000, false)
	v := ck.fuelRound(in)
	if err := ck.err(); err != nil {
		return nil, err
	}
	tenant, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	var out FuelAdjustment
	err = s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		pr, err := writeParty(ctx, q, partyID)
		if err != nil {
			return err
		}
		id, err := q.InsertFuelAdjustment(ctx, billingdb.InsertFuelAdjustmentParams{TenantID: tenant, PartyID: pr.ID,
			EffectiveFromDate: day, RateMultiplier: v.multText, AddThbPerTrip: v.addText, ReferenceFuelPrice: v.reference,
			AnnouncementNote: note, FuelBandEnabled: v.bandEnabled, FuelBandBaselineFloor: v.baseline,
			FuelBandThbPerBaht: v.perBaht, CreatedBy: actor(p)})
		if err != nil {
			return err
		}
		row, err := q.GetFuelAdjustment(ctx, id)
		if err != nil {
			return err
		}
		out = fuelAdjustmentOf(row)
		return rateCardChanged(ctx, tx, tenant, pr.ID, TableFuelAdj, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, out.BillingPartyID)
	return &out, nil
}

// VoidFuelAdjustment is POST /v1/billing/fuel-adjustments/{id}/void (accounting:edit_rate_card).
func (s *Service) VoidFuelAdjustment(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in VoidInput) (*FuelAdjustment, error) {
	reason, err := voidReason(in)
	if err != nil {
		return nil, err
	}
	by := actor(p)
	if by == nil {
		return nil, authz.ErrPermissionDenied("a void needs a user")
	}
	var out FuelAdjustment
	err = s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		cur, err := q.LockFuelAdjustment(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if cur.Voided {
			return errAnnouncement("already_voided")
		}
		if err := q.VoidFuelAdjustment(ctx, billingdb.VoidFuelAdjustmentParams{ID: id, VoidedAt: ptr(s.now().UTC()), VoidedBy: by,
			Reason: &reason}); err != nil {
			return err
		}
		row, err := q.GetFuelAdjustment(ctx, id)
		if err != nil {
			return err
		}
		out = fuelAdjustmentOf(row)
		return rateCardChanged(ctx, tx, cur.TenantID, cur.BillingPartyID, TableFuelAdj, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, out.BillingPartyID)
	return &out, nil
}

// --- service fees -------------------------------------------------------------------------------------------

// Service fee types and units (customer_service_fees CHECKs; ServiceFeeType of the web).
var (
	feeTypes = []string{"extra_stop", "waiting_time", "special_handling", "service_charge", "standby", "custom"}
	feeUnits = []string{"per_trip", "per_stop"}
)

// ServiceFeeInput is the body of POST /v1/billing/service-fees and PATCH /v1/billing/service-fees/{id} (on PATCH
// absent fields stay, null clears customTypeName or note).
type ServiceFeeInput struct {
	CustomerID     string         `json:"customerId"`
	FeeType        Field[string]  `json:"feeType"`
	CustomTypeName Field[string]  `json:"customTypeName"`
	AmountTHB      Field[float64] `json:"amountThb"`
	Unit           Field[string]  `json:"unit"`
	Note           Field[string]  `json:"note"`
}

// feeValues are the stored columns of a fee.
type feeValues struct {
	feeType, unit, amount string
	custom, note          *string
}

// fee validates the merged columns of a fee (createCustomerServiceFee / updateCustomerServiceFee): amount >= 0, a
// custom fee names its type, other types carry no custom name.
func (ck *checker) fee(feeType, unit string, amount *float64, custom, note *string) feeValues {
	v := feeValues{feeType: feeType, unit: unit}
	if !slices.Contains(feeTypes, feeType) {
		if feeType == "" {
			ck.add("feeType", "required")
		} else {
			ck.addParams("feeType", "invalid", map[string]any{"allowed": feeTypes})
		}
	}
	if !slices.Contains(feeUnits, unit) {
		if unit == "" {
			ck.add("unit", "required")
		} else {
			ck.addParams("unit", "invalid", map[string]any{"allowed": feeUnits})
		}
	}
	if a, ok := ck.money("amountThb", amount, 0, true); ok {
		v.amount = decimalText(a, 2)
	}
	if feeType == "custom" {
		if custom == nil || strings.TrimSpace(*custom) == "" {
			ck.add("customTypeName", "required")
		} else {
			c := ck.text("customTypeName", *custom, 100, true)
			v.custom = &c
		}
	}
	if note != nil {
		if n := ck.text("note", *note, 1000, false); n != "" {
			v.note = &n
		}
	}
	return v
}

// ListServiceFees is GET /v1/billing/service-fees (accounting:view_rate_card): newest first.
func (s *Service) ListServiceFees(ctx context.Context, p *authz.Principal, in ListInput) ([]ServiceFee, error) {
	all, tenants := p.TenantReach()
	out := []ServiceFee{}
	if strings.TrimSpace(in.CustomerID) != "" {
		party, found, err := s.listParty(ctx, p, in.CustomerID)
		if err != nil || !found {
			return out, err
		}
		b, err := s.partyBundle(ctx, party)
		if err != nil {
			return nil, err
		}
		for _, f := range b.Fees {
			if inReach(all, tenants, f.TenantID) {
				out = append(out, f)
			}
		}
		return out, nil
	}
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		rows, err := q.ListServiceFees(ctx, billingdb.ListServiceFeesParams{AllTenants: all, TenantIds: tenants})
		for _, r := range rows {
			out = append(out, serviceFeeOf(billingdb.GetServiceFeeRow(r)))
		}
		return err
	})
	return out, err
}

// CreateServiceFee is POST /v1/billing/service-fees (accounting:edit_rate_card): one fee per party and type (custom
// excepted; customer_service_fees_one_per_type, D5): a second is 409 already_exists (field feeType).
func (s *Service) CreateServiceFee(ctx context.Context, p *authz.Principal, rid string, in ServiceFeeInput) (*ServiceFee, error) {
	var ck checker
	partyID := ck.party("customerId", in.CustomerID)
	var amount *float64
	if in.AmountTHB.Set && !in.AmountTHB.Null {
		amount = &in.AmountTHB.V
	}
	v := ck.fee(strings.TrimSpace(in.FeeType.V), strings.TrimSpace(in.Unit.V), amount, fieldText(in.CustomTypeName),
		fieldText(in.Note))
	if err := ck.err(); err != nil {
		return nil, err
	}
	tenant, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	var out ServiceFee
	err = s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		pr, err := writeParty(ctx, q, partyID)
		if err != nil {
			return err
		}
		id, err := q.InsertServiceFee(ctx, billingdb.InsertServiceFeeParams{TenantID: tenant, PartyID: pr.ID,
			FeeType: v.feeType, CustomTypeName: v.custom, AmountThb: v.amount, Unit: v.unit, Note: v.note})
		if err != nil {
			return err
		}
		row, err := q.GetServiceFee(ctx, id)
		if err != nil {
			return err
		}
		out = serviceFeeOf(row)
		return rateCardChanged(ctx, tx, tenant, pr.ID, TableServiceFees, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, out.BillingPartyID)
	return &out, nil
}

// PatchServiceFee is PATCH /v1/billing/service-fees/{id} (accounting:edit_rate_card): a partial update; priced
// snapshots keep their amounts. The party of a fee does not change.
func (s *Service) PatchServiceFee(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in ServiceFeeInput) (*ServiceFee, error) {
	if strings.TrimSpace(in.CustomerID) != "" {
		return nil, httpx.ErrInvalidArgument(violation("customerId", "immutable"))
	}
	var out ServiceFee
	err := s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		cur, err := q.LockServiceFee(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		feeType, unit, amount := cur.FeeType, cur.Unit, cur.AmountThb
		custom, note := cur.CustomTypeName, cur.Note
		var ck checker
		if in.FeeType.Set {
			feeType = strings.TrimSpace(in.FeeType.V)
		}
		if in.Unit.Set {
			unit = strings.TrimSpace(in.Unit.V)
		}
		amountP := &amount
		if in.AmountTHB.Set {
			if in.AmountTHB.Null {
				amountP = nil
			} else {
				amount = in.AmountTHB.V
			}
		}
		if in.CustomTypeName.Set {
			custom = fieldText(in.CustomTypeName)
		}
		if feeType != "custom" {
			custom = nil // updateCustomerServiceFee: customTypeName = null for every other type
		}
		if in.Note.Set {
			note = fieldText(in.Note)
		}
		v := ck.fee(feeType, unit, amountP, custom, note)
		if err := ck.err(); err != nil {
			return err
		}
		if err := q.UpdateServiceFee(ctx, billingdb.UpdateServiceFeeParams{ID: id, FeeType: v.feeType,
			CustomTypeName: v.custom, AmountThb: v.amount, Unit: v.unit, Note: v.note}); err != nil {
			return err
		}
		row, err := q.GetServiceFee(ctx, id)
		if err != nil {
			return err
		}
		out = serviceFeeOf(row)
		return rateCardChanged(ctx, tx, cur.TenantID, cur.BillingPartyID, TableServiceFees, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, out.BillingPartyID)
	return &out, nil
}

// DeleteServiceFee is DELETE /v1/billing/service-fees/{id} (accounting:edit_rate_card): 204; priced snapshots keep
// their amounts.
func (s *Service) DeleteServiceFee(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID) error {
	var party uuid.UUID
	err := s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		cur, err := q.LockServiceFee(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		party = cur.BillingPartyID
		if _, err := q.DeleteServiceFee(ctx, id); err != nil {
			return err
		}
		return rateCardChanged(ctx, tx, cur.TenantID, cur.BillingPartyID, TableServiceFees, rid)
	})
	if err != nil {
		return mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, party)
	return nil
}

// --- standby rates ------------------------------------------------------------------------------------------

// StandbyRateInput is the body of POST /v1/billing/standby-rates and PATCH /v1/billing/standby-rates/{id}.
type StandbyRateInput struct {
	CustomerID           string         `json:"customerId"`
	RateTHB              Field[float64] `json:"rateThb"`
	EffectiveFromDateStr Field[string]  `json:"effectiveFromDateStr"`
	Note                 Field[string]  `json:"note"`
}

// ListStandbyRates is GET /v1/billing/standby-rates (accounting:view_rate_card): newest effective date first; voided
// rates only with ?includeVoided=true.
func (s *Service) ListStandbyRates(ctx context.Context, p *authz.Principal, in ListInput) ([]StandbyRate, error) {
	all, tenants := p.TenantReach()
	out := []StandbyRate{}
	if strings.TrimSpace(in.CustomerID) != "" {
		party, found, err := s.listParty(ctx, p, in.CustomerID)
		if err != nil || !found {
			return out, err
		}
		b, err := s.partyBundle(ctx, party)
		if err != nil {
			return nil, err
		}
		for _, r := range b.Standby {
			if inReach(all, tenants, r.TenantID) && (in.IncludeVoided || !r.Voided) {
				out = append(out, r)
			}
		}
		return out, nil
	}
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		rows, err := q.ListStandbyRates(ctx, billingdb.ListStandbyRatesParams{AllTenants: all, TenantIds: tenants,
			IncludeVoided: in.IncludeVoided})
		for _, r := range rows {
			out = append(out, standbyRateOf(billingdb.GetStandbyRateRow(r)))
		}
		return err
	})
	return out, err
}

// CreateStandbyRate is POST /v1/billing/standby-rates (accounting:edit_rate_card): effective from Bangkok midnight of
// the day, server-side (fixes the browser-local midnight, rate-card/page.tsx:1146).
func (s *Service) CreateStandbyRate(ctx context.Context, p *authz.Principal, rid string, in StandbyRateInput) (*StandbyRate, error) {
	var ck checker
	partyID := ck.party("customerId", in.CustomerID)
	var rateP *float64
	if in.RateTHB.Set && !in.RateTHB.Null {
		rateP = &in.RateTHB.V
	}
	rate, _ := ck.money("rateThb", rateP, 0, true)
	day := ck.date("effectiveFromDateStr", in.EffectiveFromDateStr.V)
	var note *string
	if n := fieldText(in.Note); n != nil {
		if t := ck.text("note", *n, 1000, false); t != "" {
			note = &t
		}
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	tenant, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	var out StandbyRate
	err = s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		pr, err := writeParty(ctx, q, partyID)
		if err != nil {
			return err
		}
		id, err := q.InsertStandbyRate(ctx, billingdb.InsertStandbyRateParams{TenantID: tenant, PartyID: pr.ID,
			RateThb: decimalText(rate, 2), EffectiveFromDate: day, Note: note})
		if err != nil {
			return err
		}
		row, err := q.GetStandbyRate(ctx, id)
		if err != nil {
			return err
		}
		out = standbyRateOf(row)
		return rateCardChanged(ctx, tx, tenant, pr.ID, TableStandby, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, out.BillingPartyID)
	return &out, nil
}

// PatchStandbyRate is PATCH /v1/billing/standby-rates/{id} (accounting:edit_rate_card): rate, day or note; stored
// standby prices keep their amounts (only later pricing sees the change). A voided rate is 409 failed_precondition.
func (s *Service) PatchStandbyRate(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in StandbyRateInput) (*StandbyRate, error) {
	if strings.TrimSpace(in.CustomerID) != "" {
		return nil, httpx.ErrInvalidArgument(violation("customerId", "immutable"))
	}
	var out StandbyRate
	err := s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		cur, err := q.LockStandbyRate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if cur.VoidedAt != nil {
			return errPrecondition("voided", "a voided standby rate cannot change")
		}
		var ck checker
		rate := cur.RateThb
		if in.RateTHB.Set {
			var rp *float64
			if !in.RateTHB.Null {
				rp = &in.RateTHB.V
			}
			rate, _ = ck.money("rateThb", rp, 0, true)
		}
		day := cur.EffectiveFromDate
		if in.EffectiveFromDateStr.Set {
			day = ck.date("effectiveFromDateStr", in.EffectiveFromDateStr.V)
		}
		note := cur.Note
		if in.Note.Set {
			note = nil
			if n := fieldText(in.Note); n != nil {
				if t := ck.text("note", *n, 1000, false); t != "" {
					note = &t
				}
			}
		}
		if err := ck.err(); err != nil {
			return err
		}
		if err := q.UpdateStandbyRate(ctx, billingdb.UpdateStandbyRateParams{ID: id, RateThb: decimalText(rate, 2),
			EffectiveFromDate: day, Note: note}); err != nil {
			return err
		}
		row, err := q.GetStandbyRate(ctx, id)
		if err != nil {
			return err
		}
		out = standbyRateOf(row)
		return rateCardChanged(ctx, tx, cur.TenantID, cur.BillingPartyID, TableStandby, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, out.BillingPartyID)
	return &out, nil
}

// VoidStandbyRate is POST /v1/billing/standby-rates/{id}/void (accounting:edit_rate_card): the soft delete of R20
// (standby_records keep their billing_rate_entry_id); a second void is 409 failed_precondition (already_voided).
func (s *Service) VoidStandbyRate(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in VoidInput) (*StandbyRate, error) {
	reason, err := voidReason(in)
	if err != nil {
		return nil, err
	}
	by := actor(p)
	if by == nil {
		return nil, authz.ErrPermissionDenied("a void needs a user")
	}
	var out StandbyRate
	err = s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		cur, err := q.LockStandbyRate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if cur.VoidedAt != nil {
			return errPrecondition("already_voided", "the standby rate is already voided")
		}
		if err := q.VoidStandbyRate(ctx, billingdb.VoidStandbyRateParams{ID: id, VoidedAt: ptr(s.now().UTC()), VoidedBy: by,
			Reason: &reason}); err != nil {
			return err
		}
		row, err := q.GetStandbyRate(ctx, id)
		if err != nil {
			return err
		}
		out = standbyRateOf(row)
		return rateCardChanged(ctx, tx, cur.TenantID, cur.BillingPartyID, TableStandby, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteRateCardChanged, out.BillingPartyID)
	return &out, nil
}

func ptr[T any](v T) *T { return &v }
