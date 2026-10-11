package parties

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties/partiesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Company is an invoice issuer of a tenant (GET /v1/companies, GET /v1/companies/owner, POST, PATCH). The logo, the
// stamp and the signature are private objects: the keys go back in a PATCH, the URLs are short-lived presigned GETs
// (and GET /v1/files needs company:view, internal/storage). withholdingTaxRate is a percent (3 = 3 %), snapshotted
// as a fraction onto every billing statement (R18).
type Company struct {
	ID                 uuid.UUID `json:"id"`
	TenantID           uuid.UUID `json:"tenantId"`
	NameTh             string    `json:"nameTh"`
	NameEn             *string   `json:"nameEn"`
	ShortName          *string   `json:"shortName"`
	TaxID              string    `json:"taxId"`
	BranchType         string    `json:"branchType"` // headquarters | branch
	BranchNumber       *string   `json:"branchNumber"`
	Address            string    `json:"address"`
	Phone              *string   `json:"phone"`
	Email              *string   `json:"email"`
	LogoKey            *string   `json:"logoKey"`
	LogoURL            *string   `json:"logoUrl"`
	StampKey           *string   `json:"stampKey"`
	StampURL           *string   `json:"stampUrl"`
	SignatureKey       *string   `json:"signatureKey"`
	SignatureURL       *string   `json:"signatureUrl"`
	SignatoryName      *string   `json:"signatoryName"`
	BankName           *string   `json:"bankName"`
	AccountNumber      *string   `json:"accountNumber"`
	AccountName        *string   `json:"accountName"`
	WithholdingTaxRate float64   `json:"withholdingTaxRate"`
	CompanyType        string    `json:"companyType"` // owner | subcontractor
	IsActive           bool      `json:"isActive"`
	MaxTrucks          *int32    `json:"maxTrucks"`
	MaxDrivers         *int32    `json:"maxDrivers"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// Company types (companies.company_type) and branch types (companies.branch_type).
const (
	CompanyOwner         = "owner"
	CompanySubcontractor = "subcontractor"
)

var (
	companyTypes       = []string{CompanyOwner, CompanySubcontractor}
	companyBranchTypes = []string{"headquarters", "branch"}
)

// companyAssets are the three files of a company: body field, purpose (internal/storage) and column.
var companyAssets = []struct{ field, purpose string }{
	{"logoKey", "company_logo"}, {"stampKey", "company_stamp"}, {"signatureKey", "company_signature"},
}

// companyOf builds the payload; it signs the asset URLs (Files.SignedFile), so it runs after s.tx returns.
func (s *Service) companyOf(ctx context.Context, r partiesdb.GetCompanyRow) Company {
	c := Company{ID: r.ID, TenantID: r.TenantID, NameTh: r.NameTh, NameEn: r.NameEn, ShortName: r.ShortName,
		TaxID: r.TaxID, BranchType: r.BranchType, BranchNumber: r.BranchNumber, Address: r.Address, Phone: r.Phone,
		Email: r.Email, SignatoryName: r.SignatoryName, BankName: r.BankName, AccountNumber: r.AccountNumber,
		AccountName: r.AccountName, WithholdingTaxRate: r.WithholdingTaxRate, CompanyType: r.CompanyType,
		IsActive: r.IsActive, MaxTrucks: r.MaxTrucks, MaxDrivers: r.MaxDrivers, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	c.LogoKey, c.LogoURL = s.signed(ctx, r.LogoFileID)
	c.StampKey, c.StampURL = s.signed(ctx, r.StampFileID)
	c.SignatureKey, c.SignatureURL = s.signed(ctx, r.SignatureFileID)
	return c
}

// ListCompaniesInput are the query parameters of GET /v1/companies.
type ListCompaniesInput struct {
	Type, Q string
	Limit   int
	Cursor  string
}

// ListCompanies is GET /v1/companies (company:view): the companies of the caller's tenant reach, newest first; the
// query names the reach (authz.Principal.TenantReach) and RLS re-checks every row.
func (s *Service) ListCompanies(ctx context.Context, p *authz.Principal, in ListCompaniesInput) ([]Company, string, error) {
	var ck checker
	var typ, q *string
	if in.Type != "" {
		if !slices.Contains(companyTypes, in.Type) {
			ck.add("type", "invalid")
		}
		typ = &in.Type
	}
	if v := strings.TrimSpace(in.Q); v != "" {
		if utf8.RuneCountInString(v) > 100 {
			ck.add("q", "too_long")
		}
		q = &v
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		ck.add("limit", "out_of_range")
	}
	var after cursor
	if in.Cursor != "" {
		var ok bool
		if after, ok = decodeCursor(in.Cursor); !ok || after.T == nil {
			ck.add("cursor", "invalid")
		}
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	all, tenants := p.TenantReach()
	var rows []partiesdb.ListCompaniesRow
	more := false
	err := s.tx(ctx, p, func(_ pgx.Tx, qq *partiesdb.Queries) error {
		var err error
		rows, err = qq.ListCompanies(ctx, partiesdb.ListCompaniesParams{AllTenants: all, TenantIds: tenants, CompanyType: typ,
			Q: q, AfterCreated: after.T, AfterID: after.ID, RowLimit: int32(limit) + 1})
		if len(rows) > limit {
			rows, more = rows[:limit], true
		}
		return err
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]Company, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.companyOf(ctx, partiesdb.GetCompanyRow(r)))
	}
	next := ""
	if more {
		last := rows[len(rows)-1]
		t := last.CreatedAt
		next = cursor{T: &t, ID: last.ID}.encode()
	}
	return out, next, nil
}

// OwnerCompany is GET /v1/companies/owner (company:view): the owner company (the header of invoices and receipts)
// when the caller's reach holds it; 404 otherwise.
func (s *Service) OwnerCompany(ctx context.Context, p *authz.Principal) (*Company, error) {
	all, tenants := p.TenantReach()
	var row partiesdb.GetOwnerCompanyRow
	err := s.tx(ctx, p, func(_ pgx.Tx, q *partiesdb.Queries) error {
		var err error
		row, err = q.GetOwnerCompany(ctx, partiesdb.GetOwnerCompanyParams{AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	c := s.companyOf(ctx, partiesdb.GetCompanyRow(row))
	return &c, nil
}

func (s *Service) getCompany(ctx context.Context, p *authz.Principal, id uuid.UUID) (*Company, error) {
	var row partiesdb.GetCompanyRow
	err := s.tx(ctx, p, func(_ pgx.Tx, q *partiesdb.Queries) error {
		var err error
		row, err = q.GetCompany(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	c := s.companyOf(ctx, row)
	return &c, nil
}

// CompanyInput is the body of POST /v1/companies and PATCH /v1/companies/{id} (companySchema of the web): on PATCH
// absent fields stay and null clears an optional one (a null key unlinks its file; the object stays, main spec §9.3).
type CompanyInput struct {
	NameTh             Field[string]  `json:"nameTh"`
	NameEn             Field[string]  `json:"nameEn"`
	ShortName          Field[string]  `json:"shortName"`
	TaxID              Field[string]  `json:"taxId"`
	BranchType         Field[string]  `json:"branchType"`
	BranchNumber       Field[string]  `json:"branchNumber"`
	Address            Field[string]  `json:"address"`
	Phone              Field[string]  `json:"phone"`
	Email              Field[string]  `json:"email"`
	LogoKey            Field[string]  `json:"logoKey"`
	StampKey           Field[string]  `json:"stampKey"`
	SignatureKey       Field[string]  `json:"signatureKey"`
	SignatoryName      Field[string]  `json:"signatoryName"`
	BankName           Field[string]  `json:"bankName"`
	AccountNumber      Field[string]  `json:"accountNumber"`
	AccountName        Field[string]  `json:"accountName"`
	WithholdingTaxRate Field[float64] `json:"withholdingTaxRate"`
	CompanyType        Field[string]  `json:"companyType"`
	IsActive           Field[bool]    `json:"isActive"`
	MaxTrucks          Field[int32]   `json:"maxTrucks"`
	MaxDrivers         Field[int32]   `json:"maxDrivers"`
}

// keys returns the three asset keys of the body, in companyAssets order.
func (in *CompanyInput) keys() []Field[string] {
	return []Field[string]{in.LogoKey, in.StampKey, in.SignatureKey}
}

// validRate: a percent between 0 and 100 with at most two decimals (NUMERIC(5,2)).
func validRate(r float64) bool {
	if math.IsNaN(r) || r < 0 || r > 100 {
		return false
	}
	c := r * 100
	return math.Abs(c-math.Round(c)) < 1e-6
}

// validate checks every field that is present; create also requires nameTh, taxId, branchType, address,
// withholdingTaxRate (no default: main spec §19.2 question 6) and companyType.
func (in *CompanyInput) validate(create bool) error {
	var ck checker
	required := func(field string, f Field[string], max int) {
		switch {
		case create && !f.Set, f.Set && f.Null:
			ck.add(field, "required")
		case f.Set:
			ck.text(field, f.V, max, true)
		}
	}
	required("nameTh", in.NameTh, 200)
	ck.optText("nameEn", in.NameEn, 200)
	ck.optText("shortName", in.ShortName, 20)
	switch {
	case create && !in.TaxID.Set, in.TaxID.Set && in.TaxID.Null:
		ck.add("taxId", "required")
	case in.TaxID.Set:
		ck.thaiID("taxId", in.TaxID.V, true)
	}
	switch {
	case create && !in.BranchType.Set, in.BranchType.Set && in.BranchType.Null:
		ck.add("branchType", "required")
	case in.BranchType.Set && !slices.Contains(companyBranchTypes, in.BranchType.V):
		ck.add("branchType", "invalid")
	}
	if v := strings.TrimSpace(in.BranchNumber.V); in.BranchNumber.Set && !in.BranchNumber.Null && v != "" && !branchNumberRx.MatchString(v) {
		ck.add("branchNumber", "invalid")
	}
	required("address", in.Address, 1000)
	ck.optText("phone", in.Phone, 50)
	if in.Email.Set && !in.Email.Null {
		ck.email("email", in.Email.V)
	}
	for i, k := range in.keys() {
		ck.key(companyAssets[i].field, k)
	}
	ck.optText("signatoryName", in.SignatoryName, 200)
	ck.optText("bankName", in.BankName, 200)
	ck.optText("accountNumber", in.AccountNumber, 50)
	ck.optText("accountName", in.AccountName, 200)
	switch {
	case create && !in.WithholdingTaxRate.Set, in.WithholdingTaxRate.Set && in.WithholdingTaxRate.Null:
		ck.add("withholdingTaxRate", "required")
	case in.WithholdingTaxRate.Set && !validRate(in.WithholdingTaxRate.V):
		ck.add("withholdingTaxRate", "invalid")
	}
	switch {
	case create && !in.CompanyType.Set, in.CompanyType.Set && in.CompanyType.Null:
		ck.add("companyType", "required")
	case in.CompanyType.Set && !slices.Contains(companyTypes, in.CompanyType.V):
		ck.add("companyType", "invalid")
	}
	if in.IsActive.Set && in.IsActive.Null {
		ck.add("isActive", "invalid")
	}
	for _, f := range []struct {
		name string
		v    Field[int32]
	}{{"maxTrucks", in.MaxTrucks}, {"maxDrivers", in.MaxDrivers}} {
		if f.v.Set && !f.v.Null && f.v.V < 1 {
			ck.add(f.name, "out_of_range")
		}
	}
	return ck.err()
}

// ownerOnlyInOwnFleet: the owner company is the own fleet's (invoice header; ETL maps companyType 'owner' to the own
// fleet, Appendix A §A.3.0), so no other tenant may hold one.
func ownerOnlyInOwnFleet(companyType, tenantKind string) error {
	if companyType == CompanyOwner && tenantKind != authz.TenantKindOwnFleet {
		return httpx.ErrInvalidArgument(violation("companyType", "owner_own_fleet_only"))
	}
	return nil
}

func ptr32(f Field[int32]) *int32 {
	if !f.Set || f.Null {
		return nil
	}
	v := f.V
	return &v
}

// CreateCompany is POST /v1/companies (company:manage, an active tenant): a company of the effective tenant
// (tenant_source self), its logo, stamp and signature, and outbox companies.changed (SSE global) in one transaction.
func (s *Service) CreateCompany(ctx context.Context, p *authz.Principal, rid string, in CompanyInput) (*Company, error) {
	tenant := p.EffectiveTenant()
	if tenant == nil {
		return nil, authz.ErrTenantRequired()
	}
	if err := in.validate(true); err != nil {
		return nil, err
	}
	if err := ownerOnlyInOwnFleet(in.CompanyType.V, p.TenantKind); err != nil {
		return nil, err
	}
	branch := in.BranchType.V
	number := textOf(in.BranchNumber)
	if branch == "branch" && number == nil {
		return nil, httpx.ErrInvalidArgument(violation("branchNumber", "required"))
	}
	active := true
	if in.IsActive.Set {
		active = in.IsActive.V
	}
	var email *string
	if v := textOf(in.Email); v != nil {
		e := strings.ToLower(*v)
		email = &e
	}
	var id uuid.UUID
	err := s.tx(ctx, p, func(tx pgx.Tx, q *partiesdb.Queries) error {
		var err error
		id, err = q.InsertCompany(ctx, partiesdb.InsertCompanyParams{TenantID: *tenant, NameTh: strings.TrimSpace(in.NameTh.V),
			NameEn: textOf(in.NameEn), ShortName: textOf(in.ShortName), TaxID: strings.TrimSpace(in.TaxID.V), BranchType: branch,
			BranchNumber: number, Address: strings.TrimSpace(in.Address.V), Phone: textOf(in.Phone), Email: email,
			SignatoryName: textOf(in.SignatoryName), BankName: textOf(in.BankName), AccountNumber: textOf(in.AccountNumber),
			AccountName: textOf(in.AccountName), WithholdingTaxRate: in.WithholdingTaxRate.V, CompanyType: in.CompanyType.V,
			IsActive: active, MaxTrucks: ptr32(in.MaxTrucks), MaxDrivers: ptr32(in.MaxDrivers)})
		if err != nil {
			return err
		}
		files := make([]*uuid.UUID, len(companyAssets))
		linked := false
		for i, k := range in.keys() {
			if v := textOf(k); v != nil {
				fid, err := s.commit(ctx, tx, p, *v, companyAssets[i].field, companyAssets[i].purpose, id)
				if err != nil {
					return err
				}
				files[i], linked = &fid, true
			}
		}
		if linked {
			if err := q.SetCompanyFiles(ctx, partiesdb.SetCompanyFilesParams{ID: id, LogoFileID: files[0],
				StampFileID: files[1], SignatureFileID: files[2]}); err != nil {
				return err
			}
		}
		return emit(ctx, tx, RouteCompaniesChanged, "company", id, tenant, rid)
	})
	if err != nil {
		return nil, err
	}
	return s.getCompany(ctx, p, id)
}

// PatchCompany is PATCH /v1/companies/{id} (company:manage): partial, on a company of the caller's reach (RLS); outbox
// companies.changed. A request that changes nothing writes nothing; an asset key the company already links in that
// slot (the payload sent back) stays linked as it is, whoever uploaded it.
func (s *Service) PatchCompany(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in CompanyInput) (*Company, error) {
	if err := in.validate(false); err != nil {
		return nil, err
	}
	held, err := s.heldKeys(ctx, in.keys()...)
	if err != nil {
		return nil, err
	}
	err = s.tx(ctx, p, func(tx pgx.Tx, q *partiesdb.Queries) error {
		cur, err := q.LockCompany(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		m := partiesdb.UpdateCompanyParams{ID: id, NameTh: cur.NameTh, NameEn: cur.NameEn, ShortName: cur.ShortName,
			TaxID: cur.TaxID, BranchType: cur.BranchType, BranchNumber: cur.BranchNumber, Address: cur.Address,
			Phone: cur.Phone, Email: cur.Email, LogoFileID: cur.LogoFileID, StampFileID: cur.StampFileID,
			SignatureFileID: cur.SignatureFileID, SignatoryName: cur.SignatoryName, BankName: cur.BankName,
			AccountNumber: cur.AccountNumber, AccountName: cur.AccountName, WithholdingTaxRate: cur.WithholdingTaxRate,
			CompanyType: cur.CompanyType, IsActive: cur.IsActive, MaxTrucks: cur.MaxTrucks, MaxDrivers: cur.MaxDrivers}
		changed := false
		text := func(f Field[string], dst **string) {
			if v := textOf(f); f.Set && !eq(*dst, v) {
				*dst, changed = v, true
			}
		}
		set := func(f Field[string], dst *string) {
			if v := strings.TrimSpace(f.V); f.Set && v != *dst {
				*dst, changed = v, true
			}
		}
		set(in.NameTh, &m.NameTh)
		text(in.NameEn, &m.NameEn)
		text(in.ShortName, &m.ShortName)
		set(in.TaxID, &m.TaxID)
		set(in.BranchType, &m.BranchType)
		text(in.BranchNumber, &m.BranchNumber)
		set(in.Address, &m.Address)
		text(in.Phone, &m.Phone)
		if in.Email.Set {
			var e *string
			if v := textOf(in.Email); v != nil {
				l := strings.ToLower(*v)
				e = &l
			}
			if !eq(m.Email, e) {
				m.Email, changed = e, true
			}
		}
		text(in.SignatoryName, &m.SignatoryName)
		text(in.BankName, &m.BankName)
		text(in.AccountNumber, &m.AccountNumber)
		text(in.AccountName, &m.AccountName)
		if in.WithholdingTaxRate.Set && in.WithholdingTaxRate.V != m.WithholdingTaxRate {
			m.WithholdingTaxRate, changed = in.WithholdingTaxRate.V, true
		}
		if in.CompanyType.Set && in.CompanyType.V != m.CompanyType {
			if in.CompanyType.V == CompanyOwner {
				kind, err := q.TenantKind(ctx, cur.TenantID)
				if err != nil {
					return err
				}
				if err := ownerOnlyInOwnFleet(CompanyOwner, kind); err != nil {
					return err
				}
			}
			m.CompanyType, changed = in.CompanyType.V, true
		}
		if in.IsActive.Set && in.IsActive.V != m.IsActive {
			m.IsActive, changed = in.IsActive.V, true
		}
		for _, f := range []struct {
			in  Field[int32]
			dst **int32
		}{{in.MaxTrucks, &m.MaxTrucks}, {in.MaxDrivers, &m.MaxDrivers}} {
			if v := ptr32(f.in); f.in.Set && !eq(*f.dst, v) {
				*f.dst, changed = v, true
			}
		}
		if (in.BranchType.Set || in.BranchNumber.Set) && m.BranchType == "branch" && m.BranchNumber == nil {
			return httpx.ErrInvalidArgument(violation("branchNumber", "required"))
		}
		dsts := []**uuid.UUID{&m.LogoFileID, &m.StampFileID, &m.SignatureFileID}
		for i, k := range in.keys() {
			if !k.Set {
				continue
			}
			fid, err := s.assetFile(ctx, tx, p, k, *dsts[i], held, companyAssets[i].field, companyAssets[i].purpose, id)
			if err != nil {
				return err
			}
			if !eq(*dsts[i], fid) {
				*dsts[i], changed = fid, true
			}
		}
		if !changed {
			return nil
		}
		if err := q.UpdateCompany(ctx, m); err != nil {
			return err
		}
		tenant := cur.TenantID
		return emit(ctx, tx, RouteCompaniesChanged, "company", id, &tenant, rid)
	})
	if err != nil {
		return nil, err
	}
	return s.getCompany(ctx, p, id)
}
