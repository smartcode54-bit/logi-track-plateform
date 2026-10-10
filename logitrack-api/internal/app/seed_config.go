package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// SeedConfig is the configuration of cmd/seed (developer-spec.md §14, Appendix D §D.1.1). Each database URL
// has one job (R87): ETL_DATABASE_URL (logitrack_etl) for every write and the --verify reads,
// MIGRATE_DATABASE_URL (logitrack_migrator) for --reset's TRUNCATE and the schema-version check,
// DATABASE_URL (logitrack_app) for the --verify isolation role-play. Which ones a command needs is checked
// by cmd/seed, so --dry-run runs without any. SEED_DEFAULT_PASSWORD is a secret: hashed, never printed.
type SeedConfig struct {
	Common
	Storage
	DatabaseURL        string `env:"DATABASE_URL"`
	MigrateDatabaseURL string `env:"MIGRATE_DATABASE_URL"`
	ETLDatabaseURL     string `env:"ETL_DATABASE_URL"`
	RedisURL           string `env:"REDIS_URL"`
	RedisKeyPrefix     string `env:"REDIS_KEY_PREFIX"`
	// S3PublicBaseURL and LocalMediaPublicBaseURL build the APK link of settings('mobile_app') on the
	// backend the APK is written to; the seed signs no URL.
	S3PublicBaseURL         string `env:"S3_PUBLIC_BASE_URL"`
	LocalMediaPublicBaseURL string `env:"LOCAL_MEDIA_PUBLIC_BASE_URL"`
	Argon2MemoryKB          uint32 `env:"ARGON2_MEMORY_KB" envDefault:"65536"`
	Argon2Iterations        uint32 `env:"ARGON2_ITERATIONS" envDefault:"3"`
	Argon2Parallelism       uint8  `env:"ARGON2_PARALLELISM" envDefault:"2"`
	ScryptSignerKey         string `env:"FIREBASE_SCRYPT_SIGNER_KEY"`
	ScryptSaltSep           string `env:"FIREBASE_SCRYPT_SALT_SEPARATOR"`
	ScryptRounds            int    `env:"FIREBASE_SCRYPT_ROUNDS"`
	ScryptMemCost           int    `env:"FIREBASE_SCRYPT_MEM_COST"`
	OwnFleetTenantID        string `env:"OWN_FLEET_TENANT_ID"`
	SeedProfile             string `env:"SEED_PROFILE"`
	SeedRandomSeed          string `env:"SEED_RANDOM_SEED"`
	SeedAnchorDate          string `env:"SEED_ANCHOR_DATE"`
	SeedNamespace           string `env:"SEED_NAMESPACE"`
	SeedDefaultPassword     string `env:"SEED_DEFAULT_PASSWORD"`

	// Parsed by Validate.
	Scrypt     *firebasescrypt.Params `env:"-"`
	OwnFleet   *uuid.UUID             `env:"-"`
	Namespace  uuid.UUID              `env:"-"` // uuid.Nil = the default namespace
	RandomSeed uint64                 `env:"-"` // 0 = the default seed
	Anchor     time.Time              `env:"-"` // zero = the default anchor
}

