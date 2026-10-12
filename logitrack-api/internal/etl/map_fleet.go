package etl

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// The parts of trucks and drivers T15 left to T24, the truck assignments, the driver-app installations and the
// latest vehicle positions (Appendix A §A.3.4).

// truckFileFields are the single-URL truck fields: {field, file purpose, truck_files.kind} (§A.3.7).
var truckFileFields = [][3]string{
	{"imageFrontRight", "truck_photo", "image_front_right"}, {"imageFrontLeft", "truck_photo", "image_front_left"},
	{"imageBackRight", "truck_photo", "image_back_right"}, {"imageBackLeft", "truck_photo", "image_back_left"},
	{"documentTax", "truck_document", "document_tax"}, {"documentRegister", "truck_document", "document_register"},
	{"taxReceipt", "truck_receipt", "tax_receipt"}, {"insuranceReceipt", "truck_receipt", "insurance_receipt"},
}

// truckFileRows replaces truck_files from the image, document and receipt fields, insuranceDocuments[] and the legacy
// images[] (§A.3.4, §A.3.7); a key referenced twice keeps its first kind.
func (c *docCtx) truckFileRows(id, tenant uuid.UUID) error {
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM truck_files WHERE truck_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	pos := 0
	put := func(field string, url any, purpose, kind string) error {
		fid, err := c.fileRef(field, url, purpose, "truck", id, tenant)
		if err != nil || fid == nil {
			return err
		}
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO truck_files (truck_id, file_id, kind, position) VALUES ($1, $2, $3, $4)
			ON CONFLICT (truck_id, file_id) DO NOTHING`, id, *fid, kind, pos); err != nil {
			return fmt.Errorf("etl: %s: truck file: %w", c.doc.Path, err)
		}
		pos++
		return nil
	}
	for _, f := range truckFileFields {
		if err := put(f[0], c.get(f[0]), f[1], f[2]); err != nil {
			return err
		}
	}
	for _, arr := range [][3]string{{"insuranceDocuments", "insurance_document", "insurance_document"}, {"images", "truck_photo", "legacy_image"}} {
		list, _ := c.get(arr[0]).([]any)
		for i, u := range list {
			if err := put(fmt.Sprintf("%s[%d]", arr[0], i), u, arr[1], arr[2]); err != nil {
				return err
			}
		}
	}
	return nil
}

// truckStatusHistory appends trucks statusHistory[] {status, date ISO string, changedBy displayName/email, notes}
// (Q18): date -> changed_at, changedBy -> changed_by_label, notes -> reason; free-text statuses ('Tax Renewed') kept.
func (c *docCtx) truckStatusHistory(id uuid.UUID) error {
	list, _ := c.get("statusHistory").([]any)
	var entries []statusEntry
	for i, e := range list {
		m, _ := e.(map[string]any)
		s := c.sub(m)
		field := fmt.Sprintf("statusHistory[%d]", i)
		status := s.str("status")
		at, ok := toTime(m["date"])
		if status == "" || !ok {
			c.find(field, ReasonBadTimestamp, "a status entry needs status and a parseable date; not loaded", e)
			continue
		}
		entries = append(entries, statusEntry{status: status, at: at.Truncate(time.Microsecond), label: s.str("changedBy"),
			reason: s.str("notes"), raw: e})
	}
	return c.appendStatusHistory("truck", id, entries)
}

// driverStatusHistory appends drivers statusHistory[] {status, changedAt, changedBy uid|'system', changedByName,
// reason, previousStatus}: a uid -> changed_by_user_id (user_unresolved when none), 'system' or the name ->
// changed_by_label.
func (c *docCtx) driverStatusHistory(id uuid.UUID) error {
	list, _ := c.get("statusHistory").([]any)
	var entries []statusEntry
	for i, e := range list {
		m, _ := e.(map[string]any)
		s := c.sub(m)
		field := fmt.Sprintf("statusHistory[%d]", i)
		status := s.str("status")
		at, ok := toTime(m["changedAt"])
		if status == "" || !ok {
			c.find(field, ReasonBadTimestamp, "a status entry needs status and a parseable changedAt; not loaded", e)
			continue
		}
		entry := statusEntry{status: status, previous: s.str("previousStatus"), reason: s.str("reason"), at: at.Truncate(time.Microsecond), raw: e}
		switch by := s.str("changedBy"); by {
		case "", "system":
			entry.label = by
		default:
			u, err := c.userByUID(by)
			if err != nil {
				return err
			}
			if u == nil {
				c.find(field+".changedBy", ReasonUserUnresolved, "matches no users.legacy_auth_uid; the name is the label", by)
				entry.label = s.str("changedByName")
				if entry.label == "" {
					entry.label = by
				}
			}
			entry.user = u
		}
		entries = append(entries, entry)
	}
	return c.appendStatusHistory("driver", id, entries)
}

// driverCustomerCodes replaces driver_customer_codes from customerDriverIds{customerCode: {idTypeKey: value}}: an
// unknown customer code drops its entries with customer_unresolved.
func (c *docCtx) driverCustomerCodes(id uuid.UUID) error {
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM driver_customer_codes WHERE driver_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	codes, _ := c.get("customerDriverIds").(map[string]any)
	keys := make([]string, 0, len(codes))
	for k := range codes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, code := range keys {
		vals, _ := codes[code].(map[string]any)
		var cust uuid.UUID
		err := c.tx.QueryRow(c.ctx, `SELECT id FROM customers WHERE code = $1`, strings.ToUpper(strings.TrimSpace(code))).Scan(&cust)
		if errors.Is(err, pgx.ErrNoRows) {
			c.find("customerDriverIds."+code, ReasonCustomerUnresolved, "matches no customers.code; entries dropped", codes[code])
			continue
		}
		if err != nil {
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
		types := make([]string, 0, len(vals))
		for k := range vals {
			types = append(types, k)
		}
		slices.Sort(types)
		for _, k := range types {
			v, _ := vals[k].(string)
			if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
				continue
			}
			if _, err := c.tx.Exec(c.ctx, `INSERT INTO driver_customer_codes (driver_id, customer_id, id_type_key, value) VALUES ($1, $2, $3, $4)
				ON CONFLICT DO NOTHING`, id, cust, k, v); err != nil {
				return fmt.Errorf("etl: %s: customer driver id: %w", c.doc.Path, err)
			}
		}
	}
	return nil
}

// loadTruckAssignment: truckAssignment/{id} -> truck_assignments (§A.3.4). The tenant is the truck's (truck, main spec
// §3.3); truck_id and driver_id are NOT NULL without a quarantine exception, so an unresolved truck or driver, or a
// truck in the quarantine tenant, rejects the row (assignment history carries no billing evidence). adminName ->
// admin_label.
func loadTruckAssignment(c *docCtx) error {
	tr, err := c.truckByRef(c.str("truckId"))
	if err != nil {
		return err
	}
	if !tr.ok() {
		return c.reject("truckId", ReasonTruckUnresolved, "no truck with this doc id; truck_assignments.truck_id is NOT NULL", c.get("truckId"))
	}
	if c.quarantined(tr) {
		return c.reject("truckId", ReasonTenantUnresolved, "the truck is in the quarantine tenant; retry after it is re-homed", c.get("truckId"))
	}
	drv, err := c.driverByRef(c.str("driverId"), "")
	if err != nil {
		return err
	}
	if !drv.ok() {
		return c.reject("driverId", ReasonDriverUnresolved, "no driver with this doc id; truck_assignments.driver_id is NOT NULL", c.get("driverId"))
	}
	status, ok := c.vocab("status", "active", "revoked", "cancelled")
	switch {
	case !ok:
		return c.reject("status", ReasonStatusOutOfVocab, "neither active, revoked nor cancelled", c.get("status"))
	case status == "":
		return c.reject("status", ReasonMissingRequired, "truck_assignments.status is NOT NULL", nil)
	}
	cur, found, err := c.stampedRow("truck_assignments")
	if err != nil {
		return err
	}
	tenant := c.settle(cur, found, tr.tenant, tenancy.SourceTruck, "")
	admin, err := c.user("adminId", c.str("adminId"))
	if err != nil {
		return err
	}
	r := newRow("truck_assignments").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).set("truck_id", tr.id).
		set("driver_id", drv.id).set("status", status).set("admin_user_id", admin).setPresent("admin_label", c.text("adminName")).
		set("revoked_at", c.ts("revokedAt"))
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	c.done("truck_assignments", id)
	return nil
}

// appliedDocsOr lists the documents of coll whose path the run applied or whose raw value at jsonPath (an assignment
// or task doc id) is one of refs: the parents an after-hook links.
func (r *runState) appliedDocsOr(coll string, path []string, refs []string) (map[string]map[string]any, error) {
	paths := make([]string, 0, len(r.applied[coll]))
	for _, d := range r.applied[coll] {
		paths = append(paths, d.Path)
	}
	if len(paths) == 0 && len(refs) == 0 {
		return nil, nil
	}
	rows, err := r.tx.Query(r.ctx, `SELECT doc_id, raw FROM etl.source_docs WHERE collection = $1
		AND status IN ('loaded', 'quarantined') AND (doc_path = ANY($2::text[]) OR raw #>> $3::text[] = ANY($4::text[]))`, coll, paths, path, refs)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		f, err := dump.UnmarshalFields(raw)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = f
	}
	return out, rows.Err()
}

func docIDs(r *runState, coll string) []string {
	out := make([]string, 0, len(r.applied[coll]))
	for _, d := range r.applied[coll] {
		out = append(out, d.ID)
	}
	return out
}

// afterTruckAssignments sets drivers.current_assignment_id (the home truck) from currentAssignment.assignmentId, by
// truckAssignment doc id, for the drivers or assignments this run applied (§A.3.4: back-filled after the
// assignments, main spec §13.9); a driver document without currentAssignment clears it.
func afterTruckAssignments(r *runState, _ string) error {
	drivers, err := r.appliedDocsOr("drivers", []string{"currentAssignment", "assignmentId"}, docIDs(r, "truckAssignment"))
	if err != nil {
		return err
	}
	for _, id := range sortedDocKeys(drivers) {
		m, _ := drivers[id]["currentAssignment"].(map[string]any)
		ref, _ := m["assignmentId"].(string)
		if _, err := r.tx.Exec(r.ctx, `UPDATE drivers d SET current_assignment_id = a.id
			FROM (SELECT (SELECT id FROM truck_assignments WHERE legacy_doc_id = nullif($2, '')) AS id) a
			WHERE d.legacy_doc_id = $1 AND d.current_assignment_id IS DISTINCT FROM a.id`, id, strings.TrimSpace(ref)); err != nil {
			return fmt.Errorf("driver %s: current assignment: %w", id, err)
		}
	}
	return nil
}

var taskNoPattern = regexp.MustCompile(`^(FM|LH)-([0-9]{2})([0-9]{2})([0-9]{4})-([0-9]+)$`)

// maxCounterSeq is operations.MaxBumpSeq: a legacy number past it is loaded verbatim but seeds no counter, so a planted
// FM-...-2147483647 cannot push task_number_counters to the int ceiling, where next_task_seq() fails every later
// creation of that day for every tenant (T31 review).
const maxCounterSeq = 99999

// afterTasks seeds task_number_counters with the highest legacy suffix per (type, day encoded in the number), so the
// first Go-minted number continues after it (§A.3.5, R10; numbering is global across tenants), and sets
// drivers.active_task_id from activeTruck.taskId (tasks doc id, else a unique task number) for the drivers or tasks
// this run applied.
func afterTasks(r *runState, _ string) error {
	type key struct {
		typ string
		day time.Time
	}
	max := map[key]int64{}
	for _, d := range r.applied["tasks"] {
		no, _ := d.Fields["taskId"].(string)
		m := taskNoPattern.FindStringSubmatch(strings.TrimSpace(no))
		if m == nil {
			continue
		}
		dd, _ := strconv.Atoi(m[2])
		mm, _ := strconv.Atoi(m[3])
		yy, _ := strconv.Atoi(m[4])
		day := time.Date(yy, time.Month(mm), dd, 0, 0, 0, 0, time.UTC)
		seq, err := strconv.ParseInt(m[5], 10, 32)
		if err != nil || day.Day() != dd || int(day.Month()) != mm || seq > maxCounterSeq {
			continue
		}
		k := key{map[string]string{"FM": "first_mile", "LH": "line_haul"}[m[1]], day}
		if seq > max[k] {
			max[k] = seq
		}
	}
	keys := make([]key, 0, len(max))
	for k := range max {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b key) int {
		if c := strings.Compare(a.typ, b.typ); c != 0 {
			return c
		}
		return a.day.Compare(b.day)
	})
	for _, k := range keys {
		if _, err := r.tx.Exec(r.ctx, `INSERT INTO task_number_counters AS c (task_type, plan_date, last_seq) VALUES ($1, $2, $3)
			ON CONFLICT (task_type, plan_date) DO UPDATE SET last_seq = EXCLUDED.last_seq WHERE c.last_seq < EXCLUDED.last_seq`,
			k.typ, k.day, max[k]); err != nil {
			return fmt.Errorf("task counter: %w", err)
		}
	}
	drivers, err := r.appliedDocsOr("drivers", []string{"activeTruck", "taskId"}, docIDs(r, "tasks"))
	if err != nil {
		return err
	}
	for _, id := range sortedDocKeys(drivers) {
		m, _ := drivers[id]["activeTruck"].(map[string]any)
		ref, _ := m["taskId"].(string)
		if _, err := r.tx.Exec(r.ctx, `UPDATE drivers d SET active_task_id = t.id
			FROM (SELECT coalesce((SELECT id FROM tasks WHERE legacy_doc_id = nullif($2, '')),
			  (SELECT min(id::text)::uuid FROM tasks WHERE task_no = nullif($2, '') HAVING count(*) = 1)) AS id) t
			WHERE d.legacy_doc_id = $1 AND d.active_task_id IS DISTINCT FROM t.id`, id, strings.TrimSpace(ref)); err != nil {
			return fmt.Errorf("driver %s: active task: %w", id, err)
		}
	}
	return nil
}

func sortedDocKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// loadInstallation: drivers/{id}/mobile_installations/{installId} (collection group) -> mobile_installations
// (§A.3.4): the driver from the parent path (doc id) and the tenant from the driver. driver_id is part of the primary
// key without a quarantine exception, so an installation under a deleted driver is rejected (driver_unresolved), and
// one under a quarantined driver too (tenant_unresolved: retried after the driver's re-home). A partnerId (” = own
// fleet) that disagrees with the driver's tenant is reported (tenant_mismatch) and not stored.
func loadInstallation(c *docCtx) error {
	parent := c.doc.ParentPath()
	driverDoc, ok := strings.CutPrefix(parent, "drivers/")
	if !ok || driverDoc == "" || strings.Contains(driverDoc, "/") {
		return c.reject("", ReasonDriverUnresolved, "an installation lives under drivers/{id}", parent)
	}
	drv, err := c.one(`SELECT id, tenant_id FROM drivers WHERE legacy_doc_id = $1`, driverDoc)
	if err != nil {
		return err
	}
	if !drv.ok() {
		return c.reject("driverId", ReasonDriverUnresolved, "the parent driver document did not load; driver_id is part of the key", driverDoc)
	}
	if c.quarantined(drv) {
		return c.reject("driverId", ReasonTenantUnresolved, "the driver is in the quarantine tenant; retry after its re-home", driverDoc)
	}
	if p, isStr := c.get("partnerId").(string); isStr {
		want := c.ownFleet()
		if p = strings.TrimSpace(p); p != "" {
			t, err := c.one(`SELECT id, id FROM tenants WHERE legacy_doc_id = $1`, p)
			if err != nil {
				return err
			}
			want = t.id
		}
		if want != drv.tenant {
			c.find("partnerId", ReasonTenantMismatch, "the installation's partnerId disagrees with the driver's tenant; not stored", p)
		}
	}
	platform, ok := lowerVocab(c.get("platform"), "ios", "android", "other")
	if !ok {
		c.find("platform", ReasonStatusOutOfVocab, "neither ios, android nor other; other kept", c.get("platform"))
		platform = "other"
	}
	if platform == "" {
		platform = "other"
	}
	flavor, ok := lowerVocab(c.get("flavor"), "dev", "prod")
	if !ok {
		c.find("flavor", ReasonStatusOutOfVocab, "neither dev nor prod; dev kept", c.get("flavor"))
		flavor = ""
	}
	if flavor == "" {
		flavor = "dev"
	}
	last := c.ts("lastSeenAt")
	if last == nil {
		t := c.doc.UpdateTime.Truncate(time.Microsecond)
		last = &t
	}
	first := c.ts("createdAt")
	if first == nil && !c.doc.CreateTime.IsZero() {
		t := c.doc.CreateTime.Truncate(time.Microsecond)
		first = &t
	}
	if first == nil || first.After(*last) {
		first = last
	}
	cur, found, err := c.stampedRowWhere("mobile_installations", "driver_id = $1 AND install_id = $2", drv.id, c.doc.ID)
	if err != nil {
		return err
	}
	tenant := c.settle(cur, found, drv.tenant, tenancy.SourceDriver, "")
	if _, err := c.tx.Exec(c.ctx, `INSERT INTO mobile_installations (driver_id, install_id, tenant_id, tenant_source, platform, app_version,
		  build_number, flavor, first_seen_at, last_seen_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (driver_id, install_id) DO UPDATE SET platform = EXCLUDED.platform, app_version = EXCLUDED.app_version,
		  build_number = EXCLUDED.build_number, flavor = EXCLUDED.flavor, last_seen_at = EXCLUDED.last_seen_at`,
		drv.id, c.doc.ID, tenant, string(c.tenantSource), platform, c.text("appVersion"), c.text("buildNumber"), flavor, *first, *last); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	c.table, c.target = "mobile_installations", drv.id.String()+"/"+c.doc.ID
	return nil
}

