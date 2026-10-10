package etl

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Master data (Appendix A §A.3.2, §A.3.4). Status and enum vocabularies map case-insensitively with '-' and ' ' as
// '_' (canon); a status with no lossless mapping rejects the row (status_out_of_vocab), an optional vocabulary
// column outside its CHECK is NULL with a field finding of the same code.

// canon is the lower_snake form of a legacy literal ("On-Duty" -> on_duty, "Insurance Claim" -> insurance_claim).
func canon(s string) string {
	return strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(s)))
}

// vocab maps a field onto a CHECK vocabulary: ok false with a value present means "outside the vocabulary".
func (c *docCtx) vocab(field string, allowed ...string) (string, bool) {
	s := c.str(field)
	if s == "" {
		return "", true
	}
	v := canon(s)
	return v, slices.Contains(allowed, v)
}

// optVocab is a nullable vocabulary column: outside the vocabulary -> NULL + status_out_of_vocab (field).
func (c *docCtx) optVocab(field string, allowed ...string) *string {
	v, ok := c.vocab(field, allowed...)
	if !ok {
		c.find(field, ReasonStatusOutOfVocab, "outside the column's vocabulary", c.get(field))
		return nil
	}
	if v == "" {
		return nil
	}
	return &v
}

// times sets created_at and updated_at from the legacy fields (the database default when absent).
func (c *docCtx) times(r *row) {
	r.setPresent("created_at", c.ts("createdAt"))
	r.setPresent("updated_at", c.ts("updatedAt"))
}

