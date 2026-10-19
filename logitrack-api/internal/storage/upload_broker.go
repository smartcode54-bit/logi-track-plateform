package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagedb"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func qFor(tx pgx.Tx) *storagedb.Queries { return storagedb.New(tx) }
func sessionUploadPrincipal(p *authz.Principal) bool {
	return p != nil && p.SessionID != uuid.Nil && !p.IsMachine() && p.AMR != authz.AMRFirebase
}
func (s *Service) uploadGrant(ctx context.Context, q *storagedb.Queries, row storagedb.FileObject, c Caller, ct string, size int64, opts PutOptions) (PutURL, error) {
	if authz.IsSupport(c.Principal) {
		return PutURL{}, httpx.ErrSupportReadOnly()
	}
	b, err := s.backend(row.StorageBackend)
	if err != nil {
		return PutURL{}, err
	}
	ready, err := q.ContextUploadGrantsReady(ctx)
	if err != nil {
		return PutURL{}, err
	}
	if !ready {
		return b.PresignPut(ctx, object(row), ct, size, s.cfg.PresignPutTTL, opts)
	}
	if sessionUploadPrincipal(c.Principal) {
		platform, e := q.UploadSessionPlatform(ctx, storagedb.UploadSessionPlatformParams{SessionID: c.Principal.SessionID, UserID: c.Principal.UserID})
		if e != nil {
			return PutURL{}, e
		}
		if platform != "web" {
			return b.PresignPut(ctx, object(row), ct, size, s.cfg.PresignPutTTL, opts)
		}
	}
	if !sessionUploadPrincipal(c.Principal) {
		return b.PresignPut(ctx, object(row), ct, size, s.cfg.PresignPutTTL, opts)
	}
	p := c.Principal
	expiry := s.now().Add(s.cfg.PresignPutTTL)
	_, err = q.PutUploadGrant(ctx, storagedb.PutUploadGrantParams{FileID: row.ID, UserID: p.UserID, SessionID: p.SessionID, TenantID: c.TenantID, AuthVersion: p.AuthVersion, ContextVersion: p.ContextVersion, ExpiresAt: expiry})
	if err != nil {
		return PutURL{}, errPermission("upload_context_changed")
	}
	live, err := q.UploadGrantLive(ctx, storagedb.UploadGrantLiveParams{FileID: row.ID, UserID: p.UserID, SessionID: p.SessionID, TenantID: c.TenantID, AuthVersion: p.AuthVersion, ContextVersion: p.ContextVersion, Now: s.now()})
	if err != nil {
		return PutURL{}, err
	}
	if !live {
		return PutURL{}, errPermission("upload_context_changed")
	}
	return PutURL{URL: UploadsPrefix + "/" + row.ID.String() + "/content", Method: http.MethodPut, Headers: map[string]string{"Content-Type": ct, "Content-Length": strconv.FormatInt(size, 10)}, Expires: expiry}, nil
}

