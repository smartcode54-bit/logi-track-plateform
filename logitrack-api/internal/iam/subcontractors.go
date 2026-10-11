package iam

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Carrier profiles: /v1/subcontractors* (T20, Appendix B §B.2.10). A subcontractor is a tenant of kind carrier with
// its billing_parties row (no subcontractors table, R6); these routes are the profile view of those rows, while the
// structural columns (code, kind, status, contractorTenantId) stay on PATCH /v1/tenants/{id} (T19, trigger
// t_tenant_admin_columns).
//
//   - GET /v1/subcontractors?fields=minimal: every authenticated principal, rows by RLS (tenants p_read: the own
//     tenant reach, every carrier for a steward); a scope principal (customer scope, dispatcher) reads carrier names
//     from the scope_tenants projection only (Appendix C §C.3.7). Without fields=minimal: fleet:manage_subcontractors
//     or platform:manage_tenants.
//   - POST /v1/subcontractors: steward onboarding (R60): fleet:manage_subcontractors in the own fleet (the new
//     carrier works for it, contractor reach), or platform:manage_tenants (contractor = the own fleet too).
//   - GET /v1/subcontractors/{id}: the rule of GET /v1/tenants/{id}; carriers only.
//   - PATCH /v1/subcontractors/{id}: profile fields; a steward with fleet:manage_subcontractors edits any carrier, a
//     carrier's own tenant_admin its own profile; billingDateBasis (a billing setting of the global billing party)
//     only a steward.

// Reasons of a refused subcontractor write.
const (
	ReasonOwnFleetOnly = "own_fleet_only" // carriers are onboarded from the own fleet (or by a platform admin)
	ReasonMachine      = "machine_principal"
)

// MaxTenantDocuments bounds the documents of one carrier profile.
const MaxTenantDocuments = 20

// SubcontractorMinimal is one item of GET /v1/subcontractors?fields=minimal (pickers). billingPartyId and status
// are absent for a scope principal, whose projection (scope_tenants) has neither.
type SubcontractorMinimal struct {
	ID             uuid.UUID  `json:"id"`
	Code           *string    `json:"code"`
	NameTh         string     `json:"nameTh"`
	NameEn         *string    `json:"nameEn"`
	Status         string     `json:"status,omitempty"`
	BillingPartyID *uuid.UUID `json:"billingPartyId,omitempty"`
}

// SubcontractorSummary is one item of GET /v1/subcontractors (the carrier list).
type SubcontractorSummary struct {
	ID                 uuid.UUID  `json:"id"`
	Code               *string    `json:"code"`
	NameTh             string     `json:"nameTh"`
	NameEn             *string    `json:"nameEn"`
	LegalType          *string    `json:"legalType"`
	Status             string     `json:"status"`
	ContactPerson      *string    `json:"contactPerson"`
	Phone              *string    `json:"phone"`
	Email              *string    `json:"email"`
	FleetSize          int32      `json:"fleetSize"`
	LineGroupID        *string    `json:"lineGroupId"`
	ContractorTenantID *uuid.UUID `json:"contractorTenantId"`
	BillingPartyID     *uuid.UUID `json:"billingPartyId"`
	BillingDateBasis   *string    `json:"billingDateBasis"`
	CreatedAt          time.Time  `json:"createdAt"`
}

// TenantDocument is one file of a carrier profile (tenant_files): a private object, read only through its
// short-lived URL (an ID card for 5 minutes, main spec §9.5).
type TenantDocument struct {
	FileID   uuid.UUID `json:"fileId"`
	Key      *string   `json:"key"`
	URL      *string   `json:"url"` // nil while the object is missing at source or cannot be signed
	Kind     string    `json:"kind"`
	Position int32     `json:"position"`
}

// Subcontractor is GET /v1/subcontractors/{id} and the answer of POST and PATCH: the tenant profile with its
// documents. Documents of kind id_card follow the rule of idCardNumber (seesTenantPII).
type Subcontractor struct {
	TenantDetail
	Documents []TenantDocument `json:"documents"`
}

// ListSubcontractorsInput are the query parameters of GET /v1/subcontractors.
type ListSubcontractorsInput struct {
	Fields, Q, Status string
	Limit             int
	Cursor            string
}

