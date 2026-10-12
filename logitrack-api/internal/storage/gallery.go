package storage

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagedb"
)

// The evidence gallery (T49; main spec §5.6, §9.6; Appendix B §B.2.12, §B.2.23): GET /evidence/{token} and the legacy
// query form GET /evidence?k={token} on the public listener, the port of the onRequest function tripEvidence
// (fn:tripEvidence.ts:118-163). A customer opens it from the "ดูรูปหลักฐาน" button of a LINE card; the token
// (ResolveEvidence: never expiring by default, revocable, R30, R47) is the only access control. The page keeps the
// legacy HTML (titles, Thai photo labels, styles) and its 400 / 404 / 500 pages; images are short presigned GETs on
// each object's own backend (EVIDENCE_PRESIGN_TTL), never the object keys. A view is audited (evidence_viewed through
// the security.audit consumer).

// Gallery titles and messages (fn:tripEvidence.ts).
const (
	galleryTripTitle    = "รูปหลักฐานการจัดส่ง"
	galleryStandbyTitle = "รูปหลักฐาน Standby"
	galleryBadTitle     = "ลิงก์ไม่ถูกต้อง"
	galleryBadText      = "ลิงก์ไม่ถูกต้อง"
	galleryGoneTitle    = "ไม่พบข้อมูล"
	galleryGoneText     = "ไม่พบรูปสำหรับลิงก์นี้ (อาจถูกเพิกถอนแล้ว)"
	galleryErrorTitle   = "เกิดข้อผิดพลาด"
	galleryErrorText    = "เกิดข้อผิดพลาด กรุณาลองใหม่"
	galleryEmptyText    = "ยังไม่มีรูป"
	galleryCacheControl = "private, max-age=300"
	// legacyFunctionName is the last path segment of the old Cloud Functions URL (…/tripEvidence?k=): not a token.
	legacyFunctionName = "tripEvidence"
	// galleryTimeout bounds one gallery request's database and signing work.
	galleryTimeout = 10 * time.Second
)

// photoLabel are the Thai labels of the photo types (PHOTO_LABEL, fn:tripEvidence.ts:25-49).
var photoLabel = map[string]string{
	// loading phase
	"pre_close":        "ก่อนปิดตู้",
	"closing":          "ปิดตู้",
	"seal":             "ซีล",
	"runsheet":         "ใบคุมรถ",
	"runsheet_extra_1": "ใบคุมรถ (เพิ่ม)",
	"runsheet_extra_2": "ใบคุมรถ (เพิ่ม)",
	"runsheet_extra_3": "ใบคุมรถ (เพิ่ม)",
	"truck_release":    "ปล่อยรถ",
	"checkin_app":      "เช็คอิน (แอปลูกค้า)",
	// delivery phase
	"pre_open":          "ก่อนเปิดตู้",
	"opening":           "เปิดตู้",
	"empty_container":   "ตู้เปล่า",
	"runsheet_received": "ใบคุมรถ (รับ)",
	"arrived":           "ถึงปลายทาง",
	// standby
	"customer_worksheet": "ใบงานลูกค้า",
	"site_photo":         "รูปหน้างาน",
}

// Incident photo labels (collectIncidentPhotos).
const (
	incidentMapLabel       = "เหตุล่าช้า — แผนที่/พิกัด"
	incidentSituationLabel = "เหตุล่าช้า — สถานการณ์"
)

// PhotoLabel is labelFor: the Thai label of a photo type, else the type itself, else "รูป".
func PhotoLabel(photoType string) string {
	if l, ok := photoLabel[photoType]; ok {
		return l
	}
	if photoType != "" {
		return photoType
	}
	return "รูป"
}

// GalleryPhoto is one image of the page.
type GalleryPhoto struct {
	URL   string
	Label string
}

// Gallery is one page: its title (also the h1), the subtitle as text, and the photos.
type Gallery struct {
	Title    string
	Subtitle string
	Photos   []GalleryPhoto
}

// galleryFile is a photo before its URL is signed.
type galleryFile struct {
	id    uuid.UUID
	label string
}