// Validate implements config.Validator. Messages name variables, never values.
func (c *SeedConfig) Validate() error {
	var errs []string
	c.Common.validate(&errs)
	c.Storage.validate(&errs)
	for _, kv := range [][2]string{{"DATABASE_URL", c.DatabaseURL}, {"MIGRATE_DATABASE_URL", c.MigrateDatabaseURL}, {"ETL_DATABASE_URL", c.ETLDatabaseURL}} {
		if kv[1] != "" && !strings.HasPrefix(kv[1], "postgres://") && !strings.HasPrefix(kv[1], "postgresql://") {
			errs = append(errs, config.Invalidf(kv[0], "must be a postgres:// URL"))
		}
	}
	if c.RedisURL != "" && !strings.HasPrefix(c.RedisURL, "redis://") && !strings.HasPrefix(c.RedisURL, "rediss://") {
		errs = append(errs, config.Invalidf("REDIS_URL", "must be a redis:// or rediss:// URL"))
	}
	if c.RedisKeyPrefix != "" && c.RedisKeyPrefix != "lt:"+c.AppEnv+":" {
		errs = append(errs, config.Invalidf("REDIS_KEY_PREFIX", "must be lt:{APP_ENV}: (R26)"))
	}
	for _, kv := range [][2]string{{"S3_PUBLIC_BASE_URL", c.S3PublicBaseURL}, {"LOCAL_MEDIA_PUBLIC_BASE_URL", c.LocalMediaPublicBaseURL}} {
		if kv[1] != "" && !absoluteURL(kv[1]) {
			errs = append(errs, config.Invalidf(kv[0], "must be an absolute http(s) URL without query"))
		}
	}
	if c.Argon2MemoryKB < 8192 || c.Argon2Iterations < 1 || c.Argon2Parallelism < 1 {
		errs = append(errs, "ARGON2_MEMORY_KB, ARGON2_ITERATIONS, ARGON2_PARALLELISM: memory >= 8192, iterations and parallelism >= 1")
	}
	sp, err := firebasescrypt.ParseParams(c.ScryptSignerKey, c.ScryptSaltSep, c.ScryptRounds, c.ScryptMemCost)
	if err != nil {
		errs = append(errs, err.Error()+" (set all four FIREBASE_SCRYPT_* or none)")
	}
	c.Scrypt = sp
	if c.OwnFleetTenantID != "" {
		id, err := uuid.Parse(c.OwnFleetTenantID)
		if err != nil || id == uuid.Nil {
			errs = append(errs, config.Invalidf("OWN_FLEET_TENANT_ID", "must be a uuid"))
		} else {
			c.OwnFleet = &id
		}
	}
	switch c.SeedProfile {
	case "", "smoke", "demo", "load":
	default:
		errs = append(errs, config.Invalidf("SEED_PROFILE", "must be smoke, demo or load"))
	}
	if c.SeedRandomSeed != "" {
		v, err := strconv.ParseUint(c.SeedRandomSeed, 10, 64)
		if err != nil {
			errs = append(errs, config.Invalidf("SEED_RANDOM_SEED", "must be an unsigned integer"))
		}
		c.RandomSeed = v
	}
	if c.SeedAnchorDate != "" {
		d, ok := clock.MidnightFromDateString(c.SeedAnchorDate)
		if !ok {
			errs = append(errs, config.Invalidf("SEED_ANCHOR_DATE", "must be a date YYYY-MM-DD"))
		}
		c.Anchor = d
	}
	if c.SeedNamespace != "" {
		ns, err := uuid.Parse(c.SeedNamespace)
		if err != nil || ns == uuid.Nil {
			errs = append(errs, config.Invalidf("SEED_NAMESPACE", "must be a uuid"))
		}
		c.Namespace = ns
	}
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}

// Backends builds every configured storage backend without network I/O: s3 when S3_ENDPOINT is set, local
// when LOCAL_MEDIA_DIR is set (rows keep the backend they were written to, §9.11). The seed writes new
// objects to STORAGE_BACKEND.
func (c *SeedConfig) Backends() (map[string]storage.Backend, error) {
	out := map[string]storage.Backend{}
	if c.s3Configured() {
		s3, err := storage.NewS3(storage.S3Config{
			Endpoint: c.S3Endpoint, UseSSL: c.S3UseSSL, Region: c.S3Region, AccessKeyID: c.S3AccessKeyID,
			SecretAccessKey: c.S3SecretAccessKey, PathStyle: c.S3UsePathStyle, PublicBaseURL: c.S3PublicBaseURL,
			Bucket: c.S3Bucket, PublicBucket: c.S3PublicBucket,
		})
		if err != nil {
			return nil, &config.Error{Invalid: []string{err.Error()}}
		}
		out[storage.BackendS3] = s3
	}
	if c.LocalMediaDir != "" {
		l, err := storage.NewLocal(storage.LocalConfig{Dir: c.LocalMediaDir, PublicBaseURL: c.LocalMediaPublicBaseURL,
			APIUploadPath: storage.LocalUploadPath})
		if err != nil {
			return nil, &config.Error{Invalid: []string{err.Error()}}
		}
		out[storage.BackendLocal] = l
	}
	return out, nil
}
