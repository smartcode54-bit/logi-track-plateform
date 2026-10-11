package maintenance

import (
	"context"
	"errors"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance/maintenancedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Record is a maintenance record as every route answers it. Dates are yyyy-MM-dd (Bangkok calendar), money THB with
// two decimals; the truck columns are the truck's current ones (the record's plate snapshot when the truck is not
// readable). displayCostThb is maintenanceDisplayCost (features/maintenance/utils/maintenanceDisplayCost.ts): the
// total, else labour + parts, else the driver's receipt amount, else 0. files holds the object keys (read them through
// GET /v1/files?key=).
type Record struct {
	ID                  uuid.UUID   `json:"id"`
	TenantID            uuid.UUID   `json:"tenantId"`
	TruckID             *uuid.UUID  `json:"truckId"`
	LicensePlate        string      `json:"licensePlate"`
	TruckBrand          string      `json:"truckBrand"`
	TruckModel          *string     `json:"truckModel"`
	TruckOwnership      *string     `json:"truckOwnership"`
	TruckStatus         *string     `json:"truckStatus"`
	Type                string      `json:"type"`
	ServiceType         string      `json:"serviceType"`
	StartDate           string      `json:"startDate"`
	EndDate             *string     `json:"endDate"`
	Status              string      `json:"status"`
	AppointmentTime     *string     `json:"appointmentTime"`
	PickupAppointmentAt *time.Time  `json:"pickupAppointmentAt"`
	CostLaborThb        *float64    `json:"costLaborThb"`
	CostPartsThb        *float64    `json:"costPartsThb"`
	TotalCostThb        *float64    `json:"totalCostThb"`
	InvoiceAmountThb    *float64    `json:"invoiceAmountThb"`
	DisplayCostThb      float64     `json:"displayCostThb"`
	Provider            *string     `json:"provider"`
	ProviderLat         *float64    `json:"providerLat"`
	ProviderLng         *float64    `json:"providerLng"`
	PaymentMethod       *string     `json:"paymentMethod"`
	CurrentMileage      *int32      `json:"currentMileage"`
	NextServiceMileage  *int32      `json:"nextServiceMileage"`
	DriverSubmitted     bool        `json:"driverSubmitted"`
	CheckInAt           *time.Time  `json:"checkInAt"`
	CheckOutAt          *time.Time  `json:"checkOutAt"`
	Notes               *string     `json:"notes"`
	Files               RecordFiles `json:"files"`
	CreatedBy           *uuid.UUID  `json:"createdBy"`
	UpdatedBy           *uuid.UUID  `json:"updatedBy"`
	CreatedAt           time.Time   `json:"createdAt"`
	UpdatedAt           time.Time   `json:"updatedAt"`
}

// RecordFiles are the object keys of a record's files by kind (legacy images[], receipts[], invoiceUrl).
type RecordFiles struct {
	Images   []string `json:"images"`
	Receipts []string `json:"receipts"`
	Invoices []string `json:"invoices"`
}

// fileKinds maps the JSON kinds of files onto maintenance_files.kind.
var fileKinds = map[string]string{"images": "image", "receipts": "receipt", "invoices": "invoice"}

// MaxFilesPerKind bounds the keys of one kind in a request.
const MaxFilesPerKind = 20

// DisplayCost is maintenanceDisplayCost: totalCost when > 0, else costLabor + costParts when > 0, else invoiceAmount
// when > 0, else 0 (two decimals).
func DisplayCost(total, labor, parts, invoice *float64) float64 {
	v := func(p *float64) float64 {
		if p == nil || math.IsNaN(*p) {
			return 0
		}
		return *p
	}
	switch {
	case v(total) > 0:
		return v(total)
	case v(labor)+v(parts) > 0:
		return math.Round((v(labor)+v(parts))*100) / 100
	case v(invoice) > 0:
		return v(invoice)
	}
	return 0
}

func recordOf(r maintenancedb.ListMaintenanceRow) Record {
	out := Record{ID: r.ID, TenantID: r.TenantID, TruckID: r.TruckID, LicensePlate: r.Plate, TruckBrand: r.Brand,
		TruckModel: r.TruckModel, TruckOwnership: r.TruckOwnership, TruckStatus: r.TruckStatus, Type: r.MaintenanceType,
		ServiceType: r.ServiceType, StartDate: r.StartDate.Format(time.DateOnly), EndDate: dateString(r.EndDate),
		Status: r.Status, AppointmentTime: r.AppointmentTime, PickupAppointmentAt: r.PickupAppointmentAt,
		CostLaborThb: r.CostLaborThb, CostPartsThb: r.CostPartsThb, TotalCostThb: r.TotalCostThb,
		InvoiceAmountThb: r.InvoiceAmountThb,
		DisplayCostThb:   DisplayCost(r.TotalCostThb, r.CostLaborThb, r.CostPartsThb, r.InvoiceAmountThb),
		Provider:         r.Provider, ProviderLat: r.ProviderLat, ProviderLng: r.ProviderLng, PaymentMethod: r.PaymentMethod,
		CurrentMileage: r.CurrentMileage, NextServiceMileage: r.NextServiceMileage, DriverSubmitted: r.DriverSubmitted,
		CheckInAt: r.CheckInAt, CheckOutAt: r.CheckOutAt, Notes: r.Notes,
		Files:     RecordFiles{Images: r.ImageKeys, Receipts: r.ReceiptKeys, Invoices: r.InvoiceKeys},
		CreatedBy: r.CreatedBy, UpdatedBy: r.UpdatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC()}
	out.PickupAppointmentAt, out.CheckInAt, out.CheckOutAt = utc(r.PickupAppointmentAt), utc(r.CheckInAt), utc(r.CheckOutAt)
	return out
}

// utc is the instant in UTC (pgx answers in the host's zone; the payload is RFC 3339 UTC, Appendix B §B.1.5).
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// --- list --------------------------------------------------------------------------------------------------

// ListInput are the query parameters of GET /v1/maintenance.
type ListInput struct {
	TruckID, Status, Type string
	Limit                 int
	Cursor                string
}

// StatusCounts are the records per status (meta.statusCounts of the first page).
type StatusCounts map[string]int64

// List is GET /v1/maintenance (fleet:manage_maintenance): the records of the caller's reach, newest first, keyset
// (created_at, id), no cap (getMaintenanceOverview read every record and every truck, maintenance.ts:72-104).
// status takes the canonical or a legacy literal. The first page (no cursor) carries meta.statusCounts over every
// record matching truckId and type.
func (s *Service) List(ctx context.Context, p *authz.Principal, in ListInput) ([]Record, string, StatusCounts, error) {
	var ck checker
	var truck *uuid.UUID
	if in.TruckID != "" {
		id, err := uuid.Parse(in.TruckID)
		if err != nil {
			ck.add("truckId", "invalid")
		}
		truck = &id
	}
	var status, typ *string
	if in.Status != "" {
		v, ok := NormalizeStatus(in.Status)
		if !ok {
			ck.add("status", "invalid")
		}
		status = &v
	}
	if in.Type != "" {
		v := strings.ToUpper(strings.TrimSpace(in.Type))
		if !slices.Contains(Types, v) {
			ck.add("type", "invalid")
		}
		typ = &v
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultLimit
	} else if limit < 1 || limit > MaxLimit {
		ck.add("limit", "out_of_range")
	}
	var after *cursor
	if in.Cursor != "" {
		c, ok := decodeCursor(in.Cursor)
		if !ok {
			ck.add("cursor", "invalid")
		}
		after = &c
	}
	if err := ck.err(); err != nil {
		return nil, "", nil, err
	}
	all, tenants := p.TenantReach()
	arg := maintenancedb.ListMaintenanceParams{AllTenants: all, TenantIds: tenants, TruckID: truck, Status: status,
		MaintenanceType: typ, RowLimit: int32(limit) + 1}
	if after != nil {
		arg.AfterCreated, arg.AfterID = &after.T, after.ID
	}
	var rows []maintenancedb.ListMaintenanceRow
	var counts StatusCounts
	err := s.tx(ctx, p, func(_ pgx.Tx, q *maintenancedb.Queries) error {
		var err error
		if rows, err = q.ListMaintenance(ctx, arg); err != nil {
			return err
		}
		if after != nil {
			return nil
		}
		cs, err := q.CountMaintenanceByStatus(ctx, maintenancedb.CountMaintenanceByStatusParams{AllTenants: all,
			TenantIds: tenants, TruckID: truck, MaintenanceType: typ})
		if err != nil {
			return err
		}
		counts = StatusCounts{}
		for _, st := range Statuses {
			counts[st] = 0
		}
		for _, c := range cs {
			counts[c.Status] = c.N
		}
		return nil
	})
	if err != nil {
		return nil, "", nil, err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = cursor{T: last.CreatedAt, ID: last.ID}.encode()
	}
	out := make([]Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, recordOf(r))
	}
	return out, next, counts, nil
}

