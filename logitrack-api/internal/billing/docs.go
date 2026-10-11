package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/documents"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/render"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/inbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Document kinds (statement_documents.kind) and the file each is stored as (main spec §9.2, §9.9).
const (
	KindInvoicePDF   = "invoice_summary_pdf"
	KindDetailXLSX   = "invoice_detail_xlsx"
	KindBundleZIP    = "bundle_zip"
	KindReceiptPDF   = "receipt_pdf"
	statusReady      = "ready"
	statusPending    = "pending"
	contentTypePDF   = "application/pdf"
	contentTypeXLSX  = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	contentTypeZIP   = "application/zip"
	purposeStatement = "statement_document"
	purposeReport    = "report"
)

// DocumentKinds are the kinds of statement_documents.
var DocumentKinds = []string{KindInvoicePDF, KindDetailXLSX, KindBundleZIP, KindReceiptPDF}

// invoiceKinds are rendered when a statement is created (and by a regenerate); the receipt only once it is paid
// (commit 25d5468: never in the bundle).
var invoiceKinds = []string{KindInvoicePDF, KindDetailXLSX, KindBundleZIP}

// Queue, routing keys and the event of documents.render (Appendix B §B.5.3, §B.4.3).
const (
	QueueDocuments         = "documents.render"
	RouteRegenerateJob     = "job." + jobs.TypeDocumentsRegenerate
	RouteShopeeReportJob   = "job." + jobs.TypeDocumentsShopeeReport
	RouteDocumentsRendered = "documents.rendered"
	documentsRenderTimeout = 10 * time.Minute
)

// Files is what the API side needs from object storage (storage.Service implements it): signed downloads of rendered
// documents.
type Files interface {
	SignedDownload(ctx context.Context, fileID uuid.UUID, ttl time.Duration, opts storage.GetOptions) (string, time.Time, error)
}

