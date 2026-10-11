package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagedb"
)

// ServerObject is an object a server-side writer stores (main spec §9.9: the rendered statement documents and reports
// of documents.render): the purpose (a server-only one: statement_document, report), the key, the owning tenant and
// entity, and the bytes.
type ServerObject struct {
	Purpose     string
	Key         string
	TenantID    *uuid.UUID
	OwnerID     uuid.UUID
	ContentType string
	Body        []byte
}

// StoredObject is a ServerObject written to the active backend, ready to be registered in file_objects.
type StoredObject struct {
	ServerObject
	Bucket  string
	Backend string
	SHA256  string
}

// ErrNotServerPurpose refuses PutServerObject for a purpose a client presigns or an unknown one.
var ErrNotServerPurpose = errors.New("storage: not a server-side purpose")

// PutServerObject writes the bytes of o to the active backend under o.Key in the private bucket (an earlier attempt at
// the same key is overwritten). It does not touch file_objects: the caller registers the object with RegisterServerObject
// inside the transaction that links it, so a failed transaction leaves at worst an unreferenced object, never a row
// without bytes. Writers use a fresh key per attempt (documents.render puts a render id in the key), so a committed
// object is never overwritten.
func (s *Service) PutServerObject(ctx context.Context, o ServerObject) (StoredObject, error) {
	p, ok := Purposes[o.Purpose]
	if !ok || p.Presign || o.Purpose == "apk" {
		return StoredObject{}, fmt.Errorf("%w: %q", ErrNotServerPurpose, o.Purpose)
	}
	if err := ValidateKey(o.Key); err != nil || IsPublicKey(o.Key) {
		return StoredObject{}, fmt.Errorf("storage: server object key %q: %w", o.Key, ErrInvalidKey)
	}
	if o.OwnerID == uuid.Nil || o.ContentType == "" {
		return StoredObject{}, errors.New("storage: a server object needs its owner and content type")
	}
	b, err := s.backend(s.cfg.Active)
	if err != nil {
		return StoredObject{}, err
	}
	if err := b.Put(ctx, Object{Bucket: s.cfg.Bucket, Key: o.Key}, bytes.NewReader(o.Body), int64(len(o.Body)), o.ContentType); err != nil {
		return StoredObject{}, fmt.Errorf("storage: put %s: %w", o.Key, err)
	}
	sum := sha256.Sum256(o.Body)
	return StoredObject{ServerObject: o, Bucket: s.cfg.Bucket, Backend: b.Name(), SHA256: hex.EncodeToString(sum[:])}, nil
}

// RegisterServerObject inserts the committed file_objects row of a stored object in tx (a system transaction: the
// caller has already decided the write) and returns its id. The owner kind comes from the purpose.
func RegisterServerObject(ctx context.Context, tx pgx.Tx, o StoredObject, at time.Time) (uuid.UUID, error) {
	return storagedb.New(tx).InsertServerObject(ctx, storagedb.InsertServerObjectParams{
		Bucket: o.Bucket, ObjectKey: o.Key, TenantID: o.TenantID, Purpose: o.Purpose,
		OwnerKind: Purposes[o.Purpose].OwnerKind, OwnerID: o.OwnerID, ContentType: o.ContentType,
		SizeBytes: int64(len(o.Body)), Sha256: o.SHA256, CommittedAt: at, StorageBackend: o.Backend,
	})
}

// DeleteServerObject removes the bytes of a stored object whose registration failed (best effort).
func (s *Service) DeleteServerObject(ctx context.Context, o StoredObject) error {
	b, err := s.backend(o.Backend)
	if err != nil {
		return err
	}
	return b.Delete(ctx, Object{Bucket: o.Bucket, Key: o.Key})
}

// MaxServerReadBytes bounds ReadFile: the images a renderer embeds (a company stamp, a run-sheet photo).
const MaxServerReadBytes = 25 << 20

// ReadFile reads a committed file server-side (a renderer embedding a company stamp or a trip photo): its bytes and
// content type. A row that is not committed, does not exist, or whose object is gone is ErrObjectNotFound; a file above
// MaxServerReadBytes is ErrTooLarge. It reads the row in a system transaction on its own pool connection: call it
// outside any other transaction, after the caller has decided the read (main spec §9.9).
func (s *Service) ReadFile(ctx context.Context, fileID uuid.UUID) ([]byte, string, error) {
	var row storagedb.FileObject
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		var err error
		row, err = storagedb.New(tx).GetFile(ctx, fileID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrObjectNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if row.Status != "committed" {
		return nil, "", ErrObjectNotFound
	}
	if row.SizeBytes != nil && *row.SizeBytes > MaxServerReadBytes {
		return nil, "", ErrTooLarge
	}
	b, err := s.backend(row.StorageBackend)
	if err != nil {
		return nil, "", err
	}
	r, ok := b.(Reader)
	if !ok {
		return nil, "", fmt.Errorf("storage: backend %s cannot read objects", b.Name())
	}
	rc, err := r.Read(ctx, object(row))
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, MaxServerReadBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > MaxServerReadBytes {
		return nil, "", ErrTooLarge
	}
	return body, deref(row.ContentType), nil
}

// SignedDownload is SignedURL with download options (a Content-Disposition naming the file the browser saves): the URL
// of a committed file the caller has already decided may be read, valid for ttl capped by its purpose (documents
// 15 min). Same contract as SignedFile: call it after the request transaction has closed.
func (s *Service) SignedDownload(ctx context.Context, fileID uuid.UUID, ttl time.Duration, opts GetOptions) (string, time.Time, error) {
	var row storagedb.FileObject
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		var err error
		row, err = storagedb.New(tx).GetFile(ctx, fileID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, ErrObjectNotFound
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if row.Status != "committed" {
		return "", time.Time{}, ErrObjectNotFound
	}
	return s.signGet(ctx, row, s.readTTL(row, ttl), opts)
}
