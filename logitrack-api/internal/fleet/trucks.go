package fleet

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/fleetdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/vehicleclass"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Vocabularies of the trucks CHECKs (Appendix A §A.2.2), canonical lower_snake (R65).
var (
	TruckStatuses   = []string{"active", "inactive", "maintenance", "insurance_claim", "sold"}
	RenewalStatuses = []string{"pending", "in_progress", "completed"}
	InsuranceTypes  = []string{"1", "2", "2+", "3", "3+"}
	PaymentMethods  = []string{"cash", "transfer", "company_credit"}
	Ownerships      = []string{OwnershipOwn, OwnershipSubcontractor}
)

// Ownership types (trucks.ownership_type), derived from the truck's tenant: the own fleet's trucks are own, a
// carrier's are subcontractor (tenancy.Resolve "trucks": self).
const (
	OwnershipOwn           = "own"
	OwnershipSubcontractor = "subcontractor"
)

// Defaults of the two NOT NULL responsibility columns (the truck form's defaults).
const (
	DefaultTaxResponsible         = "Operation Admin"
	DefaultMaintenanceResponsible = "Driver"
)

// Truck is a truck as GET /v1/trucks lists it and every truck payload starts with. Dates are yyyy-MM-dd; money is
// THB with two decimals. taxExpenseThb, insurancePremiumThb and paymentMethod are cost fields: null unless the
// principal also holds fleet:view_renewals, fleet:manage_renewals or fleet:edit_truck (fleet:view_trucks is the
// non-financial view, Appendix C §C.2.3 row 1).
type Truck struct {
	ID                     uuid.UUID  `json:"id"`
	TenantID               uuid.UUID  `json:"tenantId"`
	OwnershipType          string     `json:"ownershipType"`
	LicensePlate           string     `json:"licensePlate"`
	Province               string     `json:"province"`
	VIN                    *string    `json:"vin"`
	EngineNumber           *string    `json:"engineNumber"`
	GPSVehicleID           *string    `json:"gpsVehicleId"`
	Status                 string     `json:"status"`
	LegacyStatus           *string    `json:"legacyStatus"`
	Brand                  string     `json:"brand"`
	Model                  string     `json:"model"`
	Year                   int16      `json:"year"`
	Color                  string     `json:"color"`
	Type                   string     `json:"type"`
	VehicleClass           *string    `json:"vehicleClass"`
	Seats                  *int16     `json:"seats"`
	FuelType               *string    `json:"fuelType"`
	EngineCapacity         *float64   `json:"engineCapacity"`
	FuelCapacity           *float64   `json:"fuelCapacity"`
	MaxLoadWeightKg        *float64   `json:"maxLoadWeightKg"`
	RegistrationDate       *string    `json:"registrationDate"`
	BuyingDate             *string    `json:"buyingDate"`
	Notes                  *string    `json:"notes"`
	TaxExpiryDate          *string    `json:"taxExpiryDate"`
	TaxExpenseThb          *float64   `json:"taxExpenseThb"`
	TaxResponsible         string     `json:"taxResponsible"`
	TaxRenewalStatus       *string    `json:"taxRenewalStatus"`
	MaintenanceResponsible string     `json:"maintenanceResponsible"`
	LastServiceDate        *string    `json:"lastServiceDate"`
	NextServiceDate        *string    `json:"nextServiceDate"`
	NextServiceMileage     *int32     `json:"nextServiceMileage"`
	PMIntervalKm           *int32     `json:"pmIntervalKm"`
	CurrentMileage         *int32     `json:"currentMileage"`
	LastAlertMileage       *int32     `json:"lastAlertMileage"`
	ActiveMaintenanceID    *uuid.UUID `json:"activeMaintenanceId"`
	InsurancePolicyID      *string    `json:"insurancePolicyId"`
	InsurancePolicyNumber  *string    `json:"insurancePolicyNumber"`
	InsuranceCompany       *string    `json:"insuranceCompany"`
	InsuranceType          *string    `json:"insuranceType"`
	InsuranceStartDate     *string    `json:"insuranceStartDate"`
	InsuranceExpiryDate    *string    `json:"insuranceExpiryDate"`
	InsurancePremiumThb    *float64   `json:"insurancePremiumThb"`
	InsuranceNotes         *string    `json:"insuranceNotes"`
	InsuranceRenewalStatus *string    `json:"insuranceRenewalStatus"`
	PaymentMethod          *string    `json:"paymentMethod"`
	CreatedBy              *uuid.UUID `json:"createdBy"`
	UpdatedBy              *uuid.UUID `json:"updatedBy"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
}

// StatusEntry is one status_history row of a truck (append-only). Renewals append tax_renewed / insurance_renewed;
// rows the ETL loaded keep their legacy text ("Tax Renewed").
type StatusEntry struct {
	Status         string     `json:"status"`
	PreviousStatus *string    `json:"previousStatus"`
	ChangedAt      time.Time  `json:"changedAt"`
	ChangedBy      *uuid.UUID `json:"changedBy"`
	ChangedByLabel *string    `json:"changedByLabel"`
	Reason         *string    `json:"reason"`
}

// Assignment is an active truck_assignments row of the truck (the current assignment is derived, R14).
type Assignment struct {
	ID         uuid.UUID `json:"id"`
	DriverID   uuid.UUID `json:"driverId"`
	DriverName *string   `json:"driverName"`
	AssignedAt time.Time `json:"assignedAt"`
}

// TruckFile is one linked file: the key goes back in a PATCH, the URL is a short-lived presigned GET.
type TruckFile struct {
	Key string  `json:"key"`
	URL *string `json:"url"`
}

// TruckDetail is GET /v1/trucks/{id} and the answer of every truck write: the truck, its whole status history
// (newest first), its current assignments and its files by kind (only kinds that hold a file).
type TruckDetail struct {
	Truck
	StatusHistory      []StatusEntry          `json:"statusHistory"`
	CurrentAssignments []Assignment           `json:"currentAssignments"`
	Files              map[string][]TruckFile `json:"files"`
}

// TruckScope is a truck as a dispatcher reads it (the scope_trucks projection, Appendix C §C.3.7): no insurance, tax,
// cost or document column.
type TruckScope struct {
	ID           uuid.UUID `json:"id"`
	TenantID     uuid.UUID `json:"tenantId"`
	LicensePlate string    `json:"licensePlate"`
	Province     string    `json:"province"`
	VehicleClass *string   `json:"vehicleClass"`
	Type         string    `json:"type"`
	Status       string    `json:"status"`
}

// seesCost: the cost columns need more than fleet:view_trucks.
func seesCost(p *authz.Principal) bool {
	return p.Can(authz.FleetViewRenewals) || p.Can(authz.FleetManageRenewals) || p.Can(authz.FleetEditTruck)
}

func truckOf(t fleetdb.Truck, cost bool) Truck {
	out := Truck{ID: t.ID, TenantID: t.TenantID, OwnershipType: t.OwnershipType, LicensePlate: t.LicensePlate,
		Province: t.Province, VIN: t.Vin, EngineNumber: t.EngineNumber, GPSVehicleID: t.GpsVehicleID, Status: t.Status,
		LegacyStatus: t.LegacyStatus, Brand: t.Brand, Model: t.Model, Year: t.Year, Color: t.Color, Type: t.TypeRaw,
		VehicleClass: t.VehicleClass, Seats: t.Seats, FuelType: t.FuelType, EngineCapacity: t.EngineCapacity,
		FuelCapacity: t.FuelCapacity, MaxLoadWeightKg: t.MaxLoadWeightKg, RegistrationDate: dateString(t.RegistrationDate),
		BuyingDate: dateString(t.BuyingDate), Notes: t.Notes, TaxExpiryDate: dateString(t.TaxExpiryDate),
		TaxResponsible: t.TaxResponsible, TaxRenewalStatus: t.TaxRenewalStatus, MaintenanceResponsible: t.MaintenanceResponsible,
		LastServiceDate: dateString(t.LastServiceDate), NextServiceDate: dateString(t.NextServiceDate),
		NextServiceMileage: t.NextServiceMileage, PMIntervalKm: t.PmIntervalKm, CurrentMileage: t.CurrentMileage,
		LastAlertMileage: t.LastAlertMileage, ActiveMaintenanceID: t.ActiveMaintenanceID, InsurancePolicyID: t.InsurancePolicyID,
		InsurancePolicyNumber: t.InsurancePolicyNumber, InsuranceCompany: t.InsuranceCompany, InsuranceType: t.InsuranceType,
		InsuranceStartDate: dateString(t.InsuranceStartDate), InsuranceExpiryDate: dateString(t.InsuranceExpiryDate),
		InsuranceNotes: t.InsuranceNotes, InsuranceRenewalStatus: t.InsuranceRenewalStatus, CreatedBy: t.CreatedBy,
		UpdatedBy: t.UpdatedBy, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
	if cost {
		out.TaxExpenseThb, out.InsurancePremiumThb, out.PaymentMethod = t.TaxExpenseThb, t.InsurancePremiumThb, t.PaymentMethod
	}
	return out
}

// --- list --------------------------------------------------------------------------------------------------

// ListTrucksInput are the query parameters of GET /v1/trucks.
type ListTrucksInput struct {
	Ownership, TenantID, Status, Type, Q string
	Limit                                int
	Cursor                               string
}

// ListTrucks is GET /v1/trucks (fleet:view_trucks | dispatch:view_operations), keyset, no cap (the web's silent cap of
// 100, useTrucksList.ts:27-29, goes with it). Staff get []Truck, newest first; a dispatcher (or a customer-scope
// principal) gets []TruckScope from the scope_trucks projection, ordered by plate (Appendix C §C.3.7, R85).
func (s *Service) ListTrucks(ctx context.Context, p *authz.Principal, in ListTrucksInput) (any, string, error) {
	var ck checker
	opt := func(field, v string, allowed []string) *string {
		if v == "" {
			return nil
		}
		if allowed != nil && !slices.Contains(allowed, v) {
			ck.add(field, "invalid")
		}
		return &v
	}
	ownership := opt("ownership", in.Ownership, Ownerships)
	status := opt("status", in.Status, TruckStatuses)
	typ, q := searchParam(&ck, "type", in.Type), searchParam(&ck, "q", in.Q)
	var tenant *uuid.UUID
	if in.TenantID != "" {
		id, err := uuid.Parse(in.TenantID)
		if err != nil {
			ck.add("tenantId", "invalid")
		}
		tenant = &id
	}
	limit := pageLimit(&ck, in.Limit)
	scoped := (p.IsCustomerScope() || p.Dispatcher) && !p.ActOnAll
	var after cursor
	if in.Cursor != "" {
		var ok bool
		after, ok = decodeCursor(in.Cursor)
		if !ok || (scoped && after.S == nil) || (!scoped && after.T == nil) {
			ck.add("cursor", "invalid")
		}
	}
	if scoped && ownership != nil {
		ck.add("ownership", "not_available")
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	if scoped {
		return s.listScopeTrucks(ctx, p, scopedb.ListScopeTrucksParams{TenantID: tenant, Status: status, TruckType: typ,
			Q: q, AfterPlate: after.S, AfterID: after.ID, RowLimit: int32(limit) + 1}, limit)
	}
	all, tenants := p.TenantReach()
	var rows []fleetdb.Truck
	err := s.tx(ctx, p, func(_ pgx.Tx, qq *fleetdb.Queries) (err error) {
		rows, err = qq.ListTrucks(ctx, fleetdb.ListTrucksParams{AllTenants: all, TenantIds: tenants, TenantID: tenant,
			Ownership: ownership, Status: status, TruckType: typ, Q: q, AfterCreated: after.T, AfterID: after.ID,
			RowLimit: int32(limit) + 1})
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit { // one row more was asked for: there is a next page
		rows = rows[:limit]
		last := rows[limit-1]
		t := last.CreatedAt
		next = cursor{T: &t, ID: last.ID}.encode()
	}
	cost := seesCost(p)
	out := make([]Truck, 0, len(rows))
	for _, r := range rows {
		out = append(out, truckOf(r, cost))
	}
	return out, next, nil
}

func (s *Service) listScopeTrucks(ctx context.Context, p *authz.Principal, arg scopedb.ListScopeTrucksParams, limit int) (any, string, error) {
	var rows []scopedb.ScopeTruck
	err := mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) (err error) {
		rows, err = scopedb.New(tx).ListScopeTrucks(ctx, arg)
		return err
	}))
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		plate := last.LicensePlate
		next = cursor{S: &plate, ID: last.ID}.encode()
	}
	out := make([]TruckScope, 0, len(rows))
	for _, r := range rows {
		out = append(out, TruckScope{ID: r.ID, TenantID: r.TenantID, LicensePlate: r.LicensePlate, Province: r.Province,
			VehicleClass: r.VehicleClass, Type: r.TypeRaw, Status: r.Status})
	}
	return out, next, nil
}

// --- detail ------------------------------------------------------------------------------------------------

// GetTruck is GET /v1/trucks/{id} (fleet:view_trucks): a truck of the caller's tenant reach (named in the query and
// checked by RLS), its status history, its current assignments and its files; the tax and insurance receipts only
// for a caller who also sees the cost fields. The file URLs are signed after the transaction: Files.SignedFile takes
// its own pool connection.
func (s *Service) GetTruck(ctx context.Context, p *authz.Principal, id uuid.UUID) (*TruckDetail, error) {
	var t fleetdb.Truck
	var hist []fleetdb.TruckStatusHistoryRow
	var asg []fleetdb.TruckCurrentAssignmentsRow
	var files []fleetdb.TruckFilesRow
	all, tenants := p.TenantReach()
	err := s.tx(ctx, p, func(_ pgx.Tx, q *fleetdb.Queries) error {
		var err error
		t, err = q.GetTruck(ctx, fleetdb.GetTruckParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if hist, err = q.TruckStatusHistory(ctx, id); err != nil {
			return err
		}
		if asg, err = q.TruckCurrentAssignments(ctx, id); err != nil {
			return err
		}
		files, err = q.TruckFiles(ctx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	cost := seesCost(p)
	d := &TruckDetail{Truck: truckOf(t, cost), StatusHistory: make([]StatusEntry, 0, len(hist)),
		CurrentAssignments: make([]Assignment, 0, len(asg)), Files: map[string][]TruckFile{}}
	for _, h := range hist {
		d.StatusHistory = append(d.StatusHistory, StatusEntry{Status: h.Status, PreviousStatus: h.PreviousStatus,
			ChangedAt: h.ChangedAt, ChangedBy: h.ChangedByUserID, ChangedByLabel: h.ChangedByLabel, Reason: h.Reason})
	}
	for _, a := range asg {
		var name *string
		if a.DriverName != "" {
			n := a.DriverName
			name = &n
		}
		d.CurrentAssignments = append(d.CurrentAssignments, Assignment{ID: a.ID, DriverID: a.DriverID, DriverName: name,
			AssignedAt: a.CreatedAt})
	}
	for _, f := range files {
		kind, ok := kindByDB[f.Kind]
		if !ok || s.files == nil || (kind.financial && !cost) { // receipts follow the cost fields
			continue
		}
		key, url, err := s.files.SignedFile(ctx, f.FileID, 0)
		if err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
			s.log.Warn().Err(err).Str("file_id", f.FileID.String()).Msg("cannot sign a truck file URL")
		}
		if key == "" {
			continue
		}
		tf := TruckFile{Key: key}
		if url != "" {
			tf.URL = &url
		}
		d.Files[kind.json] = append(d.Files[kind.json], tf)
	}
	return d, nil
}

// --- files ---------------------------------------------------------------------------------------------------

// fileKind is one truck_files.kind: its JSON name, whether it holds at most one file, the upload purposes it
// accepts (internal/storage.Purposes) and whether it is financial: the tax and insurance receipts show the amounts
// that the cost fields hide, so they follow the same rule (seesCost) in the payload, and they take only the
// truck_receipt purpose, whose GET /v1/files rule needs fleet:view_renewals.
type fileKind struct {
	json, db  string
	single    bool
	purposes  []string
	financial bool
}

// MaxFilesPerKind bounds the files one request newly links to a multi-file kind; files the truck already links do
// not count, so a truck that holds more (renewals append without a cap) still takes its own payload back in a PATCH.
const MaxFilesPerKind = 20

// MaxFileKeys bounds the keys of one request's files, as a guard on the request's size.
const MaxFileKeys = 1000

var fileKinds = []fileKind{
	{"imageFrontRight", "image_front_right", true, []string{"truck_photo"}, false},
	{"imageFrontLeft", "image_front_left", true, []string{"truck_photo"}, false},
	{"imageBackRight", "image_back_right", true, []string{"truck_photo"}, false},
	{"imageBackLeft", "image_back_left", true, []string{"truck_photo"}, false},
	{"documentTax", "document_tax", true, []string{"truck_document", "truck_receipt"}, false},
	{"documentRegister", "document_register", true, []string{"truck_document"}, false},
	{"taxReceipt", "tax_receipt", false, []string{"truck_receipt"}, true},
	{"insuranceReceipt", "insurance_receipt", false, []string{"truck_receipt"}, true},
	{"insuranceDocument", "insurance_document", false, []string{"insurance_document", "truck_document"}, false},
	{"legacyImage", "legacy_image", false, []string{"truck_photo"}, false},
	{"other", "other", false, []string{"truck_photo", "truck_document", "truck_receipt", "insurance_document"}, false},
}

var kindByJSON, kindByDB = func() (map[string]fileKind, map[string]fileKind) {
	j, d := map[string]fileKind{}, map[string]fileKind{}
	for _, k := range fileKinds {
		j[k.json], d[k.db] = k, k
	}
	return j, d
}()

// checkFiles validates the files field of a request: known kinds, at most one file in a single-file kind, at most
// MaxFileKeys keys in all, keys that look like object keys and no key twice. How many files a multi-file kind newly
// links (MaxFilesPerKind) is checked against the truck's own files, in setFiles.
func checkFiles(ck *checker, files map[string][]string) {
	if n := len(fileKeys(files)); n > MaxFileKeys {
		ck.addParams("files", "too_many", map[string]any{"max": MaxFileKeys})
		return
	}
	seen := map[string]bool{}
	for name, keys := range files {
		k, ok := kindByJSON[name]
		field := "files." + name
		switch {
		case !ok:
			ck.add(field, "invalid")
			continue
		case k.single && len(keys) > 1:
			ck.addParams(field, "too_many", map[string]any{"max": 1})
		}
		for _, key := range keys {
			key = strings.TrimSpace(key)
			switch {
			case key == "" || len(key) > storage.MaxKeyBytes:
				ck.add(field, "invalid")
			case seen[key]:
				ck.add(field, "duplicate")
			}
			seen[key] = true
		}
	}
}

// fileKeys lists the keys of a request's files.
func fileKeys(files map[string][]string) []string {
	var out []string
	for _, keys := range files {
		for _, k := range keys {
			out = append(out, strings.TrimSpace(k))
		}
	}
	return out
}

// heldKeys looks up the file rows that hold the given keys, before the request transaction (Files.FileIDsByKey takes
// its own pool connection). nil without keys or a storage service: then every key is committed.
func (s *Service) heldKeys(ctx context.Context, keys []string) (map[string][]uuid.UUID, error) {
	if len(keys) == 0 || s.files == nil {
		return nil, nil
	}
	return s.files.FileIDsByKey(ctx, keys)
}

// fileOf resolves one key of a request in tx: a file the truck already links (held: the payload's keys go back in a
// PATCH, and an editor in contractor reach or a platform admin acting as a tenant is neither the uploader nor in its
// tenant, the rule of storage.Commit), else the key committed to the truck.
func (s *Service) fileOf(ctx context.Context, tx pgx.Tx, p *authz.Principal, key, field string, purposes []string,
	truck uuid.UUID, linked map[uuid.UUID]bool, held map[string][]uuid.UUID) (uuid.UUID, error) {
	for _, id := range held[key] {
		if linked[id] {
			return id, nil
		}
	}
	if s.files == nil {
		return uuid.Nil, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: field, Reason: "not_found", Params: map[string]any{"key": key}})
	}
	f, err := s.files.Commit(ctx, tx, storage.CommitInput{Key: key, Field: field, Purposes: purposes, OwnerID: truck,
		UserID: p.UserID, TenantID: p.EffectiveTenant()})
	if err != nil {
		return uuid.Nil, err
	}
	return f.ID, nil
}

type linkedFile struct {
	kind string
	id   uuid.UUID
}

// setFiles applies the files of a request to a truck in tx. Each kind present in the request replaces that kind's
// files (an empty list clears it; the objects stay, main spec §9.3); kinds absent keep theirs. appendKinds instead
// adds to the kinds it names (a renewal's receipt and insurance documents). It reports whether anything changed and
// the file each key resolved to.
func (s *Service) setFiles(ctx context.Context, tx pgx.Tx, q *fleetdb.Queries, p *authz.Principal, truck uuid.UUID,
	replace, appendKinds map[string][]string, held map[string][]uuid.UUID) (bool, map[string]uuid.UUID, error) {
	keyIDs := map[string]uuid.UUID{}
	if len(replace) == 0 && len(appendKinds) == 0 {
		return false, keyIDs, nil
	}
	cur, err := q.TruckFiles(ctx, truck)
	if err != nil {
		return false, nil, err
	}
	// The current set in the order the new one is built in (kinds in fileKinds order, then position), so the two
	// compare as sequences; a kind outside the vocabulary cannot exist (CHECK).
	linked := map[uuid.UUID]bool{}
	before := make([]linkedFile, 0, len(cur))
	for _, k := range fileKinds {
		for _, f := range cur {
			if f.Kind == k.db {
				linked[f.FileID] = true
				before = append(before, linkedFile{f.Kind, f.FileID})
			}
		}
	}
	// A kind may newly link at most MaxFilesPerKind files per request; the files the truck already links (the
	// payload sent back) do not count. Checked before any key is committed.
	for _, src := range []map[string][]string{replace, appendKinds} {
		for name, keys := range src {
			fresh := 0
			for _, key := range keys {
				if !slices.ContainsFunc(held[strings.TrimSpace(key)], func(id uuid.UUID) bool { return linked[id] }) {
					fresh++
				}
			}
			if fresh > MaxFilesPerKind {
				return false, nil, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "files." + name, Reason: "too_many",
					Params: map[string]any{"max": MaxFilesPerKind}})
			}
		}
	}
	resolve := func(name string, keys []string) ([]uuid.UUID, error) {
		k := kindByJSON[name]
		ids := make([]uuid.UUID, 0, len(keys))
		for _, key := range keys {
			key = strings.TrimSpace(key)
			id, err := s.fileOf(ctx, tx, p, key, "files."+name, k.purposes, truck, linked, held)
			if err != nil {
				return nil, err
			}
			keyIDs[key] = id
			ids = append(ids, id)
		}
		return ids, nil
	}
	// Resolve every key the request names first: a file it claims leaves whatever kind it is in now.
	requested := map[string][]uuid.UUID{}
	extra := map[string][]uuid.UUID{}
	claimed := map[uuid.UUID]bool{}
	for _, k := range fileKinds {
		for _, set := range []struct {
			src map[string][]string
			dst map[string][]uuid.UUID
		}{{replace, requested}, {appendKinds, extra}} {
			keys, ok := set.src[k.json]
			if !ok {
				continue
			}
			ids, err := resolve(k.json, keys)
			if err != nil {
				return false, nil, err
			}
			set.dst[k.json] = ids
			for _, id := range ids {
				claimed[id] = true
			}
		}
	}
	// The new set, kind by kind in the order of fileKinds (deterministic positions); a file is linked once (PK).
	after := []linkedFile{}
	placed := map[uuid.UUID]bool{}
	for _, k := range fileKinds {
		ids, ok := requested[k.json]
		if !ok {
			for _, f := range before {
				if f.kind == k.db && !claimed[f.id] {
					ids = append(ids, f.id)
				}
			}
		}
		for _, id := range append(ids, extra[k.json]...) {
			if !placed[id] {
				after = append(after, linkedFile{k.db, id})
				placed[id] = true
			}
		}
	}
	if slices.Equal(before, after) {
		return false, keyIDs, nil
	}
	ids := make([]uuid.UUID, 0, len(before))
	for _, f := range before {
		ids = append(ids, f.id)
	}
	if len(ids) > 0 {
		if err := q.DeleteTruckFiles(ctx, fleetdb.DeleteTruckFilesParams{TruckID: truck, FileIds: ids}); err != nil {
			return false, nil, err
		}
	}
	if len(after) == 0 {
		return true, keyIDs, nil
	}
	arg := fleetdb.InsertTruckFilesParams{TruckID: truck}
	pos := map[string]int32{}
	for _, f := range after {
		arg.FileIds = append(arg.FileIds, f.id)
		arg.Kinds = append(arg.Kinds, f.kind)
		arg.Positions = append(arg.Positions, pos[f.kind])
		pos[f.kind]++
	}
	return true, keyIDs, q.InsertTruckFiles(ctx, arg)
}

// --- writes --------------------------------------------------------------------------------------------------

// TruckInput is the body of POST /v1/trucks and PATCH /v1/trucks/{id} (the truck form, web validate/truckSchema.ts,
// in canonical vocabularies): on PATCH absent fields stay and null clears an optional one. tenantId (create only; a
// PATCH may send the truck's own tenant back) puts a subcontractor truck in a carrier of the caller's contractor
// reach; ownershipType is derived from that tenant and never read. statusNote is the reason of the status_history
// entry a status change appends. files maps a kind to its keys (POST /v1/uploads/presign, purposes truck_photo,
// truck_document, truck_receipt, insurance_document); each kind present replaces that kind's files.
type TruckInput struct {
	TenantID               Field[string]              `json:"tenantId"`
	LicensePlate           Field[string]              `json:"licensePlate"`
	Province               Field[string]              `json:"province"`
	VIN                    Field[string]              `json:"vin"`
	EngineNumber           Field[string]              `json:"engineNumber"`
	GPSVehicleID           Field[string]              `json:"gpsVehicleId"`
	Status                 Field[string]              `json:"status"`
	StatusNote             Field[string]              `json:"statusNote"`
	Brand                  Field[string]              `json:"brand"`
	Model                  Field[string]              `json:"model"`
	Year                   Field[int32]               `json:"year"`
	Color                  Field[string]              `json:"color"`
	Type                   Field[string]              `json:"type"`
	Seats                  Field[int32]               `json:"seats"`
	FuelType               Field[string]              `json:"fuelType"`
	EngineCapacity         Field[float64]             `json:"engineCapacity"`
	FuelCapacity           Field[float64]             `json:"fuelCapacity"`
	MaxLoadWeightKg        Field[float64]             `json:"maxLoadWeightKg"`
	RegistrationDate       Field[string]              `json:"registrationDate"`
	BuyingDate             Field[string]              `json:"buyingDate"`
	Notes                  Field[string]              `json:"notes"`
	TaxExpiryDate          Field[string]              `json:"taxExpiryDate"`
	TaxExpenseThb          Field[float64]             `json:"taxExpenseThb"`
	TaxResponsible         Field[string]              `json:"taxResponsible"`
	TaxRenewalStatus       Field[string]              `json:"taxRenewalStatus"`
	MaintenanceResponsible Field[string]              `json:"maintenanceResponsible"`
	LastServiceDate        Field[string]              `json:"lastServiceDate"`
	NextServiceDate        Field[string]              `json:"nextServiceDate"`
	NextServiceMileage     Field[int32]               `json:"nextServiceMileage"`
	PMIntervalKm           Field[int32]               `json:"pmIntervalKm"`
	CurrentMileage         Field[int32]               `json:"currentMileage"`
	InsurancePolicyID      Field[string]              `json:"insurancePolicyId"`
	InsurancePolicyNumber  Field[string]              `json:"insurancePolicyNumber"`
	InsuranceCompany       Field[string]              `json:"insuranceCompany"`
	InsuranceType          Field[string]              `json:"insuranceType"`
	InsuranceStartDate     Field[string]              `json:"insuranceStartDate"`
	InsuranceExpiryDate    Field[string]              `json:"insuranceExpiryDate"`
	InsurancePremiumThb    Field[float64]             `json:"insurancePremiumThb"`
	InsuranceNotes         Field[string]              `json:"insuranceNotes"`
	InsuranceRenewalStatus Field[string]              `json:"insuranceRenewalStatus"`
	PaymentMethod          Field[string]              `json:"paymentMethod"`
	Files                  Field[map[string][]string] `json:"files"`
}

// newTruck is the starting row of a create: the NOT NULL defaults of the table.
func newTruck() fleetdb.UpdateTruckParams {
	return fleetdb.UpdateTruckParams{TaxResponsible: DefaultTaxResponsible, MaintenanceResponsible: DefaultMaintenanceResponsible}
}

// rowOf is the editable part of a stored truck.
func rowOf(t fleetdb.Truck) fleetdb.UpdateTruckParams {
	return fleetdb.UpdateTruckParams{ID: t.ID, LicensePlate: t.LicensePlate, Province: t.Province, Vin: t.Vin,
		EngineNumber: t.EngineNumber, GpsVehicleID: t.GpsVehicleID, Status: t.Status, Brand: t.Brand, Model: t.Model,
		Year: t.Year, Color: t.Color, TypeRaw: t.TypeRaw, VehicleClass: t.VehicleClass, Seats: t.Seats, FuelType: t.FuelType,
		EngineCapacity: t.EngineCapacity, FuelCapacity: t.FuelCapacity, MaxLoadWeightKg: t.MaxLoadWeightKg,
		RegistrationDate: t.RegistrationDate, BuyingDate: t.BuyingDate, Notes: t.Notes, TaxExpiryDate: t.TaxExpiryDate,
		TaxExpenseThb: t.TaxExpenseThb, TaxResponsible: t.TaxResponsible, TaxRenewalStatus: t.TaxRenewalStatus,
		MaintenanceResponsible: t.MaintenanceResponsible, LastServiceDate: t.LastServiceDate, NextServiceDate: t.NextServiceDate,
		NextServiceMileage: t.NextServiceMileage, PmIntervalKm: t.PmIntervalKm, CurrentMileage: t.CurrentMileage,
		InsurancePolicyID: t.InsurancePolicyID, InsurancePolicyNumber: t.InsurancePolicyNumber, InsuranceCompany: t.InsuranceCompany,
		InsuranceType: t.InsuranceType, InsuranceStartDate: t.InsuranceStartDate, InsuranceExpiryDate: t.InsuranceExpiryDate,
		InsurancePremiumThb: t.InsurancePremiumThb, InsuranceNotes: t.InsuranceNotes, InsuranceRenewalStatus: t.InsuranceRenewalStatus,
		PaymentMethod: t.PaymentMethod}
}

// merge applies the request onto m (the stored row, or newTruck on create), validating every field that is present,
// and lists the JSON names of the fields that changed. On create the required fields must be present; on both a
// required field cannot be cleared. The plate rule applies to a plate that changes (a legacy plate sent back as it
// is stays editable); the vehicle class follows the type through the one mapper.
func merge(ck *checker, in *TruckInput, m *fleetdb.UpdateTruckParams, create bool) []string {
	var changed []string
	mark := func(field string) {
		if !slices.Contains(changed, field) {
			changed = append(changed, field)
		}
	}
	req := func(field string, f Field[string], dst *string, max int, check func(string) bool) {
		switch {
		case create && !f.Set, f.Set && f.Null:
			ck.add(field, "required")
			return
		case !f.Set:
			return
		}
		v, bad := cleanText(f.V, max)
		switch {
		case bad != "":
			ck.add(field, bad)
		case v == "":
			ck.add(field, "required")
		case v == *dst && !create:
		case check != nil && !check(v):
			ck.add(field, "invalid")
		default:
			*dst = v
			mark(field)
		}
	}
	text := func(field string, f Field[string], dst **string, max int, check func(string) (string, bool)) {
		if !f.Set {
			return
		}
		var v *string
		if !f.Null {
			s, bad := cleanText(f.V, max)
			if bad != "" {
				ck.add(field, bad)
				return
			}
			if s != "" {
				if check != nil {
					var ok bool
					if s, ok = check(s); !ok {
						ck.add(field, "invalid")
						return
					}
				}
				v = &s
			}
		}
		if !eq(*dst, v) {
			*dst = v
			mark(field)
		}
	}
	enum := func(allowed []string) func(string) (string, bool) {
		return func(s string) (string, bool) { return s, slices.Contains(allowed, s) }
	}
	date := func(field string, f Field[string], dst **time.Time) {
		if !f.Set {
			return
		}
		var v *time.Time
		if s := strings.TrimSpace(f.V); !f.Null && s != "" {
			d, ok := parseDate(s)
			if !ok {
				ck.add(field, "invalid")
				return
			}
			v = &d
		}
		if !eqTime(*dst, v) {
			*dst = v
			mark(field)
		}
	}
	num := func(field string, f Field[float64], dst **float64, max float64) {
		if !f.Set {
			return
		}
		var v *float64
		if !f.Null {
			if f.V < 0 || f.V > max {
				ck.addParams(field, "out_of_range", map[string]any{"min": 0, "max": max})
				return
			}
			if !cents(f.V) {
				ck.add(field, "too_precise")
				return
			}
			x := f.V
			v = &x
		}
		if !eq(*dst, v) {
			*dst = v
			mark(field)
		}
	}
	integer := func(field string, f Field[int32], dst **int32, lo, hi int32) {
		if !f.Set {
			return
		}
		var v *int32
		if !f.Null {
			if f.V < lo || f.V > hi {
				ck.addParams(field, "out_of_range", map[string]any{"min": lo, "max": hi})
				return
			}
			x := f.V
			v = &x
		}
		if !eq(*dst, v) {
			*dst = v
			mark(field)
		}
	}
	responsible := func(field string, f Field[string], dst *string, def string) {
		if !f.Set {
			return
		}
		v := def
		if !f.Null {
			s, bad := cleanText(f.V, 100)
			if bad != "" {
				ck.add(field, bad)
				return
			}
			if s != "" {
				v = s
			}
		}
		if v != *dst {
			*dst = v
			mark(field)
		}
	}

	req("licensePlate", in.LicensePlate, &m.LicensePlate, 20, ValidPlate)
	req("province", in.Province, &m.Province, 100, nil)
	text("vin", in.VIN, &m.Vin, 17, func(s string) (string, bool) {
		s = strings.ToUpper(s)
		return s, vinRx.MatchString(s)
	})
	text("engineNumber", in.EngineNumber, &m.EngineNumber, 50, nil)
	text("gpsVehicleId", in.GPSVehicleID, &m.GpsVehicleID, 64, nil)
	req("status", in.Status, &m.Status, 30, func(s string) bool { return slices.Contains(TruckStatuses, s) })
	req("brand", in.Brand, &m.Brand, 100, nil)
	req("model", in.Model, &m.Model, 100, nil)
	switch {
	case create && !in.Year.Set, in.Year.Set && in.Year.Null:
		ck.add("year", "required")
	case in.Year.Set && (in.Year.V < MinYear || in.Year.V > MaxYear):
		ck.addParams("year", "out_of_range", map[string]any{"min": MinYear, "max": MaxYear})
	case in.Year.Set && int16(in.Year.V) != m.Year:
		m.Year = int16(in.Year.V)
		mark("year")
	}
	req("color", in.Color, &m.Color, 100, nil)
	typeBefore := m.TypeRaw
	req("type", in.Type, &m.TypeRaw, 100, nil)
	if create || m.TypeRaw != typeBefore {
		m.VehicleClass = vehicleclass.Of(m.TypeRaw) // unknown -> NULL, never a guess (R15)
	}
	if f := in.Seats; f.Set {
		var v *int16
		switch {
		case f.Null:
		case f.V < 0 || f.V > MaxSeats:
			ck.addParams("seats", "out_of_range", map[string]any{"min": 0, "max": MaxSeats})
		default:
			v = ptr(int16(f.V))
		}
		if !ck.has("seats") && !eq(m.Seats, v) {
			m.Seats = v
			mark("seats")
		}
	}
	text("fuelType", in.FuelType, &m.FuelType, 50, nil)
	num("engineCapacity", in.EngineCapacity, &m.EngineCapacity, 20000)
	num("fuelCapacity", in.FuelCapacity, &m.FuelCapacity, 1000)
	num("maxLoadWeightKg", in.MaxLoadWeightKg, &m.MaxLoadWeightKg, 100000)
	date("registrationDate", in.RegistrationDate, &m.RegistrationDate)
	date("buyingDate", in.BuyingDate, &m.BuyingDate)
	text("notes", in.Notes, &m.Notes, 2000, nil)
	date("taxExpiryDate", in.TaxExpiryDate, &m.TaxExpiryDate)
	num("taxExpenseThb", in.TaxExpenseThb, &m.TaxExpenseThb, MaxMoney)
	responsible("taxResponsible", in.TaxResponsible, &m.TaxResponsible, DefaultTaxResponsible)
	text("taxRenewalStatus", in.TaxRenewalStatus, &m.TaxRenewalStatus, 20, enum(RenewalStatuses))
	responsible("maintenanceResponsible", in.MaintenanceResponsible, &m.MaintenanceResponsible, DefaultMaintenanceResponsible)
	date("lastServiceDate", in.LastServiceDate, &m.LastServiceDate)
	date("nextServiceDate", in.NextServiceDate, &m.NextServiceDate)
	integer("nextServiceMileage", in.NextServiceMileage, &m.NextServiceMileage, 0, MaxMileage)
	integer("pmIntervalKm", in.PMIntervalKm, &m.PmIntervalKm, 1, 500000)
	integer("currentMileage", in.CurrentMileage, &m.CurrentMileage, 0, MaxMileage)
	text("insurancePolicyId", in.InsurancePolicyID, &m.InsurancePolicyID, 100, nil)
	text("insurancePolicyNumber", in.InsurancePolicyNumber, &m.InsurancePolicyNumber, 100, nil)
	text("insuranceCompany", in.InsuranceCompany, &m.InsuranceCompany, 200, nil)
	text("insuranceType", in.InsuranceType, &m.InsuranceType, 2, enum(InsuranceTypes))
	date("insuranceStartDate", in.InsuranceStartDate, &m.InsuranceStartDate)
	date("insuranceExpiryDate", in.InsuranceExpiryDate, &m.InsuranceExpiryDate)
	num("insurancePremiumThb", in.InsurancePremiumThb, &m.InsurancePremiumThb, MaxMoney)
	text("insuranceNotes", in.InsuranceNotes, &m.InsuranceNotes, 2000, nil)
	text("insuranceRenewalStatus", in.InsuranceRenewalStatus, &m.InsuranceRenewalStatus, 20, enum(RenewalStatuses))
	text("paymentMethod", in.PaymentMethod, &m.PaymentMethod, 20, enum(PaymentMethods))
	if in.StatusNote.Set && !in.StatusNote.Null {
		if _, bad := cleanText(in.StatusNote.V, 500); bad != "" {
			ck.add("statusNote", bad)
		}
	}
	if in.Files.Set && !in.Files.Null {
		checkFiles(ck, in.Files.V)
	}
	return changed
}

// Bounds of the truck form (web validate/truckSchema.ts) and of the columns.
const (
	MinYear    = 1900
	MaxYear    = 2200
	MaxSeats   = 10
	MaxMileage = 2000000
	MaxMoney   = 1000000
)

// vinRx: a 17-character VIN (the form checks the length; letters are stored upper case).
var vinRx = regexp.MustCompile(`^[A-Z0-9]{17}$`)

func eq[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func eqTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func ptr[T any](v T) *T { return &v }

// note is the reason of a status entry: the request's statusNote, else def.
func note(in Field[string], def string) *string {
	if in.Set && !in.Null {
		if s := strings.TrimSpace(in.V); s != "" {
			return &s
		}
	}
	if def == "" {
		return nil
	}
	return &def
}

// checkGPS applies the steward rule of the GPS vehicle id: it is the key the platform's one Cartrack account resolves
// vehicles by (trucks_gps is unique across tenants, so cartrack.sync maps each Cartrack vehicle to one truck), so only
// a steward (own-fleet staff, or a platform admin acting as a tenant) sets or changes it. Anyone else who edits the
// truck may send its stored value back or clear it; a new or different value is 422 gpsVehicleId not_editable before
// any write, so the unique index never answers for a truck outside the caller's reach (no probing, no squatting).
// The field is then dropped from the request so merge does not report it twice.
func checkGPS(ck *checker, p *authz.Principal, in *TruckInput, stored *string) {
	f := in.GPSVehicleID
	if p.Steward || !f.Set || f.Null {
		return
	}
	v := strings.TrimSpace(f.V)
	if v == "" || (stored != nil && *stored == v) {
		return
	}
	ck.add("gpsVehicleId", "not_editable")
	in.GPSVehicleID = Field[string]{}
}

// targetTenant resolves the tenant of a new truck (tenancy.Resolve "trucks": self): the effective tenant, or a
// carrier of its contractor reach named in tenantId (an own-fleet admin registering a subcontractor's truck, the
// legacy ownershipType + subcontractorId). Anything else is 422 tenantId not_in_reach: rows never take a tenant the
// caller cannot reach (Appendix C §C.3.10).
func targetTenant(ck *checker, p *authz.Principal, tenant uuid.UUID, f Field[string]) uuid.UUID {
	if !f.Set || f.Null || strings.TrimSpace(f.V) == "" {
		return tenant
	}
	id, err := uuid.Parse(strings.TrimSpace(f.V))
	switch {
	case err != nil:
		ck.add("tenantId", "invalid")
	case id == tenant || slices.Contains(p.SubtenantIDs, id):
		return id
	default:
		ck.add("tenantId", "not_in_reach")
	}
	return tenant
}

// ownershipOf is the ownership a tenant kind gives its trucks.
func ownershipOf(kind string) (string, error) {
	switch kind {
	case authz.TenantKindOwnFleet:
		return OwnershipOwn, nil
	case authz.TenantKindCarrier:
		return OwnershipSubcontractor, nil
	}
	return "", httpx.ErrInvalidArgument(violation("tenantId", "invalid"))
}

// insertTruck writes a merged, validated new truck of tenant in tx with its first status entry; the caller commits
// files and emits.
func insertTruck(ctx context.Context, q *fleetdb.Queries, p *authz.Principal, tenant uuid.UUID, ownership string,
	m fleetdb.UpdateTruckParams, reason *string, at time.Time) (uuid.UUID, error) {
	user, label := actor(p)
	id, err := q.InsertTruck(ctx, fleetdb.InsertTruckParams{TenantID: tenant, OwnershipType: ownership,
		LicensePlate: m.LicensePlate, Province: m.Province, Vin: m.Vin, EngineNumber: m.EngineNumber,
		GpsVehicleID: m.GpsVehicleID, Status: m.Status, Brand: m.Brand, Model: m.Model, Year: m.Year, Color: m.Color,
		TypeRaw: m.TypeRaw, VehicleClass: m.VehicleClass, Seats: m.Seats, FuelType: m.FuelType,
		EngineCapacity: m.EngineCapacity, FuelCapacity: m.FuelCapacity, MaxLoadWeightKg: m.MaxLoadWeightKg,
		RegistrationDate: m.RegistrationDate, BuyingDate: m.BuyingDate, Notes: m.Notes, TaxExpiryDate: m.TaxExpiryDate,
		TaxExpenseThb: m.TaxExpenseThb, TaxResponsible: m.TaxResponsible, TaxRenewalStatus: m.TaxRenewalStatus,
		MaintenanceResponsible: m.MaintenanceResponsible, LastServiceDate: m.LastServiceDate,
		NextServiceDate: m.NextServiceDate, NextServiceMileage: m.NextServiceMileage, PmIntervalKm: m.PmIntervalKm,
		CurrentMileage: m.CurrentMileage, InsurancePolicyID: m.InsurancePolicyID,
		InsurancePolicyNumber: m.InsurancePolicyNumber, InsuranceCompany: m.InsuranceCompany,
		InsuranceType: m.InsuranceType, InsuranceStartDate: m.InsuranceStartDate,
		InsuranceExpiryDate: m.InsuranceExpiryDate, InsurancePremiumThb: m.InsurancePremiumThb,
		InsuranceNotes: m.InsuranceNotes, InsuranceRenewalStatus: m.InsuranceRenewalStatus,
		PaymentMethod: m.PaymentMethod, Actor: user})
	if err != nil {
		return uuid.Nil, err
	}
	return id, q.InsertTruckStatus(ctx, fleetdb.InsertTruckStatusParams{TruckID: id, Status: m.Status, ChangedAt: at,
		UserID: user, Label: label, Reason: reason})
}

// updateTruck writes a merged row in tx and appends the status entry of a status change.
func updateTruck(ctx context.Context, q *fleetdb.Queries, p *authz.Principal, m fleetdb.UpdateTruckParams,
	previousStatus string, reason *string, at time.Time) error {
	user, label := actor(p)
	m.Actor = user
	if err := q.UpdateTruck(ctx, m); err != nil {
		return err
	}
	if m.Status == previousStatus {
		return nil
	}
	return q.InsertTruckStatus(ctx, fleetdb.InsertTruckStatusParams{TruckID: m.ID, Status: m.Status,
		PreviousStatus: &previousStatus, ChangedAt: at, UserID: user, Label: label, Reason: reason})
}

// CreateTruck is POST /v1/trucks (fleet:create_truck): the truck in the effective tenant or a carrier of its
// contractor reach (tenantId), ownership from that tenant, the vehicle class from the one mapper (unknown NULL), the
// first status_history entry, its files and outbox truck.created in one transaction. A plate the tenant already has
// is 409 already_exists (trucks_plate_per_tenant), as is a GPS vehicle id another truck holds; only a steward sets
// the GPS vehicle id (checkGPS).
func (s *Service) CreateTruck(ctx context.Context, p *authz.Principal, rid string, in TruckInput) (*TruckDetail, error) {
	own, err := writeTenant(p)
	if err != nil {
		return nil, err
	}
	var ck checker
	checkGPS(&ck, p, &in, nil)
	m := newTruck()
	merge(&ck, &in, &m, true)
	tenant := targetTenant(&ck, p, own, in.TenantID)
	if err := ck.err(); err != nil {
		return nil, err
	}
	var id uuid.UUID
	err = s.tx(ctx, p, func(tx pgx.Tx, q *fleetdb.Queries) error {
		kind, err := q.TenantKind(ctx, tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("tenantId", "not_found"))
		}
		if err != nil {
			return err
		}
		ownership, err := ownershipOf(kind)
		if err != nil {
			return err
		}
		if id, err = insertTruck(ctx, q, p, tenant, ownership, m, note(in.StatusNote, "created"), s.now()); err != nil {
			return err
		}
		if in.Files.Set && !in.Files.Null {
			if _, _, err := s.setFiles(ctx, tx, q, p, id, in.Files.V, nil, nil); err != nil {
				return err
			}
		}
		return emit(ctx, tx, RouteTruckCreated, id, tenant, nil, rid)
	})
	if err != nil {
		return nil, err
	}
	return s.GetTruck(ctx, p, id)
}

// PatchTruck is PATCH /v1/trucks/{id} (fleet:edit_truck): partial, on a truck of the caller's reach (RLS). A status
// change appends status_history {status, previousStatus, changedBy, reason: statusNote}; files kinds present replace
// theirs (a key the truck already links stays linked, whoever uploaded it); outbox truck.updated lists the changed
// fields. A request that changes nothing writes nothing. tenantId and ownership are not editable (a truck's tenant is
// frozen, trg_freeze_tenant_id).
func (s *Service) PatchTruck(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in TruckInput) (*TruckDetail, error) {
	if _, err := writeTenant(p); err != nil {
		return nil, err
	}
	var held map[string][]uuid.UUID
	if in.Files.Set && !in.Files.Null {
		var err error
		if held, err = s.heldKeys(ctx, fileKeys(in.Files.V)); err != nil {
			return nil, err
		}
	}
	err := s.tx(ctx, p, func(tx pgx.Tx, q *fleetdb.Queries) error {
		cur, err := q.LockTruck(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		var ck checker
		if f := in.TenantID; f.Set && (f.Null || strings.TrimSpace(f.V) != cur.TenantID.String()) {
			ck.add("tenantId", "not_editable")
		}
		checkGPS(&ck, p, &in, cur.GpsVehicleID)
		m := rowOf(cur)
		changed := merge(&ck, &in, &m, false)
		if err := ck.err(); err != nil {
			return err
		}
		if len(changed) > 0 {
			if err := updateTruck(ctx, q, p, m, cur.Status, note(in.StatusNote, ""), s.now()); err != nil {
				return err
			}
		}
		if in.Files.Set && !in.Files.Null {
			moved, _, err := s.setFiles(ctx, tx, q, p, id, in.Files.V, nil, held)
			if err != nil {
				return err
			}
			if moved {
				changed = append(changed, "files")
			}
		}
		if len(changed) == 0 {
			return nil
		}
		return emit(ctx, tx, RouteTruckUpdated, id, cur.TenantID, changed, rid)
	})
	if err != nil {
		return nil, err
	}
	return s.GetTruck(ctx, p, id)
}
