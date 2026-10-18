package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/drivers/driversdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Upload purposes of the three driver files (internal/storage.Purposes): the ID card and the licence are PII
// (drivers:view_pii, 5-minute URLs).
const (
	PurposeProfile = "driver_profile"
	PurposeIDCard  = "driver_id_card"
	PurposeLicense = "driver_license"
)

// Bounds of the driver form (web validate/driverSchema.ts) and of the columns.
const (
	MinAge             = 20 // the form's age rule, on the Bangkok calendar
	MaxAge             = 55
	LicenseIDLength    = 8 // truckLicenseId: exactly 8 characters
	MaxContractYears   = 100
	MaxCustomerIDs     = 100 // customerDriverIds entries per driver
	InitialStatusNote  = "Initial Registration"
	statusNoteMaxRunes = 500
)

// DriverInput is the body of POST /v1/drivers and PATCH /v1/drivers/{id} (the driver form, web
// validate/driverSchema.ts, in canonical vocabularies): on PATCH absent fields stay and null clears an optional one.
// tenantId on POST puts the driver in a carrier of the caller's contractor reach (own-fleet callers, the legacy
// employmentType SUBCONTRACTOR + subcontractorId); on PATCH a different tenantId is the broker-carrier move (Appendix C
// §C.1.4: a platform admin or a steward, alone in its request). statusNote is the reason of the status_history entry
// a status change appends. profileKey, idCardKey and licenseKey are keys of POST /v1/uploads/presign (purposes
// driver_profile, driver_id_card, driver_license), committed in the driver transaction; null unlinks (the object
// stays). customerDriverIds replaces the driver's set when present.
//
// The fields of the second group are refused (422): a driver's account is created, linked and given a temporary
// password only by the users administration (POST /v1/users, PUT /v1/users/{id}/driver-link,
// POST /v1/users/{id}/password/temporary; R29: never a caller-chosen password), and the home truck and the live truck
// are written by the truck assignments and the driver app.
type DriverInput struct {
	TenantID           Field[string]                  `json:"tenantId"`
	FirstName          Field[string]                  `json:"firstName"`
	LastName           Field[string]                  `json:"lastName"`
	FullNameTh         Field[string]                  `json:"fullNameTh"`
	Mobile             Field[string]                  `json:"mobile"`
	Email              Field[string]                  `json:"email"`
	BirthDate          Field[string]                  `json:"birthDate"`
	IDCard             Field[string]                  `json:"idCard"`
	IDCardExpiredDate  Field[string]                  `json:"idCardExpiredDate"`
	TruckLicenseID     Field[string]                  `json:"truckLicenseId"`
	LicenseType        Field[string]                  `json:"licenseType"`
	LicenseExpiredDate Field[string]                  `json:"licenseExpiredDate"`
	EmploymentType     Field[string]                  `json:"employmentType"`
	ContractYears      Field[float64]                 `json:"contractYears"`
	EmploymentStart    Field[string]                  `json:"employmentStart"`
	EmploymentEnd      Field[string]                  `json:"employmentEnd"`
	HireDate           Field[string]                  `json:"hireDate"`
	ProbationPassed    Field[bool]                    `json:"probationPassed"`
	Status             Field[string]                  `json:"status"`
	StatusNote         Field[string]                  `json:"statusNote"`
	AssignToProject    Field[string]                  `json:"assignToProject"`
	ProfileKey         Field[string]                  `json:"profileKey"`
	IDCardKey          Field[string]                  `json:"idCardKey"`
	LicenseKey         Field[string]                  `json:"licenseKey"`
	CustomerDriverIDs  Field[[]CustomerDriverIDInput] `json:"customerDriverIds"`

	UserID              Field[json.RawMessage] `json:"userId"`
	AuthID              Field[json.RawMessage] `json:"authId"`
	Login               Field[json.RawMessage] `json:"login"`
	Password            Field[json.RawMessage] `json:"password"`
	LoginPassword       Field[json.RawMessage] `json:"loginPassword"`
	ActiveTruck         Field[json.RawMessage] `json:"activeTruck"`
	CurrentAssignment   Field[json.RawMessage] `json:"currentAssignment"`
	CurrentAssignmentID Field[json.RawMessage] `json:"currentAssignmentId"`
}