// nameCursor is the keyset position (name, id) of a list ordered by name, base64url JSON.
type nameCursor struct {
	S  string    `json:"s"`
	ID uuid.UUID `json:"id"`
}

func encodeNameCursor(s string, id uuid.UUID) string {
	b, _ := json.Marshal(nameCursor{S: s, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeNameCursor(s string) (nameCursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nameCursor{}, false
	}
	var c nameCursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil {
		return nameCursor{}, false
	}
	return c, true
}

// readAs runs fn in a request transaction of p: RLS decides the rows (tenants p_read for the carrier lists).
func (a *Admin) readAs(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx) error) error {
	return mapDBError(db.WithPrincipal(ctx, a.pool, p, fn))
}

// ListSubcontractors is GET /v1/subcontractors: []SubcontractorMinimal with fields=minimal, else
// []SubcontractorSummary; ordered by Thai name.
func (a *Admin) ListSubcontractors(c call, in ListSubcontractorsInput) (any, string, error) {
	var bad []httpx.FieldViolation
	minimal := in.Fields == "minimal"
	if in.Fields != "" && !minimal {
		bad = append(bad, violation("fields", "invalid"))
	}
	var status, q *string
	if in.Status != "" {
		if !slices.Contains(tenantStatuses, in.Status) {
			bad = append(bad, violation("status", "invalid"))
		}
		status = &in.Status
	}
	if s := strings.TrimSpace(in.Q); s != "" {
		if utf8.RuneCountInString(s) > 100 {
			bad = append(bad, violation("q", "too_long"))
		}
		q = &s
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		bad = append(bad, violation("limit", "out_of_range"))
	}
	var after nameCursor
	if in.Cursor != "" {
		var ok bool
		if after, ok = decodeNameCursor(in.Cursor); !ok {
			bad = append(bad, violation("cursor", "invalid"))
		}
	}
	if len(bad) > 0 {
		return nil, "", errInvalid(bad...)
	}
	p := c.p
	if !minimal && !p.Can(authz.FleetManageSubcontractors) && !p.Can(authz.PlatformManageTenants) {
		return nil, "", authz.ErrPermissionDenied("missing capability", authz.FleetManageSubcontractors, authz.PlatformManageTenants)
	}
	var afterName *string
	if in.Cursor != "" {
		afterName = &after.S
	}
	// A scope principal reads carrier names through its projection only (Appendix C §C.3.7, R85).
	if minimal && (p.IsCustomerScope() || p.Dispatcher) && !p.ActOnAll {
		if status != nil {
			return nil, "", errInvalid(violation("status", "not_available"))
		}
		var rows []scopedb.ScopeTenant
		err := a.readAs(c.ctx, p, func(tx pgx.Tx) (err error) {
			rows, err = scopedb.New(tx).ListScopeCarriers(c.ctx, scopedb.ListScopeCarriersParams{Q: q, AfterName: afterName,
				AfterID: after.ID, RowLimit: int32(limit) + 1})
			return err
		})
		if err != nil {
			return nil, "", err
		}
		next := ""
		if len(rows) > limit {
			rows = rows[:limit]
			next = encodeNameCursor(rows[limit-1].NameTh, rows[limit-1].ID)
		}
		out := make([]SubcontractorMinimal, 0, len(rows))
		for _, r := range rows {
			out = append(out, SubcontractorMinimal{ID: r.ID, Code: r.Code, NameTh: r.NameTh, NameEn: r.NameEn})
		}
		return out, next, nil
	}
	var rows []iamdb.ListCarrierProfilesRow
	err := a.readAs(c.ctx, p, func(tx pgx.Tx) (err error) {
		rows, err = iamdb.New(tx).ListCarrierProfiles(c.ctx, iamdb.ListCarrierProfilesParams{Status: status, Q: q,
			AfterName: afterName, AfterID: after.ID, RowLimit: int32(limit) + 1})
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = encodeNameCursor(rows[limit-1].NameTh, rows[limit-1].ID)
	}
	if minimal {
		out := make([]SubcontractorMinimal, 0, len(rows))
		for _, r := range rows {
			out = append(out, SubcontractorMinimal{ID: r.ID, Code: r.Code, NameTh: r.NameTh, NameEn: r.NameEn,
				Status: r.Status, BillingPartyID: r.BillingPartyID})
		}
		return out, next, nil
	}
	out := make([]SubcontractorSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, SubcontractorSummary{ID: r.ID, Code: r.Code, NameTh: r.NameTh, NameEn: r.NameEn,
			LegalType: r.LegalType, Status: r.Status, ContactPerson: r.ContactPerson, Phone: r.Phone, Email: r.Email,
			FleetSize: r.FleetSize, LineGroupID: r.LineGroupID, ContractorTenantID: r.ContractorTenantID,
			BillingPartyID: r.BillingPartyID, BillingDateBasis: r.BillingDateBasis, CreatedAt: r.CreatedAt})
	}
	return out, next, nil
}

// GetSubcontractor is GET /v1/subcontractors/{id}: the visibility rule of GET /v1/tenants/{id} (platform, a steward
// for a carrier, an active member or a platform admin acting as it), carriers only (anything else 404), with the
// profile's documents.
func (a *Admin) GetSubcontractor(c call, id uuid.UUID) (*Subcontractor, error) {
	d, err := a.GetTenant(c, id)
	if err != nil {
		return nil, err
	}
	if d.Kind != authz.TenantKindCarrier {
		return nil, httpx.ErrNotFound()
	}
	var rows []iamdb.TenantDocumentsRow
	if err := a.read(c.ctx, func(q *iamdb.Queries) (err error) {
		rows, err = q.TenantDocuments(c.ctx, id)
		return err
	}); err != nil {
		return nil, err
	}
	pii := seesTenantPII(c.p, id, d.Kind)
	out := &Subcontractor{TenantDetail: *d, Documents: make([]TenantDocument, 0, len(rows))}
	for _, r := range rows {
		if r.Kind == "id_card" && !pii {
			continue
		}
		key := r.ObjectKey
		doc := TenantDocument{FileID: r.FileID, Key: &key, Kind: r.Kind, Position: r.Position}
		if a.files != nil {
			_, u, err := a.files.SignedFile(c.ctx, r.FileID, 0)
			if err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
				a.log.Warn().Err(err).Str("file_id", r.FileID.String()).Msg("tenant document: cannot sign its URL")
			}
			if u != "" {
				doc.URL = &u
			}
		}
		out.Documents = append(out.Documents, doc)
	}
	return out, nil
}

