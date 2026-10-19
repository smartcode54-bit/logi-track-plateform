package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagedb"
)

const SupportFilesPrefix = "/v1/support/files"

type supportSourceKey struct{}

var supportFileAdapter = httpx.NewStreamAdapter("storage.phase1", "fleet", []string{"image/png", "image/jpeg", "image/gif", "application/pdf"}, false, func(c fiber.Ctx) (httpx.StreamSource, error) {
	source, ok := c.Locals(supportSourceKey{}).(httpx.StreamSource)
	if !ok {
		return source, httpx.ErrSupportViewUnavailable()
	}
	return source, nil
})

func supportFilePolicy(route string, head bool) httpx.EndpointPolicy {
	for _, p := range authz.SupportPolicySeed() {
		if p.Method == http.MethodGet && p.Route == route {
			p.SchemaID = "stream:storage.phase1"
			p.AllowHEAD = head
			p.Predicate = func(c fiber.Ctx) (httpx.PolicyClass, error) {
				_, err := httpx.StrictQuery(c)
				return httpx.PolicyTenantRead, err
			}
			return p
		}
	}
	panic("storage: unreviewed support file route")
}

// SupportFilePath never exposes an object key, signer or bearer token. The next
// GET is independently authenticated; possession of this path grants no access.
func SupportFilePath(id uuid.UUID) string {
	return "/api/go" + SupportFilesPrefix + "/" + id.String() + "/content"
}