// fileNames are the file names a download saves as (a document's own name, the bundle and receipt with the real
// customer code instead of the legacy hard-coded CJSF, §6.18 #8).
func fileName(kind, invoiceNumber, code string, year, month int) string {
	period := fmt.Sprintf("%04d%02d", year, month)
	safe := func(s string) string { return unsafeName.ReplaceAllString(s, "") }
	switch kind {
	case KindInvoicePDF:
		return safe(invoiceNumber) + "_invoice_summary.pdf"
	case KindDetailXLSX:
		return safe(invoiceNumber) + "_invoice_detail.xlsx"
	case KindBundleZIP:
		return "invoice_" + safe(code) + "_" + period + ".zip"
	case KindReceiptPDF:
		return "receipt_" + safe(code) + "_" + period + ".pdf"
	}
	return "document"
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// documentKey is the object key of a rendered statement document: documents/statements/{statementId}/{renderId}/{file}.
// The render id keeps every render's objects apart, since file_objects keys are unique and a regenerate keeps the
// previous object (main spec §9.2 refined: the legacy layout had no room for a second render).
func documentKey(statement, render uuid.UUID, kind string) string {
	file := map[string]string{KindInvoicePDF: "invoice_summary.pdf", KindDetailXLSX: "invoice_detail.xlsx",
		KindBundleZIP: "bundle.zip", KindReceiptPDF: "receipt.pdf"}[kind]
	return "documents/statements/" + statement.String() + "/" + render.String() + "/" + file
}

// DocumentDownload is GET /v1/billing/statements/{id}/documents/{kind} (accounting:billing_result): a signed URL of the
// stored document (the route answers 302 to it), named for the browser's save dialog. A re-download serves the stored
// file, never a new render from current rows (fixes billing-result/page.tsx:269-304). A document not rendered yet, still
// rendering or failed is 409 failed_precondition with details.status (pending | rendering | failed); a receipt before
// the statement is paid is 409 failed_precondition (statement_not_paid).
func (s *Service) DocumentDownload(ctx context.Context, p *authz.Principal, id uuid.UUID, kind string) (string, error) {
	if !slices.Contains(DocumentKinds, kind) {
		return "", httpx.ErrNotFound()
	}
	if s.files == nil {
		return "", httpx.ErrUnavailable("document storage is not configured")
	}
	var st billingdb.GetStatementRow
	var doc billingdb.StatementDocumentRow
	err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		var err error
		st, err = q.GetStatement(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if kind == KindReceiptPDF && st.Status != StatusPaid {
			return errPrecondition("statement_not_paid", "the receipt is issued when the statement is paid").
				WithDetails(map[string]any{"reason": "statement_not_paid", "status": statusPending})
		}
		doc, err = q.StatementDocument(ctx, billingdb.StatementDocumentParams{StatementID: id, Kind: kind})
		if errors.Is(err, pgx.ErrNoRows) {
			doc, err = billingdb.StatementDocumentRow{Kind: kind, Status: statusPending}, nil
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if doc.Status != statusReady || doc.FileID == nil {
		details := map[string]any{"reason": "document_not_ready", "status": doc.Status}
		if doc.Error != nil {
			details["error"] = *doc.Error
		}
		return "", errPrecondition("document_not_ready", "the document is not rendered yet").WithDetails(details)
	}
	code := deref(st.CustomerCodeSnapshot)
	name := fileName(kind, st.InvoiceNumber, code, int(st.PeriodYear), int(st.PeriodMonth))
	u, _, err := s.files.SignedDownload(ctx, *doc.FileID, 0, storage.GetOptions{
		ContentDisposition: `attachment; filename="` + name + `"`})
	if errors.Is(err, storage.ErrObjectNotFound) {
		return "", errPrecondition("document_missing", "the stored document is gone; regenerate it").
			WithDetails(map[string]any{"status": "failed"})
	}
	return u, err
}

// RegenerateInput is the optional body of POST /v1/billing/statements/{id}/documents/regenerate: showActualPickup,
// when given, changes whether the detail sheet carries the วันรับงานจริง column (absent = the statement's stored
// choice, billing_statements.show_actual_pickup, 0016).
type RegenerateInput struct {
	ShowActualPickup *bool `json:"showActualPickup"`
}

// regenerateParams are the parameters of a documents.regenerate job.
type regenerateParams struct {
	StatementID      uuid.UUID `json:"statementId"`
	ShowActualPickup *bool     `json:"showActualPickup,omitempty"`
}

// RegenerateDocuments is POST /v1/billing/statements/{id}/documents/regenerate (accounting:manage_statements): a
// documents.regenerate job (202 {jobId}; lock:job:documents.regenerate:{statementId}, a render in progress is 409
// already_exists with its jobId) that renders the invoice PDF, the detail sheet and the bundle again from the statement's
// lines, and the receipt when the statement is paid; each document points at its new file, the old objects stay. A
// showActualPickup in the body renders the detail sheet with (or without) the วันรับงานจริง column and is stored on the
// statement in the transaction that succeeds the job. A statement without lines (a legacy one the ETL loaded) is 409
// failed_precondition (no_lines). Only the own fleet manages statements (billingTenantOf).
func (s *Service) RegenerateDocuments(ctx context.Context, p *authz.Principal, id uuid.UUID, in RegenerateInput) (jobs.Job, error) {
	if _, err := billingTenantOf(p); err != nil {
		return jobs.Job{}, err
	}
	if s.jobs == nil {
		return jobs.Job{}, httpx.ErrUnavailable("jobs are not available")
	}
	var tenant uuid.UUID
	if err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
		st, err := q.GetStatement(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		tenant = st.TenantID
		n, err := q.StatementLineCount(ctx, id)
		if err == nil && n == 0 {
			return errPrecondition("no_lines", "a legacy statement has no stored lines to render its documents from")
		}
		return err
	}); err != nil {
		return jobs.Job{}, err
	}
	return s.jobs.Submit(ctx, jobs.SubmitInput{
		NewInput: jobs.NewInput{Type: jobs.TypeDocumentsRegenerate, OwnerUserID: actor(p), TenantID: &tenant,
			Params: regenerateParams{StatementID: id, ShowActualPickup: in.ShowActualPickup}, Scope: id.String()},
		Command: true,
	})
}

// --- the worker consumer ------------------------------------------------------------------------------------------

// DocumentFiles is what documents.render needs from object storage (storage.Service implements it).
type DocumentFiles interface {
	PutServerObject(ctx context.Context, o storage.ServerObject) (storage.StoredObject, error)
	DeleteServerObject(ctx context.Context, o storage.StoredObject) error
	ReadFile(ctx context.Context, fileID uuid.UUID) ([]byte, string, error)
}

// DocumentJobDeps are the collaborators of the documents.render consumer.
type DocumentJobDeps struct {
	// Pool is the logitrack_app pool: the consumer has no request principal and works in db.WithSystem.
	Pool db.Beginner
	// Files stores the rendered objects and reads the images they embed.
	Files DocumentFiles
	// Fonts are the Sarabun faces (PDF_FONT_DIR, else the embedded copies).
	Fonts render.Fonts
	// Locks releases the job locks of documents.regenerate and documents.shopee-report.
	Locks jobs.Locker
	// Now stamps rendered_at and the Shopee report's issue date (default time.Now).
	Now func() time.Time
	Log zerolog.Logger
}

// DocumentJob is the documents.render consumer of the worker (P3 with billing, R69; main spec §9.9): statement.created
// renders the invoice summary PDF, the detail XLSX and the bundle ZIP; statement.status_changed to paid renders the
// receipt; job.documents.regenerate renders a statement's documents again; job.documents.shopee-report renders the
// Shopee Express report.
type DocumentJob struct {
	d DocumentJobDeps
}

// NewDocumentJob builds the consumer.
func NewDocumentJob(d DocumentJobDeps) (*DocumentJob, error) {
	if d.Pool == nil || d.Files == nil {
		return nil, errors.New("billing: documents.render needs a pool and object storage")
	}
	if len(d.Fonts.Regular) == 0 || len(d.Fonts.Bold) == 0 {
		return nil, errors.New("billing: documents.render needs the Sarabun fonts (PDF_FONT_DIR)")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &DocumentJob{d: d}, nil
}

// Registration is the consumer of queue documents.render (prefetch 2; a month's Shopee report decodes its photos).
func (j *DocumentJob) Registration() mq.Registration {
	return mq.Registration{Queue: QueueDocuments, Handler: j.Handle, Timeout: documentsRenderTimeout}
}

// statementEvent is the payload of statement.created / statement.status_changed.
type statementEvent struct {
	StatementID uuid.UUID `json:"statementId"`
	Status      string    `json:"status"`
}

// errPermanent marks a render that cannot succeed by retrying (the documents are failed and the message acked): the
// event path hands it to the consumer as mq.Permanent, a regenerate fails its job.
type errPermanent struct{ error }

// Handle runs one delivery of documents.render.
func (j *DocumentJob) Handle(ctx context.Context, d *mq.Delivery) error {
	switch d.RoutingKey {
	case RouteStatementCreated, RouteStatementStatusChanged:
		var ev statementEvent
		if err := d.Decode(&ev); err != nil {
			return err
		}
		if ev.StatementID == uuid.Nil {
			return mq.Permanent(errors.New("documents.render: event without statementId"))
		}
		kinds := invoiceKinds
		if d.RoutingKey == RouteStatementStatusChanged {
			if ev.Status != StatusPaid {
				return nil // only paid issues a document (the receipt)
			}
			kinds = []string{KindReceiptPDF}
		}
		return j.statementEvent(ctx, d, ev.StatementID, kinds)
	case RouteRegenerateJob:
		return j.regenerate(ctx, d)
	case RouteShopeeReportJob:
		return j.shopee(ctx, d)
	}
	return mq.Permanent(fmt.Errorf("documents.render: unexpected routing key %s", d.RoutingKey))
}

// statementEvent renders kinds of a statement for an event, once per message (consumer_inbox).
func (j *DocumentJob) statementEvent(ctx context.Context, d *mq.Delivery, id uuid.UUID, kinds []string) error {
	seen := false
	if err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		var err error
		seen, err = billingdb.New(tx).InboxSeen(ctx, billingdb.InboxSeenParams{Consumer: d.Queue, MessageID: d.MessageID})
		return err
	}); err != nil {
		return err
	}
	if seen {
		return nil
	}
	claim := func(ctx context.Context, tx pgx.Tx) (bool, error) { return inbox.Claim(ctx, tx, d.Queue, d.MessageID) }
	err := j.renderStatement(ctx, id, kinds, nil, nil, claim)
	var perm errPermanent
	if errors.As(err, &perm) {
		// The kinds are marked failed with the cause; a retry would draw the same failure, so the message is acked
		// (no retry ladder, no dead letter). A regenerate job renders them again.
		return mq.Permanent(perm.error)
	}
	return err
}

// regenerate runs a documents.regenerate job.
func (j *DocumentJob) regenerate(ctx context.Context, d *mq.Delivery) error {
	cmd, err := jobs.DecodeCommand(d)
	if err != nil {
		return err
	}
	var job jobs.Job
	if err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		var err error
		job, err = jobs.Start(ctx, tx, cmd.JobID)
		return err
	}); errors.Is(err, jobs.ErrNotActive) {
		j.release(ctx, jobs.TypeDocumentsRegenerate, cmd)
		return nil
	} else if err != nil {
		return err
	}
	var params regenerateParams
	if json.Unmarshal(job.Params, &params) != nil || params.StatementID == uuid.Nil {
		return j.failJob(ctx, cmd, job.ID, "the job names no statement")
	}
	jobID := job.ID
	claim := func(ctx context.Context, tx pgx.Tx) (bool, error) {
		_, err := jobs.Succeed(ctx, tx, jobID, map[string]any{"statementId": params.StatementID.String()})
		if errors.Is(err, jobs.ErrNotActive) {
			return false, nil
		}
		if err != nil || params.ShowActualPickup == nil {
			return err == nil, err
		}
		// The choice is stored with the documents drawn from it, so the column names what the stored sheet shows.
		return true, billingdb.New(tx).SetStatementShowActualPickup(ctx, billingdb.SetStatementShowActualPickupParams{
			ID: params.StatementID, ShowActualPickup: *params.ShowActualPickup})
	}
	err = j.renderStatement(ctx, params.StatementID, nil, &jobID, params.ShowActualPickup, claim)
	var perm errPermanent
	if errors.As(err, &perm) {
		return j.failJob(ctx, cmd, jobID, perm.Error())
	}
	if err == nil {
		j.release(ctx, jobs.TypeDocumentsRegenerate, cmd)
	}
	return err
}