// CreateSubcontractorInput is the body of POST /v1/subcontractors: the carrier profile of the legacy wizard
// (subcontractorSchema), its billing date basis and the keys of its uploaded documents (purpose tenant_document).
type CreateSubcontractorInput struct {
	Code             string   `json:"code"`
	NameTh           string   `json:"nameTh"`
	NameEn           string   `json:"nameEn"`
	LegalType        string   `json:"legalType"`
	IDCardNumber     string   `json:"idCardNumber"`
	TaxID            string   `json:"taxId"`
	ContactPerson    string   `json:"contactPerson"`
	Phone            string   `json:"phone"`
	Email            string   `json:"email"`
	Website          string   `json:"website"`
	Address          string   `json:"address"`
	Designation      string   `json:"designation"`
	FleetSize        int32    `json:"fleetSize"`
	DispatchCenter   string   `json:"dispatchCenter"`
	ServiceRegions   []string `json:"serviceRegions"`
	VehicleTypes     []string `json:"vehicleTypes"`
	LineGroupID      string   `json:"lineGroupId"`
	Status           string   `json:"status"`
	BillingDateBasis string   `json:"billingDateBasis"`
	DocumentKeys     []string `json:"documentKeys"`
}

// checkDocumentKeys validates a documents list: at most MaxTenantDocuments distinct keys.
func checkDocumentKeys(keys []string) []httpx.FieldViolation {
	if len(keys) > MaxTenantDocuments {
		return []httpx.FieldViolation{{Field: "documentKeys", Reason: "too_many", Params: map[string]any{"max": MaxTenantDocuments}}}
	}
	var bad []httpx.FieldViolation
	for i, k := range keys {
		switch {
		case k == "" || len(k) > storage.MaxKeyBytes:
			bad = append(bad, violation(fmt.Sprintf("documentKeys[%d]", i), "invalid"))
		case slices.Contains(keys[:i], k):
			bad = append(bad, violation(fmt.Sprintf("documentKeys[%d]", i), "duplicate"))
		}
	}
	return bad
}

