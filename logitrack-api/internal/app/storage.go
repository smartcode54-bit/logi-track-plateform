package app

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Storage is the object store the api and the worker share (main spec §9, §16.1): the backend of new uploads,
// the local directory and the server side of the S3 backend. With STORAGE_BACKEND=local the S3 names may stay
// empty; LOCAL_MEDIA_DIR stays set after a switch to s3, so objects stored on disk keep downloading (and storage.gc
// still reaches them). S3_BUCKET and S3_PUBLIC_BUCKET also label local rows (file_objects.bucket).
type Storage struct {
	StorageBackend    string `env:"STORAGE_BACKEND" envDefault:"s3"`
	LocalMediaDir     string `env:"LOCAL_MEDIA_DIR"`
	S3Endpoint        string `env:"S3_ENDPOINT"`
	S3Region          string `env:"S3_REGION"`
	S3AccessKeyID     string `env:"S3_ACCESS_KEY_ID"`
	S3SecretAccessKey string `env:"S3_SECRET_ACCESS_KEY"`
	S3Bucket          string `env:"S3_BUCKET" envDefault:"logitrack"`
	S3PublicBucket    string `env:"S3_PUBLIC_BUCKET" envDefault:"logitrack-public"`
	S3UsePathStyle    bool   `env:"S3_USE_PATH_STYLE" envDefault:"true"`
	S3UseSSL          bool   `env:"S3_USE_SSL"`
}

// bucketRx is an S3 bucket name (lower case, digits, dots and hyphens, 3-63 characters).
var bucketRx = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// s3Configured reports whether the S3 backend is configured (S3_ENDPOINT set).
func (s Storage) s3Configured() bool { return s.S3Endpoint != "" }

func (s *Storage) validate(errs *[]string) {
	s.StorageBackend = strings.TrimSpace(s.StorageBackend)
	switch s.StorageBackend {
	case storage.BackendS3:
		if !s.s3Configured() {
			*errs = append(*errs, config.Invalidf("S3_ENDPOINT", "is required when STORAGE_BACKEND is s3"))
		}
	case storage.BackendLocal:
		if s.LocalMediaDir == "" {
			*errs = append(*errs, config.Invalidf("LOCAL_MEDIA_DIR", "is required when STORAGE_BACKEND is local"))
		}
	default:
		*errs = append(*errs, config.Invalidf("STORAGE_BACKEND", "must be local or s3"))
	}
	if s.LocalMediaDir != "" && !strings.HasPrefix(s.LocalMediaDir, "/") {
		*errs = append(*errs, config.Invalidf("LOCAL_MEDIA_DIR", "must be an absolute path"))
	}
	if s.s3Configured() {
		if s.S3Region == "" || s.S3AccessKeyID == "" || s.S3SecretAccessKey == "" {
			*errs = append(*errs, "S3_REGION, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY: required with S3_ENDPOINT")
		}
		if u, err := url.Parse(s.S3Endpoint); strings.Contains(s.S3Endpoint, "://") &&
			(err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
				(u.Path != "" && u.Path != "/") || u.RawQuery != "") {
			*errs = append(*errs, config.Invalidf("S3_ENDPOINT", "must be host[:port] or an http(s) origin without path or credentials"))
		} else if err == nil && strings.Contains(s.S3Endpoint, "://") && (u.Scheme == "https") != s.S3UseSSL {
			*errs = append(*errs, "S3_ENDPOINT, S3_USE_SSL: the endpoint scheme and S3_USE_SSL disagree")
		}
	}
	for _, kv := range [][2]string{{"S3_BUCKET", s.S3Bucket}, {"S3_PUBLIC_BUCKET", s.S3PublicBucket}} {
		if !bucketRx.MatchString(kv[1]) {
			*errs = append(*errs, config.Invalidf(kv[0], "must be a bucket name (3-63 lower-case letters, digits, dots, hyphens)"))
		}
	}
	if s.S3Bucket != "" && s.S3Bucket == s.S3PublicBucket {
		*errs = append(*errs, "S3_BUCKET, S3_PUBLIC_BUCKET: must differ")
	}
}