func (j *DocumentJob) failJob(ctx context.Context, cmd jobs.Command, id uuid.UUID, cause string) error {
	err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.Fail(ctx, tx, id, cause, nil)
		return err
	})
	if errors.Is(err, jobs.ErrNotActive) {
		err = nil
	}
	if err == nil {
		j.release(ctx, cmd.Type, cmd)
	}
	return err
}

func (j *DocumentJob) release(ctx context.Context, jobType string, cmd jobs.Command) {
	if j.d.Locks == nil || cmd.Scope == "" {
		return
	}
	if err := j.d.Locks.Release(context.WithoutCancel(ctx), j.d.Locks.JobKey(jobType, cmd.Scope), cmd.JobID.String()); err != nil {
		j.d.Log.Warn().Err(err).Str("job", jobType).Msg("documents.render: lock release failed; it expires on its own")
	}
}

// renderStatement renders a statement's documents: kinds (nil = the invoice kinds, plus the receipt of a paid statement),
// marked rendering first, stored under a fresh render id, then registered and pointed at in one transaction that also
// runs claim (the inbox claim of an event, the success of a job); claim false means another delivery finished first, and
// the objects just stored are removed. showActualPickup, when not nil, overrides the statement's stored choice of the
// detail sheet's วันรับงานจริง column (a regenerate that names it). A statement that no longer exists (a deleted draft)
// ends quietly. A render that cannot succeed marks the documents failed and returns errPermanent; a storage or database
// failure is returned for the retry ladder, the documents marked failed meanwhile.
func (j *DocumentJob) renderStatement(ctx context.Context, id uuid.UUID, kinds []string, jobID *uuid.UUID,
	showActualPickup *bool, claim func(context.Context, pgx.Tx) (bool, error)) error {
	in, err := j.loadStatement(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error { _, err := claim(ctx, tx); return err })
	}
	if err != nil {
		return err
	}
	if showActualPickup != nil {
		in.detail.ShowActualPickup = *showActualPickup
	}
	if kinds == nil {
		kinds = slices.Clone(invoiceKinds)
		if in.status == StatusPaid {
			kinds = append(kinds, KindReceiptPDF)
		}
	}
	if slices.Contains(kinds, KindReceiptPDF) && in.status != StatusPaid {
		kinds = slices.DeleteFunc(kinds, func(k string) bool { return k == KindReceiptPDF })
	}
	if len(kinds) == 0 {
		return db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error { _, err := claim(ctx, tx); return err })
	}
	if err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		return billingdb.New(tx).MarkDocumentsRendering(ctx, billingdb.MarkDocumentsRenderingParams{StatementID: id,
			JobID: jobID, Kinds: kinds})
	}); err != nil {
		return err
	}
	bodies, err := j.renderKinds(in, kinds)
	if err != nil {
		j.markFailed(ctx, id, kinds, jobID, err.Error())
		return errPermanent{err}
	}
	renderID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	var stored []storage.StoredObject
	for _, k := range kinds {
		o, err := j.d.Files.PutServerObject(ctx, storage.ServerObject{Purpose: purposeStatement, Key: documentKey(id, renderID, k),
			TenantID: &in.tenant, OwnerID: id, ContentType: bodies[k].contentType, Body: bodies[k].body})
		if err != nil {
			j.cleanup(ctx, stored)
			j.markFailed(ctx, id, kinds, jobID, "storage: "+err.Error())
			return err
		}
		stored = append(stored, o)
	}
	at := j.d.Now().UTC().Truncate(time.Microsecond)
	won := false
	err = db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		var err error
		if won, err = claim(ctx, tx); err != nil || !won {
			return err
		}
		q := billingdb.New(tx)
		for i, k := range kinds {
			fileID, err := storage.RegisterServerObject(ctx, tx, stored[i], at)
			if err != nil {
				return err
			}
			if err := q.FinishDocument(ctx, billingdb.FinishDocumentParams{StatementID: id, Kind: k, FileID: &fileID,
				JobID: jobID, RenderedAt: &at}); err != nil {
				return err
			}
		}
		return emit(ctx, tx, RouteDocumentsRendered, "statement", id.String(), in.tenant, map[string]any{
			"billingPartyId": in.party.String(), "statementId": id.String(), "status": in.status, "kinds": kinds}, "")
	})
	if err != nil || !won {
		j.cleanup(ctx, stored)
		if err != nil {
			j.markFailed(ctx, id, kinds, jobID, err.Error())
		}
		return err
	}
	j.d.Log.Info().Str("statement_id", id.String()).Strs("kinds", kinds).Msg("documents.render")
	return nil
}

