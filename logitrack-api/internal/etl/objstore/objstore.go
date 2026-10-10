// Package objstore is the ETL's view of the S3-compatible store (MinIO locally, R74): put, get and stat on the
// S3_BUCKET of the environment, for dumps under etl/dumps/{ts}/ and for media copied from GCS under the identical
// key. It talks to S3_ENDPOINT with the scoped S3_ACCESS_KEY_ID; it never signs client URLs.
package objstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound: the object does not exist.
var ErrNotFound = errors.New("objstore: object not found")

// Config is the S3 side of the ETL environment (main spec §16.1).
type Config struct {
	Endpoint        string // S3_ENDPOINT: host[:port] or an http(s) origin
	UseSSL          bool   // S3_USE_SSL (a scheme in Endpoint wins)
	Region          string // S3_REGION
	AccessKeyID     string // S3_ACCESS_KEY_ID
	SecretAccessKey string // S3_SECRET_ACCESS_KEY
	PathStyle       bool   // S3_USE_PATH_STYLE
}

// S3 is a minio-go client.
type S3 struct{ c *minio.Client }

// Info describes a stored object.
type Info struct {
	Size        int64
	ContentType string
}

// New builds the client without I/O. Errors never echo the endpoint or the credentials.
func New(cfg Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Region == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("objstore: S3_ENDPOINT, S3_REGION, S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY are required")
	}
	host, secure := cfg.Endpoint, cfg.UseSSL
	if strings.Contains(cfg.Endpoint, "://") {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return nil, errors.New("objstore: S3_ENDPOINT must be host[:port] or an http(s) origin without path or credentials")
		}
		host, secure = u.Host, u.Scheme == "https"
	} else if strings.ContainsAny(host, "/?#@") {
		return nil, errors.New("objstore: S3_ENDPOINT must be host[:port] or an http(s) origin")
	}
	lookup := minio.BucketLookupDNS
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	c, err := minio.New(host, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: secure, Region: cfg.Region, BucketLookup: lookup, MaxRetries: 3})
	if err != nil {
		return nil, errors.New("objstore: cannot build the S3 client")
	}
	return &S3{c: c}, nil
}

func notFound(err error) bool {
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NotFound", "NoSuchObject":
		return true
	}
	return false
}

// Put stores an object (size -1: unknown, streamed in parts).
func (s *S3) Put(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error {
	if _, err := s.c.PutObject(ctx, bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType}); err != nil {
		return fmt.Errorf("objstore: put %s: %w", key, err)
	}
	return nil
}

// Get opens an object; the caller closes it.
func (s *S3) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	if _, err := s.Stat(ctx, bucket, key); err != nil {
		return nil, err
	}
	o, err := s.c.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("objstore: get %s: %w", key, err)
	}
	return o, nil
}

// Stat reports size and type; ErrNotFound when the object does not exist.
func (s *S3) Stat(ctx context.Context, bucket, key string) (Info, error) {
	oi, err := s.c.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if notFound(err) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, fmt.Errorf("objstore: stat %s: %w", key, err)
	}
	return Info{Size: oi.Size, ContentType: oi.ContentType}, nil
}

// List returns the keys under prefix.
func (s *S3) List(ctx context.Context, bucket, prefix string) ([]string, error) {
	var keys []string
	for o := range s.c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return nil, fmt.Errorf("objstore: list %s: %w", prefix, o.Err)
		}
		keys = append(keys, o.Key)
	}
	return keys, nil
}