// StorageAPI is what only the api reads: signing origins, the private bucket's CORS, URL lifetimes, the upload
// limit, local URL signing and the evidence settings (main spec §9, §16.1).
type StorageAPI struct {
	S3PresignEndpoint       string        `env:"S3_PRESIGN_ENDPOINT"`
	S3PublicBaseURL         string        `env:"S3_PUBLIC_BASE_URL"`
	CORSAllowedOrigins      []string      `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`
	S3PresignGetTTL         time.Duration `env:"S3_PRESIGN_GET_TTL" envDefault:"1h"`
	S3PresignPutTTL         time.Duration `env:"S3_PRESIGN_PUT_TTL" envDefault:"15m"`
	UploadMaxBytes          int64         `env:"UPLOAD_MAX_BYTES" envDefault:"10485760"`
	LocalMediaPublicBaseURL string        `env:"LOCAL_MEDIA_PUBLIC_BASE_URL"`
	LocalMediaSigningKey    string        `env:"LOCAL_MEDIA_SIGNING_KEY"`
	EvidenceTokenTTLDays    int           `env:"EVIDENCE_TOKEN_TTL_DAYS" envDefault:"0"`
	EvidencePresignTTL      time.Duration `env:"EVIDENCE_PRESIGN_TTL" envDefault:"15m"`
	// CORSOrigins is CORSAllowedOrigins trimmed by validate.
	CORSOrigins []string `env:"-"`
}

// MaxUploadBytes caps UPLOAD_MAX_BYTES: a local upload is read into memory before it is written to disk.
const MaxUploadBytes = 100 << 20

func absoluteURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func (a *StorageAPI) validate(s Storage, errs *[]string) {
	if s.s3Configured() {
		if a.S3PresignEndpoint == "" {
			*errs = append(*errs, config.Invalidf("S3_PRESIGN_ENDPOINT", "is required with S3_ENDPOINT (the origin presigned URLs are signed for)"))
		} else if u, err := url.Parse(a.S3PresignEndpoint); err != nil || !absoluteURL(a.S3PresignEndpoint) || (u.Path != "" && u.Path != "/") {
			*errs = append(*errs, config.Invalidf("S3_PRESIGN_ENDPOINT", "must be an http(s) origin without path"))
		}
	}
	if a.S3PublicBaseURL != "" && !absoluteURL(a.S3PublicBaseURL) {
		*errs = append(*errs, config.Invalidf("S3_PUBLIC_BASE_URL", "must be an absolute http(s) URL without query"))
	}
	a.CORSOrigins = nil
	for _, o := range a.CORSAllowedOrigins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if u, err := url.Parse(o); err != nil || !absoluteURL(o) || u.Path != "" {
			*errs = append(*errs, config.Invalidf("CORS_ALLOWED_ORIGINS", "entries must be origins (scheme://host[:port]); * is not allowed"))
			break
		}
		a.CORSOrigins = append(a.CORSOrigins, o)
	}
	if a.S3PresignGetTTL < time.Minute || a.S3PresignGetTTL > 7*24*time.Hour {
		*errs = append(*errs, config.Invalidf("S3_PRESIGN_GET_TTL", "must be between 1m and 168h"))
	}
	if a.S3PresignPutTTL < time.Minute || a.S3PresignPutTTL > 24*time.Hour {
		*errs = append(*errs, config.Invalidf("S3_PRESIGN_PUT_TTL", "must be between 1m and 24h"))
	}
	if a.UploadMaxBytes < 1 || a.UploadMaxBytes > MaxUploadBytes {
		*errs = append(*errs, config.Invalidf("UPLOAD_MAX_BYTES", "must be between 1 and %d", MaxUploadBytes))
	}
	if s.LocalMediaDir != "" {
		if !absoluteURL(a.LocalMediaPublicBaseURL) {
			*errs = append(*errs, config.Invalidf("LOCAL_MEDIA_PUBLIC_BASE_URL",
				"must be an absolute http(s) URL without query when LOCAL_MEDIA_DIR is set (e.g. https://{host}/media)"))
		}
		if len(a.LocalMediaSigningKey) < storage.MinSigningKeyBytes {
			*errs = append(*errs, config.Invalidf("LOCAL_MEDIA_SIGNING_KEY",
				"must be at least %d bytes when LOCAL_MEDIA_DIR is set", storage.MinSigningKeyBytes))
		}
	}
	if a.EvidenceTokenTTLDays < 0 || a.EvidenceTokenTTLDays > 36500 {
		*errs = append(*errs, config.Invalidf("EVIDENCE_TOKEN_TTL_DAYS", "must be 0 (never expires) or a number of days up to 36500"))
	}
	if a.EvidencePresignTTL < time.Minute || a.EvidencePresignTTL > time.Hour {
		*errs = append(*errs, config.Invalidf("EVIDENCE_PRESIGN_TTL", "must be between 1m and 1h"))
	}
}

// storageBuild is what newStorage needs from a process configuration.
type storageBuild struct {
	Storage
	API *StorageAPI // nil in the worker: nothing is signed there
}

