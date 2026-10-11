package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagedb"
)

// Public uploads (main spec §9.7): the APKs that cmd/release publishes into the public bucket under app_releases/.
// No client presigns them through POST /v1/uploads/presign (purpose apk is server-side): the release routes authorise
// the caller (a release_publisher API key or a person with security:manage_mobile_release, internal listener only)
// and then call PresignPublic and, in the release transaction, CommitPublic.

// MaxReleaseBytes bounds one APK (the legacy builds are about 80 MB). It replaces UPLOAD_MAX_BYTES for keys under
// PublicPrefix on the API upload route (PUT /v1/uploads/local/{key}, the local backend), whose body is held in memory
// before it is written to disk like every local upload; S3 receives the body directly.
const MaxReleaseBytes = 200 << 20

// ReleaseContentType is the media type of an APK.
const ReleaseContentType = "application/vnd.android.package-archive"

// PublicCacheControl is the Cache-Control of public objects: an APK key embeds its version and never changes.
const PublicCacheControl = "public, max-age=31536000, immutable"

// ErrCommitted is PresignPublic's answer for a key whose object is already committed (a published release is
// immutable).
var ErrCommitted = errors.New("storage: the key holds a committed object")

// PendingUploadError is PresignPublic's answer for a key that holds a pending upload and Replace was not asked.
type PendingUploadError struct {
	ExpiresAt time.Time
}

func (e *PendingUploadError) Error() string { return "storage: the key holds a pending upload" }

// PublicUpload is a server-authorised upload into the public bucket.
type PublicUpload struct {
	// Key is the object key, under PublicPrefix.
	Key string
	// Purpose is the file_objects purpose (apk).
	Purpose     string
	ContentType string
	SizeBytes   int64
	// SHA256 is the declared digest (lowercase hex); CommitPublic verifies the stored object against it.
	SHA256 string
	// UploadedBy is the person who asked; nil for an API key (no users row).
	UploadedBy *uuid.UUID
	// Replace discards an upload of Key that was never committed (a pending row, or an object without a row) before
	// signing a new one; without it a pending upload is a *PendingUploadError.
	Replace bool
	// Metadata are stored with the object where the backend keeps metadata (S3: x-amz-meta-{name}).
	Metadata map[string]string
}

// PublicPresigned is the answer to PresignPublic: the upload (send Method to URL with exactly Headers and a body of
// the declared size) and the unsigned URL the object is served at once it is uploaded.
type PublicPresigned struct {
	Presigned
	PublicURL string `json:"publicUrl"`
}

// PublicObjectURL is the unsigned URL of a public object on a backend (S3_PUBLIC_BASE_URL or
// LOCAL_MEDIA_PUBLIC_BASE_URL followed by the key); an error when the backend has no public base URL.
func (s *Service) PublicObjectURL(backend, bucket, key string) (string, error) {
	b, err := s.backend(backend)
	if err != nil {
		return "", err
	}
	return b.PublicURL(Object{Bucket: bucket, Key: key})
}

// PublicBucket is S3_PUBLIC_BUCKET (file_objects.bucket of public objects).
func (s *Service) PublicBucket() string { return s.cfg.PublicBucket }