// get reads one record of the caller's reach in its own transaction (the answer of a write).
func (s *Service) get(ctx context.Context, p *authz.Principal, id uuid.UUID) (*Record, error) {
	var out *Record
	all, tenants := p.TenantReach()
	err := s.tx(ctx, p, func(_ pgx.Tx, q *maintenancedb.Queries) error {
		rows, err := q.ListMaintenance(ctx, maintenancedb.ListMaintenanceParams{AllTenants: all, TenantIds: tenants, ID: &id,
			RowLimit: 1})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return httpx.ErrNotFound()
		}
		r := recordOf(rows[0])
		out = &r
		return nil
	})
	return out, err
}

// --- input -------------------------------------------------------------------------------------------------

// Input is the body of POST /v1/maintenance and PATCH /v1/maintenance/{id}. Money is THB (two decimals), dates
// yyyy-MM-dd, appointmentTime HH:mm, pickupAppointmentAt an RFC 3339 instant or a Bangkok wall time
// yyyy-MM-ddTHH:mm[:ss] (the admin form's local value). files: {images|receipts|invoices: [keys]} replaces the kinds
// present (keys from POST /v1/uploads/presign, purpose maintenance_file; a key the record already links stays linked).
// The driver's fields (driverSubmitted, checkInAt, checkOutAt) come from the driver app only.
type Input struct {
	TruckID             Field[string]              `json:"truckId"`
	Type                Field[string]              `json:"type"`
	ServiceType         Field[string]              `json:"serviceType"`
	StartDate           Field[string]              `json:"startDate"`
	EndDate             Field[string]              `json:"endDate"`
	Status              Field[string]              `json:"status"`
	AppointmentTime     Field[string]              `json:"appointmentTime"`
	PickupAppointmentAt Field[string]              `json:"pickupAppointmentAt"`
	CostLaborThb        Field[float64]             `json:"costLaborThb"`
	CostPartsThb        Field[float64]             `json:"costPartsThb"`
	TotalCostThb        Field[float64]             `json:"totalCostThb"`
	InvoiceAmountThb    Field[float64]             `json:"invoiceAmountThb"`
	Provider            Field[string]              `json:"provider"`
	ProviderLat         Field[float64]             `json:"providerLat"`
	ProviderLng         Field[float64]             `json:"providerLng"`
	PaymentMethod       Field[string]              `json:"paymentMethod"`
	CurrentMileage      Field[int64]               `json:"currentMileage"`
	NextServiceMileage  Field[int64]               `json:"nextServiceMileage"`
	Notes               Field[string]              `json:"notes"`
	Files               Field[map[string][]string] `json:"files"`
	DriverSubmitted     Field[any]                 `json:"driverSubmitted"`
	CheckInAt           Field[any]                 `json:"checkInAt"`
	CheckOutAt          Field[any]                 `json:"checkOutAt"`
}

