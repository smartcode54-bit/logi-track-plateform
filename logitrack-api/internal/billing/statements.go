package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Statement statuses (billing_statements.status) and the invoice-set toggles of POST /v1/billing/statements.
const (
	StatusDraft     = "draft"
	StatusSent      = "sent"
	StatusPaid      = "paid"
	StatusCancelled = "cancelled"

	TypeTrip      = "trip"
	TypeMultidrop = "multidrop"
	TypeStandby   = "standby"

	CategoryPrimary       = "PRIMARY"
	CategorySupplementary = "SUPPLEMENTARY"
	CategoryUnverified    = "unverified"
)

var (
	statementStatuses = []string{StatusDraft, StatusSent, StatusPaid, StatusCancelled}
	invoiceTypes      = []string{TypeTrip, TypeMultidrop, TypeStandby}
	invoiceCategories = []string{CategoryPrimary, CategorySupplementary, CategoryUnverified}
)

// Statement is a billing statement (the invoice registry, billing_statements): totals are exact sums of its lines;
// withholdingTaxRate is the fraction snapshotted from the issuing company at creation (R18).
type Statement struct {
	ID                 uuid.UUID     `json:"id"`
	TenantID           uuid.UUID     `json:"tenantId"`
	BillingPartyID     uuid.UUID     `json:"billingPartyId"`
	CustomerID         uuid.UUID     `json:"customerId"`
	IssuerCompanyID    *uuid.UUID    `json:"issuerCompanyId"`
	InvoiceNumber      string        `json:"invoiceNumber"`
	CustomerName       *string       `json:"customerName"`
	CustomerCode       *string       `json:"customerCode"`
	Period             Period        `json:"period"`
	TotalAmount        float64       `json:"totalAmount"`
	WithholdingTaxRate float64       `json:"withholdingTaxRate"`
	WithholdingTax     float64       `json:"withholdingTax"`
	NetAmount          float64       `json:"netAmount"`
	TripCount          int32         `json:"tripCount"`
	TripOnlyCount      int32         `json:"tripOnlyCount"`
	TripSubtotal       float64       `json:"tripSubtotal"`
	StandbyCount       int32         `json:"standbyCount"`
	StandbySubtotal    float64       `json:"standbySubtotal"`
	MultiDropCount     int32         `json:"multiDropCount"`
	MultiDropSubtotal  float64       `json:"multiDropSubtotal"`
	Status             string        `json:"status"`
	GeneratedAt        time.Time     `json:"generatedAt"`
	SentAt             *time.Time    `json:"sentAt"`
	PaidAt             *time.Time    `json:"paidAt"`
	CancelledAt        *time.Time    `json:"cancelledAt"`
	DueDate            *time.Time    `json:"dueDate"`
	Note               *string       `json:"note"`
	GeneratedBy        *uuid.UUID    `json:"generatedBy"`
	CreatedAt          time.Time     `json:"createdAt"`
	UpdatedAt          time.Time     `json:"updatedAt"`
	LegacyDocID        *string       `json:"legacyDocId,omitempty"`
	Documents          []DocumentRef `json:"documents,omitempty"`
}

// Period is a Bangkok billing month.
type Period struct {
	Year  int32 `json:"year"`
	Month int32 `json:"month"`
}

