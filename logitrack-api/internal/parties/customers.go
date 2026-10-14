package parties

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties/partiesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/searchtext"
)

// CustomerMinimal is one item of GET /v1/customers?fields=minimal (pickers, customer scopes): Appendix B §B.2.10.
type CustomerMinimal struct {
	ID             uuid.UUID  `json:"id"`
	BillingPartyID *uuid.UUID `json:"billingPartyId"`
	Code           string     `json:"code"`
	Name           string     `json:"name"`
}

// DriverIDType is one entry of customers.driverIdTypes[] (customer_driver_id_types): the keys of
// drivers.customerDriverIds[customerCode].
type DriverIDType struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// Customer is the full record (GET /v1/customers without fields=minimal, GET /v1/customers/{id}, POST, PATCH). The
// logo is a private object: logoKey goes back in a PATCH, logoUrl is a short-lived presigned GET.
type Customer struct {
	ID               uuid.UUID      `json:"id"`
	BillingPartyID   *uuid.UUID     `json:"billingPartyId"`
	Code             string         `json:"code"`
	Name             string         `json:"name"`
	Description      *string        `json:"description"`
	LogoKey          *string        `json:"logoKey"`
	LogoURL          *string        `json:"logoUrl"`
	Address          *string        `json:"address"`
	TaxID            *string        `json:"taxId"`
	BranchType       *string        `json:"branchType"` // hq | branch
	BranchNumber     *string        `json:"branchNumber"`
	ContactName      *string        `json:"contactName"`
	ContactPhone     *string        `json:"contactPhone"`
	BillingEmail     *string        `json:"billingEmail"`
	PaymentTermsDays *int32         `json:"paymentTermsDays"`
	InvoiceNote      *string        `json:"invoiceNote"`
	LineGroupID      *string        `json:"lineGroupId"`
	BillingDateBasis string         `json:"billingDateBasis"` // delivered | plan (ADR 0027), on the billing party
	DriverIDTypes    []DriverIDType `json:"driverIdTypes"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

// ListCustomersInput are the query parameters of GET /v1/customers.
type ListCustomersInput struct {
	Fields, Q string
	Limit     int
	Cursor    string
}

var (
	// customerCodeRx: the key of invoice numbers and driver customer codes ('SPX', 'TTP', 'CJSF'); stored upper case
	// (the ETL upper-cases legacy codes, Appendix A §A.3.2).
	customerCodeRx = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,31}$`)
	idTypeKeyRx    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)
	branchTypes    = []string{"hq", "branch"}
	billingBases   = []string{"delivered", "plan"}
)

// MaxDriverIDTypes bounds driverIdTypes[].
const MaxDriverIDTypes = 20

const billingBasisDelivered = "delivered"

