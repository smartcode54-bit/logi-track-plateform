// Package storage is the object store of main spec §9: the file_objects registry (R1), presigned uploads and
// their commit inside the entity transaction, short-lived download URLs, the hourly storage.gc and the evidence
// token primitive (R30, R47).
//
// Two backends sit behind one interface (owner addition to T11, §9.11): "s3" (minio-go against MinIO or any
// S3-compatible service, two buckets) and "local" (files under LOCAL_MEDIA_DIR, HMAC-signed URLs served by the api
// under /media). STORAGE_BACKEND picks the backend of new uploads; every read, commit and delete dispatches on the
// row's own file_objects.storage_backend, so objects written under "local" keep working after the switch to "s3".
//
// The package runs its statements under db.WithSystem (it is on the allow-list of Appendix C §C.3.2): scope
// principals and evidence viewers never read file_objects directly; the service resolves a key only after the
// caller's rights were checked (a file is readable iff its referencing row is, C.3.5).
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// Backend names (file_objects.storage_backend, migration 0011).
const (
	BackendLocal = "local"
	BackendS3    = "s3"
)

// Object addresses one stored object: the bucket recorded in file_objects.bucket (the S3_BUCKET or
// S3_PUBLIC_BUCKET value; a label only on the local backend) and the key.
type Object struct {
	Bucket string
	Key    string
}

// Info is what a backend knows about a stored object.
type Info struct {
	Size        int64
	ContentType string
	// SHA256 is the lowercase hex digest when the backend computed it at upload (local); "" otherwise.
	SHA256 string
}

// PutURL is a presigned upload: the client sends Method to URL with exactly Headers and a body of the declared
// size (a Content-Length, never chunked) before Expires.
type PutURL struct {
	URL     string
	Method  string
	Headers map[string]string
	Expires time.Time
}

// GetOptions tune a presigned download.
type GetOptions struct {
	// ContentDisposition, when set, is returned as the Content-Disposition of the download
	// (documents: attachment with the real file name, main spec §9.5).
	ContentDisposition string
}

// PutOptions tune a presigned upload. APIPath asks the local backend for a path on the Go API (web through the
// BFF) instead of an absolute URL under LOCAL_MEDIA_PUBLIC_BASE_URL (driver app); the S3 backend ignores it.
type PutOptions struct {
	APIPath bool
}

// Backend is one storage implementation. Every method takes the row's bucket and key; a key has passed
// ValidateKey before it reaches a backend.
type Backend interface {
	// Name is BackendLocal or BackendS3.
	Name() string
	// PresignPut signs an upload of contentType and exactly size bytes (the declared size) valid for ttl. The body
	// is bound to that size and the object can be created once (S3: signed Content-Length and If-None-Match: *;
	// local: signed Content-Length and the pending-row check).
	PresignPut(ctx context.Context, o Object, contentType string, size int64, ttl time.Duration, opts PutOptions) (PutURL, error)
	// PresignGet signs a download valid for ttl and returns the URL and its expiry.
	PresignGet(ctx context.Context, o Object, ttl time.Duration, opts GetOptions) (string, time.Time, error)
	// PublicURL is the unsigned URL of an object under PublicPrefix.
	PublicURL(o Object) (string, error)
	// Stat reports size and content type; ErrObjectNotFound when the object does not exist.
	Stat(ctx context.Context, o Object) (Info, error)
	// Put stores an object server-side (rendered documents, seed, tests).
	Put(ctx context.Context, o Object, r io.Reader, size int64, contentType string) error
	// Delete removes an object; a missing object is not an error.
	Delete(ctx context.Context, o Object) error
	// Check is the readiness probe of the backend (bucket reachable, directory writable).
	Check(ctx context.Context) error
}

// Errors shared by the backends.
var (
	ErrObjectNotFound = errors.New("storage: object not found")
	ErrTooLarge       = errors.New("storage: object larger than the upload limit")
	ErrNotPublic      = errors.New("storage: only keys under " + PublicPrefix + " have a public URL")
)