// writer refuses the writes of an API key: audit rows name a user (Appendix C §C.4.13).
func writer(p *authz.Principal) error {
	if p.IsMachine() {
		return authz.ErrPermissionDenied("carrier profiles are changed by people, not API keys").
			WithDetails(map[string]any{"reason": ReasonMachine})
	}
	return nil
}

// CreateSubcontractor is POST /v1/subcontractors (fleet:manage_subcontractors in the own fleet, or
// platform:manage_tenants): a carrier working for the own fleet (contractor reach, R60), its billing party, first
// status entry, documents, tenant_created and outbox tenant.created in one transaction.
func (a *Admin) CreateSubcontractor(c call, in CreateSubcontractorInput) (*Subcontractor, error) {
	p := c.p
	if err := writer(p); err != nil {
		return nil, err
	}
	steward := p.Can(authz.FleetManageSubcontractors) && p.TenantKind == authz.TenantKindOwnFleet && p.EffectiveTenant() != nil
	if !steward && !p.Can(authz.PlatformManageTenants) {
		return nil, authz.ErrPermissionDenied("carriers are onboarded by the own fleet or a platform admin").
			WithDetails(map[string]any{"reason": ReasonOwnFleetOnly})
	}
	nc, bad := validateCarrier(CreateTenantInput{Kind: authz.TenantKindCarrier, Code: in.Code, NameTh: in.NameTh,
		NameEn: in.NameEn, LegalType: in.LegalType, IDCardNumber: in.IDCardNumber, TaxID: in.TaxID,
		ContactPerson: in.ContactPerson, Phone: in.Phone, Email: in.Email, Website: in.Website, Address: in.Address,
		Designation: in.Designation, FleetSize: in.FleetSize, DispatchCenter: in.DispatchCenter,
		ServiceRegions: in.ServiceRegions, VehicleTypes: in.VehicleTypes, LineGroupID: in.LineGroupID, Status: in.Status}, true)
	nc.basis = in.BillingDateBasis
	if nc.basis == "" {
		nc.basis = billingBasisDelivered
	}
	if !slices.Contains(billingBases, nc.basis) {
		bad = append(bad, violation("billingDateBasis", "invalid"))
	}
	bad = append(bad, checkDocumentKeys(in.DocumentKeys)...)
	if len(bad) > 0 {
		return nil, errInvalid(bad...)
	}
	nc.documents = in.DocumentKeys
	var id uuid.UUID
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, _ func(*auth.PostCommit)) error {
		var contractor uuid.UUID
		if steward {
			contractor = *p.EffectiveTenant()
		} else {
			own, err := q.OwnFleetTenantID(c.ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				return errFailedPrecondition("no_own_fleet", "there is no own-fleet tenant for the carrier to work for")
			}
			if err != nil {
				return err
			}
			contractor = own
		}
		if err := checkContractor(c, q, uuid.Nil, contractor); err != nil {
			return err
		}
		nc.params.ContractorTenantID = &contractor
		t, err := a.insertCarrier(c, tx, q, nc, "subcontractors")
		if err != nil {
			return err
		}
		id = t.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a.GetSubcontractor(c, id)
}

// PatchSubcontractorInput is the body of PATCH /v1/subcontractors/{id}: absent fields stay; null clears an optional
// field. The structural fields are refused (422 not_editable): they change through PATCH /v1/tenants/{id}.
type PatchSubcontractorInput struct {
	NameTh           Field[string]   `json:"nameTh"`
	NameEn           Field[string]   `json:"nameEn"`
	LegalType        Field[string]   `json:"legalType"`
	IDCardNumber     Field[string]   `json:"idCardNumber"`
	TaxID            Field[string]   `json:"taxId"`
	ContactPerson    Field[string]   `json:"contactPerson"`
	Phone            Field[string]   `json:"phone"`
	Email            Field[string]   `json:"email"`
	Website          Field[string]   `json:"website"`
	Address          Field[string]   `json:"address"`
	Designation      Field[string]   `json:"designation"`
	FleetSize        Field[int32]    `json:"fleetSize"`
	DispatchCenter   Field[string]   `json:"dispatchCenter"`
	ServiceRegions   Field[[]string] `json:"serviceRegions"`
	VehicleTypes     Field[[]string] `json:"vehicleTypes"`
	LineGroupID      Field[string]   `json:"lineGroupId"`
	BillingDateBasis Field[string]   `json:"billingDateBasis"`
	DocumentKeys     Field[[]string] `json:"documentKeys"`

	Code               Field[json.RawMessage] `json:"code"`
	Kind               Field[json.RawMessage] `json:"kind"`
	Status             Field[json.RawMessage] `json:"status"`
	ContractorTenantID Field[json.RawMessage] `json:"contractorTenantId"`
}