// loadVehicleLocation: vehicle_locations/{GPSVehicleId} -> vehicle_locations (§A.3.4): the latest position only, the
// truck by trucks.gps_vehicle_id; truck_id is the primary key without a quarantine exception, so no matching truck
// (or a quarantined one) rejects the row (refreshed by cartrack.sync every 3 minutes from P6, R70).
func loadVehicleLocation(c *docCtx) error {
	gps := c.str("GPSVehicleId")
	if gps == "" {
		gps = c.doc.ID
	}
	tr, err := c.one(`SELECT id, tenant_id FROM trucks WHERE gps_vehicle_id = $1`, gps)
	if err != nil {
		return err
	}
	if !tr.ok() {
		return c.reject("GPSVehicleId", ReasonTruckUnresolved, "no truck has this GPS vehicle id", gps)
	}
	if c.quarantined(tr) {
		return c.reject("GPSVehicleId", ReasonTenantUnresolved, "the truck is in the quarantine tenant", gps)
	}
	lat, lng := c.float("lat"), c.float("lng")
	if lat == nil || lng == nil {
		return c.reject("lat", ReasonMissingRequired, "vehicle_locations.lat and lng are NOT NULL", nil)
	}
	cur, found, err := c.stampedRowWhere("vehicle_locations", "truck_id = $1", tr.id)
	if err != nil {
		return err
	}
	tenant := c.settle(cur, found, tr.tenant, tenancy.SourceTruck, "")
	r := newRow("vehicle_locations").set("truck_id", tr.id).set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).
		set("gps_vehicle_id", gps).set("license_plate", c.text("licensePlate")).set("lat", *lat).set("lng", *lng).
		set("speed_kmh", c.float("speed")).set("heading", c.float("heading")).set("engine_on", c.boolean("engineOn")).
		set("position_description", c.text("positionDescription")).set("driver_name_raw", c.text("driverName")).
		set("last_gps_at", c.ts("lastGPSTime")).set("odometer_km", c.float("odometerKm")).set("fuel_level", c.float("fuelLevel")).
		set("fuel_percentage", c.float("fuelPercentage")).setPresent("updated_at", c.ts("updatedAt"))
	sets := make([]string, 0, len(r.cols))
	for _, col := range r.cols {
		if col != "truck_id" {
			q := pgx.Identifier{col}.Sanitize()
			sets = append(sets, q+" = EXCLUDED."+q)
		}
	}
	if _, err := c.tx.Exec(c.ctx, r.insertSQL()+` ON CONFLICT (truck_id) DO UPDATE SET `+strings.Join(sets, ", "), r.vals...); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	c.table, c.target = "vehicle_locations", tr.id.String()
	return nil
}