// Limits of the record form (validate/maintenanceSchema.ts).
const (
	maxLaborThb   = 1_000_000
	maxPartsThb   = 1_000_000
	maxTotalThb   = 2_000_000
	maxInvoiceThb = 2_000_000
	maxMileage    = 2_000_000
	maxServiceLen = 200
	maxNotesLen   = 2000
)

var hhmmRx = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// row is the merged state of a record (the columns a staff write sets).
type row struct {
	Type, ServiceType, Status          string
	StartDate                          time.Time
	EndDate                            *time.Time
	AppointmentTime                    *string
	PickupAppointmentAt                *time.Time
	CostLabor, CostParts, Total, Invoc *float64
	Provider                           *string
	ProviderLat, ProviderLng           *float64
	PaymentMethod                      *string
	CurrentMileage, NextServiceMileage *int32
	Notes                              *string
}

func rowOf(r maintenancedb.MaintenanceRecord) row {
	return row{Type: r.MaintenanceType, ServiceType: r.ServiceType, Status: r.Status, StartDate: r.StartDate,
		EndDate: r.EndDate, AppointmentTime: r.AppointmentTime, PickupAppointmentAt: r.PickupAppointmentAt,
		CostLabor: r.CostLaborThb, CostParts: r.CostPartsThb, Total: r.TotalCostThb, Invoc: r.InvoiceAmountThb,
		Provider: r.Provider, ProviderLat: r.ProviderLat, ProviderLng: r.ProviderLng, PaymentMethod: r.PaymentMethod,
		CurrentMileage: r.CurrentMileage, NextServiceMileage: r.NextServiceMileage, Notes: r.Notes}
}

