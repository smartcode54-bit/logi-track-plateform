//go:build integration

// Integration tests of T39 (issue #59): the documents of a statement (documents.render: statement.created,
// statement.status_changed, job.documents.regenerate) and their download, and the Shopee Express report, through the
// real API and the worker consumer driven with the outbox messages it would receive.
package billing_test

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/xuri/excelize/v2"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/render"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

func (w *world) docJob(at time.Time) *billing.DocumentJob {
	w.t.Helper()
	f, err := render.LoadFonts("")
	if err != nil {
		w.t.Fatal(err)
	}
	j, err := billing.NewDocumentJob(billing.DocumentJobDeps{Pool: w.pool, Files: w.files, Fonts: f,
		Locks: jobs.NewRedisLocker(w.rdb, w.ks), Now: func() time.Time { return at }, Log: zerolog.Nop()})
	if err != nil {
		w.t.Fatal(err)
	}
	return j
}

// event delivers the newest outbox event routingKey of a statement to the consumer, as the relay and RabbitMQ would.
func (w *world) event(j *billing.DocumentJob, routingKey, statementID string) *mq.Delivery {
	w.t.Helper()
	var id int64
	var body []byte
	if err := w.etl.QueryRow(context.Background(), `SELECT id, payload::text::bytea FROM outbox_events
		WHERE routing_key = $1 AND aggregate_id = $2 ORDER BY id DESC LIMIT 1`, routingKey, statementID).Scan(&id, &body); err != nil {
		w.t.Fatalf("%s of %s: %v", routingKey, statementID, err)
	}
	d := &mq.Delivery{Queue: billing.QueueDocuments, MessageID: strconv.FormatInt(id, 10), RoutingKey: routingKey, Body: body}
	if err := j.Registration().Handler(context.Background(), d); err != nil {
		w.t.Fatalf("%s: %v", routingKey, err)
	}
	return d
}

// command delivers the lt.jobs command of a job to the consumer.
func (w *world) command(j *billing.DocumentJob, routingKey, jobID string) error {
	w.t.Helper()
	body := scan[[]byte](w, `SELECT payload::text::bytea FROM outbox_events WHERE exchange = 'lt.jobs'
		AND routing_key = $1 AND aggregate_id = $2`, routingKey, jobID)
	return j.Registration().Handler(context.Background(), &mq.Delivery{Queue: billing.QueueDocuments, RoutingKey: routingKey, Body: body})
}