// optField is the value an optional text field takes: the trimmed text, nil for null or blank.
func optField(f Field[string]) *string {
	if f.Null {
		return nil
	}
	return optText(f.V)
}

// PatchSubcontractor is PATCH /v1/subcontractors/{id}. tenant_updated and outbox tenant.updated in the same
// transaction; a request that changes nothing writes nothing.
func (a *Admin) PatchSubcontractor(c call, id uuid.UUID, in PatchSubcontractorInput) (*Subcontractor, error) {
	p := c.p
	if err := writer(p); err != nil {
		return nil, err
	}
	steward := p.Can(authz.FleetManageSubcontractors)
	if !steward && !isOwnTenantAdmin(p, id) {
		return nil, authz.ErrPermissionDenied("missing capability", authz.FleetManageSubcontractors)
	}
	var bad []httpx.FieldViolation
	for _, f := range []struct {
		name string
		set  bool
	}{{"code", in.Code.Set}, {"kind", in.Kind.Set}, {"status", in.Status.Set}, {"contractorTenantId", in.ContractorTenantID.Set}} {
		if f.set {
			bad = append(bad, violation(f.name, "not_editable"))
		}
	}
	if in.NameTh.Set {
		if in.NameTh.Null {
			bad = append(bad, violation("nameTh", "required"))
		} else {
			checkName("nameTh", in.NameTh.V, true, &bad)
		}
	}
	if in.NameEn.Set && !in.NameEn.Null {
		checkName("nameEn", in.NameEn.V, false, &bad)
	}
	if in.LegalType.Set && (in.LegalType.Null || (in.LegalType.V != "individual" && in.LegalType.V != "company")) {
		bad = append(bad, violation("legalType", "invalid"))
	}
	for _, f := range []struct {
		name string
		v    Field[string]
	}{{"idCardNumber", in.IDCardNumber}, {"taxId", in.TaxID}} {
		if s := strings.TrimSpace(f.v.V); f.v.Set && !f.v.Null && s != "" && !ValidThaiID(s) {
			bad = append(bad, violation(f.name, "invalid"))
		}
	}
	var email *string
	if in.Email.Set && !in.Email.Null && strings.TrimSpace(in.Email.V) != "" {
		e, ok := normalizeEmail(in.Email.V)
		if !ok {
			bad = append(bad, violation("email", "invalid"))
		}
		email = &e
	}
	for _, f := range []struct {
		name string
		v    Field[string]
	}{{"contactPerson", in.ContactPerson}, {"phone", in.Phone}, {"website", in.Website}, {"designation", in.Designation},
		{"dispatchCenter", in.DispatchCenter}} {
		if f.v.Set && !f.v.Null {
			checkName(f.name, f.v.V, false, &bad)
		}
	}
	if in.Address.Set && utf8.RuneCountInString(in.Address.V) > 1000 {
		bad = append(bad, violation("address", "too_long"))
	}
	if in.FleetSize.Set && (in.FleetSize.Null || in.FleetSize.V < 0) {
		bad = append(bad, violation("fleetSize", "invalid"))
	}
	if s := strings.TrimSpace(in.LineGroupID.V); in.LineGroupID.Set && !in.LineGroupID.Null && s != "" && !lineGroupRx.MatchString(s) {
		bad = append(bad, violation("lineGroupId", "invalid"))
	}
	if in.BillingDateBasis.Set && (in.BillingDateBasis.Null || !slices.Contains(billingBases, in.BillingDateBasis.V)) {
		bad = append(bad, violation("billingDateBasis", "invalid"))
	}
	if in.DocumentKeys.Set {
		bad = append(bad, checkDocumentKeys(in.DocumentKeys.V)...)
	}
	if len(bad) > 0 {
		return nil, errInvalid(bad...)
	}
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, _ func(*auth.PostCommit)) error {
		cur, err := q.LockCarrierProfile(c.ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && cur.Kind != authz.TenantKindCarrier) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		// billingDateBasis is a setting of the carrier's billing party, global master data (R60): stewards only.
		if in.BillingDateBasis.Set && (cur.BillingDateBasis == nil || *cur.BillingDateBasis != in.BillingDateBasis.V) && !steward {
			return authz.ErrPermissionDenied("the billing date basis is maintained by the own fleet").
				WithDetails(map[string]any{"reason": ReasonStewardOnly, "field": "billingDateBasis"})
		}
		m := iamdb.UpdateCarrierProfileParams{ID: id, NameTh: cur.NameTh, NameEn: cur.NameEn, LegalType: cur.LegalType,
			IDCardNumber: cur.IDCardNumber, TaxID: cur.TaxID, ContactPerson: cur.ContactPerson, Phone: cur.Phone,
			Email: cur.Email, Website: cur.Website, Address: cur.Address, Designation: cur.Designation,
			FleetSize: cur.FleetSize, DispatchCenter: cur.DispatchCenter, ServiceRegions: cur.ServiceRegions,
			VehicleTypes: cur.VehicleTypes, LineGroupID: cur.LineGroupID}
		var fields []string
		text := func(name string, f Field[string], dst **string) {
			if !f.Set {
				return
			}
			if v := optField(f); !equalPtr(*dst, v) {
				*dst = v
				fields = append(fields, name)
			}
		}
		if in.NameTh.Set && strings.TrimSpace(in.NameTh.V) != cur.NameTh {
			m.NameTh = strings.TrimSpace(in.NameTh.V)
			fields = append(fields, "nameTh")
		}
		text("nameEn", in.NameEn, &m.NameEn)
		if in.LegalType.Set && !equalPtr(cur.LegalType, &in.LegalType.V) {
			m.LegalType = &in.LegalType.V
			fields = append(fields, "legalType")
		}
		text("idCardNumber", in.IDCardNumber, &m.IDCardNumber)
		text("taxId", in.TaxID, &m.TaxID)
		text("contactPerson", in.ContactPerson, &m.ContactPerson)
		text("phone", in.Phone, &m.Phone)
		if in.Email.Set && !equalPtr(cur.Email, email) {
			m.Email = email
			fields = append(fields, "email")
		}
		text("website", in.Website, &m.Website)
		text("address", in.Address, &m.Address)
		text("designation", in.Designation, &m.Designation)
		if in.FleetSize.Set && in.FleetSize.V != cur.FleetSize {
			m.FleetSize = in.FleetSize.V
			fields = append(fields, "fleetSize")
		}
		text("dispatchCenter", in.DispatchCenter, &m.DispatchCenter)
		if in.ServiceRegions.Set && !slices.Equal(cleanList(in.ServiceRegions.V), cur.ServiceRegions) {
			m.ServiceRegions = cleanList(in.ServiceRegions.V)
			fields = append(fields, "serviceRegions")
		}
		if in.VehicleTypes.Set && !slices.Equal(cleanList(in.VehicleTypes.V), cur.VehicleTypes) {
			m.VehicleTypes = cleanList(in.VehicleTypes.V)
			fields = append(fields, "vehicleTypes")
		}
		text("lineGroupId", in.LineGroupID, &m.LineGroupID)
		// The identity number its legal type needs (legacy subcontractorSchema), checked on the merged profile when
		// the request touches it; legacy profiles that lack it stay editable otherwise.
		if in.LegalType.Set || in.IDCardNumber.Set || in.TaxID.Set {
			legal, idCard, tax := deref(m.LegalType), deref(m.IDCardNumber), deref(m.TaxID)
			var req []httpx.FieldViolation
			for _, f := range []struct{ name, v, needs string }{{"idCardNumber", idCard, "individual"}, {"taxId", tax, "company"}} {
				if f.v == "" && legal == f.needs {
					req = append(req, violation(f.name, "required"))
				}
			}
			if len(req) > 0 {
				return errInvalid(req...)
			}
		}
		profileChanged := len(fields) > 0
		if in.BillingDateBasis.Set && (cur.BillingDateBasis == nil || *cur.BillingDateBasis != in.BillingDateBasis.V) {
			if _, err := q.SetTenantPartyBasis(c.ctx, iamdb.SetTenantPartyBasisParams{TenantID: &id,
				BillingDateBasis: in.BillingDateBasis.V}); err != nil {
				return err
			}
			fields = append(fields, "billingDateBasis")
		}
		if in.DocumentKeys.Set {
			changed, err := a.setTenantDocuments(c, tx, q, id, in.DocumentKeys.V)
			if err != nil {
				return err
			}
			if changed {
				fields = append(fields, "documents")
			}
		}
		if len(fields) == 0 {
			return nil
		}
		if profileChanged {
			if err := q.UpdateCarrierProfile(c.ctx, m); err != nil {
				return err
			}
		}
		if err := a.audit(c, tx, EventTenantUpdated, "carrier profile updated", nil, &id,
			map[string]any{"fields": fields, "source": "subcontractors"}); err != nil {
			return err
		}
		return emit(c, tx, RouteTenantUpdated, "tenant", id.String(), &id, tenantEvent{TenantID: id,
			ContractorTenantID: cur.ContractorTenantID, Fields: fields})
	})
	if err != nil {
		return nil, err
	}
	return a.GetSubcontractor(c, id)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// documentKind is the tenant_files.kind of a tenant_document key: the folder its presign variant chose