// PresignPublic registers a pending public upload of in.Key on the active backend and signs its PUT, valid for
// S3_PRESIGN_PUT_TTL. On the local backend the URL is a path on the internal listener (PUT /v1/uploads/local/{key},
// which cmd/release resolves against GO_API_INTERNAL_URL); on S3 it is signed for S3_PRESIGN_ENDPOINT with
// Cache-Control (PublicCacheControl) and in.Metadata among the signed headers. A committed key is ErrCommitted; a
// pending one is a *PendingUploadError unless in.Replace, which deletes the old object and row first (so an upload
// that crashed before its publish can be redone; nothing that was published is ever replaced). The public URL is
// checked before anything is written: without S3_PUBLIC_BASE_URL (s3) the APK could never be linked, which is 503.
func (s *Service) PresignPublic(ctx context.Context, in PublicUpload) (*PublicPresigned, error) {
	if !IsPublicKey(in.Key) || ValidateKey(in.Key) != nil {
		return nil, fmt.Errorf("storage: %q is not a public key", in.Key)
	}
	if in.SizeBytes <= 0 || !sha256Rx.MatchString(in.SHA256) {
		return nil, errors.New("storage: a public upload needs its size and sha256")
	}
	b, err := s.backend(s.cfg.Active)
	if err != nil {
		return nil, err
	}
	public, err := b.PublicURL(Object{Bucket: s.cfg.PublicBucket, Key: in.Key})
	if err != nil {
		return nil, httpx.ErrUnavailable("the storage backend has no public base URL").
			WithDetails(map[string]any{"reason": "public_url_unconfigured", "storageBackend": b.Name()}).Wrap(err)
	}
	ct := normalizeType(in.ContentType)
	var out *PublicPresigned
	err = db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		q := storagedb.New(tx)
		old, err := q.LockFileByKey(ctx, in.Key)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case old.Status != "pending":
			return ErrCommitted
		case !in.Replace:
			return &PendingUploadError{ExpiresAt: deref(old.ExpiresAt).UTC()}
		default:
			ob, err := s.backend(old.StorageBackend)
			if err != nil {
				return err
			}
			if err := ob.Delete(ctx, object(old)); err != nil {
				return httpx.ErrUnavailable("storage cannot discard the earlier upload").Wrap(err)
			}
			if err := q.DeleteFile(ctx, old.ID); err != nil {
				return err
			}
		}
		if in.Replace {
			// An object without a row (its row was collected while the object stayed, or a manual copy) would make
			// the create-only S3 PUT fail with 412: the key holds nothing committed, so it goes.
			if err := b.Delete(ctx, Object{Bucket: s.cfg.PublicBucket, Key: in.Key}); err != nil {
				return httpx.ErrUnavailable("storage cannot discard the earlier upload").Wrap(err)
			}
		}
		now := s.now()
		row, err := q.InsertPendingPublic(ctx, storagedb.InsertPendingPublicParams{
			Bucket: s.cfg.PublicBucket, ObjectKey: in.Key, Purpose: in.Purpose, ContentType: ct, SizeBytes: in.SizeBytes,
			Sha256: in.SHA256, UploadedBy: in.UploadedBy, ExpiresAt: now.Add(PendingTTL), StorageBackend: b.Name(),
		})
		if err != nil {
			return err
		}
		put, err := b.PresignPut(ctx, object(row), ct, in.SizeBytes, s.cfg.PresignPutTTL,
			PutOptions{APIPath: true, CacheControl: PublicCacheControl, Metadata: in.Metadata})
		if err != nil {
			return httpx.ErrUnavailable("storage cannot sign the upload").Wrap(err)
		}
		out = &PublicPresigned{PublicURL: public, Presigned: Presigned{Key: row.ObjectKey, URL: put.URL, Method: put.Method,
			Headers: put.Headers, ExpiresAt: put.Expires.UTC(), StorageBackend: b.Name()}}
		return nil
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		// A concurrent presign of the same key inserted first.
		return nil, &PendingUploadError{ExpiresAt: s.now().Add(PendingTTL).UTC()}
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PublicCommitted is a committed public object.
type PublicCommitted struct {
	Committed
	SHA256         string
	Bucket         string
	StorageBackend string
}