// uploadBroker authenticates both backends. No signed PUT is emitted for a human
// web session. Publish only pending bytes while holding the same lock Commit uses.
func (h handlers) uploadBroker(c fiber.Ctx) error {
	cl, err := h.caller(c)
	if err != nil {
		return err
	}
	if authz.IsSupport(cl.Principal) {
		return httpx.ErrSupportReadOnly()
	}
	if !sessionUploadPrincipal(cl.Principal) {
		return httpx.ErrNotFound()
	}
	id, err := uuid.Parse(c.Params("grantId"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	body := c.Body()
	if int64(len(body)) > h.s.cfg.UploadMaxBytes {
		return httpx.NewError(http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, "request body too large")
	}
	err = db.WithSystem(c.Context(), h.s.pool, cl.TenantID, func(tx pgx.Tx) error {
		q := storagedb.New(tx)
		f, err := q.GetFile(c.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		f, err = q.LockFileByKey(c.Context(), f.ObjectKey)
		if err != nil {
			return err
		}
		p := cl.Principal
		allowed, err := q.UploadGrantLive(c.Context(), storagedb.UploadGrantLiveParams{FileID: f.ID, UserID: p.UserID, SessionID: p.SessionID, TenantID: cl.TenantID, AuthVersion: p.AuthVersion, ContextVersion: p.ContextVersion, Now: h.s.now()})
		if err != nil {
			return err
		}
		if !allowed || f.Status != "pending" {
			return errPermission("upload_context_changed")
		}
		if f.SizeBytes == nil || *f.SizeBytes != int64(len(body)) || f.ContentType == nil || *f.ContentType != c.Get(fiber.HeaderContentType) {
			return errPermission("upload_mismatch")
		}
		sum := sha256.Sum256(body)
		if f.Sha256 != nil && *f.Sha256 != hex.EncodeToString(sum[:]) {
			return errPermission("upload_mismatch")
		}
		b, err := h.s.backend(f.StorageBackend)
		if err != nil {
			return err
		}
		// Local Stage/Publish is atomic. S3 Put targets only an immutable pending key;
		// Commit cannot run concurrently under the file row lock.
		if err := b.Put(c.Context(), object(f), bytes.NewReader(body), int64(len(body)), *f.ContentType); err != nil {
			return httpx.ErrUnavailable("storage unavailable").Wrap(err)
		}
		allowed, err = q.UploadGrantLive(c.Context(), storagedb.UploadGrantLiveParams{FileID: f.ID, UserID: p.UserID, SessionID: p.SessionID, TenantID: cl.TenantID, AuthVersion: p.AuthVersion, ContextVersion: p.ContextVersion, Now: h.s.now()})
		if err != nil || !allowed {
			// No domain can commit this revoked grant. Remove staged pending bytes too.
			_ = b.Delete(context.WithoutCancel(c.Context()), object(f))
			if err != nil {
				return err
			}
			return errPermission("upload_context_changed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	return c.SendStatus(http.StatusOK)
}

// BrokerUploadLimit grants only a body-size reservation. Handler authentication
// and grant checks repeat after reading the body and again before publication.
func (s *Service) BrokerUploadLimit(authenticate func(context.Context, string) (*authz.Principal, error)) func(ingress.UploadHead) int64 {
	return func(h ingress.UploadHead) int64 {
		if authenticate == nil || h.Query != "" || h.ContentLength <= 0 || int64(h.ContentLength) > s.cfg.UploadMaxBytes {
			return 0
		}
		raw, ok := strings.CutPrefix(h.Path, UploadsPrefix+"/")
		if !ok {
			return 0
		}
		raw, ok = strings.CutSuffix(raw, "/content")
		if !ok {
			return 0
		}
		id, err := uuid.Parse(raw)
		if err != nil || id.String() != raw {
			return 0
		}
		bearer, ok := strings.CutPrefix(h.Authorization, "Bearer ")
		if !ok || bearer == "" || strings.ContainsAny(bearer, " \t\r\n") {
			return 0
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		p, err := authenticate(ctx, bearer)
		if err != nil || !sessionUploadPrincipal(p) || authz.IsSupport(p) {
			return 0
		}
		allowed := false
		err = db.WithSystem(ctx, s.pool, p.EffectiveTenant(), func(tx pgx.Tx) error {
			q := storagedb.New(tx)
			row, err := q.GetFile(ctx, id)
			if err != nil {
				return err
			}
			if row.Status != "pending" || row.SizeBytes == nil || *row.SizeBytes != int64(h.ContentLength) || row.ContentType == nil || *row.ContentType != h.ContentType {
				return nil
			}
			allowed, err = q.UploadGrantLive(ctx, storagedb.UploadGrantLiveParams{FileID: id, UserID: p.UserID, SessionID: p.SessionID, TenantID: p.EffectiveTenant(), AuthVersion: p.AuthVersion, ContextVersion: p.ContextVersion, Now: s.now()})
			return err
		})
		if err != nil || !allowed {
			return 0
		}
		return int64(h.ContentLength)
	}
}
