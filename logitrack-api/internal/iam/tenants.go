package iam

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Tenant is a tenant of GET /v1/tenants and the response of POST / PATCH (Appendix B §B.2.4 web contract).
type Tenant struct {
	ID                 uuid.UUID  `json:"id"`
	Kind               string     `json:"kind"`
	Code               *string    `json:"code"`
	NameTh             string     `json:"nameTh"`
	NameEn             *string    `json:"nameEn"`
	Status             string     `json:"status"`
	LegalType          *string    `json:"legalType"`
	ContractorTenantID *uuid.UUID `json:"contractorTenantId"`
	CreatedAt          time.Time  `json:"createdAt"`
	BillingPartyID     *uuid.UUID `json:"billingPartyId,omitempty"`
}

// TenantDetail is GET /v1/tenants/{id}: the profile (the carrier profile is folded into tenants, R6), its
// billing party and the status history. idCardNumber (an individual carrier's ID card, PII) is shown to
// platform admins, stewards and the tenant's own tenant_admin only.
type TenantDetail struct {
	Tenant
	IDCardNumber     *string         `json:"idCardNumber,omitempty"`
	TaxID            *string         `json:"taxId"`
	ContactPerson    *string         `json:"contactPerson"`
	Phone            *string         `json:"phone"`
	Email            *string         `json:"email"`
	Website          *string         `json:"website"`
	Address          *string         `json:"address"`
	Designation      *string         `json:"designation"`
	FleetSize        int32           `json:"fleetSize"`
	DispatchCenter   *string         `json:"dispatchCenter"`
	ServiceRegions   []string        `json:"serviceRegions"`
	VehicleTypes     []string        `json:"vehicleTypes"`
	LineGroupID      *string         `json:"lineGroupId"`
	BillingDateBasis *string         `json:"billingDateBasis"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	StatusHistory    []StatusHistory `json:"statusHistory"`
}

// StatusHistory is one status_history row of a tenant.
type StatusHistory struct {
	Status         string     `json:"status"`
	PreviousStatus *string    `json:"previousStatus"`
	ChangedAt      time.Time  `json:"changedAt"`
	ChangedBy      *uuid.UUID `json:"changedBy"`
	ChangedByLabel *string    `json:"changedByLabel"`
	Reason         *string    `json:"reason"`
}

var (
	tenantKinds    = []string{authz.TenantKindOwnFleet, authz.TenantKindCarrier, authz.TenantKindQuarantine}
	tenantStatuses = []string{"active", "pending", "suspended"}
	tenantCodeRx   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	lineGroupRx    = regexp.MustCompile(`^C[0-9a-f]{32}$`)
)

func tenantOf(id uuid.UUID, kind string, code *string, nameTh string, nameEn *string, status string, legal *string,
	contractor *uuid.UUID, created time.Time) Tenant {
	return Tenant{ID: id, Kind: kind, Code: code, NameTh: nameTh, NameEn: nameEn, Status: status, LegalType: legal,
		ContractorTenantID: contractor, CreatedAt: created}
}

// ListTenants is GET /v1/tenants (platform:manage_tenants, or fleet:manage_subcontractors: a steward sees
// carriers only), newest first.
func (a *Admin) ListTenants(c call, kind, status string, limit int, cur string) ([]Tenant, string, error) {
	var bad []httpx.FieldViolation
	var kindPtr, statusPtr *string
	if kind != "" {
		if !slices.Contains(tenantKinds, kind) {
			bad = append(bad, violation("kind", "invalid"))
		}
		kindPtr = &kind
	}
	if status != "" {
		if !slices.Contains(tenantStatuses, status) {
			bad = append(bad, violation("status", "invalid"))
		}
		statusPtr = &status
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit || (c.p.IsPlatform() && limit > 100) {
		bad = append(bad, violation("limit", "out_of_range"))
	}
	var k cursor
	if cur != "" {
		var ok bool
		if k, ok = decodeCursor(cur, false); !ok {
			bad = append(bad, violation("cursor", "invalid"))
		}
	}
	if len(bad) > 0 {
		return nil, "", errInvalid(bad...)
	}
	var out []Tenant
	var next string
	err := a.read(c.ctx, func(q *iamdb.Queries) error {
		rows, err := q.ListTenants(c.ctx, iamdb.ListTenantsParams{Kind: kindPtr, CarriersOnly: !c.p.Can(authz.PlatformManageTenants),
			Status: statusPtr, AfterCreated: k.T, AfterID: k.ID, RowLimit: int32(limit) + 1})
		if err != nil {
			return err
		}
		if len(rows) > limit {
			rows = rows[:limit]
			last := rows[limit-1]
			t := last.CreatedAt
			next = encodeCursor(&t, last.ID)
		}
		out = make([]Tenant, 0, len(rows))
		for _, r := range rows {
			out = append(out, tenantOf(r.ID, r.Kind, r.Code, r.NameTh, r.NameEn, r.Status, r.LegalType, r.ContractorTenantID, r.CreatedAt))
		}
		return nil
	})
	if err == nil && (c.p.IsPlatform() || authz.IsSupport(c.p)) {
		ids := make([]uuid.UUID, len(out))
		for i := range out {
			ids[i] = out[i].ID
		}
		c.labels, err = a.loadPlatformLabels(c, ids)
		if err != nil {
			return nil, "", err
		}
		for i := range out {
			if e := a.projectTenantLabels(c, &out[i]); e != nil {
				return nil, "", e
			}
		}
	}
	return out, next, err
}

// GetTenant is GET /v1/tenants/{id}: platform:manage_tenants; a steward with fleet:manage_subcontractors for
// a carrier; or an active member of the tenant (also a platform principal acting as it). Anyone else 404.
func (a *Admin) GetTenant(c call, id uuid.UUID) (*TenantDetail, error) {
	var out *TenantDetail
	err := a.read(c.ctx, func(q *iamdb.Queries) error {
		t, err := q.GetTenant(c.ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		p := c.p
		if authz.IsSupport(p) && (p.TenantID == nil || *p.TenantID != id) {
			return httpx.ErrNotFound()
		}
		manage := !authz.IsSupport(p) && p.Can(authz.PlatformManageTenants)
		steward := !authz.IsSupport(p) && p.Can(authz.FleetManageSubcontractors) && t.Kind == authz.TenantKindCarrier
		acting := authz.IsSupport(p) && p.TenantID != nil && *p.TenantID == id
		member := false
		if !manage && !steward && !acting && t.Kind != authz.TenantKindQuarantine {
			if member, err = q.IsActiveMember(c.ctx, iamdb.IsActiveMemberParams{UserID: p.UserID, TenantID: id}); err != nil {
				return err
			}
		}
		if !manage && !steward && !acting && !member {
			return httpx.ErrNotFound()
		}
		hist, err := q.TenantStatusHistory(c.ctx, id)
		if err != nil {
			return err
		}
		d := &TenantDetail{
			Tenant: tenantOf(t.ID, t.Kind, t.Code, t.NameTh, t.NameEn, t.Status, t.LegalType, t.ContractorTenantID, t.CreatedAt),
			TaxID:  t.TaxID, ContactPerson: t.ContactPerson, Phone: t.Phone, Email: t.Email, Website: t.Website,
			Address: t.Address, Designation: t.Designation, FleetSize: t.FleetSize, DispatchCenter: t.DispatchCenter,
			ServiceRegions: t.ServiceRegions, VehicleTypes: t.VehicleTypes, LineGroupID: t.LineGroupID,
			BillingDateBasis: t.BillingDateBasis, UpdatedAt: t.UpdatedAt, StatusHistory: make([]StatusHistory, 0, len(hist)),
		}
		d.BillingPartyID = t.BillingPartyID
		if seesTenantPII(p, id, t.Kind) {
			d.IDCardNumber = t.IDCardNumber
		}
		for _, h := range hist {
			d.StatusHistory = append(d.StatusHistory, StatusHistory{Status: h.Status, PreviousStatus: h.PreviousStatus,
				ChangedAt: h.ChangedAt, ChangedBy: h.ChangedByUserID, ChangedByLabel: h.ChangedByLabel, Reason: h.Reason})
		}
		out = d
		return nil
	})
	if err == nil && out != nil {
		err = a.projectTenantLabels(c, &out.Tenant)
	}
	return out, err
}

// seesTenantPII: an individual carrier's ID card (number and documents) is shown to platform admins, stewards with
// fleet:manage_subcontractors and the tenant's own tenant_admin only (Appendix C §C.8).
func seesTenantPII(p *authz.Principal, id uuid.UUID, kind string) bool {
	if authz.IsSupport(p) {
		return p.TenantID != nil && *p.TenantID == id
	}
	if p.Can(authz.PlatformManageTenants) || (p.Can(authz.FleetManageSubcontractors) && kind == authz.TenantKindCarrier) {
		return true
	}
	return isOwnTenantAdmin(p, id)
}

// isOwnTenantAdmin: the request acts as the tenant_admin of tenant id (a platform admin acting through
// X-Act-On-Tenant: <id> included).
func isOwnTenantAdmin(p *authz.Principal, id uuid.UUID) bool {
	eff := p.EffectiveTenant()
	return !authz.IsSupport(p) && !p.IsMachine() && eff != nil && *eff == id && p.EffectiveRole() == authz.TenantAdmin
}

// CreateTenantInput is the body of POST /v1/tenants: a carrier tenant with its profile.
type CreateTenantInput struct {
	Kind               string   `json:"kind"`
	Code               string   `json:"code"`
	NameTh             string   `json:"nameTh"`
	NameEn             string   `json:"nameEn"`
	LegalType          string   `json:"legalType"`
	IDCardNumber       string   `json:"idCardNumber"`
	TaxID              string   `json:"taxId"`
	ContactPerson      string   `json:"contactPerson"`
	Phone              string   `json:"phone"`
	Email              string   `json:"email"`
	Website            string   `json:"website"`
	Address            string   `json:"address"`
	Designation        string   `json:"designation"`
	FleetSize          int32    `json:"fleetSize"`
	DispatchCenter     string   `json:"dispatchCenter"`
	ServiceRegions     []string `json:"serviceRegions"`
	VehicleTypes       []string `json:"vehicleTypes"`
	LineGroupID        string   `json:"lineGroupId"`
	ContractorTenantID string   `json:"contractorTenantId"`
	Status             string   `json:"status"`
}

// ValidThaiID checks the 13-digit Thai national ID / tax id checksum: the 13th digit is (11 - sum of the
// first 12 digits weighted 13..2 mod 11) mod 10.
func ValidThaiID(s string) bool {
	if len(s) != 13 {
		return false
	}
	sum := 0
	for i := range 13 {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
		if i < 12 {
			sum += int(c-'0') * (13 - i)
		}
	}
	return int(s[12]-'0') == (11-sum%11)%10
}

func optText(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}

func cleanList(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func checkName(field, s string, required bool, bad *[]httpx.FieldViolation) {
	switch n := utf8.RuneCountInString(strings.TrimSpace(s)); {
	case n == 0 && required:
		*bad = append(*bad, violation(field, "required"))
	case n > 200:
		*bad = append(*bad, violation(field, "too_long"))
	}
}

// CreateTenant is POST /v1/tenants (platform:manage_tenants; T19 owner addition): a carrier tenant (the
// own-fleet row comes from seed or ETL and the quarantine row from migration 0002, R56) with its
// billing_parties row, its first status_history entry, tenant_created and outbox tenant.created in one
// transaction. A platform admin then makes a user its tenant_admin with POST /v1/users {tenantId} or
// PUT /v1/tenants/{id}/members/{userId}.
func (a *Admin) CreateTenant(c call, in CreateTenantInput) (*Tenant, error) {
	if err := platformRead(c.p, authz.PlatformManageTenants); err != nil {
		return nil, err
	}

	var bad []httpx.FieldViolation
	if in.Kind != authz.TenantKindCarrier {
		bad = append(bad, violation("kind", "invalid"))
	}
	if strings.TrimSpace(in.Code) == "" {
		bad = append(bad, violation("code", "required"))
	}
	nc, more := validateCarrier(in, false)
	bad = append(bad, more...)
	if s := strings.TrimSpace(in.ContractorTenantID); s != "" {
		id, err := uuid.Parse(s)
		if err != nil || id == uuid.Nil {
			bad = append(bad, violation("contractorTenantId", "invalid"))
		}
		nc.params.ContractorTenantID = &id
	}
	if len(bad) > 0 {
		return nil, errInvalid(bad...)
	}
	nc.basis = billingBasisDelivered
	var out *Tenant
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, _ func(*auth.PostCommit)) error {
		if contractor := nc.params.ContractorTenantID; contractor != nil {
			if err := checkContractor(c, q, uuid.Nil, *contractor); err != nil {
				return err
			}
		}
		t, err := a.insertCarrier(c, tx, q, nc, "tenants")
		out = t
		return err
	})
	if err != nil {
		return nil, err
	}
	if out != nil {
		if err := a.projectTenantLabels(c, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// billingBasisDelivered is the default billing_date_basis of a billing party (ADR 0027).
const billingBasisDelivered = "delivered"

// billingBases are the billing_parties.billing_date_basis values (ADR 0027).
var billingBases = []string{"delivered", "plan"}

// newCarrier is a validated carrier of POST /v1/tenants or POST /v1/subcontractors.
type newCarrier struct {
	params    iamdb.InsertTenantParams
	basis     string   // billing_parties.billing_date_basis
	documents []string // tenant_document keys (POST /v1/subcontractors)
}

// validateCarrier checks the profile of a new carrier. With requireIDs (POST /v1/subcontractors, the rule of the
// legacy subcontractorSchema) an individual needs its ID card number and a company its tax id; both always pass
// the 13-digit Thai checksum when present (T20 acceptance: 422 with field errors).
func validateCarrier(in CreateTenantInput, requireIDs bool) (newCarrier, []httpx.FieldViolation) {
	var bad []httpx.FieldViolation
	var code *string
	if s := strings.TrimSpace(in.Code); s != "" {
		if !tenantCodeRx.MatchString(s) {
			bad = append(bad, violation("code", "invalid"))
		}
		code = &s
	}
	checkName("nameTh", in.NameTh, true, &bad)
	checkName("nameEn", in.NameEn, false, &bad)
	legal := in.LegalType
	if legal != "individual" && legal != "company" {
		bad = append(bad, violation("legalType", "invalid"))
	}
	bad = append(bad, checkThaiIDs(legal, in.IDCardNumber, in.TaxID, requireIDs)...)
	var email *string
	if s := strings.TrimSpace(in.Email); s != "" {
		e, ok := normalizeEmail(s)
		if !ok {
			bad = append(bad, violation("email", "invalid"))
		}
		email = &e
	}
	if in.FleetSize < 0 {
		bad = append(bad, violation("fleetSize", "invalid"))
	}
	if s := strings.TrimSpace(in.LineGroupID); s != "" && !lineGroupRx.MatchString(s) {
		bad = append(bad, violation("lineGroupId", "invalid"))
	}
	status := in.Status
	if status == "" {
		status = "active"
	}
	if !slices.Contains(tenantStatuses, status) {
		bad = append(bad, violation("status", "invalid"))
	}
	for _, f := range []struct{ name, v string }{{"contactPerson", in.ContactPerson}, {"phone", in.Phone},
		{"website", in.Website}, {"designation", in.Designation}, {"dispatchCenter", in.DispatchCenter}} {
		checkName(f.name, f.v, false, &bad)
	}
	if utf8.RuneCountInString(in.Address) > 1000 {
		bad = append(bad, violation("address", "too_long"))
	}
	return newCarrier{params: iamdb.InsertTenantParams{
		Code: code, NameTh: strings.TrimSpace(in.NameTh), NameEn: optText(in.NameEn), LegalType: &legal,
		IDCardNumber: optText(in.IDCardNumber), TaxID: optText(in.TaxID), ContactPerson: optText(in.ContactPerson),
		Phone: optText(in.Phone), Email: email, Website: optText(in.Website), Address: optText(in.Address),
		Designation: optText(in.Designation), FleetSize: in.FleetSize, DispatchCenter: optText(in.DispatchCenter),
		ServiceRegions: cleanList(in.ServiceRegions), VehicleTypes: cleanList(in.VehicleTypes),
		LineGroupID: optText(in.LineGroupID), Status: status,
	}}, bad
}

// checkThaiIDs validates the ID card number and the tax id of a carrier: the 13-digit Thai checksum whenever a value
// is present, and with required the one its legal type needs (individual: idCardNumber, company: taxId).
func checkThaiIDs(legal, idCard, taxID string, required bool) []httpx.FieldViolation {
	var bad []httpx.FieldViolation
	for _, f := range []struct{ name, v, needs string }{{"idCardNumber", idCard, "individual"}, {"taxId", taxID, "company"}} {
		switch s := strings.TrimSpace(f.v); {
		case s != "" && !ValidThaiID(s):
			bad = append(bad, violation(f.name, "invalid"))
		case s == "" && required && legal == f.needs:
			bad = append(bad, violation(f.name, "required"))
		}
	}
	return bad
}

// insertCarrier writes a validated carrier in tx: the tenant, its billing party, its first status_history entry,
// its documents (POST /v1/subcontractors), tenant_created and outbox tenant.created (source names the route).
func (a *Admin) insertCarrier(c call, tx pgx.Tx, q *iamdb.Queries, nc newCarrier, source string) (*Tenant, error) {
	r, err := q.InsertTenant(c.ctx, nc.params)
	if err != nil {
		return nil, err
	}
	party, err := q.InsertTenantParty(c.ctx, iamdb.InsertTenantPartyParams{TenantID: &r.ID, BillingDateBasis: nc.basis})
	if err != nil {
		return nil, err
	}
	actor := c.p.UserID
	if err := q.InsertTenantStatusHistory(c.ctx, iamdb.InsertTenantStatusHistoryParams{EntityID: r.ID, Status: r.Status,
		ChangedAt: a.clock(), ChangedByUserID: &actor}); err != nil {
		return nil, err
	}
	if len(nc.documents) > 0 {
		if _, err := a.setTenantDocuments(c, tx, q, r.ID, nc.documents); err != nil {
			return nil, err
		}
	}
	t := tenantOf(r.ID, r.Kind, r.Code, r.NameTh, r.NameEn, r.Status, r.LegalType, r.ContractorTenantID, r.CreatedAt)
	t.BillingPartyID = &party
	details := map[string]any{"kind": r.Kind, "code": r.Code, "contractorTenantId": r.ContractorTenantID, "billingPartyId": party}
	if source != "tenants" {
		details["source"] = source
	}
	if err := a.audit(c, tx, EventTenantCreated, "carrier tenant created", nil, &r.ID, details); err != nil {
		return nil, err
	}
	if err := emit(c, tx, RouteTenantCreated, "tenant", r.ID.String(), &r.ID,
		tenantEvent{TenantID: r.ID, ContractorTenantID: r.ContractorTenantID}); err != nil {
		return nil, err
	}
	return &t, nil
}

// tenantEvent is the payload of outbox tenant.created / tenant.updated: the cache layer drops the
// contractor reach of both contractors (cache:tenant:subtenants:{id}, Appendix C §C.4.14).
type tenantEvent struct {
	TenantID                   uuid.UUID  `json:"tenantId"`
	ContractorTenantID         *uuid.UUID `json:"contractorTenantId,omitempty"`
	PreviousContractorTenantID *uuid.UUID `json:"previousContractorTenantId,omitempty"`
	Fields                     []string   `json:"fields,omitempty"`
}

// checkContractor: the tenant a carrier works for exists, is the own fleet or a carrier, is not the tenant
// itself and works for nobody (contractor reach is one level, R60); a tenant that others work for cannot get
// a contractor either.
func checkContractor(c call, q *iamdb.Queries, self, contractor uuid.UUID) error {
	if contractor == self {
		return errInvalid(violation("contractorTenantId", "self"))
	}
	ct, err := q.LockTenant(c.ctx, contractor)
	if errors.Is(err, pgx.ErrNoRows) {
		return errInvalid(violation("contractorTenantId", "not_found"))
	}
	if err != nil {
		return err
	}
	if ct.Kind == authz.TenantKindQuarantine || ct.ContractorTenantID != nil {
		return errInvalid(violation("contractorTenantId", "not_a_contractor"))
	}
	if self != uuid.Nil {
		subs, err := q.Subtenants(c.ctx, self)
		if err != nil {
			return err
		}
		if len(subs) > 0 {
			return errInvalid(violation("contractorTenantId", "has_subtenants"))
		}
	}
	return nil
}

// PatchTenantInput is the body of PATCH /v1/tenants/{id}: absent fields stay; nameEn and contractorTenantId
// null clear them.
type PatchTenantInput struct {
	Code               Field[string] `json:"code"`
	Kind               Field[string] `json:"kind"`
	Status             Field[string] `json:"status"`
	NameTh             Field[string] `json:"nameTh"`
	NameEn             Field[string] `json:"nameEn"`
	ContractorTenantID Field[string] `json:"contractorTenantId"`
	Reason             Field[string] `json:"reason"`
}

// PatchTenant is PATCH /v1/tenants/{id} (platform:manage_tenants): code, status (appends status_history),
// contractorTenantId (tenant_contractor_changed) and the names (T18 owner addition). The kind never changes,
// and the own-fleet and quarantine rows keep their status (409 failed_precondition: suspending the own fleet
// would drop the tid of every staff session). Suspending or reactivating a carrier bumps the auth_version of every
// active member (claims_changed: their tokens lose or regain the tid at once, C.4.7). tenant_updated and outbox
// tenant.updated in the same transaction; a request that changes nothing writes nothing.
func (a *Admin) PatchTenant(c call, id uuid.UUID, in PatchTenantInput) (*Tenant, error) {
	if authz.IsSupport(c.p) && !lifecycleOnly(in) {
		return nil, httpx.ErrSupportReadOnly()
	}
	if err := platformRead(c.p, authz.PlatformManageTenants); err != nil {
		return nil, err
	}

	var bad []httpx.FieldViolation
	var code *string
	if in.Code.Set {
		s := strings.TrimSpace(in.Code.V)
		if in.Code.Null || !tenantCodeRx.MatchString(s) {
			bad = append(bad, violation("code", "invalid"))
		}
		code = &s
	}
	if in.Kind.Set && (in.Kind.Null || !slices.Contains(tenantKinds, in.Kind.V)) {
		bad = append(bad, violation("kind", "invalid"))
	}
	if in.Status.Set && (in.Status.Null || !slices.Contains(tenantStatuses, in.Status.V)) {
		bad = append(bad, violation("status", "invalid"))
	}
	if in.NameTh.Set {
		if in.NameTh.Null {
			bad = append(bad, violation("nameTh", "required"))
		} else {
			checkName("nameTh", in.NameTh.V, true, &bad)
		}
	}
	var nameEn *string
	if in.NameEn.Set && !in.NameEn.Null {
		checkName("nameEn", in.NameEn.V, false, &bad)
		nameEn = optText(in.NameEn.V)
	}
	var contractor *uuid.UUID
	if in.ContractorTenantID.Set && !in.ContractorTenantID.Null {
		cid, err := uuid.Parse(strings.TrimSpace(in.ContractorTenantID.V))
		if err != nil || cid == uuid.Nil {
			bad = append(bad, violation("contractorTenantId", "invalid"))
		}
		contractor = &cid
	}
	if in.Reason.Set && utf8.RuneCountInString(in.Reason.V) > 500 {
		bad = append(bad, violation("reason", "too_long"))
	}
	if len(bad) > 0 {
		return nil, errInvalid(bad...)
	}
	var out *Tenant
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		// Lock the membership snapshot's users before the tenant, as on consent and D2 paths.
		var statusMembers []uuid.UUID
		if in.Status.Set {
			var err error
			statusMembers, err = q.ActiveTenantMembers(c.ctx, id)
			if err != nil {
				return err
			}
			if err := lockSupportUsers(c, q, statusMembers...); err != nil {
				return err
			}
		}
		cur, err := q.LockTenant(c.ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if in.Status.Set {
			current, err := q.ActiveTenantMembers(c.ctx, id)
			if err != nil {
				return err
			}
			if !slices.Equal(current, statusMembers) {
				return errFailedPrecondition("memberships_changed", "tenant memberships changed; retry")
			}
		}
		structural := cur.Kind == authz.TenantKindOwnFleet || cur.Kind == authz.TenantKindQuarantine
		if in.Kind.Set && in.Kind.V != cur.Kind {
			return errFailedPrecondition("kind_immutable", "the kind of a tenant does not change")
		}
		statusChanged := in.Status.Set && in.Status.V != cur.Status
		if statusChanged && structural {
			return errFailedPrecondition("structural_tenant", "the status of the own-fleet and quarantine tenants does not change")
		}
		var fields []string
		params := iamdb.UpdateTenantParams{ID: id}
		if code != nil && (cur.Code == nil || *cur.Code != *code) {
			params.SetCode, params.Code = true, code
			fields = append(fields, "code")
		}
		if in.NameTh.Set && strings.TrimSpace(in.NameTh.V) != cur.NameTh {
			n := strings.TrimSpace(in.NameTh.V)
			params.NameTh = &n
			fields = append(fields, "nameTh")
		}
		if in.NameEn.Set && !equalPtr(cur.NameEn, nameEn) {
			params.SetNameEn, params.NameEn = true, nameEn
			fields = append(fields, "nameEn")
		}
		if statusChanged {
			params.Status = &in.Status.V
			fields = append(fields, "status")
		}
		contractorChanged := in.ContractorTenantID.Set && !equalID(cur.ContractorTenantID, contractor)
		if contractorChanged {
			if cur.Kind != authz.TenantKindCarrier {
				return errInvalid(violation("contractorTenantId", "carrier_only"))
			}
			if contractor != nil {
				if err := checkContractor(c, q, id, *contractor); err != nil {
					return err
				}
			}
			params.SetContractor, params.ContractorTenantID = true, contractor
			fields = append(fields, "contractorTenantId")
		}
		t := tenantOf(cur.ID, cur.Kind, cur.Code, cur.NameTh, cur.NameEn, cur.Status, cur.LegalType, cur.ContractorTenantID, cur.CreatedAt)
		out = &t
		if len(fields) == 0 {
			return nil
		}
		r, err := q.UpdateTenant(c.ctx, params)
		if err != nil {
			return err
		}
		t = tenantOf(r.ID, r.Kind, r.Code, r.NameTh, r.NameEn, r.Status, r.LegalType, r.ContractorTenantID, r.CreatedAt)
		actor := c.p.UserID
		if statusChanged {
			var reason *string
			if in.Reason.Set && !in.Reason.Null {
				reason = optText(in.Reason.V)
			}
			prev := cur.Status
			if err := q.InsertTenantStatusHistory(c.ctx, iamdb.InsertTenantStatusHistoryParams{EntityID: id, Status: r.Status,
				PreviousStatus: &prev, ChangedAt: a.clock(), ChangedByUserID: &actor, Reason: reason}); err != nil {
				return err
			}
		}
		if contractorChanged {
			if err := a.audit(c, tx, EventTenantContractor, "tenant contractor changed", nil, &id,
				map[string]any{"from": cur.ContractorTenantID, "to": contractor}); err != nil {
				return err
			}
		}
		details := map[string]any{"fields": fields}
		if statusChanged {
			details["status"], details["previousStatus"] = r.Status, cur.Status
		}
		// A suspended tenant is never issued as tid (auth: usable memberships), so suspending or reactivating it
		// changes every active member's claims: auth_version++ and claims_changed now (C.4.7), as removing the
		// membership would, instead of leaving live tokens on the suspended tenant until JWT_ACCESS_TTL.
		if statusChanged && (cur.Status == "suspended" || r.Status == "suspended") {
			members, err := q.ActiveTenantMembers(c.ctx, id)
			if err != nil {
				return err
			}
			for _, uid := range members {
				if err := a.claimsChanged(c, tx, uid, keep); err != nil {
					return err
				}
			}
			details["membersNotified"] = len(members)
		}
		if err := a.audit(c, tx, EventTenantUpdated, "tenant updated", nil, &id, details); err != nil {
			return err
		}
		return emit(c, tx, RouteTenantUpdated, "tenant", id.String(), &id, tenantEvent{TenantID: id,
			ContractorTenantID: r.ContractorTenantID, PreviousContractorTenantID: cur.ContractorTenantID, Fields: fields})
	})
	if err != nil {
		return nil, err
	}
	if out != nil {
		if err := a.projectTenantLabels(c, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func equalID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