// CustomerDriverIDInput is one entry of customerDriverIds: idTypeKey is one of the customer's driverIdTypes (a key
// the driver already holds for that customer may be sent back as it is).
type CustomerDriverIDInput struct {
	CustomerID string `json:"customerId"`
	IDTypeKey  string `json:"idTypeKey"`
	Value      string `json:"value"`
}

// refused adds a violation for every field this route never writes (see DriverInput).
func (in *DriverInput) refused(ck *checker) {
	for _, r := range []struct {
		set          bool
		name, reason string
	}{
		{in.UserID.Set, "userId", "not_editable"}, {in.AuthID.Set, "authId", "not_editable"},
		{in.Login.Set, "login", "not_allowed"}, {in.Password.Set, "password", "not_allowed"},
		{in.LoginPassword.Set, "loginPassword", "not_allowed"}, {in.ActiveTruck.Set, "activeTruck", "not_editable"},
		{in.CurrentAssignment.Set, "currentAssignment", "not_editable"},
		{in.CurrentAssignmentID.Set, "currentAssignmentId", "not_editable"},
	} {
		if r.set {
			ck.add(r.name, r.reason)
		}
	}
}

// piiSet reports whether the request writes a PII field (Appendix C §C.2.3 row 15), which needs drivers:view_pii.
func (in *DriverInput) piiSet() bool {
	return in.IDCard.Set || in.IDCardExpiredDate.Set || in.BirthDate.Set || in.IDCardKey.Set || in.LicenseKey.Set
}

// others reports whether the request sets a field besides tenantId and statusNote (a tenant move travels alone).
func (in *DriverInput) others() bool {
	return in.FirstName.Set || in.LastName.Set || in.FullNameTh.Set || in.Mobile.Set || in.Email.Set || in.BirthDate.Set ||
		in.IDCard.Set || in.IDCardExpiredDate.Set || in.TruckLicenseID.Set || in.LicenseType.Set ||
		in.LicenseExpiredDate.Set || in.EmploymentType.Set || in.ContractYears.Set || in.EmploymentStart.Set ||
		in.EmploymentEnd.Set || in.HireDate.Set || in.ProbationPassed.Set || in.Status.Set || in.AssignToProject.Set ||
		in.ProfileKey.Set || in.IDCardKey.Set || in.LicenseKey.Set || in.CustomerDriverIDs.Set
}

// rowOf is the editable part of a stored driver.
func rowOf(d driversdb.Driver) driversdb.UpdateDriverParams {
	return driversdb.UpdateDriverParams{ID: d.ID, FirstName: d.FirstName, LastName: d.LastName, FullNameTh: d.FullNameTh,
		Mobile: d.Mobile, Email: d.Email, ProfileFileID: d.ProfileFileID, BirthDate: d.BirthDate, IDCard: d.IDCard,
		IDCardExpiredDate: d.IDCardExpiredDate, IDCardFileID: d.IDCardFileID, TruckLicenseID: d.TruckLicenseID,
		LicenseType: d.LicenseType, LicenseExpiredDate: d.LicenseExpiredDate, LicenseFileID: d.LicenseFileID,
		EmploymentType: d.EmploymentType, ContractYears: d.ContractYears, EmploymentStart: d.EmploymentStart,
		EmploymentEnd: d.EmploymentEnd, HireDate: d.HireDate, ProbationPassed: d.ProbationPassed, Status: d.Status,
		AssignToProject: d.AssignToProject}
}

// age is the number of whole years from birth to today (both calendar days).
func age(birth, today time.Time) int {
	y := today.Year() - birth.Year()
	if today.Month() < birth.Month() || (today.Month() == birth.Month() && today.Day() < birth.Day()) {
		y--
	}
	return y
}

