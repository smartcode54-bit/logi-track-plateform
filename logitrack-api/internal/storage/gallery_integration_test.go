//go:build integration

// The evidence gallery of issue T49 (main spec §9.6, Appendix B §B.2.12) end to end on postgres:18-alpine and the
// local backend: GET /evidence/{token} and the legacy GET /evidence?k= on the public listener answer the legacy page
// with each photo as a short signed URL (never an object key), the revoked or unknown token the legacy 404 page, an
// empty one the 400 page and a database failure the 500 page; a valid token still opens 365 simulated days later, and
// every page served is audited (evidence_viewed through the security.audit consumer).
package storage_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// serveGallery runs the storage groups on the public listener only, as the ingress mounts them there.
func serveGallery(t *testing.T, svc *storage.Service) string {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	groups := svc.Groups(storage.HTTPOptions{Auth: func(c fiber.Ctx) error { return httpx.ErrUnauthenticated() },
		Caller: func(fiber.Ctx) (storage.Caller, bool) { return storage.Caller{}, false }})
	ingress.Mount(app, ingress.Public, groups, ingress.PublicPrefixes)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return "http://" + ln.Addr().String()
}

type page struct {
	status int
	header http.Header
	body   string
}

func get(t *testing.T, u string) page {
	t.Helper()
	res, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return page{res.StatusCode, res.Header, string(b)}
}