// Gallery builds the page of a resolved token: the trip's photos (its own, then its stops') and the map and situation
// photos of its incident reports, or the standby record's photos. A photo whose file is not committed is left out (the
// legacy page listed only photos with a URL).
func (s *Service) Gallery(ctx context.Context, t EvidenceTarget) (Gallery, error) {
	var g Gallery
	var files []galleryFile
	err := db.WithSystem(ctx, s.pool, &t.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return err
		}
		q := storagedb.New(tx)
		if t.Kind == EvidenceStandby {
			sb, err := q.GalleryStandby(ctx, t.ID)
			if err != nil {
				return err
			}
			g = Gallery{Title: galleryStandbyTitle,
				Subtitle: orDash(sb.StartLocationRaw) + " → " + orDash(sb.EndLocationRaw) + " · งานหมด / Standby"}
			photos, err := q.GalleryStandbyPhotos(ctx, t.ID)
			for _, p := range photos {
				files = append(files, galleryFile{p.FileID, PhotoLabel(p.PhotoType)})
			}
			return err
		}
		tr, err := q.GalleryTrip(ctx, t.ID)
		if err != nil {
			return err
		}
		tripNo := tr.TripNo
		if tr.HasOcrTripID {
			tripNo = tr.OcrTripID
		}
		g = Gallery{Title: galleryTripTitle,
			Subtitle: orDash(tr.OriginRaw) + " → " + orDash(tr.DestinationRaw) + " · เลขทริป " + tripNo}
		photos, err := q.GalleryTripPhotos(ctx, t.ID)
		if err != nil {
			return err
		}
		for _, p := range photos {
			files = append(files, galleryFile{p.FileID, PhotoLabel(p.PhotoType)})
		}
		incidents, err := q.GalleryTripIncidents(ctx, t.ID)
		if err != nil {
			return err
		}
		for _, i := range incidents {
			for _, f := range []struct {
				id    *uuid.UUID
				label string
			}{{i.MapPhotoFileID, incidentMapLabel}, {i.Situation1PhotoFileID, incidentSituationLabel},
				{i.Situation2PhotoFileID, incidentSituationLabel}} {
				if f.id != nil {
					files = append(files, galleryFile{*f.id, f.label})
				}
			}
		}
		return nil
	})
	if err != nil {
		return Gallery{}, err
	}
	ids := make([]uuid.UUID, len(files))
	for i, f := range files {
		ids[i] = f.id
	}
	signed, err := s.SignedFiles(ctx, ids, s.cfg.EvidencePresignTTL)
	if err != nil {
		return Gallery{}, err
	}
	for _, f := range files {
		if sf, ok := signed[f.id]; ok && sf.URL != "" {
			g.Photos = append(g.Photos, GalleryPhoto{URL: sf.URL, Label: f.label})
		}
	}
	return g, nil
}

// orDash is the legacy `value ?? "-"`: an absent value shows "-", an empty one stays empty.
func orDash(v *string) string {
	if v == nil {
		return "-"
	}
	return *v
}

// --- HTML (fn:tripEvidence.ts:76-116) ------------------------------------------------------------------------------------

// esc is the legacy esc(): & < > " ' as the entities it wrote.
var esc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace

const galleryStyle = `<style>
:root{color-scheme:light dark}
body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Noto Sans Thai",sans-serif;background:#f2f2f2;color:#111}
@media(prefers-color-scheme:dark){body{background:#111;color:#eee}.card{background:#1c1c1c;border-color:#333}.sub{color:#aaa}.cap{color:#aaa}}
.wrap{max-width:720px;margin:0 auto;padding:16px}
.card{background:#fff;border:1px solid #e5e5e5;border-radius:12px;padding:14px 16px;margin-bottom:14px}
h1{font-size:18px;margin:0 0 4px}
.sub{color:#666;font-size:13px;margin:0}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(150px,1fr));gap:8px}
.ph{display:block;border-radius:8px;overflow:hidden;background:#ddd;border:1px solid #e0e0e0;text-decoration:none}
.ph img{width:100%;height:150px;object-fit:cover;display:block}
.cap{font-size:11px;color:#666;padding:5px 8px}
.empty{color:#888;font-size:14px;padding:24px;text-align:center}
</style>`

// galleryPage is page(): the document around a body.
func galleryPage(title, body string) string {
	return `<!doctype html><html lang="th"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` +
		esc(title) + "</title>\n" + galleryStyle + `</head><body><div class="wrap">` + body + `</div></body></html>`
}

func messageCard(text string) string {
	return `<div class="card"><div class="empty">` + text + `</div></div>`
}

// RenderGallery is renderGallery inside page(): the header card and the photo grid.
func RenderGallery(g Gallery) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h1>` + esc(g.Title) + `</h1><p class="sub">` + esc(g.Subtitle) + `</p></div>`)
	if len(g.Photos) == 0 {
		b.WriteString(messageCard(galleryEmptyText))
	} else {
		b.WriteString(`<div class="card"><div class="grid">`)
		for _, p := range g.Photos {
			b.WriteString(`<a class="ph" href="` + esc(p.URL) + `" target="_blank" rel="noopener"><img src="` + esc(p.URL) +
				`" loading="lazy" alt="` + esc(p.Label) + `"><div class="cap">` + esc(p.Label) + `</div></a>`)
		}
		b.WriteString(`</div></div>`)
	}
	return galleryPage(g.Title, b.String())
}

