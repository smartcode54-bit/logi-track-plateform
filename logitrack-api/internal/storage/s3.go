package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
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

var (
	_ Backend = (*S3)(nil)
	_ Reader  = (*S3)(nil)
)

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

// PresignPut implements Backend: SigV4 PUT signed for S3_PRESIGN_ENDPOINT with Content-Type, Content-Length and
// If-None-Match: * among the signed headers. The object is stored with the declared type, the body must have the
// declared size (another length, or a chunked body, fails the signature or gets 411), and the PUT can only create
// the object: once it exists (uploaded, or committed) the same URL gets 412 instead of replacing it. Clients send
// the returned headers; Content-Length is their body's own, which browsers and HTTP clients set themselves.
func (s *S3) PresignPut(ctx context.Context, o Object, contentType string, size int64, ttl time.Duration, _ PutOptions) (PutURL, error) {
	c, err := s.signing()
	if err != nil {
		return PutURL{}, err
	}
	if size <= 0 {
		return PutURL{}, errors.New("storage: an upload needs its size")
	}
	exp := time.Now().Add(ttl)
	u, err := c.PresignHeader(ctx, http.MethodPut, o.Bucket, o.Key, ttl, nil, http.Header{
		"Content-Type":   {contentType},
		"Content-Length": {strconv.FormatInt(size, 10)},
		"If-None-Match":  {"*"},
	})
	if err != nil {
		return PutURL{}, err
	}
	return PutURL{URL: u.String(), Method: http.MethodPut, Expires: exp,
		Headers: map[string]string{"Content-Type": contentType, "If-None-Match": "*"}}, nil
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

// Read implements Reader.
func (s *S3) Read(ctx context.Context, o Object) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, o.Bucket, o.Key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := obj.Stat(); err != nil { // GetObject is lazy: the first call reaches the server
		_ = obj.Close()
		if notFound(err) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	return obj, nil
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

// appReleasesSid names the one statement of the public bucket's policy the api owns.
const appReleasesSid = "LogiTrackPublicAppReleases"

// lifecycleCacheRule is the one lifecycle rule of the private bucket the api owns.
const lifecycleCacheRule = "expire-cache"

// publicStatement grants anonymous GetObject under app_releases/ of the public bucket only: no listing and no
// sibling prefix (R23, ADR 0007; the grant deploy/minio/minio-init.sh applies).
func publicStatement(bucket string) map[string]any {
	return map[string]any{"Sid": appReleasesSid, "Effect": "Allow", "Principal": map[string]any{"AWS": []string{"*"}},
		"Action": []string{"s3:GetObject"}, "Resource": []string{"arn:aws:s3:::" + bucket + "/" + PublicPrefix + "*"}}
}

// publicPolicy is the public bucket's policy when it holds nothing else.
func publicPolicy(bucket string) string {
	b, _ := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": []any{publicStatement(bucket)}})
	return string(b)
}

// policyDoc is a bucket policy kept as raw JSON, so statements and fields the api does not own survive a rewrite.
type policyDoc struct {
	fields     map[string]json.RawMessage
	statements []json.RawMessage
}

func parsePolicy(text string) (policyDoc, error) {
	d := policyDoc{fields: map[string]json.RawMessage{}}
	if strings.TrimSpace(text) == "" {
		return d, nil
	}
	if err := json.Unmarshal([]byte(text), &d.fields); err != nil {
		return d, fmt.Errorf("bucket policy: %w", err)
	}
	if raw, ok := d.fields["Statement"]; ok {
		if err := json.Unmarshal(raw, &d.statements); err != nil {
			// A single statement object is valid IAM JSON too.
			d.statements = []json.RawMessage{raw}
		}
	}
	return d, nil
}

func (d policyDoc) marshal() (string, error) {
	if _, ok := d.fields["Version"]; !ok {
		d.fields["Version"] = json.RawMessage(`"2012-10-17"`)
	}
	st, err := json.Marshal(d.statements)
	if err != nil {
		return "", err
	}
	d.fields["Statement"] = st
	b, err := json.Marshal(d.fields)
	return string(b), err
}

// stringList reads an IAM value that is a string or a list of strings.
func stringList(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(raw, &many)
	return many
}

type statement struct {
	Effect    string          `json:"Effect"`
	Principal json.RawMessage `json:"Principal"`
	Action    json.RawMessage `json:"Action"`
	Resource  json.RawMessage `json:"Resource"`
	Condition json.RawMessage `json:"Condition"`
	NotAction json.RawMessage `json:"NotAction"`
	NotRes    json.RawMessage `json:"NotResource"`
}

// anonymousAllow reports a statement that grants anything to everyone (Principal "*" or AWS "*"): the only
// statements the api removes from the private bucket or replaces on the public one. Deny statements and grants
// to named principals (an operator's TLS-only rule, a backup role) are kept.
func anonymousAllow(raw json.RawMessage) (statement, bool) {
	var st statement
	if json.Unmarshal(raw, &st) != nil || !strings.EqualFold(st.Effect, "Allow") {
		return st, false
	}
	if slices.Contains(stringList(st.Principal), "*") {
		return st, true
	}
	var p map[string]json.RawMessage
	if json.Unmarshal(st.Principal, &p) == nil && slices.Contains(stringList(p["AWS"]), "*") {
		return st, true
	}
	return st, false
}

// isAppReleasesGrant reports the api's own public grant: anonymous GetObject on exactly app_releases/* of bucket.
func isAppReleasesGrant(st statement, bucket string) bool {
	return len(st.Condition) == 0 && len(st.NotAction) == 0 && len(st.NotRes) == 0 &&
		slices.Equal(stringList(st.Action), []string{"s3:GetObject"}) &&
		slices.Equal(stringList(st.Resource), []string{"arn:aws:s3:::" + bucket + "/" + PublicPrefix + "*"})
}

// privatePolicy is the private bucket's policy without its anonymous grants, and whether it changed. The empty
// string removes the policy.
func privatePolicy(current string) (string, bool, error) {
	d, err := parsePolicy(current)
	if err != nil {
		return "", false, err
	}
	kept := d.statements[:0:0]
	for _, raw := range d.statements {
		if _, anon := anonymousAllow(raw); !anon {
			kept = append(kept, raw)
		}
	}
	if len(kept) == len(d.statements) {
		return current, false, nil
	}
	if len(kept) == 0 {
		return "", true, nil
	}
	d.statements = kept
	out, err := d.marshal()
	return out, true, err
}

// mergePublicPolicy keeps every statement of the public bucket's policy that is not an anonymous grant and
// ensures the only anonymous grant is the api's app_releases/ one; changed is false when that already holds.
func mergePublicPolicy(current, bucket string) (string, bool, error) {
	d, err := parsePolicy(current)
	if err != nil {
		return "", false, err
	}
	kept := d.statements[:0:0]
	anon, ours := 0, 0
	for _, raw := range d.statements {
		st, ok := anonymousAllow(raw)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		anon++
		if isAppReleasesGrant(st, bucket) {
			ours++
		}
	}
	if anon == 1 && ours == 1 {
		return current, false, nil
	}
	own, err := json.Marshal(publicStatement(bucket))
	if err != nil {
		return "", false, err
	}
	d.statements = append(kept, own)
	out, err := d.marshal()
	return out, true, err
}

// cacheRule is the private bucket's cache/ expiry (main spec §9.1).
func cacheRule() lifecycle.Rule {
	return lifecycle.Rule{ID: lifecycleCacheRule, Status: "Enabled",
		RuleFilter: lifecycle.Filter{Prefix: "cache/"}, Expiration: lifecycle.Expiration{Days: 30}}
}

// mergeLifecycle upserts the api's cache/ rule into the bucket's lifecycle and keeps every other rule; changed is
// false when the rule is already there as wanted.
func mergeLifecycle(current *lifecycle.Configuration) (*lifecycle.Configuration, bool) {
	out := lifecycle.NewConfiguration()
	want := cacheRule()
	found, same := false, false
	if current != nil {
		for _, r := range current.Rules {
			if r.ID != lifecycleCacheRule {
				out.Rules = append(out.Rules, r)
				continue
			}
			found = true
			same = r.Status == want.Status && r.Expiration.Days == want.Expiration.Days &&
				(r.RuleFilter.Prefix == "cache/" || r.Prefix == "cache/") && r.RuleFilter.And.Prefix == "" &&
				r.Expiration.Date.IsZero()
		}
	}
	if found && same {
		return current, false
	}
	out.Rules = append(out.Rules, want)
	return out, true
}

// CORSRules is the private bucket's CORS (main spec §9.1): browser PUT and GET of presigned URLs from
// CORS_ALLOWED_ORIGINS, max-age 3600.
func CORSRules(origins []string) []cors.Rule {
	return []cors.Rule{{
		AllowedOrigin: origins,
		AllowedMethod: []string{http.MethodGet, http.MethodHead, http.MethodPut},
		AllowedHeader: []string{"Content-Type", "Content-Length", "Content-Disposition", "If-None-Match", "x-amz-*"},
		ExposeHeader:  []string{"ETag"},
		MaxAgeSeconds: 3600,
	}}
}

// Bootstrap asserts the buckets idempotently (main spec §9.1): both exist; the public bucket's only anonymous
// grant is GetObject on app_releases/; the private bucket has no anonymous grant; its CORS comes from
// CORS_ALLOWED_ORIGINS; cache/ expires after 30 days. The api owns only those pieces: policy statements that are
// not anonymous grants (a deny-insecure-transport rule, a backup role) and lifecycle rules other than
// expire-cache are read back and kept, and nothing is written when the bucket already matches. The bucket CORS
// belongs to CORS_ALLOWED_ORIGINS entirely. Each step runs even when an earlier one failed; the errors are
// returned joined. A server that does not implement bucket CORS (some MinIO releases) answers NotImplemented:
// compose also sets the server-level MINIO_API_CORS_ALLOW_ORIGIN from the same variable, so that step is logged
// and skipped.
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
	step("public policy", s.assertPolicy(ctx, s.cfg.PublicBucket, func(cur string) (string, bool, error) {
		return mergePublicPolicy(cur, s.cfg.PublicBucket)
	}))
	step("private policy", s.assertPolicy(ctx, s.cfg.Bucket, privatePolicy))
	if len(s.cfg.CORSOrigins) > 0 {
		err := s.client.SetBucketCors(ctx, s.cfg.Bucket, cors.NewConfig(CORSRules(s.cfg.CORSOrigins)))
		if code := minio.ToErrorResponse(err).Code; code == "NotImplemented" {
			log.Info().Msg("storage: the server has no bucket CORS API; the server-level CORS setting applies")
			err = nil
		}
		step("private bucket CORS", err)
	}
	step("cache/ lifecycle", s.assertLifecycle(ctx))
	return errors.Join(errs...)
}

// assertPolicy reads a bucket policy, lets merge compute the wanted one and writes it only when it changed (the
// empty string deletes the policy).
func (s *S3) assertPolicy(ctx context.Context, bucket string, merge func(string) (string, bool, error)) error {
	cur, err := s.client.GetBucketPolicy(ctx, bucket)
	if err != nil {
		return err
	}
	want, changed, err := merge(cur)
	if err != nil || !changed {
		return err
	}
	err = s.client.SetBucketPolicy(ctx, bucket, want)
	if want == "" && minio.ToErrorResponse(err).Code == "NoSuchBucketPolicy" {
		return nil
	}
	return err
}

// assertLifecycle upserts the expire-cache rule of the private bucket, keeping the bucket's other rules.
func (s *S3) assertLifecycle(ctx context.Context) error {
	cur, err := s.client.GetBucketLifecycle(ctx, s.cfg.Bucket)
	if minio.ToErrorResponse(err).Code == "NoSuchLifecycleConfiguration" {
		cur, err = nil, nil
	}
	if err != nil {
		return err
	}
	want, changed := mergeLifecycle(cur)
	if !changed {
		return nil
	}
	return s.client.SetBucketLifecycle(ctx, s.cfg.Bucket, want)
}
