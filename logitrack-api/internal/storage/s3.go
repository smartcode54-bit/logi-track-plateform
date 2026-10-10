package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/cors"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
	"github.com/rs/zerolog"
)

// S3Config configures the S3 backend (main spec §9.1, §16.1, R74).
type S3Config struct {
	// Endpoint is S3_ENDPOINT, the server-side address (compose http://minio:9000); never signed into client URLs.
	// A scheme decides TLS; without one UseSSL does.
	Endpoint string
	UseSSL   bool
	// PresignEndpoint is S3_PRESIGN_ENDPOINT, the origin browsers and the APK reach (locally http://localhost:9000,
	// deployed https://{MEDIA_DOMAIN}): presigned URLs are signed for it, offline. Empty: no URL can be signed
	// (worker).
	PresignEndpoint string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	// PathStyle is S3_USE_PATH_STYLE (true for MinIO).
	PathStyle bool
	// PublicBaseURL is S3_PUBLIC_BASE_URL, the base of public-bucket object URLs (the APK link).
	PublicBaseURL string
	// Buckets are S3_BUCKET (private) and S3_PUBLIC_BUCKET (app_releases/ only).
	Bucket, PublicBucket string
	// CORSOrigins is CORS_ALLOWED_ORIGINS: the browser origins of the private bucket's CORS.
	CORSOrigins []string
}

// S3 is the minio-go backend: one client for server-side calls at S3_ENDPOINT and one that only signs URLs for
// S3_PRESIGN_ENDPOINT. Region is fixed, so signing never asks the server for the bucket location.
type S3 struct {
	cfg    S3Config
	client *minio.Client
	signer *minio.Client
	public *url.URL
}

var _ Backend = (*S3)(nil)

// splitEndpoint turns S3_ENDPOINT or S3_PRESIGN_ENDPOINT into minio-go's host[:port] and TLS flag. An origin with a
// path, query or credentials is refused (minio-go endpoints are bare hosts).
func splitEndpoint(name, raw string, useSSL bool) (host string, secure bool, err error) {
	if !strings.Contains(raw, "://") {
		if raw == "" || strings.ContainsAny(raw, "/?#@") {
			return "", false, fmt.Errorf("storage: %s must be host[:port] or an http(s) origin", name)
		}
		return raw, useSSL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false, fmt.Errorf("storage: %s must be an http(s) origin without path or credentials", name)
	}
	return u.Host, u.Scheme == "https", nil
}

// NewS3 builds both clients without network I/O.
func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Region == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" || cfg.Bucket == "" || cfg.PublicBucket == "" {
		return nil, errors.New("storage: S3_REGION, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY, S3_BUCKET and S3_PUBLIC_BUCKET are required for the s3 backend")
	}
	lookup := minio.BucketLookupDNS
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	newClient := func(name, endpoint string, useSSL bool) (*minio.Client, error) {
		host, secure, err := splitEndpoint(name, endpoint, useSSL)
		if err != nil {
			return nil, err
		}
		c, err := minio.New(host, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
			Secure: secure, Region: cfg.Region, BucketLookup: lookup,
			// Bounded retries: with storage down a request fails within seconds instead of retrying ten times.
			MaxRetries: 3,
		})
		if err != nil {
			return nil, fmt.Errorf("storage: %s: %w", name, err)
		}
		return c, nil
	}
	s := &S3{cfg: cfg}
	var err error
	if s.client, err = newClient("S3_ENDPOINT", cfg.Endpoint, cfg.UseSSL); err != nil {
		return nil, err
	}
	if cfg.PresignEndpoint != "" {
		if !strings.Contains(cfg.PresignEndpoint, "://") {
			return nil, errors.New("storage: S3_PRESIGN_ENDPOINT must be an http(s) origin")
		}
		if s.signer, err = newClient("S3_PRESIGN_ENDPOINT", cfg.PresignEndpoint, false); err != nil {
			return nil, err
		}
	}
	if cfg.PublicBaseURL != "" {
		u, err := url.Parse(strings.TrimSuffix(cfg.PublicBaseURL, "/"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" {
			return nil, errors.New("storage: S3_PUBLIC_BASE_URL must be an absolute http(s) URL without query")
		}
		s.public = u
	}
	return s, nil
}

// Name implements Backend.
func (s *S3) Name() string { return BackendS3 }

func (s *S3) signing() (*minio.Client, error) {
	if s.signer == nil {
		return nil, errors.New("storage: S3_PRESIGN_ENDPOINT is not set")
	}
	return s.signer, nil
}

// PresignPut implements Backend: SigV4 PUT signed for S3_PRESIGN_ENDPOINT with Content-Type among the signed
// headers, so the object is stored with the type the presign declared.
func (s *S3) PresignPut(ctx context.Context, o Object, contentType string, ttl time.Duration, _ PutOptions) (PutURL, error) {
	c, err := s.signing()
	if err != nil {
		return PutURL{}, err
	}
	exp := time.Now().Add(ttl)
	u, err := c.PresignHeader(ctx, http.MethodPut, o.Bucket, o.Key, ttl, nil, http.Header{"Content-Type": {contentType}})
	if err != nil {
		return PutURL{}, err
	}
	return PutURL{URL: u.String(), Method: http.MethodPut, Headers: map[string]string{"Content-Type": contentType}, Expires: exp}, nil
}

