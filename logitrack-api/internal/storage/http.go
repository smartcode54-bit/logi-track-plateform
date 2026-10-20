package storage

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagedb"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Route prefixes of the storage service.
const (
	// UploadsPrefix holds POST /v1/uploads/presign and the web's local upload route (internal listener; the BFF
	// forwards /api/go/v1/...).
	UploadsPrefix = "/v1/uploads"
	// LocalUploadPath is the web's local upload route: PUT /v1/uploads/local/{key}?X-LT-Expires&X-LT-Signature.
	LocalUploadPath = UploadsPrefix + "/local"
	// FilesPrefix is GET /v1/files?key= (internal listener).
	FilesPrefix = "/v1/files"
	// MediaPrefix is the local backend's object route on the public listener (owner-approved widening of
	// PUBLIC_ROUTE_GROUPS, main spec §2.6): GET/HEAD /media/{key} downloads, PUT /media/{key} uploads.
	MediaPrefix = "/media"
)

// HTTPOptions wire the routes to authentication: Auth answers 401 itself and stores the principal for Caller;
// PresignLimit is the presign_user rate-limit middleware and EvidenceLimit the evidence_ip one of the gallery
// (RATE_LIMIT_EVIDENCE, 60/min per client address); nil = none.
type HTTPOptions struct {
	Auth            fiber.Handler
	UploadPrincipal func(context.Context, string) (*authz.Principal, error)
	Caller          func(c fiber.Ctx) (Caller, bool)
	PresignLimit    fiber.Handler
	EvidenceLimit   fiber.Handler
}

// Groups returns the route groups of the storage service. A signed upload gets its own body limit, the signed
// size (at most UPLOAD_MAX_BYTES), decided from its request head (ingress.Group.Uploads, UploadLimit); every
// other request, an unsigned PUT on an upload route included, keeps the 4 MiB API limit.
func (s *Service) Groups(o HTTPOptions) []ingress.Group {
	h := handlers{s: s, o: o}
	// Fiber runs a route's handlers in the order given: authentication, the rate limit, then the handler.
	presign := []any{}
	if o.PresignLimit != nil {
		presign = append(presign, o.PresignLimit)
	}
	presign = append(presign, h.presign)
	// The large-body exemption exists only while the local backend does: without it the routes answer 404 and
	// every PUT keeps the 4 MiB limit.
	var apiUpload, mediaUpload []ingress.Upload
	if s != nil {
		apiUpload = []ingress.Upload{{Prefix: UploadsPrefix + "/", Limit: s.BrokerUploadLimit(o.UploadPrincipal)}}
	}
	if s != nil && s.local != nil {
		apiUpload = append([]ingress.Upload{{Prefix: LocalUploadPath + "/", Limit: s.UploadLimit(LocalUploadPath + "/")}}, apiUpload...)
		mediaUpload = []ingress.Upload{{Prefix: MediaPrefix + "/", Limit: s.UploadLimit(MediaPrefix + "/")}}
	}
	return []ingress.Group{
		{Prefix: UploadsPrefix, Uploads: apiUpload, Mount: func(r fiber.Router) {
			r.Post("/presign", o.Auth, presign...)
			r.Put("/:grantId/content", o.Auth, h.uploadBroker)
			r.Put("/local/*", h.uploadAPI)
		}},
		{Prefix: SupportFilesPrefix, Mount: func(r fiber.Router) {
			ingress.Bind(r, "/:fileId/content", supportFilePolicy(SupportFilesPrefix+"/:fileId/content", true), o.Auth, h.supportFile)
		}},
		{Prefix: FilesPrefix, Mount: func(r fiber.Router) {
			r.Get("", o.Auth, h.file)
		}},
		{Prefix: MediaPrefix, Public: true, Uploads: mediaUpload, Mount: func(r fiber.Router) {
			r.Get("/*", h.media)
			r.Put("/*", h.uploadMedia)
		}},
		// The evidence gallery (T49): public, and on the internal listener too; the token is the access control.
		{Prefix: EvidencePath, Public: true, Mount: func(r fiber.Router) {
			gallery := []any{}
			if o.EvidenceLimit != nil {
				gallery = append(gallery, o.EvidenceLimit)
			}
			gallery = append(gallery, h.evidence)
			r.Get("", gallery[0], gallery[1:]...)
			r.Get("/:token", gallery[0], gallery[1:]...)
		}},
	}
}

type handlers struct {
	s *Service
	o HTTPOptions
}

