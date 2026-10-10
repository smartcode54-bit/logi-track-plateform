package storage

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

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
// PresignLimit is the presign_user rate-limit middleware (nil = none).
type HTTPOptions struct {
	Auth         fiber.Handler
	Caller       func(c fiber.Ctx) (Caller, bool)
	PresignLimit fiber.Handler
}

// Groups returns the route groups of the storage service. The upload routes carry their own body limit
// (UPLOAD_MAX_BYTES, through ingress.Group.UploadPaths); every other route keeps the 4 MiB API limit.
func (s *Service) Groups(o HTTPOptions) []ingress.Group {
	h := handlers{s: s, o: o}
	// Fiber runs a route's handlers in the order given: authentication, the rate limit, then the handler.
	presign := []any{}
	if o.PresignLimit != nil {
		presign = append(presign, o.PresignLimit)
	}
	presign = append(presign, h.presign)
	return []ingress.Group{
		{Prefix: UploadsPrefix, UploadPaths: []string{LocalUploadPath + "/"}, Mount: func(r fiber.Router) {
			r.Post("/presign", o.Auth, presign...)
			r.Put("/local/*", h.upload)
		}},
		{Prefix: FilesPrefix, Mount: func(r fiber.Router) {
			r.Get("", o.Auth, h.file)
		}},
		{Prefix: MediaPrefix, Public: true, UploadPaths: []string{MediaPrefix + "/"}, Mount: func(r fiber.Router) {
			r.Get("/*", h.media)
			r.Put("/*", h.upload)
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
	if strings.HasSuffix(c.Path(), "/") {
		return "", false
	}
	key, err := url.PathUnescape(c.Params("*"))
	if err != nil || ValidateKey(key) != nil {
		return "", false
	}
	return key, true
}

func signatureError(err error) error {
	if errors.Is(err, ErrSignatureExpired) {
		return errPermission("signature_expired")
	}
	return errPermission("signature_invalid")
}

// upload is the local backend's PUT (web: /v1/uploads/local/{key} through the BFF; driver app: /media/{key} on the
// public listener). The signature covers method, key, expiry and the Content-Type header, as SigV4 does for S3;
// only a pending upload of the local backend takes bytes, so a still-valid URL cannot overwrite a committed object.
func (h handlers) upload(c fiber.Ctx) error {
	l := h.s.local
	if l == nil {
		return httpx.ErrNotFound()
	}
	key, ok := objectKey(c)
	if !ok {
		return httpx.ErrNotFound()
	}
	if err := l.Verify(http.MethodPut, key, c.Get(fiber.HeaderContentType), "", c.Query(QueryExpires), c.Query(QuerySignature)); err != nil {
		return signatureError(err)
	}
	body := c.Request().Body() // the request's own buffer: no copy of up to UPLOAD_MAX_BYTES
	if int64(len(body)) > h.s.cfg.UploadMaxBytes {
		return httpx.NewError(http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, "request body too large")
	}
	pending, err := h.s.PendingLocal(c.Context(), key)
	if err != nil {
		return err
	}
	if !pending {
		return errFailedPrecondition("not_pending")
	}
	info, err := l.Write(key, bytes.NewReader(body), normalizeType(c.Get(fiber.HeaderContentType)), h.s.cfg.UploadMaxBytes)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderETag, `"`+info.SHA256+`"`)
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
		if err := l.Verify(http.MethodGet, key, "", disposition, exp, c.Query(QuerySignature)); err != nil {
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