// parseLocal reads an RFC 3339 instant, or a Bangkok wall time yyyy-MM-ddTHH:mm[:ss] (no zone).
func parseLocal(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, v, clock.Bangkok); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// merge applies the request to m and returns the fields it changed (JSON names); violations go to ck. create
// requires the record's required fields.
func merge(ck *checker, in *Input, m *row, create bool) []string {
	var changed []string
	note := func(field string, did bool) {
		if did {
			changed = append(changed, field)
		}
	}
	text := func(field string, f Field[string], max int, dst **string) {
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
				v = &s
			}
		}
		note(field, !eqPtr(*dst, v))
		*dst = v
	}
	money := func(field string, f Field[float64], max float64, dst **float64) {
		if !f.Set {
			return
		}
		var v *float64
		if !f.Null {
			x := f.V
			switch {
			case math.IsNaN(x) || x < 0 || x > max:
				ck.addParams(field, "out_of_range", map[string]any{"min": 0, "max": max})
				return
			case !cents(x):
				ck.add(field, "too_precise")
				return
			}
			v = &x
		}
		note(field, !eqPtr(*dst, v))
		*dst = v
	}
	mileage := func(field string, f Field[int64], dst **int32) {
		if !f.Set {
			return
		}
		var v *int32
		if !f.Null {
			if f.V < 0 || f.V > maxMileage {
				ck.addParams(field, "out_of_range", map[string]any{"min": 0, "max": maxMileage})
				return
			}
			x := int32(f.V)
			v = &x
		}
		note(field, !eqPtr(*dst, v))
		*dst = v
	}
	coord := func(field string, f Field[float64], lim float64, dst **float64) {
		if !f.Set {
			return
		}
		var v *float64
		if !f.Null {
			if math.IsNaN(f.V) || f.V < -lim || f.V > lim {
				ck.addParams(field, "out_of_range", map[string]any{"min": -lim, "max": lim})
				return
			}
			x := f.V
			v = &x
		}
		note(field, !eqPtr(*dst, v))
		*dst = v
	}
	if f := in.Type; f.Set || create {
		v := strings.ToUpper(strings.TrimSpace(f.V))
		switch {
		case f.Null || v == "":
			ck.add("type", "required")
		case !slices.Contains(Types, v):
			ck.add("type", "invalid")
		default:
			note("type", v != m.Type)
			m.Type = v
		}
	}
	if f := in.ServiceType; f.Set || create {
		v, bad := cleanText(f.V, maxServiceLen)
		switch {
		case f.Null || (bad == "" && v == ""):
			ck.add("serviceType", "required")
		case bad != "":
			ck.add("serviceType", bad)
		default:
			note("serviceType", v != m.ServiceType)
			m.ServiceType = v
		}
	}
	if f := in.StartDate; f.Set || create {
		d, err := time.Parse(time.DateOnly, strings.TrimSpace(f.V))
		switch {
		case f.Null || strings.TrimSpace(f.V) == "":
			ck.add("startDate", "required")
		case err != nil:
			ck.add("startDate", "invalid")
		default:
			note("startDate", !d.Equal(m.StartDate))
			m.StartDate = d
		}
	}
	if f := in.EndDate; f.Set {
		var v *time.Time
		if !f.Null && strings.TrimSpace(f.V) != "" {
			d, err := time.Parse(time.DateOnly, strings.TrimSpace(f.V))
			if err != nil {
				ck.add("endDate", "invalid")
			} else {
				v = &d
			}
		}
		note("endDate", !eqTime(m.EndDate, v))
		m.EndDate = v
	}
	if (in.StartDate.Set || in.EndDate.Set) && m.EndDate != nil && m.EndDate.Before(m.StartDate) {
		ck.add("endDate", "before_start")
	}
	if f := in.Status; f.Set || create {
		v := StatusInProgress // the record form's default (maintenanceSchema.ts)
		if f.Set {
			var ok bool
			if v, ok = NormalizeStatus(f.V); f.Null || !ok {
				ck.add("status", "invalid")
				v = m.Status
			}
		}
		note("status", v != m.Status)
		m.Status = v
	}
	if f := in.AppointmentTime; f.Set {
		var v *string
		if !f.Null && strings.TrimSpace(f.V) != "" {
			s := strings.TrimSpace(f.V)
			if !hhmmRx.MatchString(s) {
				ck.add("appointmentTime", "invalid")
			}
			v = &s
		}
		note("appointmentTime", !eqPtr(m.AppointmentTime, v))
		m.AppointmentTime = v
	}
	if f := in.PickupAppointmentAt; f.Set {
		var v *time.Time
		if !f.Null && strings.TrimSpace(f.V) != "" {
			t, ok := parseLocal(f.V)
			if !ok {
				ck.add("pickupAppointmentAt", "invalid")
			}
			v = &t
		}
		note("pickupAppointmentAt", !eqTime(m.PickupAppointmentAt, v))
		m.PickupAppointmentAt = v
	}
	money("costLaborThb", in.CostLaborThb, maxLaborThb, &m.CostLabor)
	money("costPartsThb", in.CostPartsThb, maxPartsThb, &m.CostParts)
	money("totalCostThb", in.TotalCostThb, maxTotalThb, &m.Total)
	money("invoiceAmountThb", in.InvoiceAmountThb, maxInvoiceThb, &m.Invoc)
	text("provider", in.Provider, maxServiceLen, &m.Provider)
	coord("providerLat", in.ProviderLat, 90, &m.ProviderLat)
	coord("providerLng", in.ProviderLng, 180, &m.ProviderLng)
	if f := in.PaymentMethod; f.Set {
		var v *string
		if !f.Null && strings.TrimSpace(f.V) != "" {
			s := strings.ToLower(strings.TrimSpace(f.V))
			if !slices.Contains(PaymentMethods, s) {
				ck.add("paymentMethod", "invalid")
			}
			v = &s
		}
		note("paymentMethod", !eqPtr(m.PaymentMethod, v))
		m.PaymentMethod = v
	}
	mileage("currentMileage", in.CurrentMileage, &m.CurrentMileage)
	mileage("nextServiceMileage", in.NextServiceMileage, &m.NextServiceMileage)
	text("notes", in.Notes, maxNotesLen, &m.Notes)
	for _, f := range []struct {
		name string
		set  bool
	}{{"driverSubmitted", in.DriverSubmitted.Set}, {"checkInAt", in.CheckInAt.Set}, {"checkOutAt", in.CheckOutAt.Set}} {
		if f.set {
			ck.add(f.name, "not_editable")
		}
	}
	return changed
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// checkFiles validates the files of a request: known kinds, at most MaxFilesPerKind keys each, no key twice.
func checkFiles(ck *checker, f Field[map[string][]string]) {
	if !f.Set || f.Null {
		return
	}
	seen := map[string]bool{}
	for kind, keys := range f.V {
		if _, ok := fileKinds[kind]; !ok {
			ck.addParams("files", "unknown_kind", map[string]any{"kind": kind})
			continue
		}
		if len(keys) > MaxFilesPerKind {
			ck.addParams("files."+kind, "too_many", map[string]any{"max": MaxFilesPerKind})
		}
		for _, k := range keys {
			k = strings.TrimSpace(k)
			if k == "" || seen[k] {
				ck.addParams("files."+kind, "invalid", map[string]any{"key": k})
			}
			seen[k] = true
		}
	}
}