// merge applies the request onto m (the stored row, or the defaults on create), validating every field that is
// present, and lists the JSON names of the fields that changed. On create the required fields must be present; on
// both a required field cannot be cleared. The form rules that legacy rows may not meet (the Thai ID checksum, the
// 8-character licence number, the age range) apply to a value that is new or changes, so a legacy value sent back as
// it is stays editable. The three file keys and customerDriverIds are applied in the transaction (setFile,
// setCodes).
func merge(ck *checker, in *DriverInput, m *driversdb.UpdateDriverParams, create bool, today time.Time) []string {
	var changed []string
	mark := func(field string) {
		if !slices.Contains(changed, field) {
			changed = append(changed, field)
		}
	}
	req := func(field string, f Field[string], dst *string, max int) {
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
		case v != *dst:
			*dst = v
			mark(field)
		}
	}
	// text sets an optional text field; check validates a value that is new or changes (and may normalise it).
	text := func(field string, f Field[string], dst **string, max int, check func(string) (string, string)) {
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
				if check != nil && (*dst == nil || **dst != s) {
					var reason string
					if s, reason = check(s); reason != "" {
						ck.add(field, reason)
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
	date := func(field string, f Field[string], dst **time.Time, check func(time.Time) bool) {
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
			if check != nil && (*dst == nil || !(*dst).Equal(d)) && !check(d) {
				return
			}
			v = &d
		}
		if !eqTime(*dst, v) {
			*dst = v
			mark(field)
		}
	}
	enum := func(allowed []string) func(string) (string, string) {
		return func(s string) (string, string) {
			if !slices.Contains(allowed, s) {
				return s, "invalid"
			}
			return s, ""
		}
	}

	req("firstName", in.FirstName, &m.FirstName, 100)
	req("lastName", in.LastName, &m.LastName, 100)
	// fullNameTh is required by the form (driverSchema.ts) and here; a legacy row without one stays editable
	// while the request does not touch it.
	switch f := in.FullNameTh; {
	case create && !f.Set, f.Set && f.Null:
		ck.add("fullNameTh", "required")
	case f.Set:
		v, bad := cleanText(f.V, 200)
		switch {
		case bad != "":
			ck.add("fullNameTh", bad)
		case v == "":
			ck.add("fullNameTh", "required")
		case !eq(m.FullNameTh, &v):
			m.FullNameTh = &v
			mark("fullNameTh")
		}
	}
	req("mobile", in.Mobile, &m.Mobile, 30)
	text("email", in.Email, &m.Email, 254, func(s string) (string, string) {
		s = strings.ToLower(s)
		if !emailRx.MatchString(s) {
			return s, "invalid"
		}
		return s, ""
	})
	date("birthDate", in.BirthDate, &m.BirthDate, func(d time.Time) bool {
		if a := age(d, today); a < MinAge || a > MaxAge {
			ck.addParams("birthDate", "out_of_range", map[string]any{"minAge": MinAge, "maxAge": MaxAge})
			return false
		}
		return true
	})
	text("idCard", in.IDCard, &m.IDCard, 13, func(s string) (string, string) {
		if !iam.ValidThaiID(s) {
			return s, "invalid"
		}
		return s, ""
	})
	date("idCardExpiredDate", in.IDCardExpiredDate, &m.IDCardExpiredDate, nil)
	text("truckLicenseId", in.TruckLicenseID, &m.TruckLicenseID, 50, func(s string) (string, string) {
		if len([]rune(s)) != LicenseIDLength {
			return s, "invalid_length"
		}
		return s, ""
	})
	text("licenseType", in.LicenseType, &m.LicenseType, 10, enum(LicenseTypes))
	date("licenseExpiredDate", in.LicenseExpiredDate, &m.LicenseExpiredDate, nil)
	if f := in.EmploymentType; f.Set {
		v, _ := cleanText(f.V, 30)
		switch {
		case f.Null || v == "":
			ck.add("employmentType", "required")
		case !slices.Contains(EmploymentTypes, v):
			ck.add("employmentType", "invalid")
		case v != m.EmploymentType:
			m.EmploymentType = v
			mark("employmentType")
		}
	}
	if f := in.ContractYears; f.Set {
		var v *float64
		ok := true
		switch {
		case f.Null:
		case f.V < 0 || f.V > MaxContractYears || math.IsNaN(f.V):
			ck.addParams("contractYears", "out_of_range", map[string]any{"min": 0, "max": MaxContractYears})
			ok = false
		case math.Abs(f.V*100-math.Round(f.V*100)) > 1e-6:
			ck.add("contractYears", "too_precise")
			ok = false
		default:
			v = ptr(f.V)
		}
		if ok && !eq(m.ContractYears, v) {
			m.ContractYears = v
			mark("contractYears")
		}
	}
	date("employmentStart", in.EmploymentStart, &m.EmploymentStart, nil)
	date("employmentEnd", in.EmploymentEnd, &m.EmploymentEnd, nil)
	if m.EmploymentStart != nil && m.EmploymentEnd != nil && m.EmploymentEnd.Before(*m.EmploymentStart) &&
		(in.EmploymentStart.Set || in.EmploymentEnd.Set) {
		ck.add("employmentEnd", "before_start")
	}
	date("hireDate", in.HireDate, &m.HireDate, nil)
	if f := in.ProbationPassed; f.Set {
		v := f.V && !f.Null
		if v != m.ProbationPassed {
			m.ProbationPassed = v
			mark("probationPassed")
		}
	}
	if f := in.Status; f.Set {
		v, _ := cleanText(f.V, 30)
		switch {
		case f.Null || v == "":
			ck.add("status", "required")
		case !statusOK(v):
			ck.add("status", "invalid")
		case v != m.Status:
			m.Status = v
			mark("status")
		}
	}
	if f := in.StatusNote; f.Set && !f.Null {
		if _, bad := cleanText(f.V, statusNoteMaxRunes); bad != "" {
			ck.add("statusNote", bad)
		}
	}
	text("assignToProject", in.AssignToProject, &m.AssignToProject, 200, nil)
	for _, k := range []struct {
		name string
		f    Field[string]
	}{{"profileKey", in.ProfileKey}, {"idCardKey", in.IDCardKey}, {"licenseKey", in.LicenseKey}} {
		if k.f.Set && !k.f.Null && len(strings.TrimSpace(k.f.V)) > storage.MaxKeyBytes {
			ck.add(k.name, "invalid")
		}
	}
	return changed
}

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

// --- customer driver ids ------------------------------------------------------------------------------------

type code struct {
	customer   uuid.UUID
	key, value string
}

// parseCodes validates the shape of customerDriverIds (nil when the field is absent; an empty list for null or []).
func parseCodes(ck *checker, f Field[[]CustomerDriverIDInput]) []code {
	if !f.Set {
		return nil
	}
	out := []code{}
	if f.Null {
		return out
	}
	if len(f.V) > MaxCustomerIDs {
		ck.addParams("customerDriverIds", "too_many", map[string]any{"max": MaxCustomerIDs})
		return out
	}
	seen := map[[2]string]bool{}
	for i, e := range f.V {
		at := "customerDriverIds[" + strconv.Itoa(i) + "]."
		id, err := uuid.Parse(strings.TrimSpace(e.CustomerID))
		if err != nil || id == uuid.Nil {
			ck.add(at+"customerId", "invalid")
			continue
		}
		key, bad := cleanText(e.IDTypeKey, 50)
		if bad == "" && key == "" {
			bad = "required"
		}
		if bad != "" {
			ck.add(at+"idTypeKey", bad)
			continue
		}
		val, bad := cleanText(e.Value, 100)
		if bad == "" && val == "" {
			bad = "required"
		}
		if bad != "" {
			ck.add(at+"value", bad)
			continue
		}
		if seen[[2]string{id.String(), key}] {
			ck.add(at+"idTypeKey", "duplicate")
			continue
		}
		seen[[2]string{id.String(), key}] = true
		out = append(out, code{customer: id, key: key, value: val})
	}
	return out
}

// setCodes replaces the driver's customer driver ids with codes in tx (nil = the request did not send the field).
// Each customer must exist and each key be one of its driverIdTypes, or a key the driver already holds for that
// customer (a legacy key sent back as it is). It reports whether the set changed.
func setCodes(ctx context.Context, q *driversdb.Queries, driver uuid.UUID, codes []code) (bool, error) {
	if codes == nil {
		return false, nil
	}
	cur, err := q.DriverCustomerCodes(ctx, driver)
	if err != nil {
		return false, err
	}
	held := map[[2]string]string{}
	for _, c := range cur {
		held[[2]string{c.CustomerID.String(), c.IDTypeKey}] = c.Value
	}
	if len(codes) > 0 {
		ids := make([]uuid.UUID, 0, len(codes))
		for _, c := range codes {
			if !slices.Contains(ids, c.customer) {
				ids = append(ids, c.customer)
			}
		}
		types, err := q.CustomerIDTypes(ctx, ids)
		if err != nil {
			return false, err
		}
		known, keys := map[uuid.UUID]bool{}, map[[2]string]bool{}
		for _, t := range types {
			known[t.CustomerID] = true
			if t.Key != nil {
				keys[[2]string{t.CustomerID.String(), *t.Key}] = true
			}
		}
		var ck checker
		for i, c := range codes {
			at := "customerDriverIds[" + strconv.Itoa(i) + "]."
			k := [2]string{c.customer.String(), c.key}
			_, kept := held[k]
			switch {
			case !known[c.customer]:
				ck.add(at+"customerId", "not_found")
			case !keys[k] && !kept:
				ck.add(at+"idTypeKey", "not_found")
			}
		}
		if err := ck.err(); err != nil {
			return false, err
		}
	}
	if len(codes) == len(held) {
		same := true
		for _, c := range codes {
			if v, ok := held[[2]string{c.customer.String(), c.key}]; !ok || v != c.value {
				same = false
				break
			}
		}
		if same {
			return false, nil
		}
	}
	if len(cur) > 0 {
		if err := q.DeleteDriverCustomerCodes(ctx, driver); err != nil {
			return false, err
		}
	}
	if len(codes) == 0 {
		return true, nil
	}
	arg := driversdb.InsertDriverCustomerCodesParams{DriverID: driver}
	for _, c := range codes {
		arg.CustomerIds = append(arg.CustomerIds, c.customer)
		arg.Keys = append(arg.Keys, c.key)
		arg.Vals = append(arg.Vals, c.value)
	}
	return true, q.InsertDriverCustomerCodes(ctx, arg)
}

// --- files ---------------------------------------------------------------------------------------------------

// textOf is the value a key field takes: the trimmed text, nil for null or blank.
func textOf(f Field[string]) *string {
	if f.Null {
		return nil
	}
	if s := strings.TrimSpace(f.V); s != "" {
		return &s
	}
	return nil
}

// heldKeys looks up the file rows that hold the keys a PATCH carries, before its transaction (Files.FileIDsByKey
// takes its own pool connection). nil without keys or a storage service: then every key is committed.
func (s *Service) heldKeys(ctx context.Context, fields ...Field[string]) (map[string][]uuid.UUID, error) {
	var keys []string
	for _, f := range fields {
		if v := textOf(f); f.Set && v != nil {
			keys = append(keys, *v)
		}
	}
	if len(keys) == 0 || s.files == nil {
		return nil, nil
	}
	return s.files.FileIDsByKey(ctx, keys)
}

// setFile applies one key field to its column in tx: nil for null or blank (unlink; the object stays, main spec
// §9.3); the file the locked row already links when the key is that file's (kept as it is, whoever uploaded it: the
// payload's keys go back in a PATCH, and an editor in contractor reach or a platform admin acting as a tenant is
// neither the uploader nor in its tenant, the rule of storage.Commit); else the key committed to the driver. It
// reports whether the column changed.
func (s *Service) setFile(ctx context.Context, tx pgx.Tx, p *authz.Principal, f Field[string], dst **uuid.UUID,
	held map[string][]uuid.UUID, field, purpose string, driver uuid.UUID) (bool, error) {
	if !f.Set {
		return false, nil
	}
	v := textOf(f)
	var next *uuid.UUID
	switch {
	case v == nil:
	case *dst != nil && slices.Contains(held[*v], **dst):
		next = *dst
	default:
		if s.files == nil {
			return false, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: field, Reason: "not_found", Params: map[string]any{"key": *v}})
		}
		c, err := s.files.Commit(ctx, tx, storage.CommitInput{Key: *v, Field: field, Purposes: []string{purpose},
			OwnerID: driver, UserID: p.UserID, TenantID: p.EffectiveTenant()})
		if err != nil {
			return false, err
		}
		next = &c.ID
	}
	if eq(*dst, next) {
		return false, nil
	}
	*dst = next
	return true, nil
}