// newStorage builds the storage service over the configured backends without network I/O (the local backend
// creates its directory tree). Errors are *config.Error: a bad setting stops the process with exit 2.
func newStorage(b storageBuild, pool *pgxpool.Pool, log zerolog.Logger) (*storage.Service, *storage.S3, error) {
	cfg := storage.Config{Active: b.StorageBackend, Bucket: b.S3Bucket, PublicBucket: b.S3PublicBucket}
	var backends []storage.Backend
	var s3 *storage.S3
	if b.s3Configured() {
		s3c := storage.S3Config{
			Endpoint: b.S3Endpoint, UseSSL: b.S3UseSSL, Region: b.S3Region, AccessKeyID: b.S3AccessKeyID,
			SecretAccessKey: b.S3SecretAccessKey, PathStyle: b.S3UsePathStyle, Bucket: b.S3Bucket, PublicBucket: b.S3PublicBucket,
		}
		if b.API != nil {
			s3c.PresignEndpoint, s3c.PublicBaseURL, s3c.CORSOrigins = b.API.S3PresignEndpoint, b.API.S3PublicBaseURL, b.API.CORSOrigins
		}
		var err error
		if s3, err = storage.NewS3(s3c); err != nil {
			return nil, nil, &config.Error{Invalid: []string{err.Error()}}
		}
		backends = append(backends, s3)
	}
	if b.LocalMediaDir != "" {
		lc := storage.LocalConfig{Dir: b.LocalMediaDir, APIUploadPath: storage.LocalUploadPath}
		if b.API != nil {
			lc.PublicBaseURL, lc.SigningKey = b.API.LocalMediaPublicBaseURL, []byte(b.API.LocalMediaSigningKey)
		}
		l, err := storage.NewLocal(lc)
		if err != nil {
			return nil, nil, &config.Error{Invalid: []string{err.Error()}}
		}
		backends = append(backends, l)
	}
	if b.API != nil {
		cfg.PresignGetTTL, cfg.PresignPutTTL, cfg.UploadMaxBytes = b.API.S3PresignGetTTL, b.API.S3PresignPutTTL, b.API.UploadMaxBytes
		cfg.EvidenceTokenTTLDays, cfg.EvidencePresignTTL = b.API.EvidenceTokenTTLDays, b.API.EvidencePresignTTL
	}
	svc, err := storage.New(pool, cfg, log, backends...)
	if err != nil {
		return nil, nil, &config.Error{Invalid: []string{err.Error()}}
	}
	names := make([]string, 0, len(backends))
	for _, bk := range backends {
		names = append(names, bk.Name())
	}
	log.Info().Str("storage_backend", b.StorageBackend).Strs("storage_backends_readable", names).Msg("object storage")
	return svc, s3, nil
}

// bootstrapS3 re-asserts the buckets, policies, CORS and lifecycle in the background (main spec §9.1) until it
// succeeds or ctx ends, so the api starts while MinIO is still coming up.
func bootstrapS3(ctx context.Context, s3 *storage.S3, log zerolog.Logger) {
	if s3 == nil {
		return
	}
	go func() {
		delay := time.Second
		for {
			bctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := s3.Bootstrap(bctx, log)
			cancel()
			if err == nil {
				log.Info().Msg("storage: buckets, policies, CORS and lifecycle asserted")
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			delay = min(2*delay, time.Minute)
		}
	}()
}

// StorageCaller maps the request principal onto the storage service's caller: staff of the active tenant read
// its files (and its carriers'); platform_admin may upload platform objects. Cross-tenant platform reads arrive
// with X-Act-On-Tenant (T07), which sets the acting tenant.
func StorageCaller(c fiber.Ctx) (storage.Caller, bool) {
	p := auth.PrincipalFrom(c)
	if p == nil {
		return storage.Caller{}, false
	}
	tid := p.EffectiveTenant()
	return storage.Caller{
		UserID: p.UserID, TenantID: tid,
		Staff:         tid != nil && p.TenantRole != "" && p.TenantRole != authz.Driver,
		PlatformAdmin: p.HasPlatform(authz.PlatformAdmin),
	}, true
}

// StorageGroups are POST /v1/uploads/presign, GET /v1/files and the local backend's upload and /media routes (T11)
// behind auth.RequireAuth where a principal is needed; presign spends the presign_user budget (120/min per user).
func StorageGroups(d APIDeps) []ingress.Group {
	o := storage.HTTPOptions{Auth: d.Auth.RequireAuth(), Caller: StorageCaller}
	if d.Limiter != nil {
		o.PresignLimit = d.Limiter.Middleware(d.RateLimitEnabled, ratelimit.Rule{Bucket: ratelimit.PresignUser,
			By: func(c fiber.Ctx) string {
				if p := auth.PrincipalFrom(c); p != nil {
					return p.UserID.String()
				}
				return ""
			}})
	}
	return d.Storage.Groups(o)
}