// PresignGet implements Backend.
func (s *S3) PresignGet(ctx context.Context, o Object, ttl time.Duration, opts GetOptions) (string, time.Time, error) {
	c, err := s.signing()
	if err != nil {
		return "", time.Time{}, err
	}
	var params url.Values
	if opts.ContentDisposition != "" {
		params = url.Values{"response-content-disposition": {opts.ContentDisposition}}
	}
	exp := time.Now().Add(ttl)
	u, err := c.PresignedGetObject(ctx, o.Bucket, o.Key, ttl, params)
	if err != nil {
		return "", time.Time{}, err
	}
	return u.String(), exp, nil
}

// PublicURL implements Backend: S3_PUBLIC_BASE_URL/<key> for app_releases/.
func (s *S3) PublicURL(o Object) (string, error) {
	if !IsPublicKey(o.Key) {
		return "", ErrNotPublic
	}
	if s.public == nil {
		return "", errors.New("storage: S3_PUBLIC_BASE_URL is not set")
	}
	return s.public.String() + "/" + escapeKey(o.Key), nil
}

func notFound(err error) bool {
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NotFound", "NoSuchObject":
		return true
	}
	return false
}

// Stat implements Backend.
func (s *S3) Stat(ctx context.Context, o Object) (Info, error) {
	oi, err := s.client.StatObject(ctx, o.Bucket, o.Key, minio.StatObjectOptions{})
	if notFound(err) {
		return Info{}, ErrObjectNotFound
	}
	if err != nil {
		return Info{}, err
	}
	return Info{Size: oi.Size, ContentType: oi.ContentType}, nil
}

// Put implements Backend.
func (s *S3) Put(ctx context.Context, o Object, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, o.Bucket, o.Key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Delete implements Backend (S3 answers 204 for a missing key too).
func (s *S3) Delete(ctx context.Context, o Object) error {
	err := s.client.RemoveObject(ctx, o.Bucket, o.Key, minio.RemoveObjectOptions{})
	if notFound(err) {
		return nil
	}
	return err
}

// Check implements Backend: the private bucket exists (Appendix B §B.2.1 readiness).
func (s *S3) Check(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.cfg.Bucket)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("storage: bucket missing")
	}
	return nil
}

// publicPolicy grants anonymous GetObject under app_releases/ of the public bucket only: no listing and no
// sibling prefix (R23, ADR 0007; the same document deploy/minio/minio-init.sh applies).
func publicPolicy(bucket string) string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},` +
		`"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::` + bucket + `/` + PublicPrefix + `*"]}]}`
}

// CORSRules is the private bucket's CORS (main spec §9.1): browser PUT and GET of presigned URLs from
// CORS_ALLOWED_ORIGINS, max-age 3600.
func CORSRules(origins []string) []cors.Rule {
	return []cors.Rule{{
		AllowedOrigin: origins,
		AllowedMethod: []string{http.MethodGet, http.MethodHead, http.MethodPut},
		AllowedHeader: []string{"Content-Type", "Content-Length", "Content-Disposition", "x-amz-*"},
		ExposeHeader:  []string{"ETag"},
		MaxAgeSeconds: 3600,
	}}
}

// Bootstrap asserts the buckets idempotently (main spec §9.1): both exist, the public policy covers app_releases/
// only, the private bucket has no anonymous access, its CORS comes from CORS_ALLOWED_ORIGINS and cache/ expires
// after 30 days. Each step runs even when an earlier one failed; the errors are returned joined. A server that does
// not implement bucket CORS (some MinIO releases) answers NotImplemented: compose also sets the server-level
// MINIO_API_CORS_ALLOW_ORIGIN from the same variable, so that step is logged and skipped.
func (s *S3) Bootstrap(ctx context.Context, log zerolog.Logger) error {
	var errs []error
	step := func(name string, err error) {
		if err != nil {
			log.Warn().Err(err).Str("step", name).Msg("storage bootstrap step failed")
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	for _, b := range []string{s.cfg.Bucket, s.cfg.PublicBucket} {
		ok, err := s.client.BucketExists(ctx, b)
		if err == nil && !ok {
			err = s.client.MakeBucket(ctx, b, minio.MakeBucketOptions{Region: s.cfg.Region})
			if code := minio.ToErrorResponse(err).Code; code == "BucketAlreadyOwnedByYou" || code == "BucketAlreadyExists" {
				err = nil
			}
		}
		step("bucket "+b, err)
	}
	step("public policy", s.client.SetBucketPolicy(ctx, s.cfg.PublicBucket, publicPolicy(s.cfg.PublicBucket)))
	if err := s.client.SetBucketPolicy(ctx, s.cfg.Bucket, ""); err != nil && minio.ToErrorResponse(err).Code != "NoSuchBucketPolicy" {
		step("private policy", err)
	}
	if len(s.cfg.CORSOrigins) > 0 {
		err := s.client.SetBucketCors(ctx, s.cfg.Bucket, cors.NewConfig(CORSRules(s.cfg.CORSOrigins)))
		if code := minio.ToErrorResponse(err).Code; code == "NotImplemented" {
			log.Info().Msg("storage: the server has no bucket CORS API; the server-level CORS setting applies")
			err = nil
		}
		step("private bucket CORS", err)
	}
	lc := lifecycle.NewConfiguration()
	lc.Rules = []lifecycle.Rule{{ID: "expire-cache", Status: "Enabled",
		RuleFilter: lifecycle.Filter{Prefix: "cache/"}, Expiration: lifecycle.Expiration{Days: 30}}}
	step("cache/ lifecycle", s.client.SetBucketLifecycle(ctx, s.cfg.Bucket, lc))
	return errors.Join(errs...)
}
