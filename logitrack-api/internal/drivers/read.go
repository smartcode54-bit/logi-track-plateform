package drivers

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/drivers/driversdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// ActiveTruck is drivers.activeTruck: the truck the driver is responsible for right now (set by the driver app at
// check-in from the task's truck, cleared when the trip ends). Not the home truck, not the truck of a task.
type ActiveTruck struct {
	TruckID    uuid.UUID  `json:"truckId"`
	TruckPlate *string    `json:"truckPlate"`
	TaskID     *uuid.UUID `json:"taskId"`
	StartedAt  *time.Time `json:"startedAt"`
}

// CurrentAssignment is drivers.currentAssignment: the home truck, from the driver's active truck assignment (a
// default at task assignment, never a filter). The truck columns are null when the caller cannot read the assignment
// or the truck (an assignment carries its truck's tenant); a current_assignment_id naming a row that is no longer
// active (loaded data) is no home truck and shows as null.
type CurrentAssignment struct {
	ID         uuid.UUID  `json:"id"`
	TruckID    *uuid.UUID `json:"truckId"`
	TruckPlate *string    `json:"truckPlate"`
	TruckModel *string    `json:"truckModel"`
	AssignedAt *time.Time `json:"assignedAt"`
}

// Driver is a driver as GET /v1/drivers lists it and every driver payload starts with: no PII (Appendix C §C.2.3
// row 15) and no file. Dates are yyyy-MM-dd. tenantNameTh / tenantNameEn / tenantKind name the driver's tenant (the
// legacy subcontractorName); displayName is the Thai full name, else first and last name.
type Driver struct {
	ID                 uuid.UUID          `json:"id"`
	TenantID           uuid.UUID          `json:"tenantId"`
	TenantNameTh       *string            `json:"tenantNameTh"`
	TenantNameEn       *string            `json:"tenantNameEn"`
	TenantKind         *string            `json:"tenantKind"`
	UserID             *uuid.UUID         `json:"userId"`
	LegacyDocID        *string            `json:"legacyDocId"`
	DisplayName        string             `json:"displayName"`
	FirstName          string             `json:"firstName"`
	LastName           string             `json:"lastName"`
	FullNameTh         *string            `json:"fullNameTh"`
	Mobile             string             `json:"mobile"`
	Email              *string            `json:"email"`
	Status             string             `json:"status"`
	EmploymentType     string             `json:"employmentType"`
	ContractYears      *float64           `json:"contractYears"`
	EmploymentStart    *string            `json:"employmentStart"`
	EmploymentEnd      *string            `json:"employmentEnd"`
	HireDate           *string            `json:"hireDate"`
	ProbationPassed    bool               `json:"probationPassed"`
	AssignToProject    *string            `json:"assignToProject"`
	TruckLicenseID     *string            `json:"truckLicenseId"`
	LicenseType        *string            `json:"licenseType"`
	LicenseExpiredDate *string            `json:"licenseExpiredDate"`
	ActiveTruck        *ActiveTruck       `json:"activeTruck"`
	CurrentAssignment  *CurrentAssignment `json:"currentAssignment"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
}

// PII are the personal fields of a driver payload (Appendix C §C.2.3 row 15): present only for a caller holding
// drivers:view_pii, absent otherwise. The document URLs are presigned GETs of 5 minutes (main spec §9.5).
type PII struct {
	IDCard            *string `json:"idCard"`
	IDCardExpiredDate *string `json:"idCardExpiredDate"`
	BirthDate         *string `json:"birthDate"`
	IDCardKey         *string `json:"idCardKey"`
	IDCardURL         *string `json:"idCardUrl"`
	LicenseKey        *string `json:"licenseKey"`
	LicenseURL        *string `json:"licenseUrl"`
}

// StatusEntry is one status_history row of a driver (append-only).
type StatusEntry struct {
	Status         string     `json:"status"`
	PreviousStatus *string    `json:"previousStatus"`
	ChangedAt      time.Time  `json:"changedAt"`
	ChangedBy      *uuid.UUID `json:"changedBy"`
	ChangedByLabel *string    `json:"changedByLabel"`
	Reason         *string    `json:"reason"`
}

// CustomerDriverID is one customer driver id (driver_customer_codes; legacy customerDriverIds{code:{key:value}}):
// the id a customer knows the driver by, under one of the id types the customer defines (driverIdTypes).
type CustomerDriverID struct {
	CustomerID   uuid.UUID `json:"customerId"`
	CustomerCode string    `json:"customerCode"`
	CustomerName string    `json:"customerName"`
	IDTypeKey    string    `json:"idTypeKey"`
	Value        string    `json:"value"`
}

// Login is the user account linked to the driver (drivers.user_id; T19 links it, POST /v1/users or
// PUT /v1/users/{id}/driver-link): null without a link or when the caller cannot read the users row.
type Login struct {
	UserID             uuid.UUID `json:"userId"`
	Email              *string   `json:"email"`
	Status             string    `json:"status"`
	MustChangePassword bool      `json:"mustChangePassword"`
}

// DriverDetail is GET /v1/drivers/{id} and the answer of every driver write: the driver, its profile photo, its
// customer driver ids, its whole status history (newest first), its login, and its PII when the caller holds
// drivers:view_pii.
type DriverDetail struct {
	Driver
	ProfileKey        *string            `json:"profileKey"`
	ProfileURL        *string            `json:"profileUrl"`
	CustomerDriverIDs []CustomerDriverID `json:"customerDriverIds"`
	StatusHistory     []StatusEntry      `json:"statusHistory"`
	Login             *Login             `json:"login"`
	*PII
}

// DriverMinimal is an item of GET /v1/drivers?fields=minimal: the pickers (the driver link of the users page, the
// truck assignment page, R33).
type DriverMinimal struct {
	ID          uuid.UUID `json:"id"`
	DisplayName *string   `json:"displayName"`
}

// DriverScope is a driver as a dispatcher reads it (the scope_drivers projection, Appendix C §C.3.7): no ID card,
// licence, birth date, employment or document column.
type DriverScope struct {
	ID            uuid.UUID  `json:"id"`
	TenantID      uuid.UUID  `json:"tenantId"`
	DisplayName   string     `json:"displayName"`
	FirstName     string     `json:"firstName"`
	LastName      string     `json:"lastName"`
	FullNameTh    *string    `json:"fullNameTh"`
	Mobile        string     `json:"mobile"`
	Status        string     `json:"status"`
	ActiveTruckID *uuid.UUID `json:"activeTruckId"`
}

// ListMeta is the meta of GET /v1/drivers: statusCounts counts every row matching the filters other than status
// (and the cursor), so the page's stats are right whatever the page (DriversList.tsx counted the first 100 rows).
type ListMeta struct {
	StatusCounts map[string]int32 `json:"statusCounts"`
}

// displayName is the Thai full name, else first and last name (lib/driverName.ts without the e-mail fallback). It
// trims spaces only, exactly as the scope list's SQL sort key (btrim), so a keyset cursor built from it continues
// that order.
func displayName(fullTh *string, first, last string) string {
	if fullTh != nil {
		if s := strings.Trim(*fullTh, " "); s != "" {
			return s
		}
	}
	return strings.Trim(first+" "+last, " ")
}

// joined are the columns the list and the detail join to a driver row.
type joined struct {
	TenantNameTh, TenantNameEn, TenantKind *string
	AssignmentTruckID                      *uuid.UUID
	AssignmentTruckPlate                   *string
	AssignmentTruckModel                   *string
	AssignmentCreatedAt                    *time.Time
	AssignmentStatus                       *string // NULL when the caller cannot read the assignment
}

func driverOf(d driversdb.Driver, j joined) Driver {
	out := Driver{ID: d.ID, TenantID: d.TenantID, TenantNameTh: j.TenantNameTh, TenantNameEn: j.TenantNameEn,
		TenantKind: j.TenantKind, UserID: d.UserID, LegacyDocID: d.LegacyDocID,
		DisplayName: displayName(d.FullNameTh, d.FirstName, d.LastName), FirstName: d.FirstName, LastName: d.LastName,
		FullNameTh: d.FullNameTh, Mobile: d.Mobile, Email: d.Email, Status: d.Status, EmploymentType: d.EmploymentType,
		ContractYears: d.ContractYears, EmploymentStart: dateString(d.EmploymentStart),
		EmploymentEnd: dateString(d.EmploymentEnd), HireDate: dateString(d.HireDate), ProbationPassed: d.ProbationPassed,
		AssignToProject: d.AssignToProject, TruckLicenseID: d.TruckLicenseID, LicenseType: d.LicenseType,
		LicenseExpiredDate: dateString(d.LicenseExpiredDate), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
	if d.ActiveTruckID != nil {
		out.ActiveTruck = &ActiveTruck{TruckID: *d.ActiveTruckID, TruckPlate: d.ActiveTruckPlate, TaskID: d.ActiveTaskID,
			StartedAt: d.ActiveStartedAt}
	}
	if d.CurrentAssignmentID != nil && (j.AssignmentStatus == nil || *j.AssignmentStatus == "active") {
		out.CurrentAssignment = &CurrentAssignment{ID: *d.CurrentAssignmentID, TruckID: j.AssignmentTruckID,
			TruckPlate: j.AssignmentTruckPlate, TruckModel: j.AssignmentTruckModel, AssignedAt: j.AssignmentCreatedAt}
	}
	return out
}

// --- list --------------------------------------------------------------------------------------------------

// ListInput are the query parameters of GET /v1/drivers.
type ListInput struct {
	Status, Q, Fields, TenantID string
	Limit                       int
	Cursor                      string
}

// FieldsMinimal is ?fields=minimal.
const FieldsMinimal = "minimal"

// List is GET /v1/drivers (drivers:view | dispatch:view_operations), keyset, no cap (the web's silent cap of 100,
// DriversList.tsx:58-63, goes with it), with meta.statusCounts. Staff get []Driver, newest first, or []DriverMinimal
// with ?fields=minimal; a dispatcher (or a customer-scope principal) reads the scope_drivers projection by display
// name ([]DriverScope, or []DriverMinimal), never the PII columns (Appendix C §C.3.7, R85).
func (s *Service) List(ctx context.Context, p *authz.Principal, in ListInput) (any, string, *ListMeta, error) {
	var ck checker
	var status, q *string
	if in.Status != "" {
		if !statusOK(in.Status) {
			ck.add("status", "invalid")
		}
		status = &in.Status
	}
	if v := strings.TrimSpace(in.Q); v != "" {
		if utf8.RuneCountInString(v) > 100 {
			ck.add("q", "too_long")
		}
		q = &v
	}
	minimal := false
	switch in.Fields {
	case "":
	case FieldsMinimal:
		minimal = true
	default:
		ck.add("fields", "invalid")
	}
	var tenant *uuid.UUID
	if in.TenantID != "" {
		id, err := uuid.Parse(in.TenantID)
		if err != nil {
			ck.add("tenantId", "invalid")
		}
		tenant = &id
	}
	limit := pageLimit(&ck, in.Limit)
	sc := scoped(p)
	var after cursor
	if in.Cursor != "" {
		var ok bool
		after, ok = decodeCursor(in.Cursor)
		if !ok || (sc && after.S == nil) || (!sc && after.T == nil) {
			ck.add("cursor", "invalid")
		}
	}
	if err := ck.err(); err != nil {
		return nil, "", nil, err
	}
	if sc {
		return s.listScope(ctx, p, tenant, status, q, after, limit, minimal)
	}
	all, tenants := p.TenantReach()
	var rows []driversdb.ListDriversRow
	var counts []driversdb.CountDriversByStatusRow
	err := s.tx(ctx, p, func(_ pgx.Tx, qq *driversdb.Queries) (err error) {
		rows, err = qq.ListDrivers(ctx, driversdb.ListDriversParams{AllTenants: all, TenantIds: tenants, TenantID: tenant,
			Status: status, Q: q, AfterCreated: after.T, AfterID: after.ID, RowLimit: int32(limit) + 1})
		if err != nil {
			return err
		}
		counts, err = qq.CountDriversByStatus(ctx, driversdb.CountDriversByStatusParams{AllTenants: all, TenantIds: tenants,
			TenantID: tenant, Q: q})
		return err
	})
	if err != nil {
		return nil, "", nil, err
	}
	meta := &ListMeta{StatusCounts: zeroCounts()}
	for _, c := range counts {
		meta.StatusCounts[c.Status] = c.N
	}
	next := ""
	if len(rows) > limit { // one row more was asked for: there is a next page
		rows = rows[:limit]
		last := rows[limit-1].Driver
		t := last.CreatedAt
		next = cursor{T: &t, ID: last.ID}.encode()
	}
	if minimal {
		out := make([]DriverMinimal, 0, len(rows))
		for _, r := range rows {
			out = append(out, minimalOf(r.Driver.ID, displayName(r.Driver.FullNameTh, r.Driver.FirstName, r.Driver.LastName)))
		}
		return out, next, meta, nil
	}
	out := make([]Driver, 0, len(rows))
	for _, r := range rows {
		out = append(out, driverOf(r.Driver, joined{r.TenantNameTh, r.TenantNameEn, r.TenantKind, r.AssignmentTruckID,
			r.AssignmentTruckPlate, r.AssignmentTruckModel, r.AssignmentCreatedAt, r.AssignmentStatus}))
	}
	return out, next, meta, nil
}

func zeroCounts() map[string]int32 {
	m := map[string]int32{}
	for _, st := range Statuses {
		m[st] = 0
	}
	return m
}

func minimalOf(id uuid.UUID, name string) DriverMinimal {
	m := DriverMinimal{ID: id}
	if name != "" {
		m.DisplayName = &name
	}
	return m
}

func (s *Service) listScope(ctx context.Context, p *authz.Principal, tenant *uuid.UUID, status, q *string, after cursor,
	limit int, minimal bool) (any, string, *ListMeta, error) {
	var rows []scopedb.ScopeDriver
	var counts []scopedb.CountScopeDriversByStatusRow
	err := mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) (err error) {
		sq := scopedb.New(tx)
		rows, err = sq.ListScopeDrivers(ctx, scopedb.ListScopeDriversParams{TenantID: tenant, Status: status, Q: q,
			AfterName: after.S, AfterID: after.ID, RowLimit: int32(limit) + 1})
		if err != nil {
			return err
		}
		counts, err = sq.CountScopeDriversByStatus(ctx, scopedb.CountScopeDriversByStatusParams{TenantID: tenant, Q: q})
		return err
	}))
	if err != nil {
		return nil, "", nil, err
	}
	meta := &ListMeta{StatusCounts: zeroCounts()}
	for _, c := range counts {
		meta.StatusCounts[c.Status] = c.N
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		name := displayName(last.FullNameTh, last.FirstName, last.LastName)
		next = cursor{S: &name, ID: last.ID}.encode()
	}
	if minimal {
		out := make([]DriverMinimal, 0, len(rows))
		for _, r := range rows {
			out = append(out, minimalOf(r.ID, displayName(r.FullNameTh, r.FirstName, r.LastName)))
		}
		return out, next, meta, nil
	}
	out := make([]DriverScope, 0, len(rows))
	for _, r := range rows {
		out = append(out, DriverScope{ID: r.ID, TenantID: r.TenantID, DisplayName: displayName(r.FullNameTh, r.FirstName, r.LastName),
			FirstName: r.FirstName, LastName: r.LastName, FullNameTh: r.FullNameTh, Mobile: r.Mobile, Status: r.Status,
			ActiveTruckID: r.ActiveTruckID})
	}
	return out, next, meta, nil
}

// --- detail ------------------------------------------------------------------------------------------------

// Get is GET /v1/drivers/{id} (drivers:view): a driver of the caller's tenant reach (named in the query and checked
// by RLS) with activeTruck and currentAssignment (two separate concepts), its customer driver ids, status history,
// linked login and profile photo; the PII fields and documents only for a caller holding drivers:view_pii. The file
// URLs are signed after the transaction: Files.SignedFile takes its own pool connection.
func (s *Service) Get(ctx context.Context, p *authz.Principal, id uuid.UUID) (*DriverDetail, error) {
	var r driversdb.GetDriverRow
	var hist []driversdb.DriverStatusHistoryRow
	var codes []driversdb.DriverCustomerCodesRow
	var login *driversdb.DriverLoginRow
	all, tenants := p.TenantReach()
	err := s.tx(ctx, p, func(_ pgx.Tx, q *driversdb.Queries) error {
		var err error
		r, err = q.GetDriver(ctx, driversdb.GetDriverParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if hist, err = q.DriverStatusHistory(ctx, id); err != nil {
			return err
		}
		if codes, err = q.DriverCustomerCodes(ctx, id); err != nil {
			return err
		}
		if r.Driver.UserID != nil {
			l, err := q.DriverLogin(ctx, *r.Driver.UserID)
			switch {
			case errors.Is(err, pgx.ErrNoRows): // not readable under the caller's RLS
			case err != nil:
				return err
			default:
				login = &l
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	d := r.Driver
	out := &DriverDetail{Driver: driverOf(d, joined{r.TenantNameTh, r.TenantNameEn, r.TenantKind, r.AssignmentTruckID,
		r.AssignmentTruckPlate, r.AssignmentTruckModel, r.AssignmentCreatedAt, r.AssignmentStatus}),
		CustomerDriverIDs: make([]CustomerDriverID, 0, len(codes)), StatusHistory: make([]StatusEntry, 0, len(hist))}
	for _, c := range codes {
		out.CustomerDriverIDs = append(out.CustomerDriverIDs, CustomerDriverID{CustomerID: c.CustomerID,
			CustomerCode: c.CustomerCode, CustomerName: c.CustomerName, IDTypeKey: c.IDTypeKey, Value: c.Value})
	}
	for _, h := range hist {
		out.StatusHistory = append(out.StatusHistory, StatusEntry{Status: h.Status, PreviousStatus: h.PreviousStatus,
			ChangedAt: h.ChangedAt, ChangedBy: h.ChangedByUserID, ChangedByLabel: h.ChangedByLabel, Reason: h.Reason})
	}
	if login != nil {
		out.Login = &Login{UserID: login.ID, Email: login.Email, Status: login.Status, MustChangePassword: login.MustChangePassword}
	}
	out.ProfileKey, out.ProfileURL = s.signed(ctx, d.ProfileFileID, 0)
	if p.Can(authz.DriversViewPII) {
		pii := &PII{IDCard: d.IDCard, IDCardExpiredDate: dateString(d.IDCardExpiredDate), BirthDate: dateString(d.BirthDate)}
		pii.IDCardKey, pii.IDCardURL = s.signed(ctx, d.IDCardFileID, PIITTL)
		pii.LicenseKey, pii.LicenseURL = s.signed(ctx, d.LicenseFileID, PIITTL)
		out.PII = pii
	}
	return out, nil
}

// signed returns the key and a presigned GET of a committed file (nil, nil without a file or a storage service).
func (s *Service) signed(ctx context.Context, fileID *uuid.UUID, ttl time.Duration) (key, url *string) {
	if fileID == nil || s.files == nil {
		return nil, nil
	}
	k, u, err := s.files.SignedFile(ctx, *fileID, ttl)
	if err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
		s.log.Warn().Err(err).Str("file_id", fileID.String()).Msg("cannot sign a driver file URL")
	}
	if k != "" {
		key = &k
	}
	if u != "" {
		url = &u
	}
	return key, url
}

// Document kinds of GET /v1/drivers/{id}/documents/{kind} (Appendix B §B.2.7).
const (
	DocumentProfile = "profile"
	DocumentIDCard  = "id_card"
	DocumentLicense = "license"
)

// DocumentURL is GET /v1/drivers/{id}/documents/{kind} (drivers:view_pii): a 5-minute presigned GET of the driver's
// profile photo, ID card or licence (main spec §9.5, §19 Q5), signed after the driver row was read under the caller's
// RLS and reach ("a file is readable iff its referencing row is"). An unknown kind, a driver outside the reach and a
// driver without that document are all 404.
func (s *Service) DocumentURL(ctx context.Context, p *authz.Principal, id uuid.UUID, kind string) (string, error) {
	if kind != DocumentProfile && kind != DocumentIDCard && kind != DocumentLicense {
		return "", httpx.ErrNotFound()
	}
	var file *uuid.UUID
	all, tenants := p.TenantReach()
	err := s.tx(ctx, p, func(_ pgx.Tx, q *driversdb.Queries) error {
		r, err := q.GetDriver(ctx, driversdb.GetDriverParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		switch kind {
		case DocumentProfile:
			file = r.Driver.ProfileFileID
		case DocumentIDCard:
			file = r.Driver.IDCardFileID
		default:
			file = r.Driver.LicenseFileID
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if file == nil || s.files == nil {
		return "", httpx.ErrNotFound()
	}
	_, u, err := s.files.SignedFile(ctx, *file, PIITTL)
	if errors.Is(err, storage.ErrObjectNotFound) || (err == nil && u == "") {
		return "", httpx.ErrNotFound()
	}
	return u, err
}
