package storage

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7/pkg/lifecycle"
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
	put, err := s.PresignPut(ctx, o, "image/jpeg", 1234, 15*time.Minute, PutOptions{})
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
	if pq.Get("X-Amz-Expires") != "900" || pq.Get("X-Amz-SignedHeaders") != "content-length;content-type;host;if-none-match" ||
		put.Headers["Content-Type"] != "image/jpeg" || put.Headers["If-None-Match"] != "*" || len(put.Headers) != 2 || put.Method != "PUT" {
		t.Fatalf("put %+v: Content-Type, Content-Length and If-None-Match must be signed, TTL S3_PRESIGN_PUT_TTL", put)
	}
	if _, err := s.PresignPut(ctx, o, "image/jpeg", 0, time.Minute, PutOptions{}); err == nil {
		t.Fatal("a PUT signed without its size")
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
	if _, err := s.PresignPut(context.Background(), Object{Bucket: "logitrack", Key: "a.jpg"}, "image/jpeg", 1, time.Minute, PutOptions{}); err == nil {
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
		!strings.Contains(strings.Join(r[0].AllowedHeader, ","), "If-None-Match") ||
		strings.Join(r[0].AllowedOrigin, ",") != "http://localhost:3000" {
		t.Fatalf("cors %+v", r)
	}
}

// The bootstrap owns only the anonymous grants and the expire-cache rule: an operator's other statements and
// lifecycle rules survive (§9.10 swap to a managed S3), and nothing is rewritten when the bucket already matches.
func TestBootstrapMergesPoliciesAndLifecycle(t *testing.T) {
	const deny = `{"Sid":"TLSOnly","Effect":"Deny","Principal":"*","Action":"s3:*","Resource":["arn:aws:s3:::logitrack/*"],` +
		`"Condition":{"Bool":{"aws:SecureTransport":"false"}}}`
	const backup = `{"Effect":"Allow","Principal":{"AWS":["arn:aws:iam::1:role/backup"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::logitrack/*"]}`
	const anon = `{"Effect":"Allow","Principal":{"AWS":"*"},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::logitrack/*"]}`
	doc := func(stmts ...string) string {
		return `{"Version":"2012-10-17","Statement":[` + strings.Join(stmts, ",") + `]}`
	}
	sids := func(t *testing.T, policy string) []string {
		t.Helper()
		var d struct{ Statement []map[string]any }
		if err := json.Unmarshal([]byte(policy), &d); err != nil {
			t.Fatalf("%s: %v", policy, err)
		}
		var out []string
		for _, st := range d.Statement {
			sid, _ := st["Sid"].(string)
			if sid == "" {
				sid = st["Effect"].(string)
			}
			out = append(out, sid)
		}
		return out
	}

	// Private bucket: anonymous grants go, everything else stays; no policy and an already clean one are left.
	for name, tc := range map[string]struct {
		in, want string
		changed  bool
	}{
		"none":            {"", "", false},
		"clean":           {doc(deny, backup), doc(deny, backup), false},
		"anonymous only":  {doc(anon), "", true},
		"mixed":           {doc(deny, anon, backup), "TLSOnly Allow", true},
		"principal star":  {doc(`{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::logitrack/*"}`), "", true},
		"anonymous deny":  {doc(deny), doc(deny), false},
		"single stmt obj": {`{"Version":"2012-10-17","Statement":` + anon + `}`, "", true},
	} {
		got, changed, err := privatePolicy(tc.in)
		if err != nil || changed != tc.changed {
			t.Errorf("%s: changed %v, err %v", name, changed, err)
			continue
		}
		if name == "mixed" {
			if s := strings.Join(sids(t, got), " "); s != tc.want {
				t.Errorf("%s: statements %s, want %s", name, s, tc.want)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}

	// Public bucket: the api's grant is ensured, foreign anonymous grants replaced, other statements kept.
	pub := "logitrack-public"
	if _, changed, err := mergePublicPolicy(publicPolicy(pub), pub); err != nil || changed {
		t.Fatalf("own policy: changed %v %v", changed, err)
	}
	minioShape := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],` +
		`"Resource":["arn:aws:s3:::logitrack-public/app_releases/*"]}]}`
	if _, changed, err := mergePublicPolicy(minioShape, pub); err != nil || changed {
		t.Fatalf("the grant as MinIO returns it: changed %v %v", changed, err)
	}
	wide := strings.ReplaceAll(anon, "logitrack/*", "logitrack-public/*")
	got, changed, err := mergePublicPolicy(doc(deny, wide), pub)
	if err != nil || !changed || strings.Join(sids(t, got), " ") != "TLSOnly "+appReleasesSid {
		t.Fatalf("public merge: %s %v %v", got, changed, err)
	}
	if got, _, _ := mergePublicPolicy("", pub); strings.Join(sids(t, got), " ") != appReleasesSid {
		t.Fatalf("empty public policy: %s", got)
	}

	// Lifecycle: expire-cache upserted, other rules kept, an unchanged configuration is not rewritten.
	other := lifecycle.Rule{ID: "abort-multipart", Status: "Enabled", AbortIncompleteMultipartUpload: lifecycle.AbortIncompleteMultipartUpload{DaysAfterInitiation: 7}}
	cfg, changed := mergeLifecycle(&lifecycle.Configuration{Rules: []lifecycle.Rule{other}})
	if !changed || len(cfg.Rules) != 2 || cfg.Rules[0].ID != "abort-multipart" || cfg.Rules[1].ID != lifecycleCacheRule {
		t.Fatalf("upsert: %+v", cfg.Rules)
	}
	if _, changed := mergeLifecycle(cfg); changed {
		t.Fatal("an unchanged lifecycle is rewritten")
	}
	stale := cacheRule()
	stale.Expiration.Days = 7
	cfg, changed = mergeLifecycle(&lifecycle.Configuration{Rules: []lifecycle.Rule{stale, other}})
	if !changed || len(cfg.Rules) != 2 || cfg.Rules[0].ID != "abort-multipart" || cfg.Rules[1].Expiration.Days != 30 {
		t.Fatalf("stale rule: %+v", cfg.Rules)
	}
	if cfg, changed := mergeLifecycle(nil); !changed || len(cfg.Rules) != 1 {
		t.Fatalf("no lifecycle: %+v", cfg)
	}
}