func fileKeys(f Field[map[string][]string]) []string {
	var out []string
	if !f.Set || f.Null {
		return nil
	}
	for _, keys := range f.V {
		for _, k := range keys {
			out = append(out, strings.TrimSpace(k))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// heldKeys looks up the file rows that hold the given keys, before the request transaction.
func (s *Service) heldKeys(ctx context.Context, keys []string) (map[string][]uuid.UUID, error) {
	if len(keys) == 0 || s.files == nil {
		return nil, nil
	}
	return s.files.FileIDsByKey(ctx, keys)
}

// setFiles replaces the kinds the request names (a key the record already links stays linked, whoever uploaded it;
// new keys are committed to the record). It reports whether anything changed.
func (s *Service) setFiles(ctx context.Context, tx pgx.Tx, q *maintenancedb.Queries, p *authz.Principal, id uuid.UUID,
	files map[string][]string, held map[string][]uuid.UUID) (bool, error) {
	if len(files) == 0 {
		return false, nil
	}
	cur, err := q.RecordFiles(ctx, id)
	if err != nil {
		return false, err
	}
	linked := map[uuid.UUID]bool{}
	before := map[string][]uuid.UUID{}
	for _, f := range cur {
		linked[f.FileID] = true
		before[f.Kind] = append(before[f.Kind], f.FileID)
	}
	var kinds []string
	var ids []uuid.UUID
	var idKinds []string
	var pos []int32
	changed := false
	for _, jsonKind := range slices.Sorted(maps.Keys(files)) {
		kind := fileKinds[jsonKind]
		kinds = append(kinds, kind)
		var next []uuid.UUID
		for i, key := range files[jsonKind] {
			key = strings.TrimSpace(key)
			fid, err := s.fileOf(ctx, tx, p, key, "files."+jsonKind, id, linked, held)
			if err != nil {
				return false, err
			}
			next = append(next, fid)
			ids, idKinds, pos = append(ids, fid), append(idKinds, kind), append(pos, int32(i))
		}
		if !slices.Equal(next, before[kind]) {
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if err := q.DeleteRecordFiles(ctx, maintenancedb.DeleteRecordFilesParams{MaintenanceID: id, Kinds: kinds}); err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return true, nil
	}
	return true, q.InsertRecordFiles(ctx, maintenancedb.InsertRecordFilesParams{MaintenanceID: id, FileIds: ids,
		Kinds: idKinds, Positions: pos})
}

// fileOf resolves one key: a file the record already links (the payload's keys go back in a PATCH), else the key
// committed to the record (purpose maintenance_file; the uploader or its tenant, storage.Commit).
func (s *Service) fileOf(ctx context.Context, tx pgx.Tx, p *authz.Principal, key, field string, record uuid.UUID,
	linked map[uuid.UUID]bool, held map[string][]uuid.UUID) (uuid.UUID, error) {
	for _, id := range held[key] {
		if linked[id] {
			return id, nil
		}
	}
	if s.files == nil {
		return uuid.Nil, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: field, Reason: "not_found", Params: map[string]any{"key": key}})
	}
	f, err := s.files.Commit(ctx, tx, storage.CommitInput{Key: key, Field: field, Purposes: []string{"maintenance_file"},
		OwnerID: record, UserID: p.UserID, TenantID: p.EffectiveTenant()})
	if err != nil {
		return uuid.Nil, err
	}
	return f.ID, nil
}

// --- the truck ---------------------------------------------------------------------------------------------

// truckState is the locked truck as the maintenance flows change it.
type truckState struct {
	maintenancedb.LockTruckRow
	changed []string
	reason  string
}

func (t *truckState) set(field string) {
	if !slices.Contains(t.changed, field) {
		t.changed = append(t.changed, field)
	}
}

func (t *truckState) setStatus(status, reason string) {
	if t.Status != status {
		t.Status, t.reason = status, reason
		t.set("status")
	}
}

func (t *truckState) setActive(id *uuid.UUID) {
	if !eqPtr(t.ActiveMaintenanceID, id) {
		t.ActiveMaintenanceID = id
		t.set("activeMaintenanceId")
	}
}

func (t *truckState) setLastAlert(v *int32) {
	if !eqPtr(t.LastAlertMileage, v) {
		t.LastAlertMileage = v
		t.set("lastAlertMileage")
	}
}

func (t *truckState) setCurrentMileage(v int32) {
	if !eqPtr(t.CurrentMileage, &v) {
		t.CurrentMileage = &v
		t.set("currentMileage")
	}
}

// lockTruck locks the truck of a write in the caller's reach (the first lock of every maintenance write).
func lockTruck(ctx context.Context, q *maintenancedb.Queries, p *authz.Principal, id uuid.UUID) (*truckState, error) {
	all, tenants := p.TenantReach()
	t, err := q.LockTruck(ctx, maintenancedb.LockTruckParams{ID: id, AllTenants: all, TenantIds: tenants})
	if err != nil {
		return nil, err
	}
	return &truckState{LockTruckRow: t}, nil
}

// activeIsOpen reports whether the truck's active maintenance is another record that is still open.
func activeIsOpen(ctx context.Context, q *maintenancedb.Queries, t *truckState, self uuid.UUID) (bool, error) {
	if t.ActiveMaintenanceID == nil || *t.ActiveMaintenanceID == self {
		return false, nil
	}
	st, err := q.RecordStatus(ctx, *t.ActiveMaintenanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return open(st), nil
}

// applyToTruck applies a record's state to its truck (maintenance.ts:120-238, one transaction here):
//   - an open record becomes the truck's active maintenance unless another open record already is;
//   - in_progress puts the truck in the shop (status maintenance);
//   - completed restores an in-shop truck to active, records the last service date (endDate), the current mileage
//     and, for PM, the next service mileage, and clears the active maintenance and the PM alert mark;
//   - cancelled restores an in-shop truck to active and clears the active maintenance and the PM alert mark.
//
// statusChanged says whether the status effects apply (a new record, or a status change); completion whether the
// completion fields do (the status became completed, or a completed record's end date, mileage, next service mileage
// or type changed: the legacy form re-applied them on every save of a completed record).
func applyToTruck(ctx context.Context, q *maintenancedb.Queries, t *truckState, id uuid.UUID, m row, statusChanged, completion bool) error {
	switch m.Status {
	case StatusPMBooking, StatusScheduled, StatusInProgress:
		if !statusChanged {
			return nil
		}
		busy, err := activeIsOpen(ctx, q, t, id)
		if err != nil {
			return err
		}
		if !busy {
			t.setActive(&id)
		}
		if m.Status == StatusInProgress {
			t.setStatus(truckMaintenance, "maintenance_in_progress")
		}
	case StatusCompleted, StatusCancelled:
		if statusChanged {
			if t.Status == truckMaintenance {
				t.setStatus(truckActive, "maintenance_"+m.Status)
			}
			busy, err := activeIsOpen(ctx, q, t, id)
			if err != nil {
				return err
			}
			if !busy {
				t.setActive(nil)
			}
			t.setLastAlert(nil)
		}
		if m.Status == StatusCompleted && completion {
			if m.EndDate != nil && !eqTime(t.LastServiceDate, m.EndDate) {
				d := *m.EndDate
				t.LastServiceDate = &d
				t.set("lastServiceDate")
			}
			if m.CurrentMileage != nil {
				t.setCurrentMileage(*m.CurrentMileage)
			}
			if m.Type == "PM" && m.NextServiceMileage != nil && !eqPtr(t.NextServiceMileage, m.NextServiceMileage) {
				v := *m.NextServiceMileage
				t.NextServiceMileage = &v
				t.set("nextServiceMileage")
			}
		}
	}
	return nil
}

// saveTruck writes the truck's changed maintenance columns, its status_history row when the status changed, and
// truck.updated. A truck nothing changed is not written.
func (s *Service) saveTruck(ctx context.Context, tx pgx.Tx, q *maintenancedb.Queries, p *authz.Principal, t *truckState,
	previousStatus, rid string) error {
	if len(t.changed) == 0 {
		return nil
	}
	user, label := actor(p)
	if err := q.UpdateTruckMaintenance(ctx, maintenancedb.UpdateTruckMaintenanceParams{Status: t.Status,
		ActiveMaintenanceID: t.ActiveMaintenanceID, LastAlertMileage: t.LastAlertMileage, CurrentMileage: t.CurrentMileage,
		LastServiceDate: t.LastServiceDate, NextServiceMileage: t.NextServiceMileage, Actor: user, ID: t.ID}); err != nil {
		return err
	}
	if t.Status != previousStatus {
		prev := previousStatus
		reason := t.reason
		if err := q.InsertTruckStatus(ctx, maintenancedb.InsertTruckStatusParams{TruckID: t.ID, Status: t.Status,
			PreviousStatus: &prev, ChangedAt: s.now(), UserID: user, Label: label, Reason: &reason}); err != nil {
			return err
		}
	}
	return emitTruck(ctx, tx, t.ID, t.TenantID, t.changed, rid)
}

// --- create ------------------------------------------------------------------------------------------------

// Create is POST /v1/maintenance (fleet:manage_maintenance): the record of a truck of the caller's reach (tenant
// and plate snapshot from the truck), its files, the truck's maintenance columns (applyToTruck) and outbox
// maintenance.created (+ truck.updated) in one transaction (maintenance.ts:110-143 wrote them one by one). status
// defaults to in_progress (the form's default).
func (s *Service) Create(ctx context.Context, p *authz.Principal, rid string, in Input) (*Record, error) {
	if err := writeTenant(p); err != nil {
		return nil, err
	}
	var ck checker
	var truckID uuid.UUID
	switch f := in.TruckID; {
	case !f.Set || f.Null || strings.TrimSpace(f.V) == "":
		ck.add("truckId", "required")
	default:
		id, err := uuid.Parse(strings.TrimSpace(f.V))
		if err != nil {
			ck.add("truckId", "invalid")
		}
		truckID = id
	}
	m := row{}
	merge(&ck, &in, &m, true)
	checkFiles(&ck, in.Files)
	if err := ck.err(); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	err = s.tx(ctx, p, func(tx pgx.Tx, q *maintenancedb.Queries) error {
		t, err := lockTruck(ctx, q, p, truckID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("truckId", "not_found"))
		}
		if err != nil {
			return err
		}
		previous := t.Status
		user, _ := actor(p)
		plate, brand := t.LicensePlate, t.Brand
		rec, err := q.InsertRecord(ctx, maintenancedb.InsertRecordParams{ID: id, TenantID: t.TenantID,
			TruckID: &truckID, PlateSnapshot: &plate, BrandSnapshot: &brand, MaintenanceType: m.Type,
			ServiceType: m.ServiceType, StartDate: m.StartDate, EndDate: m.EndDate, Status: m.Status,
			AppointmentTime: m.AppointmentTime, PickupAppointmentAt: m.PickupAppointmentAt, CostLaborThb: m.CostLabor,
			CostPartsThb: m.CostParts, TotalCostThb: m.Total, InvoiceAmountThb: m.Invoc, Provider: m.Provider,
			ProviderLat: m.ProviderLat, ProviderLng: m.ProviderLng, PaymentMethod: m.PaymentMethod,
			CurrentMileage: m.CurrentMileage, NextServiceMileage: m.NextServiceMileage, Notes: m.Notes, Actor: user})
		if err != nil {
			return err
		}
		if in.Files.Set && !in.Files.Null {
			if _, err := s.setFiles(ctx, tx, q, p, id, in.Files.V, nil); err != nil {
				return err
			}
		}
		if err := applyToTruck(ctx, q, t, id, m, true, true); err != nil {
			return err
		}
		if err := s.saveTruck(ctx, tx, q, p, t, previous, rid); err != nil {
			return err
		}
		return emitRecord(ctx, tx, q, RouteCreated, rec, nil, rid)
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, p, id)
}

// --- patch -------------------------------------------------------------------------------------------------

// transitions are the status changes a PATCH may make; completed and cancelled are final.
var transitions = map[string][]string{
	StatusPMBooking:  {StatusScheduled, StatusInProgress, StatusCompleted, StatusCancelled},
	StatusScheduled:  {StatusPMBooking, StatusInProgress, StatusCompleted, StatusCancelled},
	StatusInProgress: {StatusCompleted, StatusCancelled},
}

// CanTransition reports whether a record may move from one status to another (the same status always may).
func CanTransition(from, to string) bool {
	return from == to || slices.Contains(transitions[from], to)
}

// Patch is PATCH /v1/maintenance/{id} (fleet:manage_maintenance): partial, on a record of the caller's reach. A status
// change must be a valid transition (409 failed_precondition invalid_transition; completed and cancelled are final);
// the record, its files, the truck's maintenance columns (applyToTruck) and outbox maintenance.updated (+ truck.updated)
// change in one transaction (maintenance.ts:160-241). Moving a PM booking to scheduled or in_progress also emits
// maintenance.reminder_requested (the legacy page's notifyMaintenanceReminder call, maintenance.ts:197-225). A request
// that changes nothing writes nothing. truckId and the driver's fields are not editable.
func (s *Service) Patch(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in Input) (*Record, error) {
	if err := writeTenant(p); err != nil {
		return nil, err
	}
	var precheck checker
	checkFiles(&precheck, in.Files)
	if err := precheck.err(); err != nil {
		return nil, err
	}
	held, err := s.heldKeys(ctx, fileKeys(in.Files))
	if err != nil {
		return nil, err
	}
	all, tenants := p.TenantReach()
	err = s.tx(ctx, p, func(tx pgx.Tx, q *maintenancedb.Queries) error {
		truckID, err := q.RecordTruck(ctx, maintenancedb.RecordTruckParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		var t *truckState
		if truckID != nil {
			// The truck first, then the record (the lock order of every maintenance write).
			if t, err = lockTruck(ctx, q, p, *truckID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		cur, err := q.LockRecord(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		var ck checker
		if f := in.TruckID; f.Set && (f.Null || cur.TruckID == nil || strings.TrimSpace(f.V) != cur.TruckID.String()) {
			ck.add("truckId", "not_editable")
		}
		m := rowOf(cur)
		changed := merge(&ck, &in, &m, false)
		if err := ck.err(); err != nil {
			return err
		}
		if !CanTransition(cur.Status, m.Status) {
			return errFailedPrecondition("invalid_transition", map[string]any{"from": cur.Status, "to": m.Status})
		}
		rec := cur
		if len(changed) > 0 {
			user, _ := actor(p)
			if rec, err = q.UpdateRecord(ctx, maintenancedb.UpdateRecordParams{MaintenanceType: m.Type,
				ServiceType: m.ServiceType, StartDate: m.StartDate, EndDate: m.EndDate, Status: m.Status,
				AppointmentTime: m.AppointmentTime, PickupAppointmentAt: m.PickupAppointmentAt, CostLaborThb: m.CostLabor,
				CostPartsThb: m.CostParts, TotalCostThb: m.Total, InvoiceAmountThb: m.Invoc, Provider: m.Provider,
				ProviderLat: m.ProviderLat, ProviderLng: m.ProviderLng, PaymentMethod: m.PaymentMethod,
				CurrentMileage: m.CurrentMileage, NextServiceMileage: m.NextServiceMileage, Notes: m.Notes, Actor: user,
				ID: id}); err != nil {
				return err
			}
		}
		if in.Files.Set && !in.Files.Null {
			moved, err := s.setFiles(ctx, tx, q, p, id, in.Files.V, held)
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
		if t != nil {
			previous := t.Status
			statusChanged := slices.Contains(changed, "status")
			completion := slices.ContainsFunc(changed, func(f string) bool {
				return slices.Contains([]string{"status", "endDate", "currentMileage", "nextServiceMileage", "type"}, f)
			})
			if err := applyToTruck(ctx, q, t, id, m, statusChanged, completion); err != nil {
				return err
			}
			if err := s.saveTruck(ctx, tx, q, p, t, previous, rid); err != nil {
				return err
			}
		}
		if err := emitRecord(ctx, tx, q, RouteUpdated, rec, changed, rid); err != nil {
			return err
		}
		if cur.Status == StatusPMBooking && (m.Status == StatusScheduled || m.Status == StatusInProgress) {
			if _, err := emitReminder(ctx, tx, rec, "", "", rid); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, p, id)
}

// --- remind ------------------------------------------------------------------------------------------------

// RemindInput is the body of POST /v1/maintenance/{id}/remind: the staff's push text (else the legacy text built from
// the record: "นัดเช็คระยะ" and "รถ {plate} วันที่ … เวลา … ที่ … — กรุณาเข้าอู่ตามนัด").
type RemindInput struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Limits of the reminder text.
const (
	MaxReminderTitle = 100
	MaxReminderBody  = 500
)

// Remind is POST /v1/maintenance/{id}/remind (fleet:manage_maintenance; notifyMaintenanceReminder, fn:triggers.ts:362,
// which took the driver id from the browser): outbox maintenance.reminder_requested, whose notify.fcm consumer sends
// FCM maintenance_scheduled to the drivers of the record's truck, read server-side. An open record of the caller's
// reach whose truck has a driver; else 409 failed_precondition (not_open, no_driver). It answers the event id.
func (s *Service) Remind(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in RemindInput) (uuid.UUID, error) {
	if err := writeTenant(p); err != nil {
		return uuid.Nil, err
	}
	var ck checker
	title, bad := cleanText(in.Title, MaxReminderTitle)
	if bad != "" {
		ck.add("title", bad)
	}
	body, bad := cleanText(in.Body, MaxReminderBody)
	if bad != "" {
		ck.add("body", bad)
	}
	if err := ck.err(); err != nil {
		return uuid.Nil, err
	}
	all, tenants := p.TenantReach()
	var event uuid.UUID
	err := s.tx(ctx, p, func(tx pgx.Tx, q *maintenancedb.Queries) error {
		rows, err := q.ListMaintenance(ctx, maintenancedb.ListMaintenanceParams{AllTenants: all, TenantIds: tenants, ID: &id,
			RowLimit: 1})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return httpx.ErrNotFound()
		}
		r := rows[0]
		if !open(r.Status) {
			return errFailedPrecondition("not_open", map[string]any{"status": r.Status})
		}
		if r.TruckID == nil {
			return errFailedPrecondition("no_driver", nil)
		}
		drivers, err := q.TruckDriverIDs(ctx, *r.TruckID)
		if err != nil {
			return err
		}
		if len(drivers) == 0 {
			return errFailedPrecondition("no_driver", nil)
		}
		rec := maintenancedb.MaintenanceRecord{ID: r.ID, TenantID: r.TenantID, TruckID: r.TruckID}
		event, err = emitReminder(ctx, tx, rec, title, body, rid)
		return err
	})
	return event, err
}