func TestEvidenceGallery(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	svc, err := storage.New(f.pool, storage.Config{Active: storage.BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public",
		PresignGetTTL: time.Hour, EvidencePresignTTL: 15 * time.Minute}, zerolog.Nop(), f.local(t))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return f.now })
	base := serveGallery(t, svc)

	id := func(sql string, args ...any) uuid.UUID {
		t.Helper()
		var v uuid.UUID
		if err := f.etl.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return v
	}
	file := func(key, status string) uuid.UUID {
		return id(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, status, committed_at, expires_at,
			storage_backend, content_type) VALUES ('logitrack', $1, $2, 'trip_photo', $3::text,
			CASE WHEN $3::text = 'committed' THEN now() END, CASE WHEN $3::text = 'pending' THEN now() + interval '1 hour' END,
			'local', 'image/jpeg') RETURNING id`, key, f.own, status)
	}
	tok, err := storage.NewEvidenceToken(f.now)
	if err != nil {
		t.Fatal(err)
	}
	// A trip: two photos of its own, one of a stop, one still pending (never shown), an incident's map and situation.
	trip := id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, origin_raw, destination_raw,
		ocr_data, evidence_token) VALUES ($1, 'form', 'TR-GAL-1', 'delivered', 'first_mile', 'ฮับ <A>', 'SOCE',
		'{"tripId": "SPXTH0099"}', $2) RETURNING id`, f.own, tok)
	stop := id(`INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw) VALUES ($1, 1, 'S1') RETURNING id`, trip)
	for i, p := range []struct {
		typ  string
		stop *uuid.UUID
		file uuid.UUID
	}{
		{"seal", nil, file("trips/gal/seal.jpg", "committed")},
		{"pre_close", nil, file("trips/gal/pre_close.jpg", "committed")},
		{"stop_1_arrived", &stop, file("trips/gal/stop1.jpg", "committed")},
		{"arrived", nil, file("trips/gal/pending.jpg", "pending")},
	} {
		if _, err := f.etl.Exec(ctx, `INSERT INTO trip_photos (trip_id, stop_id, photo_type, file_id, position) VALUES ($1, $2, $3, $4, $5)`,
			trip, p.stop, p.typ, p.file, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.etl.Exec(ctx, `INSERT INTO incident_reports (tenant_id, tenant_source, trip_id, delay_cause, map_photo_file_id,
		situation1_photo_file_id) VALUES ($1, 'trip', $2, 'incident_cause_traffic', $3, $4)`, f.own, trip,
		file("incidents/gal/map.jpg", "committed"), file("incidents/gal/s1.jpg", "committed")); err != nil {
		t.Fatal(err)
	}

	// 365 simulated days later (EVIDENCE_TOKEN_TTL_DAYS=0) the link still opens.
	f.now = f.now.Add(365 * 24 * time.Hour)
	for _, u := range []string{base + "/evidence/" + tok, base + "/evidence?k=" + url.QueryEscape(tok),
		base + "/evidence/tripEvidence?k=" + tok} {
		p := get(t, u)
		if p.status != http.StatusOK || p.header.Get("Cache-Control") != "private, max-age=300" ||
			!strings.HasPrefix(p.header.Get("Content-Type"), "text/html") || p.header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s: %d %v", u, p.status, p.header)
		}
		for _, want := range []string{"<title>รูปหลักฐานการจัดส่ง</title>", `<p class="sub">ฮับ &lt;A&gt; → SOCE · เลขทริป SPXTH0099</p>`,
			`alt="ซีล"`, `alt="ก่อนปิดตู้"`, `alt="stop_1_arrived"`, `alt="เหตุล่าช้า — แผนที่/พิกัด"`, `alt="เหตุล่าช้า — สถานการณ์"`} {
			if !strings.Contains(p.body, want) {
				t.Fatalf("%s: the page lacks %q:\n%s", u, want, p.body)
			}
		}
		if n := strings.Count(p.body, `<a class="ph"`); n != 5 {
			t.Fatalf("%d photos on the page, want 5 (the pending one is left out)", n)
		}
		if strings.Contains(p.body, "pending.jpg") || !strings.Contains(p.body, "http://media.test/media/trips/gal/seal.jpg?") {
			t.Fatalf("photos are signed URLs of committed files only:\n%s", p.body)
		}
		// The photos keep the legacy order: the trip's own (by position), its stops', then the incidents'.
		if i, j, k := strings.Index(p.body, "seal.jpg"), strings.Index(p.body, "stop1.jpg"), strings.Index(p.body, "map.jpg"); i >= j || j >= k {
			t.Fatalf("photo order: seal %d, stop %d, incident %d", i, j, k)
		}
	}
	// The signed URLs live EVIDENCE_PRESIGN_TTL: an image of a page served now expires in 15 minutes.
	p := get(t, base+"/evidence/"+tok)
	start := strings.Index(p.body, `src="`) + len(`src="`)
	signed, err := url.Parse(strings.ReplaceAll(p.body[start:start+strings.Index(p.body[start:], `"`)], "&amp;", "&"))
	if err != nil {
		t.Fatal(err)
	}
	if exp := signed.Query().Get("X-LT-Expires"); exp == "" {
		t.Fatalf("the image URL carries no expiry: %s", signed)
	} else if want := f.now.Add(15 * time.Minute).Unix(); exp != strconv.FormatInt(want, 10) {
		t.Fatalf("image expiry %s, want %d (EVIDENCE_PRESIGN_TTL)", exp, want)
	}

	// The standby gallery.
	stok, err := storage.NewEvidenceToken(f.now)
	if err != nil {
		t.Fatal(err)
	}
	standby := id(`INSERT INTO standby_records (tenant_id, tenant_source, start_location_raw, evidence_token)
		VALUES ($1, 'form', 'HUBA', $2) RETURNING id`, f.own, stok)
	if _, err := f.etl.Exec(ctx, `INSERT INTO standby_photos (standby_id, photo_type, file_id) VALUES ($1, 'site_photo', $2)`,
		standby, file("standby/gal/site.jpg", "committed")); err != nil {
		t.Fatal(err)
	}
	p = get(t, base+"/evidence/"+stok)
	if p.status != http.StatusOK || !strings.Contains(p.body, "<title>รูปหลักฐาน Standby</title>") ||
		!strings.Contains(p.body, `<p class="sub">HUBA → - · งานหมด / Standby</p>`) || !strings.Contains(p.body, `alt="รูปหน้างาน"`) {
		t.Fatalf("standby gallery: %d %s", p.status, p.body)
	}

	// The legacy status pages, byte for byte: no token (and the legacy function's own name) 400; unknown 404.
	for u, want := range map[string]page{
		base + "/evidence":              {status: http.StatusBadRequest, body: storage.GalleryBadRequestPage},
		base + "/evidence?k=":           {status: http.StatusBadRequest, body: storage.GalleryBadRequestPage},
		base + "/evidence/tripEvidence": {status: http.StatusBadRequest, body: storage.GalleryBadRequestPage},
		base + "/evidence/unknownToken": {status: http.StatusNotFound, body: storage.GalleryNotFoundPage},
	} {
		if p := get(t, u); p.status != want.status || p.body != want.body || p.header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %q %s", u, p.status, p.header.Get("Cache-Control"), p.body)
		}
	}

	// Revoked (R47): 404 from the next request on, for both spellings of the link.
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		_, err := storage.RevokeEvidence(ctx, tx, storage.EvidenceTrip, trip, f.now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{base + "/evidence/" + tok, base + "/evidence?k=" + tok} {
		if p := get(t, u); p.status != http.StatusNotFound || p.body != storage.GalleryNotFoundPage {
			t.Fatalf("revoked %s: %d %s", u, p.status, p.body)
		}
	}

	// Every page served is audited through the security.audit consumer: the record, the photo count and the token
	// format, never the token.
	rows, err := f.etl.Query(ctx, `SELECT payload FROM outbox_events WHERE routing_key = 'security.event' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var views []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), tok) || strings.Contains(string(raw), stok) {
			t.Fatalf("an audit row carries the token: %s", raw)
		}
		var ev map[string]any
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		views = append(views, ev)
	}
	rows.Close()
	if len(views) != 5 {
		t.Fatalf("%d evidence_viewed events, want 5 (three trip pages, one more, the standby page)", len(views))
	}
	if d, _ := views[0]["details"].(map[string]any); views[0]["eventType"] != storage.EvidenceViewed || d["recordId"] != trip.String() ||
		d["photos"] != float64(5) || d["tokenFormat"] != "v1" || d["kind"] != "trip" {
		t.Fatalf("evidence_viewed: %v", views[0])
	}

	// A database failure is the legacy 500 page.
	broken, err := storage.New(closedPool(t, f), storage.Config{Active: storage.BackendLocal, Bucket: "logitrack",
		PublicBucket: "logitrack-public", EvidencePresignTTL: 15 * time.Minute}, zerolog.Nop(), f.local(t))
	if err != nil {
		t.Fatal(err)
	}
	if p := get(t, serveGallery(t, broken)+"/evidence/"+stok); p.status != http.StatusInternalServerError || p.body != storage.GalleryErrorPage {
		t.Fatalf("database down: %d %s", p.status, p.body)
	}
}

// closedPool is a pool of the test database that is already closed: every query fails.
func closedPool(t *testing.T, f *fixture) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.NewWithConfig(context.Background(), f.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	return p
}