func (j *DocumentJob) cleanup(ctx context.Context, stored []storage.StoredObject) {
	for _, o := range stored {
		if err := j.d.Files.DeleteServerObject(context.WithoutCancel(ctx), o); err != nil {
			j.d.Log.Warn().Err(err).Str("key", o.Key).Msg("documents.render: an unregistered object stays behind")
		}
	}
}

func (j *DocumentJob) markFailed(ctx context.Context, id uuid.UUID, kinds []string, jobID *uuid.UUID, cause string) {
	if len(cause) > 500 {
		cause = cause[:500]
	}
	if err := db.WithSystem(context.WithoutCancel(ctx), j.d.Pool, nil, func(tx pgx.Tx) error {
		return billingdb.New(tx).FailDocuments(ctx, billingdb.FailDocumentsParams{StatementID: id, Error: cause,
			JobID: jobID, Kinds: kinds})
	}); err != nil {
		j.d.Log.Warn().Err(err).Str("statement_id", id.String()).Msg("documents.render: could not mark the documents failed")
	}
}

// statementInput is a statement read for its documents.
type statementInput struct {
	tenant, party uuid.UUID
	status        string
	invoice       render.InvoiceInput
	detail        render.DetailInput
	paidAt        time.Time
	generatedAt   time.Time
}