// DocumentRef is one rendered document of a statement (statement_documents, R14); the download is
// GET /v1/billing/statements/{id}/documents/{kind} (T39).
type DocumentRef struct {
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Error      *string    `json:"error,omitempty"`
	RenderedAt *time.Time `json:"renderedAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

func statementOf(r billingdb.GetStatementRow) Statement {
	return Statement{ID: r.ID, TenantID: r.TenantID, BillingPartyID: r.BillingPartyID, CustomerID: r.BillingPartyID,
		IssuerCompanyID: r.IssuerCompanyID, InvoiceNumber: r.InvoiceNumber, CustomerName: r.CustomerNameSnapshot,
		CustomerCode: r.CustomerCodeSnapshot, Period: Period{Year: r.PeriodYear, Month: r.PeriodMonth},
		TotalAmount: r.TotalAmount, WithholdingTaxRate: r.WithholdingTaxRate, WithholdingTax: r.WithholdingTax,
		NetAmount: r.NetAmount, TripCount: r.TripCount, TripOnlyCount: r.TripOnlyCount, TripSubtotal: r.TripSubtotal,
		StandbyCount: r.StandbyCount, StandbySubtotal: r.StandbySubtotal, MultiDropCount: r.MultiDropCount,
		MultiDropSubtotal: r.MultiDropSubtotal, Status: r.Status, GeneratedAt: r.GeneratedAt.UTC(), SentAt: utc(r.SentAt),
		PaidAt: utc(r.PaidAt), CancelledAt: utc(r.CancelledAt), DueDate: utc(r.DueDate), Note: r.Note,
		GeneratedBy: r.GeneratedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), LegacyDocID: r.LegacyDocID}
}

// StatementInput is the body of POST /v1/billing/statements. Types and categories are the invoice-set toggles
// (absent = all; an empty list is refused); issuerCompanyId names the issuing company of the billing tenant (absent =
// its owner company, else its only active company).
type StatementInput struct {
	CustomerID      string    `json:"customerId"`
	Year            int       `json:"year"`
	Month           int       `json:"month"`
	Types           *[]string `json:"types"`
	Categories      *[]string `json:"categories"`
	Note            string    `json:"note"`
	IssuerCompanyID string    `json:"issuerCompanyId"`
}

// toggles validates an optional list of toggles: absent = all of allowed, empty or unknown values refused.
func (ck *checker) toggles(field string, v *[]string, allowed []string) []string {
	if v == nil {
		return slices.Clone(allowed)
	}
	if len(*v) == 0 {
		ck.add(field, "required")
		return nil
	}
	var out []string
	for _, x := range *v {
		if !slices.Contains(allowed, x) {
			ck.addParams(field, "invalid", map[string]any{"allowed": allowed})
			return nil
		}
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// invoiceNumber formats {customerCode}-{YYYYMM}-{seq %03d} (four digits after 999; billing-document/page.tsx:456,
// billingStatement.ts:96-97).
func invoiceNumber(code string, year, month, seq int) string {
	return fmt.Sprintf("%s-%04d%02d-%03d", code, year, month, seq)
}

// CreateStatement is POST /v1/billing/statements (accounting:billing_document): one transaction that numbers the
// invoice from next_invoice_seq() (the billing tenant's counter for the party and month; another carrier's counter
// is 409 failed_precondition, R61), inserts the draft statement of the billing tenant with the issuing company's
// withholding rate (R18), its lines from the stored snapshots (never repriced; main spec §6.14) and the exact totals,
// and appends statement.created (documents.render, T39). Nothing billable is 409 failed_precondition
// (no_billable_rows). Any failure rolls the counter back with the rest, so the numbering has no gap (§6.18 #14).
func (s *Service) CreateStatement(ctx context.Context, p *authz.Principal, rid string, in StatementInput) (*Statement, error) {
	var ck checker
	partyID := ck.party("customerId", in.CustomerID)
	if in.Year < 2000 || in.Year > 2999 {
		ck.add("year", "out_of_range")
	}
	if in.Month < 1 || in.Month > 12 {
		ck.add("month", "out_of_range")
	}
	types := ck.toggles("types", in.Types, invoiceTypes)
	categories := ck.toggles("categories", in.Categories, invoiceCategories)
	note := ck.text("note", in.Note, 1000, false)
	var issuer uuid.UUID
	if v := strings.TrimSpace(in.IssuerCompanyID); v != "" {
		var err error
		if issuer, err = uuid.Parse(v); err != nil || issuer == uuid.Nil {
			ck.add("issuerCompanyId", "invalid")
		}
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	tenant, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	from, to := bangkokMonth(in.Year, in.Month)
	var id, party uuid.UUID
	err = s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		pr, err := writeParty(ctx, q, partyID)
		if err != nil {
			return err
		}
		party = pr.ID
		company, err := issuerCompany(ctx, q, tenant, issuer)
		if err != nil {
			return err
		}
		code, name := party.String(), (*string)(nil)
		switch {
		case pr.CustomerCode != nil && strings.TrimSpace(*pr.CustomerCode) != "":
			code = strings.TrimSpace(*pr.CustomerCode)
		case pr.TenantCode != nil && strings.TrimSpace(*pr.TenantCode) != "":
			code = strings.TrimSpace(*pr.TenantCode)
		}
		if pr.CustomerName != nil {
			name = pr.CustomerName
		} else if pr.TenantName != nil {
			name = pr.TenantName
		}
		seq, err := q.NextInvoiceSeq(ctx, billingdb.NextInvoiceSeqParams{TenantID: tenant, PartyID: party,
			PeriodYear: int32(in.Year), PeriodMonth: int32(in.Month), CustomerCode: code})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && seq == 0) {
			return errPrecondition("counter_owned_by_other_carrier",
				"another billing carrier numbers this customer's invoices for the month")
		}
		if err != nil {
			return err
		}
		var due *time.Time
		if pr.PaymentTermsDays != nil {
			d := now.Add(time.Duration(*pr.PaymentTermsDays) * 24 * time.Hour)
			due = &d
		}
		var notePtr *string
		if note != "" {
			notePtr = &note
		}
		codeSnap := code
		id, err = q.InsertStatement(ctx, billingdb.InsertStatementParams{TenantID: tenant, PartyID: party,
			IssuerCompanyID: &company.id, InvoiceNumber: invoiceNumber(code, in.Year, in.Month, int(seq)),
			CustomerName: name, CustomerCode: &codeSnap, PeriodYear: int32(in.Year), PeriodMonth: int32(in.Month),
			GeneratedAt: now, DueDate: due, Note: notePtr, GeneratedBy: actor(p)})
		if err != nil {
			return err
		}
		n, err := q.InsertStatementLines(ctx, billingdb.InsertStatementLinesParams{StatementID: id, PartyID: party,
			TenantID: tenant, PlanBasis: pr.BillingDateBasis == "plan", PeriodFrom: from, PeriodTo: to,
			IncludeTrips: slices.Contains(types, TypeTrip), IncludeMultidrop: slices.Contains(types, TypeMultidrop),
			IncludeStandby: slices.Contains(types, TypeStandby), Categories: categories})
		if err != nil {
			return err
		}
		if n == 0 {
			return errPrecondition("no_billable_rows", "nothing priced to invoice for this customer and month")
		}
		totals, err := q.StatementTotals(ctx, id)
		if err != nil {
			return err
		}
		wht := compute.WithholdingTHB(totals.Total, company.rate)
		if err := q.UpdateStatementTotals(ctx, billingdb.UpdateStatementTotalsParams{ID: id,
			WithholdingTax: decimalText(wht, 2)}); err != nil {
			return err
		}
		return emit(ctx, tx, RouteStatementCreated, "statement", id.String(), tenant,
			map[string]any{"billingPartyId": party.String(), "statementId": id.String(), "status": StatusDraft}, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteStatementCreated, party)
	return s.GetStatement(ctx, p, id)
}

// issuer is the company a statement is issued by and its withholding rate as a fraction.
type issuer struct {
	id   uuid.UUID
	rate float64
}

// issuerCompany picks the issuing company of the billing tenant: the one the request names (it must belong to the
// tenant), else the tenant's owner company, else its only active company; none is 422 issuerCompanyId required.
func issuerCompany(ctx context.Context, q *billingdb.Queries, tenant, named uuid.UUID) (issuer, error) {
	if named != uuid.Nil {
		c, err := q.IssuerCompany(ctx, billingdb.IssuerCompanyParams{ID: named, TenantID: tenant})
		if errors.Is(err, pgx.ErrNoRows) {
			return issuer{}, httpx.ErrInvalidArgument(violation("issuerCompanyId", "not_found"))
		}
		return issuer{id: c.ID, rate: c.WithholdingTaxRate}, err
	}
	cs, err := q.IssuerCompanies(ctx, tenant)
	if err != nil {
		return issuer{}, err
	}
	switch {
	case len(cs) > 0 && cs[0].CompanyType == "owner":
		return issuer{id: cs[0].ID, rate: cs[0].WithholdingTaxRate}, nil
	case len(cs) == 1:
		return issuer{id: cs[0].ID, rate: cs[0].WithholdingTaxRate}, nil
	}
	return issuer{}, httpx.ErrInvalidArgument(violation("issuerCompanyId", "required"))
}

// GetStatement is GET /v1/billing/statements/{id} (accounting:billing_result): the statement and its documents.
func (s *Service) GetStatement(ctx context.Context, p *authz.Principal, id uuid.UUID) (*Statement, error) {
	var out Statement
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		r, err := q.GetStatement(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		out = statementOf(r)
		docs, err := q.StatementDocuments(ctx, id)
		out.Documents = make([]DocumentRef, 0, len(docs))
		for _, d := range docs {
			out.Documents = append(out.Documents, DocumentRef{Kind: d.Kind, Status: d.Status, Error: d.Error,
				RenderedAt: utc(d.RenderedAt), UpdatedAt: d.UpdatedAt.UTC()})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListStatementsInput are the query parameters of GET /v1/billing/statements.
type ListStatementsInput struct {
	CustomerID, Status string
	Year, Month        int
	Limit              int
	Cursor             string
}

// ListStatements is GET /v1/billing/statements (accounting:billing_result): newest first, keyset (generated_at, id);
// filters by party, status and period (the period moves from a post-filter into the query, billingStatement.ts:239).
func (s *Service) ListStatements(ctx context.Context, p *authz.Principal, in ListStatementsInput) ([]Statement, string, error) {
	var ck checker
	limit := ck.limitOf(in.Limit, DefaultLimit)
	var status *string
	if in.Status != "" && in.Status != "all" {
		if !slices.Contains(statementStatuses, in.Status) {
			ck.addParams("status", "invalid", map[string]any{"allowed": statementStatuses})
		}
		status = &in.Status
	}
	var year, month *int32
	if in.Year != 0 {
		if in.Year < 2000 || in.Year > 2999 {
			ck.add("year", "out_of_range")
		}
		y := int32(in.Year)
		year = &y
	}
	if in.Month != 0 {
		if in.Month < 1 || in.Month > 12 {
			ck.add("month", "out_of_range")
		}
		m := int32(in.Month)
		month = &m
	}
	var after cursor
	if in.Cursor != "" {
		var ok bool
		if after, ok = decodeCursor(in.Cursor); !ok || after.T == nil || after.ID == uuid.Nil {
			ck.add("cursor", "invalid")
		}
	}
	if err := ck.err(); err != nil {
		return nil, "", err
	}
	var party *uuid.UUID
	if strings.TrimSpace(in.CustomerID) != "" {
		id, found, err := s.listParty(ctx, p, in.CustomerID)
		if err != nil || !found {
			return []Statement{}, "", err
		}
		party = &id
	}
	all, tenants := p.TenantReach()
	var out []Statement
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		rows, err := q.ListStatements(ctx, billingdb.ListStatementsParams{AllTenants: all, TenantIds: tenants,
			PartyID: party, Status: status, PeriodYear: year, PeriodMonth: month, AfterGenerated: after.T,
			AfterID: after.ID, RowLimit: int32(limit) + 1})
		for _, r := range rows {
			out = append(out, statementOf(billingdb.GetStatementRow(r)))
		}
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		t := last.GeneratedAt
		next = cursor{T: &t, ID: last.ID}.encode()
	}
	if out == nil {
		out = []Statement{}
	}
	return out, next, nil
}

// StatementLine is one billing_statement_lines row: rowId follows the rows API (trip id, {tripId}_s{n} for a
// multi-drop stop, the standby id; main spec §6.14).
type StatementLine struct {
	ID        uuid.UUID  `json:"id"`
	RowID     string     `json:"rowId"`
	RowType   string     `json:"rowType"`
	TripID    *uuid.UUID `json:"tripId"`
	TripNo    *string    `json:"tripNo"`
	StopIndex *int32     `json:"stopIndex"`
	StandbyID *uuid.UUID `json:"standbyId"`
	AmountTHB float64    `json:"amountThb"`
}

// StatementLines is GET /v1/billing/statements/{id}/lines (accounting:billing_result): every line (a statement holds
// a month of one party; no cap).
func (s *Service) StatementLines(ctx context.Context, p *authz.Principal, id uuid.UUID) ([]StatementLine, error) {
	out := []StatementLine{}
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		if _, err := q.GetStatement(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		} else if err != nil {
			return err
		}
		rows, err := q.StatementLines(ctx, id)
		for _, r := range rows {
			l := StatementLine{ID: r.ID, RowType: r.RowType, TripID: r.TripID, TripNo: r.TripNo, StopIndex: r.StopIndex,
				StandbyID: r.StandbyID, AmountTHB: r.AmountThb}
			switch {
			case r.StandbyID != nil:
				l.RowID = r.StandbyID.String()
			case r.TripID != nil && r.StopIndex != nil:
				l.RowID = fmt.Sprintf("%s_s%d", r.TripID, *r.StopIndex)
			case r.TripID != nil:
				l.RowID = r.TripID.String()
			}
			out = append(out, l)
		}
		return err
	})
	return out, err
}

// StatusInput is the body of POST /v1/billing/statements/{id}/status.
type StatusInput struct {
	Status string `json:"status"`
}

// transitions is the statement state machine (main spec §6.14, §6.18 #24): draft -> sent -> paid; draft and sent ->
// cancelled; paid and cancelled are terminal.
var transitions = map[string][]string{
	StatusDraft: {StatusSent, StatusCancelled},
	StatusSent:  {StatusPaid, StatusCancelled},
}

// SetStatementStatus is POST /v1/billing/statements/{id}/status (accounting:manage_statements): the statement is
// locked FOR UPDATE (so the change waits for every pricing transaction that read its period FOR SHARE), the server
// clock stamps sent_at / paid_at / cancelled_at, and statement.status_changed is appended (paid renders the receipt,
// T39). sent and paid lock the period (PeriodLock). Any other move is 409 failed_precondition (invalid_transition).
func (s *Service) SetStatementStatus(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in StatusInput) (*Statement, error) {
	to := strings.TrimSpace(in.Status)
	if !slices.Contains([]string{StatusSent, StatusPaid, StatusCancelled}, to) {
		return nil, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "status", Reason: "invalid",
			Params: map[string]any{"allowed": []string{StatusSent, StatusPaid, StatusCancelled}}})
	}
	var party uuid.UUID
	err := s.tx(ctx, p, func(tx pgx.Tx, q *billingdb.Queries) error {
		cur, err := q.LockStatement(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		party = cur.BillingPartyID
		if !slices.Contains(transitions[cur.Status], to) {
			return httpx.NewError(http.StatusConflict, CodeFailedPrecondition, "the statement cannot move to this status").
				WithDetails(map[string]any{"reason": "invalid_transition", "from": cur.Status, "to": to})
		}
		if err := q.SetStatementStatus(ctx, billingdb.SetStatementStatusParams{ID: id, Status: to,
			At: s.now().UTC().Truncate(time.Microsecond)}); err != nil {
			return err
		}
		return emit(ctx, tx, RouteStatementStatusChanged, "statement", id.String(), cur.TenantID,
			map[string]any{"billingPartyId": cur.BillingPartyID.String(), "statementId": id.String(), "status": to,
				"previousStatus": cur.Status}, rid)
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	s.invalidate(ctx, RouteStatementStatusChanged, party)
	return s.GetStatement(ctx, p, id)
}

// DeleteStatement is DELETE /v1/billing/statements/{id} (accounting:manage_statements): drafts only (409
// statement_not_draft otherwise, as trg_billing_statement_draft_only_delete enforces); its number stays unused.
func (s *Service) DeleteStatement(ctx context.Context, p *authz.Principal, id uuid.UUID) error {
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		cur, err := q.LockStatement(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if cur.Status != StatusDraft {
			return httpx.NewError(http.StatusConflict, CodeStatementNotDraft, "only a draft statement can be deleted").
				WithDetails(map[string]any{"status": cur.Status, "invoiceNumber": cur.InvoiceNumber})
		}
		_, err = q.DeleteStatement(ctx, id)
		return err
	})
	return mapDBError(err)
}