// setFiles applies the three key fields; it lists the JSON names of those that changed.
func (s *Service) setFiles(ctx context.Context, tx pgx.Tx, p *authz.Principal, in *DriverInput,
	m *driversdb.UpdateDriverParams, held map[string][]uuid.UUID) ([]string, error) {
	var changed []string
	for _, f := range []struct {
		name, purpose string
		in            Field[string]
		dst           **uuid.UUID
	}{
		{"profileKey", PurposeProfile, in.ProfileKey, &m.ProfileFileID},
		{"idCardKey", PurposeIDCard, in.IDCardKey, &m.IDCardFileID},
		{"licenseKey", PurposeLicense, in.LicenseKey, &m.LicenseFileID},
	} {
		moved, err := s.setFile(ctx, tx, p, f.in, f.dst, held, f.name, f.purpose, m.ID)
		if err != nil {
			return nil, err
		}
		if moved {
			changed = append(changed, f.name)
		}
	}
	return changed, nil
}

// --- writes --------------------------------------------------------------------------------------------------

// targetTenant resolves the tenant of a new driver (tenancy.Resolve "drivers": self): the effective tenant, or a
// carrier of its contractor reach named in tenantId (an own-fleet admin registering a subcontractor's driver, the
// legacy employmentType SUBCONTRACTOR + subcontractorId). Anything else is 422 tenantId not_in_reach: rows never take
// a tenant the caller cannot reach (Appendix C §C.3.10).
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

