package etl

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Companies, rate tables, fuel snapshots, statements and counters (Appendix A §A.3.2, §A.3.9, §A.3.10). The rate
// tables, statements and counters belong to the billing carrier, the own fleet today (form, R61).

// loadCompany: companies/{id} -> companies (§A.3.2). companyType owner -> the own fleet; subcontractor -> the carrier
// tenant with the same taxId, then the same Thai name (self); no carrier -> the quarantine tenant (relationship
// UNVERIFIED, auth report §8). withholdingTaxRate is loaded as stored; a document without one takes 3, the default
// the company form and every reader apply (companySchema.ts), since the column has no default. Logo, stamp and
// signature URLs become file_objects (company_logo, company_stamp, company_signature).
func loadCompany(c *docCtx) error {
	name := c.text("nameTh", "name")
	if name == nil {
		return c.reject("nameTh", ReasonMissingRequired, "companies.name_th is NOT NULL", nil)
	}
	taxID := c.text("taxId")
	if taxID == nil {
		return c.reject("taxId", ReasonMissingRequired, "companies.tax_id is NOT NULL", nil)
	}
	address := c.text("address")
	if address == nil {
		return c.reject("address", ReasonMissingRequired, "companies.address is NOT NULL", nil)
	}
	ctype, ok := c.vocab("companyType", "owner", "subcontractor")
	switch {
	case !ok:
		return c.reject("companyType", ReasonStatusOutOfVocab, "neither owner nor subcontractor", c.get("companyType"))
	case ctype == "":
		return c.reject("companyType", ReasonMissingRequired, "companies.company_type is NOT NULL", nil)
	}
	var branch string
	switch s := c.str("branchType"); s {
	case "", "headquarters", "hq", "สำนักงานใหญ่":
		branch = "headquarters"
	case "branch", "สาขา":
		branch = "branch"
	default:
		return c.reject("branchType", ReasonStatusOutOfVocab, "neither headquarters nor branch", s)
	}
	wht := 3.0
	if c.has("withholdingTaxRate") {
		f := c.float("withholdingTaxRate")
		if f == nil || *f < 0 || *f > 100 {
			return c.reject("withholdingTaxRate", ReasonBadNumber, "companies.withholding_tax_rate is a percent 0-100", c.get("withholdingTaxRate"))
		}
		wht = *f
	}
	if ctype == "owner" {
		var other string
		err := c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM companies WHERE company_type = 'owner'
			AND legacy_doc_id IS DISTINCT FROM $1`, c.doc.ID).Scan(&other)
		if err == nil {
			return c.reject("companyType", ReasonDuplicateNaturalKey, "a second owner company (companies_owner_one); the owner company is "+other, ctype)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	tenant, why := c.ownFleet(), ""
	if ctype == "subcontractor" {
		var t uuid.UUID
		err := c.tx.QueryRow(c.ctx, `SELECT id FROM (
			  SELECT id, 1 AS rank, legacy_doc_id FROM tenants WHERE kind = 'carrier' AND tax_id = $1
			  UNION ALL SELECT id, 2, legacy_doc_id FROM tenants WHERE kind = 'carrier' AND name_th = $2
			) x ORDER BY rank, legacy_doc_id COLLATE "C" LIMIT 1`, *taxID, *name).Scan(&t)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			tenant, why = uuid.Nil, "no carrier tenant has this taxId or Thai name (companies <-> subcontractors is UNVERIFIED)"
		case err != nil:
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		default:
			tenant = t
		}
	}
	cur, found, err := c.stampedRow("companies")
	if err != nil {
		return err
	}
	tenant = c.settle(cur, found, tenant, tenancy.SourceSelf, why)
	id, err := c.rowID("companies")
	if err != nil {
		return err
	}
	r := newRow("companies").set("id", id).set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).
		set("name_th", name).set("name_en", c.text("nameEn")).set("short_name", c.text("shortName")).set("tax_id", taxID).
		set("branch_type", branch).set("branch_number", c.text("branchNumber")).set("address", address).
		set("phone", c.text("phone")).set("email", c.text("email")).set("signatory_name", c.text("signatoryName")).
		set("bank_name", c.text("bankName")).set("account_number", c.text("accountNumber")).set("account_name", c.text("accountName")).
		set("withholding_tax_rate", wht).set("company_type", ctype).setPresent("is_active", c.boolean("isActive"))
	for _, f := range []string{"maxTrucks", "maxDrivers"} {
		n := c.integer(f)
		if n != nil && *n <= 0 {
			c.find(f, ReasonBadNumber, "must be > 0", c.get(f))
			n = nil
		}
		r.set(map[string]string{"maxTrucks": "max_trucks", "maxDrivers": "max_drivers"}[f], n)
	}
	if err := c.files(r, id, tenant, "company", [][3]string{{"logoUrl", "company_logo", "logo_file_id"},
		{"stampUrl", "company_stamp", "stamp_file_id"}, {"signatureUrl", "company_signature", "signature_file_id"}}); err != nil {
		return err
	}
	c.times(r)
	if _, err := upsertLegacy(c, r); err != nil {
		return err
	}
	c.done("companies", id)
	return nil
}

// --- rate tables -----------------------------------------------------------------------------------------------

// void are the void columns of an announcement row (ADR 0009 §1).
type void struct {
	voided bool
	at     *time.Time
	by     *uuid.UUID
	reason *string
}

// voidOf reads voided, voidedAt, voidedBy (uid -> user) and voidedReason. A voided legacy row without voidedAt takes
// its updatedAt, else the document's update time (the CHECK needs a void instant; the legacy reader ignores it).
func (c *docCtx) voidOf() (void, error) {
	v := void{voided: c.isTrue("voided")}
	if !v.voided {
		return v, nil
	}
	v.at = c.ts("voidedAt")
	if v.at == nil {
		v.at = c.ts("updatedAt")
	}
	if v.at == nil {
		t := c.doc.UpdateTime.Truncate(time.Microsecond)
		v.at = &t
	}
	var err error
	if v.by, err = c.user("voidedBy", c.str("voidedBy")); err != nil {
		return v, err
	}
	v.reason = c.text("voidedReason")
	return v, nil
}

// upsertAnnouncement writes an immutable announcement row (customer_rate_entries, customer_fuel_rate_adjustments,
// ADR 0009 §1: void only, never deleted, trg_announcement_void_only). A new document inserts its row with the void
// columns as stored; a re-applied document adds only a void the row does not have yet (the trigger needs voided_by:
// a void whose voidedBy resolves to no user stays a user_unresolved finding until the user exists and the document
// is retried). Any other change of a legacy announcement is not applied: the row keeps what was announced.
func (c *docCtx) upsertAnnouncement(r *row, v void) (uuid.UUID, error) {
	var id uuid.UUID
	var voided bool
	err := c.tx.QueryRow(c.ctx, `SELECT id, voided FROM `+pgx.Identifier{r.table}.Sanitize()+` WHERE legacy_doc_id = $1`, c.doc.ID).Scan(&id, &voided)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		r.set("legacy_doc_id", c.doc.ID).set("voided", v.voided).set("voided_at", v.at).set("voided_by", v.by).set("voided_reason", v.reason)
		if err := c.tx.QueryRow(c.ctx, r.insertSQL()+" RETURNING id", r.vals...).Scan(&id); err != nil {
			return uuid.Nil, fmt.Errorf("etl: %s: insert %s: %w", c.doc.Path, r.table, err)
		}
		return id, nil
	case err != nil:
		return uuid.Nil, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	if v.voided && !voided {
		if v.by == nil {
			c.find("voidedBy", ReasonUserUnresolved, "the void is not applied: an announcement void needs voided_by (trg_announcement_void_only)", c.get("voidedBy"))
			return id, nil
		}
		if _, err := c.tx.Exec(c.ctx, `UPDATE `+pgx.Identifier{r.table}.Sanitize()+` SET voided = true, voided_at = $2, voided_by = $3,
			voided_reason = $4 WHERE id = $1`, id, v.at, v.by, v.reason); err != nil {
			return uuid.Nil, fmt.Errorf("etl: %s: void: %w", c.doc.Path, err)
		}
	}
	return id, nil
}

// loadRateEntry: customer_rate_entries (§A.3.9). hubId upper, destinationCode as stored (the SPK collapse is baked
// in), vehicle_class := normalizeVehicleClass(stored) (compute.FoldVehicleClass: '2 WHEELS' -> 2W; a stored '4WJ'
// default stays, R15 applies to trips), jobCategory absent -> PRIMARY (the one place a default is correct).
func loadRateEntry(c *docCtx) error {
	party, ok, err := c.requiredParty("customerId")
	if !ok {
		return err
	}
	importID := c.text("importId")
	if importID == nil {
		return c.reject("importId", ReasonMissingRequired, "customer_rate_entries.import_id is NOT NULL", nil)
	}
	hub := strings.ToUpper(c.str("hubId"))
	if hub == "" {
		return c.reject("hubId", ReasonMissingRequired, "customer_rate_entries.hub_code is NOT NULL", nil)
	}
	dest := c.str("destinationCode")
	if dest == "" {
		return c.reject("destinationCode", ReasonMissingRequired, "customer_rate_entries.destination_code is NOT NULL", nil)
	}
	class, ok := compute.FoldVehicleClass(c.str("vehicleClass"))
	if !ok {
		return c.reject("vehicleClass", ReasonMissingRequired, "customer_rate_entries.vehicle_class is NOT NULL", nil)
	}
	rate, ok, err := c.requiredMoney("rateThb", "customer_rate_entries.rate_thb", true)
	if !ok {
		return err
	}
	cat := "PRIMARY"
	if s := strings.ToUpper(c.str("jobCategory")); s != "" {
		if s != "PRIMARY" && s != "SUPPLEMENTARY" {
			return c.reject("jobCategory", ReasonStatusOutOfVocab, "neither PRIMARY nor SUPPLEMENTARY", c.get("jobCategory"))
		}
		cat = s
	}
	day, at, ok := c.effectiveFrom()
	if !ok {
		return c.reject("effectiveFrom", ReasonMissingRequired, "effective_from_date / effective_from_at are NOT NULL", c.get("effectiveFrom"))
	}
	v, err := c.voidOf()
	if err != nil {
		return err
	}
	by, err := c.user("createdBy", c.str("createdBy"))
	if err != nil {
		return err
	}
	r := newRow("customer_rate_entries").set("tenant_id", c.ownFleet()).set("tenant_source", "form").set("billing_party_id", *party).
		set("import_id", importID).set("hub_code", hub).set("raw_hub_name", c.text("rawHubName")).set("destination_code", dest).
		set("vehicle_class", class).set("rate_thb", numeric2(rate)).set("distance_km", c.float("distanceKm")).set("job_category", cat).
		set("effective_from_date", day).set("effective_from_at", at).set("imported_at", c.ts("importedAt")).set("created_by", by)
	c.times(r)
	id, err := c.upsertAnnouncement(r, v)
	if err != nil {
		return err
	}
	c.tenantSource = tenancy.SourceForm
	c.done("customer_rate_entries", id)
	return nil
}

// loadFuelAdjustment: customer_fuel_rate_adjustments (§A.3.9): customer-level, no fallback; band provenance inputs as
// stored (referenceFuelPriceThbPerLitre, fuelBand*). A band switched on without its floor and rate loads switched off
// with a bad_number finding (the CHECK needs both).
func loadFuelAdjustment(c *docCtx) error {
	party, ok, err := c.requiredParty("customerId")
	if !ok {
		return err
	}
	if !c.has("rateMultiplier") {
		return c.reject("rateMultiplier", ReasonMissingRequired, "customer_fuel_rate_adjustments.rate_multiplier is NOT NULL", nil)
	}
	mult := c.float("rateMultiplier")
	if mult == nil || *mult <= 0 {
		return c.reject("rateMultiplier", ReasonBadNumber, "rate_multiplier must be > 0", c.get("rateMultiplier"))
	}
	day, at, ok := c.effectiveFrom()
	if !ok {
		return c.reject("effectiveFrom", ReasonMissingRequired, "effective_from_date / effective_from_at are NOT NULL", c.get("effectiveFrom"))
	}
	add := numeric2(0)
	if p := c.money("addThbPerTrip", false); p != nil {
		add = *p
	}
	note := ""
	if s := c.text("announcementNote"); s != nil {
		note = *s
	}
	band := c.isTrue("fuelBandEnabled")
	floor, perBaht := c.integer("fuelBandBaselineFuelFloor"), c.float("fuelBandThbPerBaht")
	if band && (floor == nil || perBaht == nil) {
		c.find("fuelBandEnabled", ReasonBadNumber, "a fuel band needs fuelBandBaselineFuelFloor and fuelBandThbPerBaht; loaded switched off", true)
		band = false
	}
	v, err := c.voidOf()
	if err != nil {
		return err
	}
	by, err := c.user("createdBy", c.str("createdBy"))
	if err != nil {
		return err
	}
	r := newRow("customer_fuel_rate_adjustments").set("tenant_id", c.ownFleet()).set("tenant_source", "form").
		set("billing_party_id", *party).set("effective_from_date", day).set("effective_from_at", at).set("rate_multiplier", *mult).
		set("add_thb_per_trip", add).set("reference_fuel_price_thb", c.money("referenceFuelPriceThbPerLitre", false)).
		set("announcement_note", note).set("fuel_band_enabled", band).set("fuel_band_baseline_floor", floor).
		set("fuel_band_thb_per_baht", perBaht).set("created_by", by)
	c.times(r)
	id, err := c.upsertAnnouncement(r, v)
	if err != nil {
		return err
	}
	c.tenantSource = tenancy.SourceForm
	c.done("customer_fuel_rate_adjustments", id)
	return nil
}

// loadServiceFee: customer_service_fees (§A.3.9). Several fees of one (party, feeType) all load before 0010 and the
// later ones carry duplicate_service_fee (D5; the Go selector reproduces the legacy winners until the owner's sign-off:
// extra_stop first, standby last by legacy doc id). Once 0010 has built customer_service_fees_one_per_type, a further
// duplicate of a non-custom type cannot load and is rejected with duplicate_service_fee.
func loadServiceFee(c *docCtx) error {
	party, ok, err := c.requiredParty("customerId")
	if !ok {
		return err
	}
	ft, ok := lowerVocab(c.get("feeType"), "extra_stop", "waiting_time", "special_handling", "service_charge", "standby", "custom")
	switch {
	case !ok:
		return c.reject("feeType", ReasonStatusOutOfVocab, "no fee type of the CHECK", c.get("feeType"))
	case ft == "":
		return c.reject("feeType", ReasonMissingRequired, "customer_service_fees.fee_type is NOT NULL", nil)
	}
	custom := c.text("customTypeName")
	if ft == "custom" && custom == nil {
		return c.reject("customTypeName", ReasonMissingRequired, "a custom fee needs customTypeName", nil)
	}
	amount, ok, err := c.requiredMoney("amountThb", "customer_service_fees.amount_thb", true)
	if !ok {
		return err
	}
	unit, ok := lowerVocab(c.get("unit"), "per_trip", "per_stop")
	switch {
	case !ok:
		return c.reject("unit", ReasonStatusOutOfVocab, "neither per_trip nor per_stop", c.get("unit"))
	case unit == "":
		return c.reject("unit", ReasonMissingRequired, "customer_service_fees.unit is NOT NULL", nil)
	}
	if ft != "custom" {
		var other string
		err := c.tx.QueryRow(c.ctx, `SELECT legacy_doc_id FROM customer_service_fees WHERE billing_party_id = $1 AND fee_type = $2
			AND legacy_doc_id IS DISTINCT FROM $3 ORDER BY legacy_doc_id COLLATE "C" LIMIT 1`, *party, ft, c.doc.ID).Scan(&other)
		switch {
		case err == nil:
			enforced, err := c.run.d5Enforced(d5ServiceFeeTypes)
			if err != nil {
				return err
			}
			if enforced {
				return c.reject("feeType", ReasonDuplicateServiceFee, "the party already has a "+ft+" fee ("+other+") and 0010 allows one", ft)
			}
			c.find("feeType", ReasonDuplicateServiceFee, "the party's "+ft+" fee is also "+other+" (D5: both loaded until the owner's sign-off)", ft)
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	r := newRow("customer_service_fees").set("tenant_id", c.ownFleet()).set("tenant_source", "form").set("billing_party_id", *party).
		set("fee_type", ft).set("custom_type_name", custom).set("amount_thb", numeric2(amount)).set("unit", unit).set("note", c.text("note"))
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	c.tenantSource = tenancy.SourceForm
	c.done("customer_service_fees", id)
	return nil
}

// loadStandbyRate: standby_rate_entries (§A.3.9): effectiveFrom is browser-local midnight, its Bangkok day the key
// (UNVERIFIED that every admin browser ran in ICT; the reconciliation lists every standby rate). No legacy void.
func loadStandbyRate(c *docCtx) error {
	party, ok, err := c.requiredParty("customerId")
	if !ok {
		return err
	}
	rate, ok, err := c.requiredMoney("rateThb", "standby_rate_entries.rate_thb", true)
	if !ok {
		return err
	}
	day, at, ok := c.effectiveFrom()
	if !ok {
		return c.reject("effectiveFrom", ReasonMissingRequired, "effective_from_date / effective_from_at are NOT NULL", c.get("effectiveFrom"))
	}
	r := newRow("standby_rate_entries").set("tenant_id", c.ownFleet()).set("tenant_source", "form").set("billing_party_id", *party).
		set("rate_thb", numeric2(rate)).set("effective_from_date", day).set("effective_from_at", at).set("note", c.text("note"))
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	c.tenantSource = tenancy.SourceForm
	c.done("standby_rate_entries", id)
	return nil
}

// --- fuel price snapshots ----------------------------------------------------------------------------------------

var (
	dayKey   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	monthKey = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)
)

// snapshotFields are the columns both fuel snapshot tables share: captured_at (NOT NULL), fetched_at (fetchedAtIso),
// source (NOT NULL), locale and the items as plain JSON. ok is false when the row was rejected.
func (c *docCtx) snapshotFields(r *row) (bool, error) {
	captured := c.ts("capturedAt")
	if captured == nil {
		return false, c.reject("capturedAt", ReasonMissingRequired, "captured_at is NOT NULL", nil)
	}
	source := c.text("source")
	if source == nil {
		return false, c.reject("source", ReasonMissingRequired, "source is NOT NULL", nil)
	}
	locale, ok := lowerVocab(c.get("locale"), "th", "en")
	if !ok {
		c.find("locale", ReasonStatusOutOfVocab, "neither th nor en; th kept", c.get("locale"))
		locale = ""
	}
	if locale == "" {
		locale = "th"
	}
	items := plain(c.get("items"))
	if _, isList := items.([]any); !isList {
		items = []any{}
	}
	r.set("captured_at", captured).set("fetched_at", c.ts("fetchedAtIso")).set("source", source).set("locale", locale).
		set("items", items)
	return true, nil
}

// loadFuelDaily: fuel_daily_snapshots/{yyyy-MM-dd} (§A.3.10): create-only observations (ADR 0009 §5), so a re-applied
// day keeps its first capture. Only status 'ok' exists on daily rows (the CHECK); another status is rejected.
func loadFuelDaily(c *docCtx) error {
	if !dayKey.MatchString(c.doc.ID) {
		return c.reject("", ReasonMissingRequired, "the doc id is not yyyy-MM-dd (day_key)", c.doc.ID)
	}
	day, err := time.Parse("2006-01-02", c.doc.ID)
	if err != nil {
		return c.reject("", ReasonBadTimestamp, "the doc id is not a calendar day", c.doc.ID)
	}
	if st, ok := lowerVocab(c.get("status"), "ok"); !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "a daily snapshot is ok only", st)
	}
	r := newRow("fuel_daily_snapshots").set("day_key", day).set("status", "ok")
	if ok, err := c.snapshotFields(r); !ok {
		return err
	}
	if _, err := c.tx.Exec(c.ctx, r.insertSQL()+` ON CONFLICT (day_key) DO NOTHING`, r.vals...); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	c.table, c.target = "fuel_daily_snapshots", c.doc.ID
	return nil
}

// loadFuelMonthly: fuel_monthly_snapshots/{yyyy-MM} (§A.3.10): upserted; legacy status 'error' rows (the fetch-error
// clobber, fixed in Go) load as they are with their errorMessage.
func loadFuelMonthly(c *docCtx) error {
	if !monthKey.MatchString(c.doc.ID) {
		return c.reject("", ReasonMissingRequired, "the doc id is not yyyy-MM (month_key)", c.doc.ID)
	}
	st, ok := lowerVocab(c.get("status"), "ok", "error")
	switch {
	case !ok:
		return c.reject("status", ReasonStatusOutOfVocab, "neither ok nor error", c.get("status"))
	case st == "":
		st = "ok"
	}
	r := newRow("fuel_monthly_snapshots").set("month_key", c.doc.ID).set("status", st).set("error_message", c.text("errorMessage"))
	if ok, err := c.snapshotFields(r); !ok {
		return err
	}
	if _, err := c.tx.Exec(c.ctx, r.insertSQL()+` ON CONFLICT (month_key) DO UPDATE SET captured_at = EXCLUDED.captured_at,
		fetched_at = EXCLUDED.fetched_at, source = EXCLUDED.source, locale = EXCLUDED.locale, status = EXCLUDED.status,
		error_message = EXCLUDED.error_message, items = EXCLUDED.items`, r.vals...); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	c.table, c.target = "fuel_monthly_snapshots", c.doc.ID
	return nil
}

// --- statements and counters -----------------------------------------------------------------------------------

// legacyWHTRate is the withholding tax rate every legacy statement used (hard-coded 0.01, R18, D8).
var legacyWHTRate = pgtype.Numeric{Int: big.NewInt(100), Exp: -4, Valid: true}

// loadStatement: billing_statements (§A.3.9). invoice_number := invoiceNumber ?? doc id; legacy totals as stored (half-up
// to 2 dp, never recomputed, R18) with the hard-coded legacy WHT rate 0.0100; no statement lines (never persisted). A
// sent or paid statement is the period lock pricing reads (R17), so one that cannot load aborts the run (R19 guard).
func loadStatement(c *docCtx) error {
	party, ok, err := c.requiredParty("customerId")
	if !ok {
		return err
	}
	inv := c.str("invoiceNumber")
	if inv == "" {
		inv = c.doc.ID
	}
	var holder string
	err = c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM billing_statements WHERE invoice_number = $1
		AND legacy_doc_id IS DISTINCT FROM $2`, inv, c.doc.ID).Scan(&holder)
	if err == nil {
		return c.reject("invoiceNumber", ReasonDuplicateNaturalKey, "the invoice number is already statement "+holder, inv)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	per, _ := c.get("period").(map[string]any)
	y, yok := toFloat(per["year"])
	m, mok := toFloat(per["month"])
	if !yok || !mok || y < 2000 || y > 2999 || m < 1 || m > 12 || y != float64(int(y)) || m != float64(int(m)) {
		return c.reject("period", ReasonMissingRequired, "period {month, year} is NOT NULL", c.get("period"))
	}
	status, ok := c.vocab("status", "draft", "sent", "paid", "cancelled")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "no statement status", c.get("status"))
	}
	if status == "" {
		status = "draft"
	}
	amounts := map[string]pgtype.Numeric{}
	for f, col := range map[string]string{"totalAmount": "total_amount", "withholdingTax": "withholding_tax", "netAmount": "net_amount"} {
		n, ok, err := c.requiredMoney(f, "billing_statements."+col, false)
		if !ok {
			return err
		}
		amounts[col] = numeric2(n)
	}
	gen := c.ts("generatedAt")
	if gen == nil {
		gen = c.ts("createdAt")
	}
	if gen == nil {
		t := c.doc.CreateTime
		if t.IsZero() {
			t = c.doc.UpdateTime
		}
		t = t.Truncate(time.Microsecond)
		gen = &t
	}
	issuer, err := c.companyRef(c.str("issuerCompanyId"), c.str("companyId"))
	if err != nil {
		return err
	}
	by, err := c.user("generatedBy", c.str("generatedBy"))
	if err != nil {
		return err
	}
	r := newRow("billing_statements").set("tenant_id", c.ownFleet()).set("tenant_source", "form").set("billing_party_id", *party).
		set("issuer_company_id", issuer).set("invoice_number", inv).set("customer_name_snapshot", c.text("customerName")).
		set("customer_code_snapshot", c.text("customerCode")).set("period_year", int(y)).set("period_month", int(m)).
		set("total_amount", amounts["total_amount"]).set("withholding_tax_rate", legacyWHTRate).set("withholding_tax", amounts["withholding_tax"]).
		set("net_amount", amounts["net_amount"]).set("status", status).set("generated_at", gen).set("sent_at", c.ts("sentAt")).
		set("paid_at", c.ts("paidAt")).set("cancelled_at", c.ts("cancelledAt")).set("due_date", c.ts("dueDate")).
		set("note", c.text("note")).set("generated_by", by)
	for f, col := range map[string]string{"tripCount": "trip_count", "tripOnlyCount": "trip_only_count", "standbyCount": "standby_count",
		"multiDropCount": "multi_drop_count"} {
		n := c.integer(f)
		if n != nil && *n < 0 {
			c.find(f, ReasonBadNumber, "negative count", c.get(f))
			n = nil
		}
		r.setPresent(col, n)
	}
	for f, col := range map[string]string{"tripSubtotal": "trip_subtotal", "standbySubtotal": "standby_subtotal", "multiDropSubtotal": "multi_drop_subtotal"} {
		r.setPresent(col, c.money(f, false))
	}
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	c.tenantSource = tenancy.SourceForm
	c.done("billing_statements", id)
	return nil
}