func (h handlers) supportFile(c fiber.Ctx) error {
	if !authz.IsSupport(authz.PrincipalFrom(c)) {
		return httpx.ErrSupportViewUnavailable()
	}
	id, err := uuid.Parse(c.Params("fileId"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	return h.s.SupportContent(c, id)
}

// SupportContent is also the driver-document transport adapter. It resolves the
// current referencing row on every request/chunk, with no ordinary signer fallback.
func (s *Service) SupportContent(c fiber.Ctx, id uuid.UUID) error {
	source, err := s.SupportFileSource(c.Context(), authz.PrincipalFrom(c), httpx.ProjectionAudienceFrom(c), id, FileRef{})
	if err != nil {
		return err
	}
	return StreamSupportSource(c, source)
}

// StreamSupportSource sends an authorized source through the registered broker.
// Source providers have no response context or writer and cannot emit bytes.
func StreamSupportSource(c fiber.Ctx, source httpx.StreamSource) error {
	c.Locals(supportSourceKey{}, source)
	return httpx.Stream(c, supportFileAdapter)
}

func (s *Service) SupportFileSource(ctx context.Context, p *authz.Principal, a httpx.ProjectionAudience, id uuid.UUID, expected FileRef) (httpx.StreamSource, error) {
	if !a.Support || a.Tenant == "" || a.Finalize == nil {
		return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
	}
	if !a.Consents[a.Tenant].Enabled {
		return httpx.StreamSource{}, httpx.NewError(403, httpx.CodeSupportAccessMasked, "support access is masked")
	}
	tenant, err := uuid.Parse(a.Tenant)
	if err != nil {
		return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
	}
	var row storagedb.FileObject
	resolve := func(ctx context.Context) error {
		return db.WithSystem(ctx, s.pool, &tenant, func(tx pgx.Tx) error {
			q := storagedb.New(tx)
			next, err := q.GetFile(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.ErrNotFound()
			}
			if err != nil {
				return err
			}
			if next.TenantID == nil || *next.TenantID != tenant || next.Status != "committed" || next.Visibility != "private" || next.OwnerID == nil || next.OwnerKind == nil {
				return httpx.ErrNotFound()
			}
			if expected.ID != uuid.Nil && (next.ID != expected.ID || deref(next.OwnerKind) != OwnerDriver || deref(next.OwnerID) != expected.OwnerID || next.Purpose != expected.Purpose) {
				return httpx.ErrNotFound()
			}
			ok, err := q.SupportFileReferenced(ctx, storagedb.SupportFileReferencedParams{OwnerKind: *next.OwnerKind, OwnerID: *next.OwnerID, TenantID: tenant, FileID: id, Purpose: next.Purpose})
			if err != nil {
				return err
			}
			if !ok {
				return httpx.ErrNotFound()
			}
			if row.ID != uuid.Nil && (row.Bucket != next.Bucket || row.ObjectKey != next.ObjectKey || row.StorageBackend != next.StorageBackend || deref(row.ContentType) != deref(next.ContentType) || deref(row.Sha256) != deref(next.Sha256) || deref(row.SizeBytes) != deref(next.SizeBytes) || deref(row.OwnerID) != deref(next.OwnerID) || deref(row.OwnerKind) != deref(next.OwnerKind)) {
				return httpx.ErrSupportViewUnavailable()
			}
			if row.ID == uuid.Nil {
				row = next
			}
			return nil
		})
	}
	if p == nil {
		return httpx.StreamSource{}, httpx.ErrUnauthenticated()
	}
	expiry := p.TokenExpiresAt
	check := func(ctx context.Context) error {
		if !expiry.IsZero() && !expiry.After(time.Now()) {
			return httpx.ErrSupportViewUnavailable()
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := a.Finalize(ctx, []string{a.Tenant}, func(consents map[string]httpx.Consent) error {
			live := consents[a.Tenant]
			if !live.Enabled || live.Version != a.ConsentVersion {
				return httpx.ErrSupportViewUnavailable()
			}
			return nil
		}); err != nil {
			return err
		}
		return resolve(ctx)
	}
	if err := check(ctx); err != nil {
		return httpx.StreamSource{}, err
	}
	ct := deref(row.ContentType)
	switch ct {
	case "application/pdf", "image/png", "image/jpeg", "image/gif":
	default:
		return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
	}
	if row.SizeBytes == nil || *row.SizeBytes <= 0 || *row.SizeBytes > MaxServerReadBytes {
		return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
	}
	backend, err := s.backend(row.StorageBackend)
	if err != nil {
		return httpx.StreamSource{}, err
	}
	reader, ok := backend.(Reader)
	if !ok {
		return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
	}
	rc, err := reader.Read(ctx, object(row))
	if err != nil {
		return httpx.StreamSource{}, httpx.ErrNotFound()
	}
	defer func() { _ = rc.Close() }()
	// Bounded storage reads also revalidate; never buffer a whole private file
	// against a single admission check, and never hold consent locks over storage I/O.
	var raw bytes.Buffer
	chunk := make([]byte, 32<<10)
	for {
		if err := check(ctx); err != nil {
			return httpx.StreamSource{}, err
		}
		n, readErr := rc.Read(chunk)
		if raw.Len()+n > MaxServerReadBytes {
			return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
		}
		_, _ = raw.Write(chunk[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
		}
	}
	if int64(raw.Len()) != *row.SizeBytes {
		return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
	}
	if row.Sha256 != nil {
		sum := sha256.Sum256(raw.Bytes())
		if hex.EncodeToString(sum[:]) != *row.Sha256 {
			return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
		}
	}
	body, err := supportFileBytes(raw.Bytes(), ct)
	if err != nil {
		return httpx.StreamSource{}, httpx.ErrSupportViewUnavailable()
	}
	source := httpx.StreamSource{Tenant: a.Tenant, ContentType: ct, Check: check,
		Size: int64(len(body)), KnownSize: true, Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }}
	return source, nil
}

// Decode/re-encode removes image metadata and polyglot trailers. Enforce pixel
// bounds before allocation. PDFs are attachment-only, with no HTML/type fallback.
func supportFileBytes(body []byte, ct string) ([]byte, error) {
	if ct == "application/pdf" {
		if !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.Contains(body[max(0, len(body)-1024):], []byte("%%EOF")) {
			return nil, errors.New("invalid PDF")
		}
		return body, nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	expected := map[string]string{"image/png": "png", "image/jpeg": "jpeg", "image/gif": "gif"}[ct]
	if err != nil || format != expected || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return nil, errors.New("invalid image")
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	switch format {
	case "jpeg":
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 90})
	case "png":
		err = png.Encode(&out, img)
	case "gif":
		// Single decoded frame, no comments/application extensions or original metadata.
		err = gif.Encode(&out, img, nil)
	default:
		return nil, errors.New("unsupported image")
	}
	return out.Bytes(), err
}