// (subcontractors/{id}/{id_cards|company_docs|other}/..., main spec §9.2), so an ID card always lands under the
// restricted folder whatever a client claims.
func documentKind(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) >= 4 && parts[0] == "subcontractors" {
		switch parts[2] {
		case "id_cards":
			return "id_card"
		case "company_docs":
			return "company_doc"
		}
	}
	return "other"
}

// setTenantDocuments makes keys the documents of the tenant, in that order: each key is committed in tx (purpose
// tenant_document, owner the tenant: an upload of the caller or of its tenant, Stat-checked; a key already
// committed to this tenant is kept), files no longer listed lose their link (their objects stay, main spec §9.3).
// changed reports a different list.
func (a *Admin) setTenantDocuments(c call, tx pgx.Tx, q *iamdb.Queries, tenantID uuid.UUID, keys []string) (bool, error) {
	before, err := q.TenantDocuments(c.ctx, tenantID)
	if err != nil {
		return false, err
	}
	linked := make(map[string]iamdb.TenantDocumentsRow, len(before))
	for _, d := range before {
		linked[d.ObjectKey] = d
	}
	ids := make([]uuid.UUID, 0, len(keys))
	changed := len(before) != len(keys)
	for i, k := range keys {
		// A document already linked stays as it is (whoever uploaded it); a new key is committed to the tenant.
		var fileID uuid.UUID
		var kind string
		if d, ok := linked[k]; ok {
			fileID, kind = d.FileID, d.Kind
		} else {
			field := fmt.Sprintf("documentKeys[%d]", i)
			if a.files == nil {
				return false, errInvalid(httpx.FieldViolation{Field: field, Reason: "not_found", Params: map[string]any{"key": k}})
			}
			f, err := a.files.Commit(c.ctx, tx, storage.CommitInput{Key: k, Field: field, Purposes: []string{"tenant_document"},
				OwnerID: tenantID, UserID: c.p.UserID, TenantID: c.p.EffectiveTenant()})
			if err != nil {
				return false, err
			}
			fileID, kind = f.ID, documentKind(f.Key)
		}
		if !changed && (before[i].FileID != fileID || before[i].Kind != kind) {
			changed = true
		}
		if err := q.UpsertTenantDocument(c.ctx, iamdb.UpsertTenantDocumentParams{TenantID: tenantID, FileID: fileID,
			Kind: kind, Position: int32(i)}); err != nil {
			return false, err
		}
		ids = append(ids, fileID)
	}
	if _, err := q.DeleteTenantDocumentsExcept(c.ctx, iamdb.DeleteTenantDocumentsExceptParams{TenantID: tenantID, Keep: ids}); err != nil {
		return false, err
	}
	return changed, nil
}