// companyRef resolves an issuer company doc id (the first non-empty of ids) to companies.id; NULL when none.
func (c *docCtx) companyRef(ids ...string) (*uuid.UUID, error) {
	for _, raw := range ids {
		if raw == "" {
			continue
		}
		var id uuid.UUID
		err := c.tx.QueryRow(c.ctx, `SELECT id FROM companies WHERE legacy_doc_id = $1`, raw).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
		return &id, nil
	}
	return nil, nil
}

// afterStatements raises billing_date_locked (D7, info) on the plan-basis trips of this load that have no billingDate
// while the Bangkok month of their task's plan date (the date a restamp would write) already has a sent or paid
// statement of the trip's party: the restamp is refused there, so the owner decides by hand.
func afterStatements(r *runState, _ string) error {
	rows, err := r.tx.Query(r.ctx, `SELECT sd.doc_path FROM etl.source_docs sd
		JOIN trip_records tr ON tr.id = sd.target_id::uuid AND sd.target_table = 'trip_records'
		JOIN billing_parties bp ON bp.id = tr.billing_party_id AND bp.billing_date_basis = 'plan'
		JOIN tasks t ON t.id = tr.task_id
		WHERE sd.collection = 'trip_records' AND tr.billing_date IS NULL
		  AND EXISTS (SELECT 1 FROM billing_statements s WHERE s.billing_party_id = tr.billing_party_id AND s.status IN ('sent', 'paid')
		      AND s.period_year = extract(year FROM bkk_date(t.plan_at))::int AND s.period_month = extract(month FROM bkk_date(t.plan_at))::int)
		ORDER BY sd.doc_path`)
	if err != nil {
		return err
	}
	paths, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := r.extraFinding("trip_records", p, Finding{Field: "billingDate", Reason: ReasonBillingDateLocked,
			Detail: "the task's plan month has a sent or paid statement of this party: no restamp (D7)"}); err != nil {
			return err
		}
	}
	return nil
}