// noRedirect sends a request without following a redirect (the 302 points at a signed media URL).
func (w *world) noRedirect(method, path, bearer string) (*http.Response, []byte) {
	w.t.Helper()
	req, err := http.NewRequest(method, w.internal+path, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

// download follows the 302 of a document route to the stored object: its key, the signed disposition and the bytes.
func (w *world) download(path, bearer string) (key, disposition string, body []byte) {
	w.t.Helper()
	res, raw := w.noRedirect(http.MethodGet, path, bearer)
	if res.StatusCode != http.StatusFound {
		w.t.Fatalf("GET %s: %d %s", path, res.StatusCode, raw)
	}
	u, err := url.Parse(res.Header.Get("Location"))
	if err != nil || u.Host != "media.test" || !strings.HasPrefix(u.Path, "/media/") || u.Query().Get(storage.QuerySignature) == "" {
		w.t.Fatalf("Location %q", res.Header.Get("Location"))
	}
	key = strings.TrimPrefix(u.Path, "/media/")
	var fileID string
	if err := w.etl.QueryRow(context.Background(), `SELECT id::text FROM file_objects WHERE object_key = $1 AND status = 'committed'`,
		key).Scan(&fileID); err != nil {
		w.t.Fatalf("%s: %v", key, err)
	}
	body, _, err = w.files.ReadFile(context.Background(), uuid.MustParse(fileID))
	if err != nil {
		w.t.Fatal(err)
	}
	return key, u.Query().Get(storage.QueryDisposition), body
}

func documentStatuses(r resp) map[string]string {
	out := map[string]string{}
	docs, _ := r.data()["documents"].([]any)
	for _, d := range docs {
		m := d.(map[string]any)
		out[str(m["kind"])] = str(m["status"])
	}
	return out
}

// stamp gives the owner company a PNG stamp stored on the local backend.
func (w *world) stamp() {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 60, 60))); err != nil {
		w.t.Fatal(err)
	}
	key := "companies/" + w.companyO + "/stamp-1.png"
	be, _ := w.files.Backend(storage.BackendLocal)
	if err := be.Put(context.Background(), storage.Object{Bucket: "logitrack", Key: key}, bytes.NewReader(b.Bytes()),
		int64(b.Len()), "image/png"); err != nil {
		w.t.Fatal(err)
	}
	id := w.id(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, owner_kind, owner_id, content_type, size_bytes,
		status, committed_at, storage_backend) VALUES ('logitrack', $1, $2, 'company_stamp', 'company', $3, 'image/png', $4,
		'committed', now(), 'local') RETURNING id::text`, key, w.O, w.companyO, b.Len())
	w.exec(`UPDATE companies SET stamp_file_id = $1, short_name = 'WRT', bank_name = 'กสิกรไทย', account_number = '123-4',
		account_name = 'บริษัท O' WHERE id = $2`, id, w.companyO)
}

// AC: re-download serves the stored document; the receipt is rendered only on mark-as-paid; the WHT printed is the
// statement's.
func TestStatementDocuments(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	w.hub("SPK-GW", "เจแอนด์ที บางปู", "J&T EXPRESS บางปู")
	w.stamp()
	drv := w.driver(w.A, "สมชาย ใจดี", "0812345678")
	for i, amount := range []float64{1493.43, 1453.43} {
		task := w.task(taskSpec{tenant: w.O, party: w.partyX, src: "SPK-GW", dest: "SPK890103", class: "6WH", plate: "70-1111",
			cat: "PRIMARY", driver: drv, plan: bkk(2026, 10, 3+i, 8, 0)})
		id := w.tripOf(w.O, w.partyX, "SPX-"+strconv.Itoa(i+1), task, drv, bkk(2026, 10, 4+i, 9, 0), ptr(bkk(2026, 10, 3+i, 8, 0)),
			"PRIMARY", false)
		w.snapshot(id, w.O, ptr(amount), false)
		w.priced(id, "SPK-GW", "SPK890103", []string{"2026-10-01", "2026-10-02"}[i], -40, false)
	}
	m := w.trip(w.O, w.partyX, "MD-1", bkk(2026, 10, 7, 9, 0), ptr(bkk(2026, 10, 6, 8, 0)), ptr("PRIMARY"))
	w.snapshot(m, w.O, ptr(1350.0), true)
	w.stop(m, 1, "SPK890103", 1200)
	w.stop(m, 2, "SPK777", 150)
	st := w.statement("ta.o", map[string]any{"customerId": w.partyX, "year": 2026, "month": 10})
	expect(t, st, http.StatusCreated, "")
	id := str(st.data()["id"])
	// 1493.43 + 1453.43 + 1200 + 150 = 4296.86; WHT 3 % = 128.91 (Round2(128.9058)); net 4167.95
	if num(st.data()["totalAmount"]) != 4296.86 || num(st.data()["withholdingTax"]) != 128.91 || num(st.data()["netAmount"]) != 4167.95 {
		t.Fatalf("statement %s", st.raw)
	}
	docPath := "/v1/billing/statements/" + id + "/documents/"
	// Before the render: 409 with the status, and the detail lists nothing yet.
	pending := w.get(docPath+"invoice_summary_pdf", tok)
	expect(t, pending, http.StatusConflict, "failed_precondition")
	if pending.detail("status") != "pending" {
		t.Fatalf("%s", pending.raw)
	}
	expect(t, w.get(docPath+"nonsense", tok), http.StatusNotFound, "not_found")

	job := w.docJob(bkk(2026, 11, 1, 10, 0))
	created := w.event(job, billing.RouteStatementCreated, id)
	detail := w.get("/v1/billing/statements/"+id, tok)
	expect(t, detail, http.StatusOK, "")
	if got := documentStatuses(detail); len(got) != 3 || got["invoice_summary_pdf"] != "ready" || got["invoice_detail_xlsx"] != "ready" ||
		got["bundle_zip"] != "ready" {
		t.Fatalf("documents after statement.created %v (no receipt before paid)", got)
	}
	for _, d := range detail.data()["documents"].([]any) {
		m := d.(map[string]any)
		if str(m["url"]) != "/v1/billing/statements/"+id+"/documents/"+str(m["kind"]) || m["renderedAt"] == nil {
			t.Fatalf("document %v", m)
		}
	}
	if n := scan[int](w, `SELECT count(*) FROM outbox_events WHERE routing_key = 'documents.rendered' AND aggregate_id = $1`, id); n != 1 {
		t.Fatalf("documents.rendered events %d", n)
	}

	// The stored files: keys under documents/statements/{id}/{renderId}/, the PDF, the XLSX with the statement's totals and
	// withholding (3 %, its stored rate), the bundle with both and no receipt.
	pdfKey, disp, pdf := w.download(docPath+"invoice_summary_pdf", tok)
	if !strings.HasPrefix(pdfKey, "documents/statements/"+id+"/") || !strings.HasSuffix(pdfKey, "/invoice_summary.pdf") ||
		!bytes.HasPrefix(pdf, []byte("%PDF-")) || disp != `attachment; filename="CJSF-202610-001_invoice_summary.pdf"` {
		t.Fatalf("pdf %s %q", pdfKey, disp)
	}
	_, _, xlsx := w.download(docPath+"invoice_detail_xlsx", tok)
	f, err := excelize.OpenReader(bytes.NewReader(xlsx))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := f.GetRows("รายละเอียดขนส่ง ตุลาคม 2569")
	foot := rows[len(rows)-3:]
	// No วันรับงานจริง column: ค่าขนส่ง/เที่ยว is column 11 and ยอดรวม column 12.
	if foot[0][12] != "4,296.86" || foot[1][11] != "ภาษีหัก ณ ที่จ่าย 3%" || foot[1][12] != "-128.91" || foot[2][12] != "4,167.95" {
		t.Fatalf("detail footer %q", foot)
	}
	if rows[4][1] != "วันที่ตามแผน" || rows[5][2] != "SPX-1" || rows[5][1] != "03/10/2026" || rows[5][3] != "SPK-GW → SPK890103" ||
		rows[5][6] != "สมชาย ใจดี" || rows[5][7] != "A" || rows[7][2] != "MD-1" || rows[7][10] != "150.00" || rows[7][12] != "1,350.00" ||
		rows[7][7] != "WRT" {
		t.Fatalf("detail rows %q", rows[4:])
	}
	_ = f.Close()
	_, zdisp, bundle := w.download(docPath+"bundle_zip", tok)
	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range zr.File {
		names = append(names, e.Name)
		if e.Name == "invoice_summary.pdf" {
			rc, _ := e.Open()
			inner, _ := io.ReadAll(rc)
			_ = rc.Close()
			if !bytes.Equal(inner, pdf) {
				t.Fatal("the bundle's PDF is not the stored invoice")
			}
		}
	}
	if !slices.Equal(names, []string{"invoice_summary.pdf", "invoice_detail.xlsx"}) || zdisp != `attachment; filename="invoice_CJSF_202610.zip"` {
		t.Fatalf("bundle %q %q", names, zdisp)
	}
	// Re-download serves the stored file; a duplicated message renders nothing new.
	again, _, _ := w.download(docPath+"invoice_summary_pdf", tok)
	if again != pdfKey {
		t.Fatalf("re-download %s, first %s", again, pdfKey)
	}
	if err := job.Registration().Handler(context.Background(), created); err != nil {
		t.Fatal(err)
	}
	if n := scan[int](w, `SELECT count(*) FROM file_objects WHERE purpose = 'statement_document' AND owner_id = $1`, id); n != 3 {
		t.Fatalf("%d document files after a duplicate delivery", n)
	}

	// The receipt: refused before paid, sent renders nothing, paid renders it (never into the bundle).
	notPaid := w.get(docPath+"receipt_pdf", tok)
	expect(t, notPaid, http.StatusConflict, "failed_precondition")
	if notPaid.detail("reason") != "statement_not_paid" {
		t.Fatalf("%s", notPaid.raw)
	}
	expect(t, w.post("/v1/billing/statements/"+id+"/status", tok, map[string]any{"status": "sent"}), http.StatusOK, "")
	w.event(job, billing.RouteStatementStatusChanged, id)
	if got := documentStatuses(w.get("/v1/billing/statements/"+id, tok)); len(got) != 3 {
		t.Fatalf("sent rendered %v", got)
	}
	expect(t, w.post("/v1/billing/statements/"+id+"/status", tok, map[string]any{"status": "paid"}), http.StatusOK, "")
	w.event(job, billing.RouteStatementStatusChanged, id)
	if got := documentStatuses(w.get("/v1/billing/statements/"+id, tok)); got["receipt_pdf"] != "ready" || len(got) != 4 {
		t.Fatalf("paid rendered %v", got)
	}
	_, rdisp, receipt := w.download(docPath+"receipt_pdf", tok)
	if !bytes.HasPrefix(receipt, []byte("%PDF-")) || rdisp != `attachment; filename="receipt_CJSF_202610.pdf"` || bytes.Equal(receipt, pdf) {
		t.Fatalf("receipt %q", rdisp)
	}

	// Regenerate: a job (one at a time) that renders every document again, the receipt too since the statement is paid;
	// each document points at a new file and the old objects stay.
	expect(t, w.post("/v1/billing/statements/"+id+"/documents/regenerate", w.login("ta.a"), nil), http.StatusForbidden, "permission_denied")
	reg := w.post("/v1/billing/statements/"+id+"/documents/regenerate", tok, nil)
	expect(t, reg, http.StatusAccepted, "")
	jobID := str(reg.data()["jobId"])
	held := w.post("/v1/billing/statements/"+id+"/documents/regenerate", tok, nil)
	expect(t, held, http.StatusConflict, "already_exists")
	if held.detail("jobId") != jobID {
		t.Fatalf("%s", held.raw)
	}
	if err := w.command(w.docJob(bkk(2026, 11, 2, 10, 0)), billing.RouteRegenerateJob, jobID); err != nil {
		t.Fatal(err)
	}
	j := w.get("/v1/jobs/"+jobID, tok)
	if str(j.data()["status"]) != jobs.StatusSucceeded {
		t.Fatalf("regenerate job %s", j.raw)
	}
	newKey, _, newPDF := w.download(docPath+"invoice_summary_pdf", tok)
	if newKey == pdfKey || !bytes.HasPrefix(newPDF, []byte("%PDF-")) {
		t.Fatalf("regenerated key %s (was %s)", newKey, pdfKey)
	}
	if n := scan[int](w, `SELECT count(*) FROM file_objects WHERE purpose = 'statement_document' AND owner_id = $1`, id); n != 8 {
		t.Fatalf("%d document files after a regenerate, want 4 old + 4 new", n)
	}
	// The lock is released with the job: another regenerate may start.
	expect(t, w.post("/v1/billing/statements/"+id+"/documents/regenerate", tok, nil), http.StatusAccepted, "")

	// A legacy statement (no stored lines) cannot be rendered.
	legacy := w.id(`INSERT INTO billing_statements (tenant_id, billing_party_id, invoice_number, period_year, period_month,
		total_amount, withholding_tax_rate, withholding_tax, net_amount, status, generated_at, legacy_doc_id)
		VALUES ($1, $2, 'CJSF-202601-001', 2026, 1, 100, 0.01, 1, 99, 'paid', now(), 'legacy-1') RETURNING id::text`, w.O, w.partyX)
	noLines := w.post("/v1/billing/statements/"+legacy+"/documents/regenerate", tok, nil)
	expect(t, noLines, http.StatusConflict, "failed_precondition")
	if noLines.detail("reason") != "no_lines" {
		t.Fatalf("%s", noLines.raw)
	}

	// Another tenant's staff cannot read the statement or its documents.
	r := w.get(docPath+"invoice_summary_pdf", w.login("ta.c"))
	if r.status != http.StatusNotFound && r.status != http.StatusForbidden {
		t.Fatalf("carrier C reads a document: %d %s", r.status, r.raw)
	}

	// A draft deleted before its render ends quietly.
	t2 := w.trip(w.O, w.partyY, "Y-1", bkk(2026, 10, 5, 10, 0), nil, ptr("PRIMARY"))
	w.snapshot(t2, w.O, ptr(100.0), false)
	draft := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, draft, http.StatusCreated, "")
	draftID := str(draft.data()["id"])
	if d := w.del("/v1/billing/statements/"+draftID, tok); d.status != http.StatusNoContent {
		t.Fatalf("delete %d", d.status)
	}
	w.event(job, billing.RouteStatementCreated, draftID)
	if n := scan[int](w, `SELECT count(*) FROM file_objects WHERE owner_id = $1`, draftID); n != 0 {
		t.Fatalf("a deleted draft rendered %d files", n)
	}
}

// A render that cannot succeed marks its documents failed (the download answers the reason) and acks; a storage failure
// returns to the retry ladder; a regenerate job ends failed.
func TestStatementDocumentFailures(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	t1 := w.trip(w.O, w.partyY, "Y-1", bkk(2026, 10, 5, 10, 0), nil, ptr("PRIMARY"))
	w.snapshot(t1, w.O, ptr(100.0), false)
	st := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, st, http.StatusCreated, "")
	id := str(st.data()["id"])
	docPath := "/v1/billing/statements/" + id + "/documents/"
	body := scan[[]byte](w, `SELECT payload::text::bytea FROM outbox_events WHERE routing_key = 'statement.created' AND aggregate_id = $1`, id)

	// Regression (T39 review): a render that cannot succeed (fonts that do not parse) is permanent on the event path
	// too: the kinds are failed with the cause and the message acked, not retried up the ladder into the dead letter.
	bad, err := billing.NewDocumentJob(billing.DocumentJobDeps{Pool: w.pool, Files: w.files,
		Fonts: render.Fonts{Regular: []byte("not a font"), Bold: []byte("not a font")}, Locks: jobs.NewRedisLocker(w.rdb, w.ks),
		Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	created := &mq.Delivery{Queue: billing.QueueDocuments, MessageID: "9000", RoutingKey: billing.RouteStatementCreated, Body: body}
	if err := bad.Handle(context.Background(), created); err == nil || !mq.IsPermanent(err) {
		t.Fatalf("a render that cannot succeed: %v, want a permanent error", err)
	}
	r := w.get(docPath+"invoice_summary_pdf", tok)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("status") != "failed" || !strings.Contains(str(r.detail("error")), "invoice PDF") {
		t.Fatalf("%s", r.raw)
	}

	broken := &failingFiles{Service: w.files, fail: true}
	f, _ := render.LoadFonts("")
	j, err := billing.NewDocumentJob(billing.DocumentJobDeps{Pool: w.pool, Files: broken, Fonts: f,
		Locks: jobs.NewRedisLocker(w.rdb, w.ks), Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	d := &mq.Delivery{Queue: billing.QueueDocuments, MessageID: "9001", RoutingKey: billing.RouteStatementCreated, Body: body}
	if err := j.Handle(context.Background(), d); err == nil || mq.IsPermanent(err) {
		t.Fatalf("a storage failure: %v; the retry ladder must run it again", err)
	}
	r = w.get(docPath+"bundle_zip", tok)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("status") != "failed" || !strings.Contains(str(r.detail("error")), "storage") {
		t.Fatalf("%s", r.raw)
	}
	// The retry succeeds once storage is back.
	broken.fail = false
	if err := j.Handle(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if got := documentStatuses(w.get("/v1/billing/statements/"+id, tok)); got["bundle_zip"] != "ready" {
		t.Fatalf("after the retry %v", got)
	}

	// The receipt of the statement once paid, the same way: acked as permanent, failed with its cause.
	expect(t, w.post("/v1/billing/statements/"+id+"/status", tok, map[string]any{"status": "sent"}), http.StatusOK, "")
	expect(t, w.post("/v1/billing/statements/"+id+"/status", tok, map[string]any{"status": "paid"}), http.StatusOK, "")
	paid := scan[[]byte](w, `SELECT payload::text::bytea FROM outbox_events WHERE routing_key = 'statement.status_changed'
		AND aggregate_id = $1 ORDER BY id DESC LIMIT 1`, id)
	err = bad.Handle(context.Background(), &mq.Delivery{Queue: billing.QueueDocuments, MessageID: "9002",
		RoutingKey: billing.RouteStatementStatusChanged, Body: paid})
	if err == nil || !mq.IsPermanent(err) {
		t.Fatalf("a receipt that cannot be drawn: %v, want a permanent error", err)
	}
	r = w.get(docPath+"receipt_pdf", tok)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("status") != "failed" || !strings.Contains(str(r.detail("error")), "receipt PDF") {
		t.Fatalf("%s", r.raw)
	}
	if got := documentStatuses(w.get("/v1/billing/statements/"+id, tok)); got["bundle_zip"] != "ready" {
		t.Fatalf("a failed receipt touched the ready documents: %v", got)
	}
	if _, err := billing.NewDocumentJob(billing.DocumentJobDeps{Pool: w.pool, Files: w.files}); err == nil {
		t.Fatal("a consumer without fonts was built")
	}
}

// detailSheet downloads a statement's stored invoice_detail.xlsx and returns the rows of its sheet.
func (w *world) detailSheet(statementID, bearer, sheet string) [][]string {
	w.t.Helper()
	_, _, body := w.download("/v1/billing/statements/"+statementID+"/documents/invoice_detail_xlsx", bearer)
	f, err := excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows, err := f.GetRows(sheet)
	if err != nil {
		w.t.Fatal(err)
	}
	return rows
}

// Regression (T39 review): the detail sheet's optional วันรับงานจริง column (the Billing Document page's toggle,
// ADR 0028) is chosen when the statement is created, stored with it, and changed by a regenerate that names it; a
// regenerate without it keeps the stored choice.
func TestStatementDetailActualPickup(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	w.hub("SPK-GW", "เจแอนด์ที บางปู", "J&T EXPRESS บางปู")
	drv := w.driver(w.O, "สมชาย ใจดี", "0812345678")
	for party, no := range map[string]string{w.partyX: "X-1", w.partyY: "Y-1"} {
		task := w.task(taskSpec{tenant: w.O, party: party, src: "SPK-GW", dest: "SPK890103", class: "6WH", plate: "70-1111",
			cat: "PRIMARY", driver: drv, plan: bkk(2026, 10, 3, 8, 0)})
		w.exec(`UPDATE tasks SET actual_pickup_at = $2 WHERE id = $1`, task, bkk(2026, 10, 3, 9, 15))
		trip := w.tripOf(w.O, party, no, task, drv, bkk(2026, 10, 4, 9, 0), ptr(bkk(2026, 10, 3, 8, 0)), "PRIMARY", false)
		w.snapshot(trip, w.O, ptr(1000.0), false)
		w.priced(trip, "SPK-GW", "SPK890103", "", 0, false)
	}
	bad := w.post("/v1/billing/statements", tok, map[string]any{"customerId": w.partyX, "year": 2026, "month": 10,
		"showActualPickup": "yes"})
	if bad.status != http.StatusBadRequest {
		t.Fatalf("a non-boolean showActualPickup: %d %s", bad.status, bad.raw)
	}
	with := w.statement("ta.o", map[string]any{"customerId": w.partyX, "year": 2026, "month": 10, "showActualPickup": true})
	expect(t, with, http.StatusCreated, "")
	without := w.statement("ta.o", map[string]any{"customerId": w.partyY, "year": 2026, "month": 10})
	expect(t, without, http.StatusCreated, "")
	withID, withoutID := str(with.data()["id"]), str(without.data()["id"])
	if with.data()["showActualPickup"] != true || without.data()["showActualPickup"] != false {
		t.Fatalf("stored choice: %v / %v", with.data()["showActualPickup"], without.data()["showActualPickup"])
	}
	job := w.docJob(bkk(2026, 11, 1, 10, 0))
	w.event(job, billing.RouteStatementCreated, withID)
	w.event(job, billing.RouteStatementCreated, withoutID)
	sheet := "รายละเอียดขนส่ง ตุลาคม 2569"
	// header row 5 (index 4); the column follows the date column. CJSF (X) is plan basis, TTP (Y) delivered.
	if rows := w.detailSheet(withID, tok, sheet); rows[4][1] != "วันที่ตามแผน" || rows[4][2] != "วันรับงานจริง" ||
		rows[4][3] != "เลขใบงาน" || rows[5][2] != "03/10/2026 09:15" || rows[5][3] != "X-1" {
		t.Fatalf("detail with the column %q", rows[4:6])
	}
	if rows := w.detailSheet(withoutID, tok, sheet); rows[4][1] != "วันที่" || rows[4][2] != "เลขใบงาน" || rows[5][2] != "Y-1" {
		t.Fatalf("detail without the column %q", rows[4:6])
	}

	// regenerate posts a regenerate with body and runs its job.
	regenerate := func(id string, body any) {
		t.Helper()
		r := w.post("/v1/billing/statements/"+id+"/documents/regenerate", tok, body)
		expect(t, r, http.StatusAccepted, "")
		if err := w.command(w.docJob(bkk(2026, 11, 2, 10, 0)), billing.RouteRegenerateJob, str(r.data()["jobId"])); err != nil {
			t.Fatal(err)
		}
		if j := w.get("/v1/jobs/"+str(r.data()["jobId"]), tok); str(j.data()["status"]) != jobs.StatusSucceeded {
			t.Fatalf("regenerate %s", j.raw)
		}
	}
	stored := func(id string) any { return w.get("/v1/billing/statements/"+id, tok).data()["showActualPickup"] }
	if r := w.post("/v1/billing/statements/"+withID+"/documents/regenerate", tok, map[string]any{"showActualPickup": 1}); r.status != http.StatusBadRequest {
		t.Fatalf("a non-boolean showActualPickup on regenerate: %d %s", r.status, r.raw)
	}
	// Off: the column leaves the sheet and the statement stores the new choice with the documents.
	regenerate(withID, map[string]any{"showActualPickup": false})
	if rows := w.detailSheet(withID, tok, sheet); rows[4][2] != "เลขใบงาน" || rows[5][2] != "X-1" || stored(withID) != false {
		t.Fatalf("after turning it off %q, stored %v", rows[4], stored(withID))
	}
	// On for the other statement, then a regenerate without a body keeps it.
	regenerate(withoutID, map[string]any{"showActualPickup": true})
	regenerate(withoutID, nil)
	if rows := w.detailSheet(withoutID, tok, sheet); rows[4][2] != "วันรับงานจริง" || rows[5][2] != "03/10/2026 09:15" ||
		stored(withoutID) != true {
		t.Fatalf("after turning it on %q, stored %v", rows[4:6], stored(withoutID))
	}
}

// failingFiles is the storage service with a put that fails until fail is cleared.
type failingFiles struct {
	*storage.Service
	fail bool
}

func (f *failingFiles) PutServerObject(ctx context.Context, o storage.ServerObject) (storage.StoredObject, error) {
	if f.fail || f.Service == nil {
		return storage.StoredObject{}, io.ErrUnexpectedEOF
	}
	return f.Service.PutServerObject(ctx, o)
}

// photo stores a JPEG run-sheet of a trip on the local backend.
func (w *world) photo(trip, photoType string) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 400))
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		w.t.Fatal(err)
	}
	key := "trips/" + trip + "/" + photoType + ".jpg"
	be, _ := w.files.Backend(storage.BackendLocal)
	if err := be.Put(context.Background(), storage.Object{Bucket: "logitrack", Key: key}, bytes.NewReader(b.Bytes()),
		int64(b.Len()), "image/jpeg"); err != nil {
		w.t.Fatal(err)
	}
	file := w.id(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, owner_kind, owner_id, content_type,
		size_bytes, status, committed_at, storage_backend) VALUES ('logitrack', $1, $2, 'trip_photo', 'trip', $3,
		'image/jpeg', $4, 'committed', now(), 'local') RETURNING id::text`, key, w.O, trip, b.Len())
	w.exec(`INSERT INTO trip_photos (trip_id, photo_type, file_id) VALUES ($1, $2, $3)`, trip, photoType, file)
}