// errPIIWrite: writing a PII field needs the capability that reads it.
func errPIIWrite() error {
	return authz.ErrPermissionDenied("writing a driver's personal data needs drivers:view_pii", authz.DriversViewPII)
}

// Create is POST /v1/drivers (drivers:create): the driver profile in the effective tenant or a carrier of its
// contractor reach (tenantId), its files, customer driver ids, the first status_history entry ("Initial
// Registration") and outbox driver.created in one transaction. No account side effect: the login is POST /v1/users
// {role: driver, driverId} (users:manage, T19), which the web calls after this route.
func (s *Service) Create(ctx context.Context, p *authz.Principal, rid string, in DriverInput) (*DriverDetail, error) {
	if err := authz.RequireTenantWrite(p); err != nil {
		return nil, err
	}
	own, err := writeTenant(p)
	if err != nil {
		return nil, err
	}
	var ck checker
	in.refused(&ck)
	m := driversdb.UpdateDriverParams{Status: StatusActive}
	merge(&ck, &in, &m, true, s.today())
	tenant := targetTenant(&ck, p, own, in.TenantID)
	codes := parseCodes(&ck, in.CustomerDriverIDs)
	if err := ck.err(); err != nil {
		return nil, err
	}
	if in.piiSet() && !p.Can(authz.DriversViewPII) {
		return nil, errPIIWrite()
	}
	var id uuid.UUID
	err = s.tx(ctx, p, func(tx pgx.Tx, q *driversdb.Queries) error {
		kind, err := q.TenantKind(ctx, tenant)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == authz.TenantKindQuarantine) {
			return httpx.ErrInvalidArgument(violation("tenantId", "not_found"))
		}
		if err != nil {
			return err
		}
		if m.EmploymentType == "" { // the legacy SUBCONTRACTOR goes with a carrier's driver
			m.EmploymentType = "full_time"
			if kind == authz.TenantKindCarrier {
				m.EmploymentType = "subcontractor"
			}
		}
		if id, err = q.InsertDriver(ctx, driversdb.InsertDriverParams{TenantID: tenant, FirstName: m.FirstName,
			LastName: m.LastName, FullNameTh: m.FullNameTh, Mobile: m.Mobile, Email: m.Email, BirthDate: m.BirthDate,
			IDCard: m.IDCard, IDCardExpiredDate: m.IDCardExpiredDate, TruckLicenseID: m.TruckLicenseID,
			LicenseType: m.LicenseType, LicenseExpiredDate: m.LicenseExpiredDate, EmploymentType: m.EmploymentType,
			ContractYears: m.ContractYears, EmploymentStart: m.EmploymentStart, EmploymentEnd: m.EmploymentEnd,
			HireDate: m.HireDate, ProbationPassed: m.ProbationPassed, Status: m.Status,
			AssignToProject: m.AssignToProject}); err != nil {
			return err
		}
		m.ID = id
		files, err := s.setFiles(ctx, tx, p, &in, &m, nil)
		if err != nil {
			return err
		}
		if len(files) > 0 {
			if err := q.UpdateDriver(ctx, m); err != nil {
				return err
			}
		}
		if _, err := setCodes(ctx, q, id, codes); err != nil {
			return err
		}
		user, label := actor(p)
		if err := q.InsertDriverStatus(ctx, driversdb.InsertDriverStatusParams{DriverID: id, Status: m.Status,
			ChangedAt: s.now(), UserID: user, Label: label, Reason: note(in.StatusNote, InitialStatusNote)}); err != nil {
			return err
		}
		return emit(ctx, tx, RouteDriverCreated, id, tenant, nil, rid)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, p, id)
}