// stringList is a text[] from a legacy array of strings (other elements are skipped).
func (c *docCtx) stringList(field string) []string {
	out := []string{}
	arr, _ := c.get(field).([]any)
	for _, e := range arr {
		if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func (c *docCtx) done(table string, id uuid.UUID) {
	c.table, c.target = table, id.String()
}

// billingBasis is billingDateBasis: absent -> delivered (tripBillingOnDelivered.ts:45-47).
func (c *docCtx) billingBasis() string {
	v, ok := c.vocab("billingDateBasis", "delivered", "plan")
	if !ok {
		c.find("billingDateBasis", ReasonStatusOutOfVocab, "not delivered or plan; the party keeps the default delivered", c.get("billingDateBasis"))
		return "delivered"
	}
	if v == "" {
		return "delivered"
	}
	return v
}

// loadSubcontractor: subcontractors/{id} -> tenants(kind='carrier') + billing_parties(kind='tenant') + tenant_files
// (§A.3.2). Every carrier works for the own fleet (contractor_tenant_id, R60). The doc that settings/tenancy names
// as the own fleet fills the own-fleet tenant's profile instead (tenantLookups.ts:12-21). Every carrier gets its
// billing party, as POST /v1/tenants creates one, so party references to it always resolve.
func loadSubcontractor(c *docCtx) error {
	name := c.text("name")
	if name == nil {
		return c.reject("name", ReasonMissingRequired, "tenants.name_th is NOT NULL", nil)
	}
	legal, ok := c.vocab("type", "individual", "company")
	if !ok {
		return c.reject("type", ReasonStatusOutOfVocab, "type is neither individual nor company", c.get("type"))
	}
	if legal == "" {
		legal = "individual" // the subcontractor form's default
	}
	status, ok := c.vocab("status", "active", "pending", "suspended")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "no lossless tenant status", c.get("status"))
	}
	if status == "" {
		status = "active"
	}
	r := newRow("tenants").set("name_th", name).set("legal_type", legal).set("status", status).
		set("id_card_number", c.text("idCardNumber")).set("tax_id", c.text("taxId")).
		set("contact_person", c.text("contactPerson")).set("phone", c.text("phone")).set("email", c.text("email")).
		set("website", c.text("website")).set("address", c.text("address")).set("designation", c.text("designation")).
		set("dispatch_center", c.text("dispatchCenter")).set("service_regions", c.stringList("serviceRegions")).
		set("vehicle_types", c.stringList("vehicleTypes")).set("line_group_id", c.text("lineGroupId"))
	if n := c.integer("fleetSize"); n != nil {
		if *n < 0 {
			c.find("fleetSize", ReasonBadNumber, "fleet size below zero", c.get("fleetSize"))
		} else {
			r.set("fleet_size", *n)
		}
	}
	if code := c.text("code"); code != nil {
		var holder string
		err := c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM tenants WHERE code = $1 AND legacy_doc_id IS DISTINCT FROM $2`,
			*code, c.doc.ID).Scan(&holder)
		switch {
		case err == nil:
			c.find("code", ReasonDuplicateNaturalKey, "tenant code already held by "+holder, *code)
		case errors.Is(err, pgx.ErrNoRows):
			r.set("code", code)
		default:
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	c.times(r)

	var id uuid.UUID
	var ownLegacy *string
	if err := c.tx.QueryRow(c.ctx, `SELECT legacy_doc_id FROM tenants WHERE id = $1`, c.e.cfg.OwnFleetTenantID).Scan(&ownLegacy); err != nil {
		return fmt.Errorf("etl: %s: own fleet: %w", c.doc.Path, err)
	}
	if ownLegacy != nil && *ownLegacy == c.doc.ID {
		id = c.e.cfg.OwnFleetTenantID
		sets := make([]string, len(r.cols))
		for i, col := range r.cols {
			sets[i] = pgx.Identifier{col}.Sanitize() + fmt.Sprintf(" = $%d", i+2)
		}
		if _, err := c.tx.Exec(c.ctx, `UPDATE tenants SET `+strings.Join(sets, ", ")+` WHERE id = $1`, append([]any{id}, r.vals...)...); err != nil {
			return fmt.Errorf("etl: %s: own-fleet profile: %w", c.doc.Path, err)
		}
	} else {
		r.set("kind", "carrier").set("contractor_tenant_id", c.e.cfg.OwnFleetTenantID)
		var err error
		if id, err = upsertLegacy(c, r); err != nil {
			return err
		}
	}
	c.tenantSource = tenancy.SourceSelf
	if _, err := c.tx.Exec(c.ctx, `INSERT INTO billing_parties (kind, tenant_id, billing_date_basis) VALUES ('tenant', $1, $2)
		ON CONFLICT (tenant_id) WHERE tenant_id IS NOT NULL DO UPDATE SET billing_date_basis = EXCLUDED.billing_date_basis`,
		id, c.billingBasis()); err != nil {
		return fmt.Errorf("etl: %s: billing party: %w", c.doc.Path, err)
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM tenant_files WHERE tenant_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	docs, _ := c.get("documents").([]any)
	for i, u := range docs {
		fid, err := c.fileRef(fmt.Sprintf("documents[%d]", i), u, "tenant_document", "tenant", id, id)
		if err != nil {
			return err
		}
		if fid != nil {
			if _, err := c.tx.Exec(c.ctx, `INSERT INTO tenant_files (tenant_id, file_id, kind, position) VALUES ($1, $2, 'other', $3)
				ON CONFLICT DO NOTHING`, id, *fid, i); err != nil {
				return fmt.Errorf("etl: %s: tenant file: %w", c.doc.Path, err)
			}
		}
	}
	c.done("tenants", id)
	return nil
}

// loadCustomer: customers/{id} -> customers + billing_parties(kind='customer') + customer_driver_id_types (§A.3.2).
// name or customerName, code or customerCode (Q10); the code is upper-cased and a code another doc holds rejects
// the second doc (duplicate_natural_key: no billing fields live on customers).
func loadCustomer(c *docCtx) error {
	code := strings.ToUpper(strings.TrimSpace(c.str("code")))
	if code == "" {
		code = strings.ToUpper(c.str("customerCode"))
	}
	if code == "" {
		return c.reject("code", ReasonMissingRequired, "customers.code is NOT NULL", nil)
	}
	name := c.text("name", "customerName")
	if name == nil {
		return c.reject("name", ReasonMissingRequired, "customers.name is NOT NULL", nil)
	}
	var holder string
	err := c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM customers WHERE code = $1 AND legacy_doc_id IS DISTINCT FROM $2`,
		code, c.doc.ID).Scan(&holder)
	if err == nil {
		return c.reject("code", ReasonDuplicateNaturalKey, "customer code already held by "+holder, code)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	var branch *string
	switch s := c.str("branchType"); s {
	case "":
	case "สำนักงานใหญ่", "hq":
		v := "hq"
		branch = &v
	case "สาขา", "branch":
		v := "branch"
		branch = &v
	default:
		c.find("branchType", ReasonStatusOutOfVocab, "neither สำนักงานใหญ่ nor สาขา", s)
	}
	terms := c.integer("paymentTermsDays")
	if terms != nil && *terms < 0 {
		c.find("paymentTermsDays", ReasonBadNumber, "negative payment terms", c.get("paymentTermsDays"))
		terms = nil
	}
	r := newRow("customers").set("code", code).set("name", name).set("description", c.text("description")).
		set("address", c.text("address")).set("tax_id", c.text("taxId")).set("branch_type", branch).
		set("branch_number", c.text("branchNumber")).set("contact_name", c.text("contactName")).
		set("contact_phone", c.text("contactPhone")).set("billing_email", c.text("billingEmail")).
		set("payment_terms_days", terms).set("invoice_note", c.text("invoiceNote")).set("line_group_id", c.text("lineGroupId"))
	c.times(r)
	id, err := c.rowID("customers")
	if err != nil {
		return err
	}
	logo, err := c.fileRef("logoUrl", c.get("logoUrl"), "customer_logo", "customer", id, uuid.Nil)
	if err != nil {
		return err
	}
	r.set("id", id).set("logo_file_id", logo)
	if _, err := upsertLegacy(c, r); err != nil {
		return err
	}
	if _, err := c.tx.Exec(c.ctx, `INSERT INTO billing_parties (kind, customer_id, billing_date_basis) VALUES ('customer', $1, $2)
		ON CONFLICT (customer_id) WHERE customer_id IS NOT NULL DO UPDATE SET billing_date_basis = EXCLUDED.billing_date_basis`,
		id, c.billingBasis()); err != nil {
		return fmt.Errorf("etl: %s: billing party: %w", c.doc.Path, err)
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM customer_driver_id_types WHERE customer_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	types, _ := c.get("driverIdTypes").([]any)
	seen := map[string]bool{}
	for i, t := range types {
		m, _ := t.(map[string]any)
		key, _ := m["key"].(string)
		label, _ := m["label"].(string)
		if strings.TrimSpace(key) == "" || strings.TrimSpace(label) == "" || seen[key] {
			c.find(fmt.Sprintf("driverIdTypes[%d]", i), ReasonMissingRequired, "an id type needs a unique key and a label", t)
			continue
		}
		seen[key] = true
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO customer_driver_id_types (customer_id, key, label, position) VALUES ($1, $2, $3, $4)`,
			id, key, label, i); err != nil {
			return fmt.Errorf("etl: %s: driver id type: %w", c.doc.Path, err)
		}
	}
	c.done("customers", id)
	return nil
}

// vehicleClass folds a legacy class onto the task enum (taskTruckTypeFromTruckDoc, normalizeVehicleClass); a class
// outside the enum is NULL + vehicle_class_out_of_enum, never guessed (R15). Blank is NULL without a finding.
func (c *docCtx) vehicleClass(field string) *string {
	raw := c.str(field)
	if raw == "" {
		return nil
	}
	v, ok := compute.FoldVehicleClass(raw)
	if !ok || !slices.Contains([]string{"4W", "4WJ", "6WH", "10WH", "18WH", "VAN"}, v) {
		c.find(field, ReasonVehicleClassOutOfEnum, "folds to no class of the enum", raw)
		return nil
	}
	return &v
}

// loadTruck: trucks/{id} -> trucks (§A.3.4); tenant from ownership (tenancy.Resolve, self).
func loadTruck(c *docCtx) error {
	for _, f := range []string{"licensePlate", "province", "brand", "model", "color", "type", "year"} {
		if !c.has(f) {
			return c.reject(f, ReasonMissingRequired, "a NOT NULL truck column has no value", nil)
		}
	}
	year := c.integer("year")
	if year == nil || *year < 1900 || *year > 2200 {
		return c.reject("year", ReasonBadNumber, "trucks.year is a NOT NULL smallint year", c.get("year"))
	}
	status, ok := c.vocab("truckStatus", "active", "inactive", "maintenance", "insurance_claim", "sold", "available")
	if !ok || status == "" {
		return c.reject("truckStatus", ReasonStatusOutOfVocab, "no lossless truck status", c.get("truckStatus"))
	}
	var legacyStatus *string
	if raw := c.str("truckStatus"); status == "available" || raw != status {
		legacyStatus = &raw // 'Available' and other outliers keep the raw literal (UNVERIFIED semantics)
	}
	if status == "available" {
		status = "active"
	}
	tenant := c.stamp(tenancy.Trucks, c.f)
	own := "own"
	if c.str("ownershipType") == "subcontractor" {
		own = "subcontractor"
	}
	plate := c.str("licensePlate")
	var dup string
	err := c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM trucks WHERE tenant_id = $1 AND license_plate = $2
		AND legacy_doc_id IS DISTINCT FROM $3`, tenant, plate, c.doc.ID).Scan(&dup)
	if err == nil {
		return c.reject("licensePlate", ReasonDuplicateNaturalKey, "the plate is already a truck of this tenant ("+dup+")", plate)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	gps := c.text("GPSVehicleId")
	if gps != nil {
		var holder string
		err := c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM trucks WHERE gps_vehicle_id = $1 AND legacy_doc_id IS DISTINCT FROM $2`,
			*gps, c.doc.ID).Scan(&holder)
		if err == nil {
			c.find("GPSVehicleId", ReasonDuplicateNaturalKey, "the GPS id already belongs to truck "+holder, *gps)
			gps = nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	pm := c.integer("pmIntervalKm")
	if pm != nil && (*pm < 1 || *pm > 500000) {
		c.find("pmIntervalKm", ReasonBadNumber, "outside 1-500000", c.get("pmIntervalKm"))
		pm = nil
	}
	r := newRow("trucks").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).set("ownership_type", own).
		set("license_plate", plate).set("province", c.text("province")).set("vin", c.text("vin")).
		set("engine_number", c.text("engineNumber")).set("gps_vehicle_id", gps).set("status", status).
		set("legacy_status", legacyStatus).set("brand", c.text("brand")).set("model", c.text("model")).set("year", *year).
		set("color", c.text("color")).set("type_raw", c.text("type")).set("vehicle_class", c.vehicleClass("type")).
		set("seats", c.integer("seats")).set("fuel_type", c.text("fuelType")).set("engine_capacity", c.float("engineCapacity")).
		set("fuel_capacity", c.float("fuelCapacity")).set("max_load_weight_kg", c.float("maxLoadWeight")).
		set("registration_date", c.date("registrationDate")).set("buying_date", c.date("buyingDate")).set("notes", c.text("notes")).
		set("tax_expiry_date", c.date("taxExpiryDate")).set("tax_expense_thb", c.money("taxExpense", false)).
		set("tax_renewal_status", c.optVocab("taxRenewalStatus", "pending", "in_progress", "completed")).
		set("last_service_date", c.date("lastServiceDate")).set("next_service_date", c.date("nextServiceDate")).
		set("next_service_mileage", c.integer("nextServiceMileage")).set("pm_interval_km", pm).
		set("current_mileage", c.integer("currentMileage")).set("last_alert_mileage", c.integer("lastAlertMileage")).
		set("insurance_policy_id", c.text("insurancePolicyId")).set("insurance_policy_number", c.text("insurancePolicyNumber")).
		set("insurance_company", c.text("insuranceCompany")).set("insurance_type", c.optVocab("insuranceType", "1", "2", "2+", "3", "3+")).
		set("insurance_start_date", c.date("insuranceStartDate")).set("insurance_expiry_date", c.date("insuranceExpiryDate")).
		set("insurance_premium_thb", c.money("insurancePremium", false)).set("insurance_notes", c.text("insuranceNotes")).
		set("insurance_renewal_status", c.optVocab("insuranceRenewalStatus", "pending", "in_progress", "completed")).
		set("payment_method", c.optVocab("paymentMethod", "cash", "transfer", "company_credit")).
		setPresent("tax_responsible", c.text("taxResponsible")).setPresent("maintenance_responsible", c.text("maintenanceResponsible"))
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	c.done("trucks", id)
	return nil
}

// loadDriver: drivers/{id} -> drivers (§A.3.4): tenant self (subcontractorId) or own fleet; a subcontractorId that
// names no carrier quarantines the driver. authId (legacy authUid) is the user link; a linked driver also gets its
// driver membership in the same transaction (C.3.6).
func loadDriver(c *docCtx) error {
	for _, f := range []string{"firstName", "lastName", "mobile"} {
		if !c.has(f) {
			return c.reject(f, ReasonMissingRequired, "a NOT NULL driver column has no value", nil)
		}
	}
	status, ok := c.vocab("status", "active", "inactive", "on_duty")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "no lossless driver status", c.get("status"))
	}
	if status == "" {
		status = "active" // the driver form's default
	}
	tenant := c.stamp(tenancy.Drivers, c.f)
	uid := c.str("authId")
	uidField := "authId"
	if uid == "" {
		uid, uidField = c.str("authUid"), "authUid"
	}
	var userID *uuid.UUID
	var authUID *string
	if uid != "" {
		var holder string
		err := c.tx.QueryRow(c.ctx, `SELECT legacy_doc_id FROM drivers WHERE legacy_auth_uid = $1 AND legacy_doc_id IS DISTINCT FROM $2`,
			uid, c.doc.ID).Scan(&holder)
		switch {
		case err == nil:
			c.find(uidField, ReasonDuplicateNaturalKey, "the auth uid already belongs to driver "+holder, uid)
		case errors.Is(err, pgx.ErrNoRows):
			authUID = &uid
			// A quarantined driver is not linked: the quarantine tenant has no memberships (C.1.8); the raw uid
			// stays in legacy_auth_uid for the link after a re-home.
			if c.outcome != Quarantined {
				if userID, err = c.user(uidField, uid); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	r := newRow("drivers").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).
		set("legacy_auth_uid", authUID).set("user_id", userID).
		set("first_name", c.text("firstName")).set("last_name", c.text("lastName")).set("full_name_th", c.text("fullNameTh")).
		set("mobile", c.text("mobile")).set("email", c.text("email")).set("birth_date", c.date("birthDate")).
		set("id_card", c.text("idCard")).set("id_card_expired_date", c.date("idCardExpiredDate")).
		set("truck_license_id", c.text("truckLicenseId")).set("license_type", c.licenseType()).
		set("license_expired_date", c.date("truckLicenseExpiredDate")).
		set("contract_years", c.float("contractYears")).set("hire_date", c.date("hireDate")).set("status", status).
		set("assign_to_project", c.text("assignToProject")).setPresent("probation_passed", c.boolean("probationPassed")).
		setPresent("employment_type", c.optVocab("employmentType", "full_time", "subcontractor", "part_time"))
	if at, ok := c.get("activeTruck").(map[string]any); ok {
		sub := &docCtx{ctx: c.ctx, tx: c.tx, e: c.e, coll: c.coll, doc: c.doc, f: at}
		tr, err := sub.truck("truckId")
		if err != nil {
			return err
		}
		for _, f := range sub.findings {
			c.find("activeTruck."+f.Field, f.Reason, f.Detail, f.Raw)
		}
		if tr.ok() {
			r.set("active_truck_id", tr.id)
		}
		r.set("active_truck_plate", sub.text("truckPlate")).set("active_started_at", sub.ts("startedAt"))
	} else {
		r.set("active_truck_id", nil).set("active_truck_plate", nil).set("active_started_at", nil)
	}
	c.times(r)
	id, err := c.rowID("drivers")
	if err != nil {
		return err
	}
	files := [][3]string{{"profileImage", "driver_profile", "profile_file_id"}, {"idCardImage", "driver_id_card", "id_card_file_id"},
		{"truckLicenseImage", "driver_license", "license_file_id"}}
	for _, f := range files {
		fid, err := c.fileRef(f[0], c.get(f[0]), f[1], "driver", id, tenant)
		if err != nil {
			return err
		}
		r.set(f[2], fid)
	}
	if _, err := upsertLegacy(c, r.set("id", id)); err != nil {
		return err
	}
	if userID != nil {
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'driver')
			ON CONFLICT (user_id, tenant_id) DO NOTHING`, *userID, tenant); err != nil {
			return fmt.Errorf("etl: %s: driver membership: %w", c.doc.Path, err)
		}
	}
	c.done("drivers", id)
	return nil
}

// licenseType keeps the Thai licence classes exactly (บ.1 ... ท.4); anything else is NULL + a field finding.
func (c *docCtx) licenseType() *string {
	s := c.str("licenseType")
	if s == "" {
		return nil
	}
	if slices.Contains([]string{"บ.1", "บ.2", "บ.3", "บ.4", "ท.1", "ท.2", "ท.3", "ท.4"}, s) {
		return &s
	}
	c.find("licenseType", ReasonStatusOutOfVocab, "not a Thai licence class", s)
	return nil
}