func (h handlers) caller(c fiber.Ctx) (Caller, error) {
	cl, ok := h.o.Caller(c)
	if !ok {
		return Caller{}, httpx.ErrUnauthenticated()
	}
	return cl, nil
}

// presign is POST /v1/uploads/presign: the web's presign (through the BFF), so a local upload URL is an API path.
func (h handlers) presign(c fiber.Ctx) error {
	cl, err := h.caller(c)
	if err != nil {
		return err
	}
	var in PresignInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Presign(c.Context(), cl, in, PutOptions{APIPath: true})
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// file is GET /v1/files?key=: 302 to a short-lived URL of the object on its own backend.
func (h handlers) file(c fiber.Ctx) error {
	if authz.IsSupport(authz.PrincipalFrom(c)) {
		return httpx.ErrSupportViewUnavailable()
	}
	cl, err := h.caller(c)
	if err != nil {
		return err
	}
	for k := range c.Queries() {
		if k != "key" {
			return httpx.ErrBadRequest("unknown query parameter " + k)
		}
	}
	key := c.Query("key")
	if key == "" {
		return violation("key", "required", nil)
	}
	u, err := h.s.DownloadURL(c.Context(), cl, key)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	c.Set(fiber.HeaderLocation, u)
	return c.SendStatus(http.StatusFound)
}

// objectKey is the key of a /media/{key} or /v1/uploads/local/{key} request: the raw wildcard, unescaped once and
// validated. A path that ends in "/" is a directory and never a key.
func objectKey(c fiber.Ctx) (string, bool) {
	return rawObjectKey(c.Path(), c.Params("*"))
}

func rawObjectKey(path, raw string) (string, bool) {
	if strings.HasSuffix(path, "/") {
		return "", false
	}
	key, err := url.PathUnescape(raw)
	if err != nil || validLocalKey(key) != nil {
		return "", false
	}
	return key, true
}

// contentLength is the signed spelling of a request's Content-Length ("" when the body is chunked or has none, so
// it never matches a signed size).
func contentLength(n int) string {
	if n < 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// maxUpload is the largest body the local upload route below prefix takes for key: UPLOAD_MAX_BYTES, and for an
// APK (a key under PublicPrefix) MaxReleaseBytes on the API route only. The public /media route takes no APK at all
// (0): release uploads stay on the internal listener (R43).
func (s *Service) maxUpload(prefix, key string) int64 {
	if !IsPublicKey(key) {
		return s.cfg.UploadMaxBytes
	}
	if prefix == LocalUploadPath+"/" {
		return max(s.cfg.UploadMaxBytes, MaxReleaseBytes)
	}
	return 0
}

// UploadLimit is the head check of the local upload route below prefix (ingress.Upload.Limit): the signed size
// when the request's path, Content-Type, Content-Length and query carry a valid, unexpired upload signature, else
// 0. It runs in fasthttp's HeaderReceived, before any body byte is read, so an unsigned request reserves no more
// than the 4 MiB / 30 s of every other route; the handler verifies again. The size may reach maxUpload: an APK on
// the API route is the one upload above UPLOAD_MAX_BYTES.
func (s *Service) UploadLimit(prefix string) func(ingress.UploadHead) int64 {
	return func(h ingress.UploadHead) int64 {
		l := s.local
		if l == nil || h.ContentLength <= 0 {
			return 0
		}
		raw, ok := strings.CutPrefix(h.Path, prefix)
		if !ok {
			return 0
		}
		key, ok := rawObjectKey(h.Path, raw)
		if !ok || int64(h.ContentLength) > s.maxUpload(prefix, key) {
			return 0
		}
		q, err := url.ParseQuery(h.Query)
		if err != nil {
			return 0
		}
		if l.Verify(http.MethodPut, key, h.ContentType, contentLength(h.ContentLength), "", q.Get(QueryExpires), q.Get(QuerySignature)) != nil {
			return 0
		}
		return int64(h.ContentLength)
	}
}

func signatureError(err error) error {
	if errors.Is(err, ErrSignatureExpired) {
		return errPermission("signature_expired")
	}
	return errPermission("signature_invalid")
}

// uploadAPI is the local backend's PUT on the internal listener: /v1/uploads/local/{key} (the web through the BFF,
// cmd/release with an APK).
func (h handlers) uploadAPI(c fiber.Ctx) error { return h.upload(c, LocalUploadPath+"/") }

// uploadMedia is the local backend's PUT on /media/{key} (the driver app, public listener); an APK key is 404 there.
func (h handlers) uploadMedia(c fiber.Ctx) error { return h.upload(c, MediaPrefix+"/") }

// upload is the local backend's PUT (web: /v1/uploads/local/{key} through the BFF; driver app: /media/{key} on the
// public listener). The signature covers method, key, expiry, the Content-Type header and the declared size
// (Content-Length), as a SigV4 PUT does on S3. Only a pending upload of the local backend takes bytes, and the
// bytes are renamed into place while that row is locked (PublishLocal), so a still-valid URL can neither
// overwrite a committed object nor replace the bytes a concurrent commit verifies. prefix is the route's own
// (maxUpload: APKs only on the API route).
func (h handlers) upload(c fiber.Ctx, prefix string) error {
	l := h.s.local
	if l == nil {
		return httpx.ErrNotFound()
	}
	key, ok := objectKey(c)
	if !ok {
		return httpx.ErrNotFound()
	}
	limit := h.s.maxUpload(prefix, key)
	if limit <= 0 {
		return httpx.ErrNotFound()
	}
	ct := c.Get(fiber.HeaderContentType)
	if err := l.Verify(http.MethodPut, key, ct, contentLength(c.Request().Header.ContentLength()), "",
		c.Query(QueryExpires), c.Query(QuerySignature)); err != nil {
		return signatureError(err)
	}
	body := c.Request().Body() // the request's own buffer: no copy of up to UPLOAD_MAX_BYTES
	if int64(len(body)) > limit {
		return httpx.NewError(http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, "request body too large")
	}
	// A cheap refusal before anything touches the disk; PublishLocal decides under the row lock.
	var bound bool
	err := db.WithSystem(c.Context(), h.s.pool, nil, func(tx pgx.Tx) error {
		q := storagedb.New(tx)
		row, e := q.GetFileByKey(c.Context(), key)
		if e != nil {
			return e
		}
		ready, e := q.ContextUploadGrantsReady(c.Context())
		if e != nil || !ready {
			return e
		}
		bound, e = q.HasUploadGrant(c.Context(), row.ID)
		return e
	})
	if err != nil || bound {
		return errPermission("authenticated_upload_required")
	}
	pending, err := h.s.PendingLocal(c.Context(), key)
	if err != nil {
		return err
	}
	if !pending {
		return errFailedPrecondition("not_pending")
	}
	st, err := l.Stage(key, bytes.NewReader(body), normalizeType(ct), limit)
	if err != nil {
		return err
	}
	defer st.Discard()
	if err := h.s.PublishLocal(c.Context(), key, st); err != nil {
		return err
	}
	c.Set(fiber.HeaderETag, `"`+st.Info.SHA256+`"`)
	return c.SendStatus(http.StatusOK)
}

// media is GET (and HEAD) /media/{key} of the local backend: a signed, expiring URL for private objects and an
// unsigned one under app_releases/; a directory path, an invalid key or a missing object is 404. The file is
// streamed from LOCAL_MEDIA_DIR with the content type recorded at upload.
func (h handlers) media(c fiber.Ctx) error {
	l := h.s.local
	if l == nil {
		return httpx.ErrNotFound()
	}
	key, ok := objectKey(c)
	if !ok {
		return httpx.ErrNotFound()
	}
	cacheControl := "public, max-age=31536000, immutable" // APKs: immutable keys (main spec §9.7)
	disposition := c.Query(QueryDisposition)
	if !IsPublicKey(key) {
		exp := c.Query(QueryExpires)
		if err := l.Verify(http.MethodGet, key, "", "", disposition, exp, c.Query(QuerySignature)); err != nil {
			return signatureError(err)
		}
		e, _ := strconv.ParseInt(exp, 10, 64)
		left := max(0, e-l.now().Unix())
		cacheControl = "private, max-age=" + strconv.FormatInt(left, 10)
	}
	f, info, err := l.Open(key)
	if errors.Is(err, ErrObjectNotFound) {
		return httpx.ErrNotFound()
	}
	if err != nil {
		return err
	}
	ct := info.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Set(fiber.HeaderContentType, ct)
	c.Set(fiber.HeaderCacheControl, cacheControl)
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	if info.SHA256 != "" {
		c.Set(fiber.HeaderETag, `"`+info.SHA256+`"`)
	}
	if disposition != "" && !IsPublicKey(key) {
		c.Set(fiber.HeaderContentDisposition, disposition)
	}
	return c.SendStream(f, int(info.Size))
}