// ListCustomers is GET /v1/customers, ordered by code: []CustomerMinimal with fields=minimal (any authenticated
// principal), else []Customer (fleet:manage_customers). A customer-scope principal sees only its own billing
// parties (T20 acceptance), here and through RLS (0012).
func (s *Service) ListCustomers(ctx context.Context, p *authz.Principal, in ListCustomersInput) (any, string, error) {
	var ck checker
	minimal := in.Fields == "minimal"
	if in.Fields != "" && !minimal {
		ck.add("fields", "invalid")
	}
	// q: its folded words, each in the code or the name (searchtext, the web's lib/searchText.ts); nil is no search.
	var q []string
	if v := strings.TrimSpace(in.Q); v != "" {
		if utf8.RuneCountInString(v) > searchtext.MaxQueryLen {
			ck.add("q", "too_long")
		}
		q = searchtext.LikePatterns(v)
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
		if after, ok = decodeCursor(in.Cursor); !ok || after.S == nil {
			ck.add("cursor", "invalid")
		}
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	if !minimal && !p.Can(authz.FleetManageCustomers) {
		return nil, "", authz.ErrPermissionDenied("missing capability", authz.FleetManageCustomers)
	}
	scoped := p.IsCustomerScope()
	parties := []uuid.UUID{}
	if scoped {
		parties = p.PartyIDs
	}
	var rows []partiesdb.ListCustomersRow
	var types map[uuid.UUID][]DriverIDType
	more := false
	err := s.tx(ctx, p, func(_ pgx.Tx, qq *partiesdb.Queries) error {
		var err error
		rows, err = qq.ListCustomers(ctx, partiesdb.ListCustomersParams{QWords: q, Scoped: scoped, PartyIds: parties,
			AfterCode: after.S, AfterID: after.ID, RowLimit: int32(limit) + 1})
		if err != nil {
			return err
		}
		if len(rows) > limit { // one row more was asked for: there is a next page
			rows, more = rows[:limit], true
		}
		if minimal {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.Customer.ID)
		}
		types, err = driverIDTypes(ctx, qq, ids)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if more {
		last := rows[len(rows)-1].Customer
		code := last.Code
		next = cursor{S: &code, ID: last.ID}.encode()
	}
	if minimal {
		out := make([]CustomerMinimal, 0, len(rows))
		for _, r := range rows {
			out = append(out, CustomerMinimal{ID: r.Customer.ID, BillingPartyID: r.BillingPartyID, Code: r.Customer.Code, Name: r.Customer.Name})
		}
		return out, next, nil
	}
	out := make([]Customer, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.customerOf(ctx, r.Customer, r.BillingPartyID, r.BillingDateBasis, types[r.Customer.ID]))
	}
	return out, next, nil
}

func driverIDTypes(ctx context.Context, q *partiesdb.Queries, ids []uuid.UUID) (map[uuid.UUID][]DriverIDType, error) {
	out := map[uuid.UUID][]DriverIDType{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.ListDriverIDTypes(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.CustomerID] = append(out[r.CustomerID], DriverIDType{Key: r.Key, Label: r.Label})
	}
	return out, nil
}

// customerOf builds the payload; it signs the logo URL (Files.SignedFile), so it runs after s.tx returns.
func (s *Service) customerOf(ctx context.Context, c partiesdb.Customer, party *uuid.UUID, basis *string, types []DriverIDType) Customer {
	if types == nil {
		types = []DriverIDType{}
	}
	b := billingBasisDelivered
	if basis != nil {
		b = *basis
	}
	key, url := s.signed(ctx, c.LogoFileID)
	return Customer{ID: c.ID, BillingPartyID: party, Code: c.Code, Name: c.Name, Description: c.Description,
		LogoKey: key, LogoURL: url, Address: c.Address, TaxID: c.TaxID, BranchType: c.BranchType,
		BranchNumber: c.BranchNumber, ContactName: c.ContactName, ContactPhone: c.ContactPhone,
		BillingEmail: c.BillingEmail, PaymentTermsDays: c.PaymentTermsDays, InvoiceNote: c.InvoiceNote,
		LineGroupID: c.LineGroupID, BillingDateBasis: b, DriverIDTypes: types, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// GetCustomer is GET /v1/customers/{id} (fleet:manage_customers): the full record. The logo is signed after the
// transaction: Files.SignedFile takes its own pool connection.
func (s *Service) GetCustomer(ctx context.Context, p *authz.Principal, id uuid.UUID) (*Customer, error) {
	var r partiesdb.GetCustomerRow
	var types map[uuid.UUID][]DriverIDType
	err := s.tx(ctx, p, func(_ pgx.Tx, q *partiesdb.Queries) error {
		var err error
		r, err = q.GetCustomer(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		types, err = driverIDTypes(ctx, q, []uuid.UUID{id})
		return err
	})
	if err != nil {
		return nil, err
	}
	c := s.customerOf(ctx, r.Customer, r.BillingPartyID, r.BillingDateBasis, types[id])
	return &c, nil
}

// CustomerInput is the body of POST /v1/customers and PATCH /v1/customers/{id} (customerSchema of the web): on
// PATCH absent fields stay and null clears an optional one (logoKey null removes the logo; the object stays,
// main spec §9.3).
type CustomerInput struct {
	Code             Field[string]         `json:"code"`
	Name             Field[string]         `json:"name"`
	Description      Field[string]         `json:"description"`
	LogoKey          Field[string]         `json:"logoKey"`
	Address          Field[string]         `json:"address"`
	TaxID            Field[string]         `json:"taxId"`
	BranchType       Field[string]         `json:"branchType"`
	BranchNumber     Field[string]         `json:"branchNumber"`
	ContactName      Field[string]         `json:"contactName"`
	ContactPhone     Field[string]         `json:"contactPhone"`
	BillingEmail     Field[string]         `json:"billingEmail"`
	PaymentTermsDays Field[int32]          `json:"paymentTermsDays"`
	InvoiceNote      Field[string]         `json:"invoiceNote"`
	LineGroupID      Field[string]         `json:"lineGroupId"`
	BillingDateBasis Field[string]         `json:"billingDateBasis"`
	DriverIDTypes    Field[[]DriverIDType] `json:"driverIdTypes"`
}

// validate checks every field that is present; create also requires code and name.
func (in *CustomerInput) validate(create bool) error {
	var ck checker
	if create || in.Code.Set {
		code := strings.ToUpper(strings.TrimSpace(in.Code.V))
		switch {
		case in.Code.Null || code == "":
			ck.add("code", "required")
		case !customerCodeRx.MatchString(code):
			ck.add("code", "invalid")
		}
		in.Code.V = code
	}
	if create || in.Name.Set {
		if in.Name.Null {
			ck.add("name", "required")
		} else {
			ck.text("name", in.Name.V, 200, true)
		}
	}
	ck.optText("description", in.Description, 1000)
	ck.key("logoKey", in.LogoKey)
	ck.optText("address", in.Address, 1000)
	if in.TaxID.Set && !in.TaxID.Null {
		ck.thaiID("taxId", in.TaxID.V, false)
	}
	if in.BranchType.Set && !in.BranchType.Null && in.BranchType.V != "" && !slices.Contains(branchTypes, in.BranchType.V) {
		ck.add("branchType", "invalid")
	}
	if v := strings.TrimSpace(in.BranchNumber.V); in.BranchNumber.Set && !in.BranchNumber.Null && v != "" && !branchNumberRx.MatchString(v) {
		ck.add("branchNumber", "invalid")
	}
	ck.optText("contactName", in.ContactName, 200)
	ck.optText("contactPhone", in.ContactPhone, 50)
	if in.BillingEmail.Set && !in.BillingEmail.Null {
		ck.email("billingEmail", in.BillingEmail.V)
	}
	if in.PaymentTermsDays.Set && !in.PaymentTermsDays.Null && (in.PaymentTermsDays.V < 0 || in.PaymentTermsDays.V > 3650) {
		ck.add("paymentTermsDays", "out_of_range")
	}
	ck.optText("invoiceNote", in.InvoiceNote, 2000)
	if v := strings.TrimSpace(in.LineGroupID.V); in.LineGroupID.Set && !in.LineGroupID.Null && v != "" && !lineGroupRx.MatchString(v) {
		ck.add("lineGroupId", "invalid")
	}
	if in.BillingDateBasis.Set && (in.BillingDateBasis.Null || !slices.Contains(billingBases, in.BillingDateBasis.V)) {
		ck.add("billingDateBasis", "invalid")
	}
	if in.DriverIDTypes.Set && !in.DriverIDTypes.Null {
		types := in.DriverIDTypes.V
		if len(types) > MaxDriverIDTypes {
			ck.bad = append(ck.bad, httpx.FieldViolation{Field: "driverIdTypes", Reason: "too_many", Params: map[string]any{"max": MaxDriverIDTypes}})
		}
		for i, t := range types {
			key := strings.TrimSpace(t.Key)
			switch {
			case !idTypeKeyRx.MatchString(key):
				ck.add(fmt.Sprintf("driverIdTypes[%d].key", i), "invalid")
			case slices.ContainsFunc(types[:i], func(o DriverIDType) bool { return strings.TrimSpace(o.Key) == key }):
				ck.add(fmt.Sprintf("driverIdTypes[%d].key", i), "duplicate")
			}
			ck.text(fmt.Sprintf("driverIdTypes[%d].label", i), t.Label, 100, true)
			types[i] = DriverIDType{Key: key, Label: strings.TrimSpace(t.Label)}
		}
	}
	return ck.err()
}

// branchRule: a branch has a 5-digit branch number (it is printed on the invoice). Checked on the merged record when
// the request touches either field, so a legacy record that lacks it stays editable otherwise.
func branchRule(touched bool, branchType, branchNumber *string) error {
	if touched && branchType != nil && *branchType == "branch" && branchNumber == nil {
		return httpx.ErrInvalidArgument(violation("branchNumber", "required"))
	}
	return nil
}

// setDriverIDTypes replaces the customer's driverIdTypes[].
func setDriverIDTypes(ctx context.Context, q *partiesdb.Queries, id uuid.UUID, types []DriverIDType) error {
	if err := q.DeleteDriverIDTypes(ctx, id); err != nil {
		return err
	}
	if len(types) == 0 {
		return nil
	}
	keys, labels := make([]string, len(types)), make([]string, len(types))
	for i, t := range types {
		keys[i], labels[i] = t.Key, t.Label
	}
	return q.InsertDriverIDTypes(ctx, partiesdb.InsertDriverIDTypesParams{CustomerID: id, Keys: keys, Labels: labels})
}

// CreateCustomer is POST /v1/customers (fleet:manage_customers, a steward key: R60): the customer, exactly one
// billing_parties row (kind customer, the billing date basis, R6), its driverIdTypes and logo, and outbox
// customers.changed (SSE global, cache:customer:{id}) in one transaction.
func (s *Service) CreateCustomer(ctx context.Context, p *authz.Principal, rid string, in CustomerInput) (*Customer, error) {
	if err := in.validate(true); err != nil {
		return nil, err
	}
	branch, number := textOf(in.BranchType), textOf(in.BranchNumber)
	if err := branchRule(true, branch, number); err != nil {
		return nil, err
	}
	basis := billingBasisDelivered
	if in.BillingDateBasis.Set {
		basis = in.BillingDateBasis.V
	}
	var terms *int32
	if in.PaymentTermsDays.Set && !in.PaymentTermsDays.Null {
		terms = &in.PaymentTermsDays.V
	}
	var email *string
	if v := textOf(in.BillingEmail); v != nil {
		e := strings.ToLower(*v)
		email = &e
	}
	var id uuid.UUID
	err := s.tx(ctx, p, func(tx pgx.Tx, q *partiesdb.Queries) error {
		var err error
		id, err = q.InsertCustomer(ctx, partiesdb.InsertCustomerParams{Code: in.Code.V, Name: strings.TrimSpace(in.Name.V),
			Description: textOf(in.Description), Address: textOf(in.Address), TaxID: textOf(in.TaxID), BranchType: branch,
			BranchNumber: number, ContactName: textOf(in.ContactName), ContactPhone: textOf(in.ContactPhone),
			BillingEmail: email, PaymentTermsDays: terms, InvoiceNote: textOf(in.InvoiceNote), LineGroupID: textOf(in.LineGroupID)})
		if err != nil {
			return err
		}
		if _, err := q.InsertCustomerParty(ctx, partiesdb.InsertCustomerPartyParams{CustomerID: &id, BillingDateBasis: basis}); err != nil {
			return err
		}
		if in.DriverIDTypes.Set && !in.DriverIDTypes.Null {
			if err := setDriverIDTypes(ctx, q, id, in.DriverIDTypes.V); err != nil {
				return err
			}
		}
		if v := textOf(in.LogoKey); v != nil {
			fid, err := s.commit(ctx, tx, p, *v, "logoKey", "customer_logo", id)
			if err != nil {
				return err
			}
			if err := q.SetCustomerLogo(ctx, partiesdb.SetCustomerLogoParams{LogoFileID: &fid, ID: id}); err != nil {
				return err
			}
		}
		return emit(ctx, tx, RouteCustomersChanged, "customer", id, nil, rid)
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, RouteCustomersChanged, id)
	return s.GetCustomer(ctx, p, id)
}

// PatchCustomer is PATCH /v1/customers/{id} (fleet:manage_customers): the merged record, the billing party's basis,
// driverIdTypes and logo in one transaction with outbox customers.changed; a request that changes nothing writes
// nothing, and a logoKey the customer already links (the payload sent back) stays linked, whoever uploaded it.
func (s *Service) PatchCustomer(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in CustomerInput) (*Customer, error) {
	if err := in.validate(false); err != nil {
		return nil, err
	}
	held, err := s.heldKeys(ctx, in.LogoKey)
	if err != nil {
		return nil, err
	}
	changed := false
	err = s.tx(ctx, p, func(tx pgx.Tx, q *partiesdb.Queries) error {
		cur, err := q.LockCustomer(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		c := cur.Customer
		m := partiesdb.UpdateCustomerParams{ID: id, Code: c.Code, Name: c.Name, Description: c.Description,
			LogoFileID: c.LogoFileID, Address: c.Address, TaxID: c.TaxID, BranchType: c.BranchType,
			BranchNumber: c.BranchNumber, ContactName: c.ContactName, ContactPhone: c.ContactPhone,
			BillingEmail: c.BillingEmail, PaymentTermsDays: c.PaymentTermsDays, InvoiceNote: c.InvoiceNote,
			LineGroupID: c.LineGroupID}
		text := func(f Field[string], dst **string) {
			if v := textOf(f); f.Set && !eq(*dst, v) {
				*dst, changed = v, true
			}
		}
		if in.Code.Set && in.Code.V != c.Code {
			m.Code, changed = in.Code.V, true
		}
		if in.Name.Set && strings.TrimSpace(in.Name.V) != c.Name {
			m.Name, changed = strings.TrimSpace(in.Name.V), true
		}
		text(in.Description, &m.Description)
		text(in.Address, &m.Address)
		text(in.TaxID, &m.TaxID)
		text(in.BranchType, &m.BranchType)
		text(in.BranchNumber, &m.BranchNumber)
		text(in.ContactName, &m.ContactName)
		text(in.ContactPhone, &m.ContactPhone)
		if in.BillingEmail.Set {
			var e *string
			if v := textOf(in.BillingEmail); v != nil {
				l := strings.ToLower(*v)
				e = &l
			}
			if !eq(m.BillingEmail, e) {
				m.BillingEmail, changed = e, true
			}
		}
		if in.PaymentTermsDays.Set {
			var t *int32
			if !in.PaymentTermsDays.Null {
				t = &in.PaymentTermsDays.V
			}
			if !eq(m.PaymentTermsDays, t) {
				m.PaymentTermsDays, changed = t, true
			}
		}
		text(in.InvoiceNote, &m.InvoiceNote)
		text(in.LineGroupID, &m.LineGroupID)
		if err := branchRule(in.BranchType.Set || in.BranchNumber.Set, m.BranchType, m.BranchNumber); err != nil {
			return err
		}
		if in.LogoKey.Set {
			fid, err := s.assetFile(ctx, tx, p, in.LogoKey, c.LogoFileID, held, "logoKey", "customer_logo", id)
			if err != nil {
				return err
			}
			if !eq(m.LogoFileID, fid) {
				m.LogoFileID, changed = fid, true
			}
		}
		if changed {
			if err := q.UpdateCustomer(ctx, m); err != nil {
				return err
			}
		}
		if in.BillingDateBasis.Set {
			n, err := q.SetCustomerPartyBasis(ctx, partiesdb.SetCustomerPartyBasisParams{CustomerID: &id, BillingDateBasis: in.BillingDateBasis.V})
			if err != nil {
				return err
			}
			if cur.BillingPartyID == nil {
				// A customer without its party (never written by this API): give it one now (R6).
				if _, err := q.InsertCustomerParty(ctx, partiesdb.InsertCustomerPartyParams{CustomerID: &id,
					BillingDateBasis: in.BillingDateBasis.V}); err != nil {
					return err
				}
				n = 1
			}
			changed = changed || n > 0
		}
		if in.DriverIDTypes.Set {
			types := in.DriverIDTypes.V
			before, err := driverIDTypes(ctx, q, []uuid.UUID{id})
			if err != nil {
				return err
			}
			if !slices.Equal(before[id], types) && (len(before[id]) > 0 || len(types) > 0) {
				if err := setDriverIDTypes(ctx, q, id, types); err != nil {
					return err
				}
				changed = true
			}
		}
		if !changed {
			return nil
		}
		return emit(ctx, tx, RouteCustomersChanged, "customer", id, nil, rid)
	})
	if err != nil {
		return nil, err
	}
	if changed {
		s.invalidate(ctx, RouteCustomersChanged, id)
	}
	return s.GetCustomer(ctx, p, id)
}