// CommitPublic commits the pending public upload of key inside tx, the caller's WithSystem transaction (the
// release): it locks the row, Stats the object on the row's backend (it exists, its size and content type are the
// declared ones) and verifies its sha256 against the declared digest. The local backend recorded the digest when it
// stored the bytes; S3 keeps none of a presigned PUT, so the object is read back and hashed (an APK is read once per
// release). The row becomes committed, owned by (ownerKind of its purpose, ownerID). Failures are 422
// invalid_argument on field "key" with the reasons of Commit (not_found, purpose_mismatch, already_committed,
// upload_missing, size_mismatch, content_type_mismatch, checksum_mismatch); the caller rolls back.
func (s *Service) CommitPublic(ctx context.Context, tx pgx.Tx, key, purpose string, ownerID uuid.UUID) (PublicCommitted, error) {
	bad := func(reason string, params map[string]any) error {
		if params == nil {
			params = map[string]any{}
		}
		params["key"] = key
		return violation("key", reason, params)
	}
	if !IsPublicKey(key) || ValidateKey(key) != nil {
		return PublicCommitted{}, bad("not_found", nil)
	}
	q := storagedb.New(tx)
	row, err := q.LockFileByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (row.Visibility != "public" || row.Bucket != s.cfg.PublicBucket)) {
		return PublicCommitted{}, bad("not_found", nil)
	}
	if err != nil {
		return PublicCommitted{}, err
	}
	if row.Purpose != purpose {
		return PublicCommitted{}, bad("purpose_mismatch", map[string]any{"purpose": row.Purpose})
	}
	if row.Status != "pending" {
		return PublicCommitted{}, bad("already_committed", nil)
	}
	b, err := s.backend(row.StorageBackend)
	if err != nil {
		return PublicCommitted{}, err
	}
	o := object(row)
	info, err := b.Stat(ctx, o)
	if errors.Is(err, ErrObjectNotFound) {
		return PublicCommitted{}, bad("upload_missing", nil)
	}
	if err != nil {
		return PublicCommitted{}, httpx.ErrUnavailable("storage unavailable").Wrap(err)
	}
	if want := deref(row.SizeBytes); info.Size != want {
		return PublicCommitted{}, bad("size_mismatch", map[string]any{"expected": want, "actual": info.Size})
	}
	if want := deref(row.ContentType); want != "" && normalizeType(info.ContentType) != want {
		return PublicCommitted{}, bad("content_type_mismatch", map[string]any{"expected": want, "actual": info.ContentType})
	}
	sum := info.SHA256
	if sum == "" {
		if sum, err = s.hashObject(ctx, b, o, info.Size); err != nil {
			return PublicCommitted{}, err
		}
	}
	if want := deref(row.Sha256); sum != want {
		return PublicCommitted{}, bad("checksum_mismatch", nil)
	}
	if err := q.CommitFile(ctx, storagedb.CommitFileParams{
		CommittedAt: s.now(), OwnerKind: Purposes[row.Purpose].OwnerKind, OwnerID: ownerID,
		ContentType: normalizeType(info.ContentType), SizeBytes: info.Size, ID: row.ID,
	}); err != nil {
		return PublicCommitted{}, err
	}
	return PublicCommitted{
		Committed: Committed{ID: row.ID, Key: row.ObjectKey, ContentType: normalizeType(info.ContentType), SizeBytes: info.Size},
		SHA256:    sum, Bucket: row.Bucket, StorageBackend: row.StorageBackend,
	}, nil
}

// hashObject reads an object back and returns its sha256 (at most size bytes are read: a longer object cannot match
// the size Stat reported).
func (s *Service) hashObject(ctx context.Context, b Backend, o Object, size int64) (string, error) {
	r, ok := b.(Reader)
	if !ok {
		return "", fmt.Errorf("storage: backend %s cannot read objects back", b.Name())
	}
	rc, err := r.Read(ctx, o)
	if errors.Is(err, ErrObjectNotFound) {
		return "", violation("key", "upload_missing", map[string]any{"key": o.Key})
	}
	if err != nil {
		return "", httpx.ErrUnavailable("storage unavailable").Wrap(err)
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(rc, size)); err != nil {
		return "", httpx.ErrUnavailable("storage unavailable").Wrap(err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