// The Shopee Express report: the rows filtered on the server (the customer resolves per trip, un-billed trips count),
// and the PDF rendered once per rows version, a digest of what it prints (302 to it while current, else a new render),
// one render at a time per reach.
func TestShopeeReport(t *testing.T) {
	w := newWorld(t)
	tok := w.login("ta.o")
	w.hub("SPK-GW", "เจแอนด์ที บางปู", "")
	drv := w.driver(w.O, "สมชาย ใจดี", "081")
	billed := w.trip(w.O, w.partyY, "TTP-1", bkk(2026, 9, 3, 10, 0), nil, ptr("PRIMARY"))
	w.snapshot(billed, w.O, ptr(900.0), false)
	w.priced(billed, "SPK-GW", "SPK890103", "", 0, false)
	w.exec(`UPDATE trip_records SET driver_id = $2, parcel_count = 12 WHERE id = $1`, billed, drv)
	w.photo(billed, "runsheet_received")
	w.photo(billed, "stop_2_runsheet_received")
	w.photo(billed, "seal") // not a signed run-sheet
	linked := w.task(taskSpec{tenant: w.O, srcParty: w.partyY, src: "SPK-GW", dest: "SPK777", class: "6WH"})
	unbilled := w.tripOf(w.O, "", "TTP-2", linked, "", bkk(2026, 9, 20, 10, 0), nil, "", false) // second half
	w.trip(w.O, w.partyX, "OTHER", bkk(2026, 9, 4, 10, 0), nil, ptr("PRIMARY"))                 // another customer
	w.trip(w.C, w.partyY, "HIDDEN", bkk(2026, 9, 5, 10, 0), nil, ptr("PRIMARY"))                // outside O's reach

	q := url.Values{"customerId": {w.customerY}, "year": {"2026"}, "month": {"9"}}
	r := w.get("/v1/billing/shopee-report?"+q.Encode(), tok)
	expect(t, r, http.StatusOK, "")
	trips, _ := r.data()["trips"].([]any)
	if len(trips) != 2 || num(r.data()["photoCount"]) != 2 || num(r.data()["missingPhotoCount"]) != 1 || num(r.data()["driverCount"]) != 2 {
		t.Fatalf("report %s", r.raw)
	}
	first := trips[0].(map[string]any)
	if str(first["tripNo"]) != "TTP-1" || str(first["originDisplay"]) != "เจแอนด์ที บางปู" || str(first["destinationDisplay"]) != "SPK890103" ||
		str(first["driverName"]) != "สมชาย ใจดี" || num(first["parcelCount"]) != 12 || len(first["signedRunsheetPhotos"].([]any)) != 2 {
		t.Fatalf("first trip %v", first)
	}
	if _, has := first["estimateThb"]; has {
		t.Fatal("the report carries a price")
	}
	q.Set("half", "first")
	if r := w.get("/v1/billing/shopee-report?"+q.Encode(), tok); len(r.data()["trips"].([]any)) != 1 {
		t.Fatalf("first half %s", r.raw)
	}
	q.Set("half", "second")
	if r := w.get("/v1/billing/shopee-report?"+q.Encode(), tok); len(r.data()["trips"].([]any)) != 1 ||
		str(r.data()["trips"].([]any)[0].(map[string]any)["tripNo"]) != "TTP-2" {
		t.Fatalf("second half %s", r.raw)
	}
	q.Set("half", "bogus")
	if r := w.get("/v1/billing/shopee-report?"+q.Encode(), tok); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("bad half %d", r.status)
	}
	q.Del("half")

	// The PDF: no render yet -> a job (one per customer, month and half); the worker renders it; then 302.
	pdfPath := "/v1/billing/shopee-report.pdf?" + q.Encode()
	start := w.get(pdfPath, tok)
	expect(t, start, http.StatusAccepted, "")
	jobID := str(start.data()["jobId"])
	if again := w.get(pdfPath, tok); str(again.data()["jobId"]) != jobID {
		t.Fatalf("a running render answers its own job: %s", again.raw)
	}
	if err := w.command(w.docJob(bkk(2026, 10, 1, 9, 0)), billing.RouteShopeeReportJob, jobID); err != nil {
		t.Fatal(err)
	}
	j := w.get("/v1/jobs/"+jobID, tok)
	res, _ := j.data()["result"].(map[string]any)
	if str(j.data()["status"]) != jobs.StatusSucceeded || num(res["tripCount"]) != 2 || num(res["photoCount"]) != 2 ||
		str(res["fileName"]) != "shopee_express_report_TTP_202609.pdf" {
		t.Fatalf("job %s", j.raw)
	}
	key, disp, pdf := w.download(pdfPath, tok)
	if !strings.HasPrefix(key, "documents/reports/"+jobID+"/") || !bytes.HasPrefix(pdf, []byte("%PDF-")) ||
		disp != `attachment; filename="shopee_express_report_TTP_202609.pdf"` || bytes.Count(pdf, []byte("/Subtype /Image")) < 1 {
		t.Fatalf("pdf %s %q", key, disp)
	}
	// A write that changes nothing the PDF prints keeps the render current.
	w.exec(`UPDATE trip_records SET parcel_count = parcel_count WHERE id = $1`, unbilled)
	if k, _, _ := w.download(pdfPath, tok); k != key {
		t.Fatalf("a no-op write re-rendered: %s, was %s", k, key)
	}

	// Regression (T39 review): the rows version was the trip count and the latest trip_records / trip_photos
	// updated_at, so a correction elsewhere the PDF reads kept redirecting to the old render. Each correction below
	// must make it stale: a new job, whose render the route then redirects to.
	worker := w.docJob(bkk(2026, 10, 1, 9, 0))
	rerender := func(what string) {
		t.Helper()
		r := w.get(pdfPath, tok)
		if r.status != http.StatusAccepted || str(r.data()["jobId"]) == "" || str(r.data()["jobId"]) == jobID {
			t.Fatalf("after %s: %d %s (the stale render was reused)", what, r.status, r.raw)
		}
		jobID = str(r.data()["jobId"])
		if err := w.command(worker, billing.RouteShopeeReportJob, jobID); err != nil {
			t.Fatal(err)
		}
		k, _, _ := w.download(pdfPath, tok)
		if k == key || !strings.HasPrefix(k, "documents/reports/"+jobID+"/") {
			t.Fatalf("after %s: %s, was %s", what, k, key)
		}
		key = k
	}
	w.exec(`UPDATE trip_records SET parcel_count = 13 WHERE id = $1`, billed)
	rerender("a parcel count")
	w.exec(`UPDATE drivers SET full_name_th = 'สมชาย ใจงาม' WHERE id = $1`, drv)
	rerender("a driver's name")
	w.exec(`UPDATE hubs SET name_th = 'เจแอนด์ที บางปู 2' WHERE source_id = 'SPK-GW'`)
	rerender("a hub's label")
	w.exec(`UPDATE tasks SET license_plate_snapshot = '70-9999', truck_type = '10WH' WHERE id = $1`, linked)
	rerender("the task's plate and class")
	// A run-sheet removed from the older trip while the newer trip changed last (the maximum updated_at stays).
	w.exec(`UPDATE trip_records SET parcel_count = parcel_count WHERE id = $1`, unbilled)
	w.exec(`DELETE FROM trip_photos WHERE trip_id = $1 AND photo_type = 'stop_2_runsheet_received'`, billed)
	rerender("a run-sheet removed")
	r = w.get("/v1/billing/shopee-report?"+q.Encode(), tok)
	trips = r.data()["trips"].([]any)
	first, second := trips[0].(map[string]any), trips[1].(map[string]any)
	if str(first["driverName"]) != "สมชาย ใจงาม" || str(first["originDisplay"]) != "เจแอนด์ที บางปู 2" ||
		len(first["signedRunsheetPhotos"].([]any)) != 1 || str(second["truckLicensePlate"]) != "70-9999" ||
		str(second["vehicleClass"]) != "10WH" {
		t.Fatalf("report after the corrections %s", r.raw)
	}
	if res := w.get("/v1/jobs/"+jobID, tok).data()["result"].(map[string]any); num(res["photoCount"]) != 1 {
		t.Fatalf("the last render %v", res)
	}

	// Regression (T39 review): the render lock is per reach. While the own fleet's render runs, carrier C (another
	// reach: its own trip of the customer) starts its own render, a job it can read, instead of being handed O's.
	w.exec(`UPDATE trip_records SET parcel_count = 14 WHERE id = $1`, billed)
	running := w.get(pdfPath, tok)
	expect(t, running, http.StatusAccepted, "")
	tokC := w.login("ta.c")
	carrier := w.get(pdfPath, tokC)
	expect(t, carrier, http.StatusAccepted, "")
	jobC := str(carrier.data()["jobId"])
	if jobC == "" || jobC == str(running.data()["jobId"]) {
		t.Fatalf("carrier C was handed the own fleet's render: %s", carrier.raw)
	}
	expect(t, w.get("/v1/jobs/"+jobC, tokC), http.StatusOK, "")
	if again := w.get(pdfPath, tokC); str(again.data()["jobId"]) != jobC {
		t.Fatalf("C's running render answers its own job: %s", again.raw)
	}
	if again := w.get(pdfPath, tok); str(again.data()["jobId"]) != str(running.data()["jobId"]) {
		t.Fatalf("O's running render answers its own job: %s", again.raw)
	}
	if err := w.command(worker, billing.RouteShopeeReportJob, jobC); err != nil {
		t.Fatal(err)
	}
	if res := w.get("/v1/jobs/"+jobC, tokC).data()["result"].(map[string]any); num(res["tripCount"]) != 1 {
		t.Fatalf("C's render %v (its own trip only)", res)
	}
	if k, _, _ := w.download(pdfPath, tokC); !strings.HasPrefix(k, "documents/reports/"+jobC+"/") {
		t.Fatalf("C's PDF %s", k)
	}
	// A plain user holds no accounting:shopee_report.
	expect(t, w.get("/v1/billing/shopee-report.pdf?customerId="+w.customerY+"&year=2026&month=9", w.login("us.o")),
		http.StatusForbidden, "permission_denied")
}