// loadStatement reads a statement, its lines and its issuer in a READ ONLY system transaction, then the company's stamp
// and signature from storage (an unreadable image is left out, as the browser skipped it).
func (j *DocumentJob) loadStatement(ctx context.Context, id uuid.UUID) (statementInput, error) {
	var st billingdb.RenderStatementRow
	var lines []billingdb.RenderLinesRow
	var hubs []billingdb.DisplayHubsRow
	err := readShared(ctx, j.d.Pool, func(q *billingdb.Queries) error {
		var err error
		if st, err = q.RenderStatement(ctx, id); err != nil {
			return err
		}
		if lines, err = q.RenderLines(ctx, id); err != nil {
			return err
		}
		hubs, err = q.DisplayHubs(ctx)
		return err
	})
	if err != nil {
		return statementInput{}, err
	}
	disp := newHubDisplay(hubs)
	plan := st.BillingDateBasis == "plan"
	rows := make([]documents.Row, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, documentRow(l, disp, plan))
	}
	slices.SortStableFunc(rows, func(a, b documents.Row) int {
		x, _ := documents.AxisDate(a)
		y, _ := documents.AxisDate(b)
		return compareInstants(x, y)
	})
	in := statementInput{tenant: st.TenantID, party: st.BillingPartyID, status: st.Status, generatedAt: st.GeneratedAt}
	if st.PaidAt != nil {
		in.paidAt = *st.PaidAt
	}
	cust := render.Customer{Code: firstNonBlank(deref(st.CustomerCodeSnapshot), deref(st.CustomerCode), deref(st.TenantCode))}
	if st.PartyKind == "tenant" {
		cust.Name = firstNonBlank(deref(st.CustomerNameSnapshot), deref(st.TenantName))
		cust.Address, cust.TaxID = deref(st.TenantAddress), deref(st.TenantTaxID)
		cust.ContactName, cust.ContactPhone = deref(st.TenantContact), deref(st.TenantPhone)
	} else {
		cust.Name = firstNonBlank(deref(st.CustomerNameSnapshot), deref(st.CustomerName))
		cust.Address, cust.TaxID = deref(st.CustomerAddress), deref(st.CustomerTaxID)
		cust.BranchType, cust.BranchNumber = deref(st.CustomerBranchType), deref(st.CustomerBranchNumber)
		cust.ContactName, cust.ContactPhone = deref(st.CustomerContactName), deref(st.CustomerContactPhone)
		cust.InvoiceNote = deref(st.InvoiceNote)
		if st.PaymentTermsDays != nil {
			n := int(*st.PaymentTermsDays)
			cust.PaymentTermsDays = &n
		}
	}
	prov := render.Provider{Name: deref(st.CompanyName), ShortName: deref(st.CompanyShortName), Address: deref(st.CompanyAddress),
		TaxID: deref(st.CompanyTaxID), BankName: deref(st.BankName), AccountNumber: deref(st.AccountNumber),
		AccountName: deref(st.AccountName), SignatoryName: deref(st.SignatoryName)}
	prov.Stamp = j.image(ctx, st.StampFileID)
	prov.Signature = j.image(ctx, st.SignatureFileID)
	totals := documents.DetailTotals{GrandTotal: st.TotalAmount, WithholdingRate: st.WithholdingTaxRate,
		WithholdingTax: st.WithholdingTax, Net: st.NetAmount}
	statement := render.Statement{InvoiceNumber: st.InvoiceNumber, Year: int(st.PeriodYear), Month: int(st.PeriodMonth),
		IssuedAt: st.GeneratedAt, Totals: totals}
	in.invoice = render.InvoiceInput{Rows: rows, Customer: cust, Provider: prov, Statement: statement}
	in.detail = render.DetailInput{Rows: rows, Year: statement.Year, Month: statement.Month, CustomerName: cust.Name,
		ProviderName: prov.Name, OwnerShortName: prov.ShortName, Totals: totals, ShowActualPickup: st.ShowActualPickup}
	return in, nil
}