// Patch is PATCH /v1/drivers/{id} (drivers:edit): partial, on a driver of the caller's reach (RLS). A status change
// appends status_history {status, previousStatus, changedBy, reason: statusNote}; the file keys and customerDriverIds
// replace theirs; outbox driver.updated lists the changed fields. A request that changes nothing writes nothing. A
// tenantId other than the driver's is the broker-carrier move (Mover, Appendix C §C.1.4), alone in its request; the
// answer is then the driver as the caller still reads it, or nil when the move took it out of the caller's reach.
func (s *Service) Patch(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in DriverInput) (*DriverDetail, error) {
	if err := authz.RequireTenantWrite(p); err != nil {
		return nil, err
	}
	if _, err := writeTenant(p); err != nil {
		return nil, err
	}
	var ck checker
	in.refused(&ck)
	if err := ck.err(); err != nil {
		return nil, err
	}
	if in.TenantID.Set {
		moved, err := s.move(ctx, p, rid, id, &in)
		if err != nil {
			return nil, err
		}
		if moved {
			d, err := s.Get(ctx, p, id)
			if e, ok := errors.AsType[*httpx.Error](err); ok && e.Status == http.StatusNotFound {
				return nil, nil // the driver left the caller's reach
			}
			return d, err
		}
	}
	if in.piiSet() && !p.Can(authz.DriversViewPII) {
		return nil, errPIIWrite()
	}
	codes := parseCodes(&ck, in.CustomerDriverIDs)
	if err := ck.err(); err != nil {
		return nil, err
	}
	held, err := s.heldKeys(ctx, in.ProfileKey, in.IDCardKey, in.LicenseKey)
	if err != nil {
		return nil, err
	}
	err = s.tx(ctx, p, func(tx pgx.Tx, q *driversdb.Queries) error {
		cur, err := q.LockDriver(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		var ck checker
		m := rowOf(cur)
		changed := merge(&ck, &in, &m, false, s.today())
		if err := ck.err(); err != nil {
			return err
		}
		files, err := s.setFiles(ctx, tx, p, &in, &m, held)
		if err != nil {
			return err
		}
		if len(changed) > 0 || len(files) > 0 {
			if err := q.UpdateDriver(ctx, m); err != nil {
				return err
			}
		}
		changed = append(changed, files...)
		if m.Status != cur.Status {
			user, label := actor(p)
			if err := q.InsertDriverStatus(ctx, driversdb.InsertDriverStatusParams{DriverID: id, Status: m.Status,
				PreviousStatus: &cur.Status, ChangedAt: s.now(), UserID: user, Label: label,
				Reason: note(in.StatusNote, "")}); err != nil {
				return err
			}
		}
		moved, err := setCodes(ctx, q, id, codes)
		if err != nil {
			return err
		}
		if moved {
			changed = append(changed, "customerDriverIds")
		}
		if len(changed) == 0 {
			return nil
		}
		return emit(ctx, tx, RouteDriverUpdated, id, cur.TenantID, changed, rid)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, p, id)
}

// move handles the tenantId of a PATCH: the driver's own tenant is dropped from the request (the form sends it back)
// and the rest of the request goes on; another tenant is the broker-carrier move, which travels alone (422 tenantId
// exclusive otherwise) and is performed by the Mover in its own transaction. moved reports whether the Mover ran.
func (s *Service) move(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in *DriverInput) (bool, error) {
	f := in.TenantID
	raw := strings.TrimSpace(f.V)
	if f.Null || raw == "" {
		return false, httpx.ErrInvalidArgument(violation("tenantId", "required"))
	}
	to, err := uuid.Parse(raw)
	if err != nil || to == uuid.Nil {
		return false, httpx.ErrInvalidArgument(violation("tenantId", "invalid"))
	}
	all, tenants := p.TenantReach()
	var cur uuid.UUID
	err = s.tx(ctx, p, func(_ pgx.Tx, q *driversdb.Queries) error {
		var err error
		cur, err = q.DriverTenant(ctx, driversdb.DriverTenantParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		return err
	})
	if err != nil {
		return false, err
	}
	if cur == to {
		in.TenantID = Field[string]{}
		return false, nil
	}
	if in.others() {
		return false, httpx.ErrInvalidArgument(violation("tenantId", "exclusive"))
	}
	if s.mover == nil {
		return false, httpx.ErrUnavailable("driver moves are not configured")
	}
	if _, err := s.mover.MoveDriver(ctx, p, rid, id, to); err != nil {
		return false, err
	}
	return true, nil
}
