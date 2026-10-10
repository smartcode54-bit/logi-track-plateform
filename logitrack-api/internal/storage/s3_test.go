package storage

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testS3Config() S3Config {
	return S3Config{
		Endpoint: "http://minio:9000", PresignEndpoint: "https://media.logitrack.test", Region: "us-east-1",
		AccessKeyID: "placeholder-access", SecretAccessKey: "placeholder-secret", PathStyle: true,
		Bucket: "logitrack", PublicBucket: "logitrack-public", PublicBaseURL: "https://media.logitrack.test/logitrack-public",
	}
}

// R74: presigned URLs carry the S3_PRESIGN_ENDPOINT origin (the one browsers and the APK reach), never the
// server-side S3_ENDPOINT, and are signed offline (no request reaches minio:9000 here).
func TestPresignedURLsCarryThePresignEndpoint(t *testing.T) {
	s, err := NewS3(testS3Config())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	o := Object{Bucket: "logitrack", Key: "trips/t1/seal-1.jpg"}
	put, err := s.PresignPut(ctx, o, "image/jpeg", 15*time.Minute, PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	get, _, err := s.PresignGet(ctx, o, time.Hour, GetOptions{ContentDisposition: `attachment; filename="x.jpg"`})
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"put": put.URL, "get": get} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if u.Scheme != "https" || u.Host != "media.logitrack.test" || strings.Contains(raw, "minio") {
			t.Errorf("%s URL %s is not on S3_PRESIGN_ENDPOINT", name, raw)
		}
		if u.Path != "/logitrack/trips/t1/seal-1.jpg" || u.Query().Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
			t.Errorf("%s URL %s: path-style SigV4 expected", name, raw)
		}
	}
	pq := mustQuery(t, put.URL)
	if pq.Get("X-Amz-Expires") != "900" || !strings.Contains(pq.Get("X-Amz-SignedHeaders"), "content-type") ||
		put.Headers["Content-Type"] != "image/jpeg" || put.Method != "PUT" {
		t.Fatalf("put %+v: Content-Type must be a signed header, TTL S3_PRESIGN_PUT_TTL", put)
	}
	gq := mustQuery(t, get)
	if gq.Get("X-Amz-Expires") != "3600" || gq.Get("response-content-disposition") == "" {
		t.Fatalf("get %s", get)
	}
	if strings.Contains(put.URL, "placeholder-secret") || strings.Contains(get, "placeholder-secret") {
		t.Fatal("secret in a URL")
	}
	pub, err := s.PublicURL(Object{Bucket: "logitrack-public", Key: "app_releases/prod/logitrack-prod-v3.5.0.apk"})
	if err != nil || pub != "https://media.logitrack.test/logitrack-public/app_releases/prod/logitrack-prod-v3.5.0.apk" {
		t.Fatalf("public %s %v", pub, err)
	}
	if _, err := s.PublicURL(o); err != ErrNotPublic {
		t.Fatalf("private key public URL: %v", err)
	}
}

func TestNewS3RefusesBadEndpoints(t *testing.T) {
	for name, mut := range map[string]func(*S3Config){
		"endpoint with path":       func(c *S3Config) { c.Endpoint = "http://minio:9000/s3" },
		"endpoint with creds":      func(c *S3Config) { c.Endpoint = "http://u:p@minio:9000" },
		"presign without scheme":   func(c *S3Config) { c.PresignEndpoint = "media.logitrack.test" },
		"presign with path":        func(c *S3Config) { c.PresignEndpoint = "https://media.logitrack.test/x" },
		"no region":                func(c *S3Config) { c.Region = "" },
		"no secret":                func(c *S3Config) { c.SecretAccessKey = "" },
		"public base with a query": func(c *S3Config) { c.PublicBaseURL = "https://m.test/b?x=1" },
	} {
		cfg := testS3Config()
		mut(&cfg)
		if _, err := NewS3(cfg); err == nil {
			t.Errorf("%s accepted", name)
		} else if strings.Contains(err.Error(), "placeholder-secret") {
			t.Errorf("%s: the error carries the secret", name)
		}
	}
	cfg := testS3Config()
	cfg.Endpoint, cfg.UseSSL = "minio:9000", false
	if _, err := NewS3(cfg); err != nil {
		t.Fatalf("host:port endpoint: %v", err)
	}
	// The worker has no presign endpoint: it signs nothing.
	cfg.PresignEndpoint = ""
	s, err := NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PresignPut(context.Background(), Object{Bucket: "logitrack", Key: "a.jpg"}, "image/jpeg", time.Minute, PutOptions{}); err == nil {
		t.Fatal("signed without S3_PRESIGN_ENDPOINT")
	}
}

func TestBucketPolicyAndCORS(t *testing.T) {
	var doc struct {
		Statement []struct {
			Effect, Principal any
			Action            []string
			Resource          []string
		}
	}
	if err := json.Unmarshal([]byte(publicPolicy("logitrack-public")), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Statement) != 1 || len(doc.Statement[0].Action) != 1 || doc.Statement[0].Action[0] != "s3:GetObject" ||
		len(doc.Statement[0].Resource) != 1 || doc.Statement[0].Resource[0] != "arn:aws:s3:::logitrack-public/app_releases/*" {
		t.Fatalf("public policy %+v: anonymous GetObject on app_releases/ only", doc)
	}
	r := CORSRules([]string{"http://localhost:3000"})
	if len(r) != 1 || strings.Join(r[0].AllowedMethod, ",") != "GET,HEAD,PUT" || r[0].MaxAgeSeconds != 3600 ||
		strings.Join(r[0].AllowedOrigin, ",") != "http://localhost:3000" {
		t.Fatalf("cors %+v", r)
	}
}