// Gallery pages other than a gallery: 400 (no token), 404 (unknown, revoked or expired), 500 (anything else), the
// legacy pages byte for byte.
var (
	GalleryBadRequestPage = galleryPage(galleryBadTitle, messageCard(galleryBadText))
	GalleryNotFoundPage   = galleryPage(galleryGoneTitle, messageCard(galleryGoneText))
	GalleryErrorPage      = galleryPage(galleryErrorTitle, messageCard(galleryErrorText))
)

// galleryCSP lets the page show the signed images (any http(s) origin: the S3 presign endpoint or the local /media
// base) and its inline style, and nothing else.
const galleryCSP = "default-src 'none'; img-src https: http:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// evidence is GET /evidence/{token} and GET /evidence?k={token}. The query form wins, as fn:tripEvidence.ts read it
// first; the legacy function's own name as a path segment is not a token.
func (h handlers) evidence(c fiber.Ctx) error {
	galleryHeaders(c)
	token := strings.TrimSpace(c.Query("k"))
	if token == "" {
		token = strings.TrimSpace(c.Params("token"))
	}
	if token == legacyFunctionName {
		token = ""
	}
	ctx, cancel := context.WithTimeout(c.Context(), galleryTimeout)
	defer cancel()
	t, err := h.s.ResolveEvidence(ctx, token)
	switch {
	case errors.Is(err, ErrEvidenceEmpty):
		return galleryHTML(c, http.StatusBadRequest, GalleryBadRequestPage)
	case errors.Is(err, ErrEvidenceNotFound):
		return galleryHTML(c, http.StatusNotFound, GalleryNotFoundPage)
	case err != nil:
		h.s.log.Error().Err(err).Msg("evidence gallery: token lookup failed")
		return galleryHTML(c, http.StatusInternalServerError, GalleryErrorPage)
	}
	g, err := h.s.Gallery(ctx, t)
	if errors.Is(err, pgx.ErrNoRows) {
		return galleryHTML(c, http.StatusNotFound, GalleryNotFoundPage)
	}
	if err != nil {
		h.s.log.Error().Err(err).Str("kind", string(t.Kind)).Str("record_id", t.ID.String()).
			Msg("evidence gallery: the page could not be built")
		return galleryHTML(c, http.StatusInternalServerError, GalleryErrorPage)
	}
	h.s.auditEvidenceView(ctx, t, token, len(g.Photos), httpx.RequestIDFrom(c), httpx.ClientIPFrom(c))
	c.Set(fiber.HeaderCacheControl, galleryCacheControl)
	return galleryHTML(c, http.StatusOK, RenderGallery(g))
}

// galleryHeaders: the token in the page URL never leaves in a Referer (the images are on another origin), the page is
// never framed or indexed, and only the signed images and the inline style load. A page that is not a gallery is not
// cached (a revoked link answers 404 at once; a 500 is retried).
func galleryHeaders(c fiber.Ctx) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderReferrerPolicy, "no-referrer")
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Set(fiber.HeaderContentSecurityPolicy, galleryCSP)
	c.Set("X-Robots-Tag", "noindex, nofollow")
}

func galleryHTML(c fiber.Ctx, status int, page string) error {
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Status(status).SendString(page)
}

// EvidenceViewed is the security event of an opened gallery (main spec §7.7, written by the security.audit consumer).
const EvidenceViewed = "evidence_viewed"

// auditEvidenceView appends evidence_viewed for the security.audit consumer: the record, the number of photos shown,
// the token format (never the token) and the client address. A failure is logged; the customer still sees the page.
func (s *Service) auditEvidenceView(ctx context.Context, t EvidenceTarget, token string, photos int, requestID, ip string) {
	format := "legacy"
	if _, ok := EvidenceIssuedAt(token); ok {
		format = "v1"
	}
	tenant := t.TenantID
	ev := security.Event{EventType: EvidenceViewed, Severity: security.SeverityInfo,
		Summary: "evidence gallery opened", TenantID: &tenant, RequestID: requestID, OccurredAt: s.now().UTC(),
		Details: map[string]any{"kind": string(t.Kind), "recordId": t.ID.String(), "photos": photos, "tokenFormat": format,
			"ip": ip}}
	actx := context.WithoutCancel(ctx)
	err := db.WithSystem(actx, s.pool, &tenant, func(tx pgx.Tx) error {
		return security.Emit(actx, tx, ev, "evidence", t.ID.String())
	})
	if err != nil {
		s.log.Warn().Err(err).Str("record_id", t.ID.String()).Msg("evidence gallery: evidence_viewed not recorded")
	}
}