// loadCounter: billing_counters/{customerId}_{YYYYMM} (§A.3.9): the doc id splits on the last '_'; last_seq and the
// invoice prefix customerCode as stored (the party's customer code when the document has none).
func loadCounter(c *docCtx) error {
	i := strings.LastIndexByte(c.doc.ID, '_')
	if i <= 0 || len(c.doc.ID)-i-1 != 6 {
		return c.reject("", ReasonMissingRequired, "the doc id is not {customerId}_{YYYYMM}", c.doc.ID)
	}
	y, err1 := strconv.Atoi(c.doc.ID[i+1 : i+5])
	m, err2 := strconv.Atoi(c.doc.ID[i+5:])
	if err1 != nil || err2 != nil || y < 2000 || y > 2999 || m < 1 || m > 12 {
		return c.reject("", ReasonMissingRequired, "the doc id is not {customerId}_{YYYYMM}", c.doc.ID)
	}
	party, err := c.partyID(c.doc.ID[:i])
	if err != nil {
		return err
	}
	if party == nil {
		return c.reject("", ReasonPartyUnresolved, "the counter's customer id matches no customers or subcontractors doc", c.doc.ID[:i])
	}
	seq := c.integer("lastSeq")
	if seq == nil {
		z := int64(0)
		seq = &z
	}
	if *seq < 0 {
		return c.reject("lastSeq", ReasonBadNumber, "last_seq >= 0", c.get("lastSeq"))
	}
	code := c.text("customerCode")
	if code == nil {
		var s string
		if err := c.tx.QueryRow(c.ctx, `SELECT coalesce(cu.code::text, t.code::text, '') FROM billing_parties bp
			LEFT JOIN customers cu ON cu.id = bp.customer_id LEFT JOIN tenants t ON t.id = bp.tenant_id WHERE bp.id = $1`, *party).Scan(&s); err != nil {
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
		if s == "" {
			return c.reject("customerCode", ReasonMissingRequired, "billing_counters.customer_code is NOT NULL", nil)
		}
		code = &s
	}
	if _, err := c.tx.Exec(c.ctx, `INSERT INTO billing_counters (billing_party_id, period_year, period_month, tenant_id, tenant_source, last_seq, customer_code)
		VALUES ($1, $2, $3, $4, 'form', $5, $6)
		ON CONFLICT (billing_party_id, period_year, period_month) DO UPDATE SET last_seq = EXCLUDED.last_seq, customer_code = EXCLUDED.customer_code`,
		*party, y, m, c.ownFleet(), *seq, *code); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	c.tenantSource = tenancy.SourceForm
	c.table, c.target = "billing_counters", fmt.Sprintf("%s/%d/%d", party, y, m)
	return nil
}