func compareInstants(a, b time.Time) int {
	ms := func(t time.Time) int64 {
		if t.IsZero() {
			return 0
		}
		return t.UnixMilli()
	}
	x, y := ms(a), ms(b)
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// image reads an issuer image (stamp, signature); nil when there is none or it cannot be read.
func (j *DocumentJob) image(ctx context.Context, id *uuid.UUID) *render.Image {
	if id == nil {
		return nil
	}
	body, _, err := j.d.Files.ReadFile(ctx, *id)
	if err != nil {
		j.d.Log.Warn().Err(err).Str("file_id", id.String()).Msg("documents.render: issuer image left out")
		return nil
	}
	img, err := render.Embeddable(body)
	if err != nil {
		j.d.Log.Warn().Err(err).Str("file_id", id.String()).Msg("documents.render: issuer image left out")
		return nil
	}
	return &img
}

// documentRow is a statement line as the documents read it: the stored amount, the display fields of its trip, stop or
// standby record.
func documentRow(l billingdb.RenderLinesRow, disp hubDisplay, plan bool) documents.Row {
	r := documents.Row{ID: l.ID.String(), EstimateTHB: l.AmountThb,
		DriverName: driverName(l.FullNameTh, l.FirstName, l.LastName, l.DriverEmail), DriverPhone: deref(l.DriverMobile),
		SubcontractorName: subcontractor(l.DriverTenantKind, l.DriverTenantName), VehicleClass: firstStr(l.TruckType, l.TripVehicleClass)}
	if l.ActualPickupAt != nil {
		r.ActualPickupAt = *l.ActualPickupAt
	}
	cat, _ := jobCategoryOf(l.TripJobCategory, l.TaskJobCategory)
	switch l.RowType {
	case RowStandby:
		r.Type = documents.RowStandby
		r.ID = idStr(l.StandbyID)
		if t := firstTime(l.EndedAt, l.StartedAt); t != nil {
			r.DeliveredAt = *t
		}
		src := deref(firstStr(l.SourceHubRaw, l.StartLocationRaw))
		dst := deref(firstStr(l.DestinationRaw, l.EndLocationRaw))
		r.HubDisplayName, r.OriginHubCode = disp.name(src), disp.code(src)
		d := disp.name(dst)
		r.DestinationDisplayName = &d
		r.TruckLicensePlate = deref(firstStr(l.TaskPlate, l.StandbyPlate))
		r.TripNo = deref(firstStr(l.StandbyTripNo, l.MigratedFromTripNo))
		cat, _ = jobCategoryOf(l.StandbyJobCategory, l.TaskJobCategory)
	default:
		r.TripRecordID = idStr(l.TripID)
		r.ID = r.TripRecordID
		r.TripNo = deref(l.TripNo)
		if l.DeliveredAt != nil {
			r.DeliveredAt = *l.DeliveredAt
		}
		if l.BillingDate != nil {
			r.BillingDate = *l.BillingDate
		}
		r.BillingDateBasis = documents.BasisDelivered
		if plan {
			r.BillingDateBasis = documents.BasisPlan
		}
		hub := deref(l.LookupHubCode)
		dest := deref(l.LookupDestinationCode)
		if l.RowType == RowMultidropStop && l.StopIndex != nil {
			r.Type = documents.RowMultidropStop
			r.StopIndex = int(*l.StopIndex)
			r.ID = r.TripRecordID + "_s" + strconv.Itoa(r.StopIndex)
			r.TripNo += "-s" + strconv.Itoa(r.StopIndex)
			dest = deref(l.StopDestinationCode)
		} else {
			r.Type = documents.RowTrip
		}
		r.LookupHubCode = hub
		r.LookupDestinationCode = &dest
		r.HubDisplayName = disp.name(hub)
		r.OriginHubCode = disp.code(firstNonBlank(hub, deref(l.SourceHubRaw)))
		dn := disp.name(dest)
		r.DestinationDisplayName = &dn
		r.AddTHBPerTrip = nonZero(l.AddThbPerTrip)
		if d := dateStr(l.RoundEffectiveFromDate); d != nil {
			r.RoundEffectiveFromDate = *d
		}
		r.FuelBandLowerTHB, r.FuelBandUpperTHB, r.ReferenceFuelPriceTHB = l.FuelBandLowerThb, l.FuelBandUpperThb, l.ReferenceFuelPriceThb
		r.TruckLicensePlate = deref(firstStr(l.TaskPlate, l.TripPlate))
	}
	if cat != nil {
		r.JobCategory = *cat
	}
	return r
}

type renderedBody struct {
	body        []byte
	contentType string
}

// renderKinds draws the documents of kinds.
func (j *DocumentJob) renderKinds(in statementInput, kinds []string) (map[string]renderedBody, error) {
	out := map[string]renderedBody{}
	need := func(k string) bool { return slices.Contains(kinds, k) }
	if need(KindInvoicePDF) || need(KindBundleZIP) {
		pdf, _, err := render.InvoicePDF(j.d.Fonts, in.invoice)
		if err != nil {
			return nil, fmt.Errorf("invoice PDF: %w", err)
		}
		out[KindInvoicePDF] = renderedBody{pdf, contentTypePDF}
	}
	if need(KindDetailXLSX) || need(KindBundleZIP) {
		x, err := render.DetailXLSX(in.detail)
		if err != nil {
			return nil, fmt.Errorf("detail sheet: %w", err)
		}
		out[KindDetailXLSX] = renderedBody{x, contentTypeXLSX}
	}
	if need(KindBundleZIP) {
		z, err := render.BundleZIP(out[KindInvoicePDF].body, out[KindDetailXLSX].body, in.generatedAt)
		if err != nil {
			return nil, fmt.Errorf("bundle: %w", err)
		}
		out[KindBundleZIP] = renderedBody{z, contentTypeZIP}
	}
	if need(KindReceiptPDF) {
		receipt := in.invoice
		receipt.Receipt = true
		if !in.paidAt.IsZero() {
			receipt.Statement.IssuedAt = in.paidAt
		}
		pdf, _, err := render.InvoicePDF(j.d.Fonts, receipt)
		if err != nil {
			return nil, fmt.Errorf("receipt PDF: %w", err)
		}
		out[KindReceiptPDF] = renderedBody{pdf, contentTypePDF}
	}
	return out, nil
}
