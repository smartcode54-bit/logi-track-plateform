// Package app wires processes together: configuration, logger, telemetry and
// the HTTP listeners. Env names follow main spec §16; values are never logged.
package app

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Common is read by every Go process.
type Common struct {
	AppEnv          string  `env:"APP_ENV,required,notEmpty"`
	LogLevel        string  `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat       string  `env:"LOG_FORMAT" envDefault:"json"`
	OTelEndpoint    string  `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	OTelServiceName string  `env:"OTEL_SERVICE_NAME"`
	OTelSamplerArg  float64 `env:"OTEL_TRACES_SAMPLER_ARG" envDefault:"1"`
}

func (c Common) validate(errs *[]string) {
	if c.AppEnv != "" && !config.ValidAppEnv(c.AppEnv) {
		*errs = append(*errs, config.Invalidf("APP_ENV", "must be one of %s", strings.Join(config.AppEnvs, ", ")))
	}
	if _, err := zerolog.ParseLevel(strings.ToLower(c.LogLevel)); err != nil || c.LogLevel == "" {
		*errs = append(*errs, config.Invalidf("LOG_LEVEL", "must be one of trace, debug, info, warn, error"))
	}
	if c.LogFormat != "json" && c.LogFormat != "console" {
		*errs = append(*errs, config.Invalidf("LOG_FORMAT", "must be json or console"))
	}
	if c.OTelSamplerArg < 0 || c.OTelSamplerArg > 1 {
		*errs = append(*errs, config.Invalidf("OTEL_TRACES_SAMPLER_ARG", "must be between 0 and 1"))
	}
}

// Runtime is read by the long-running processes (api, worker, scheduler).
type Runtime struct {
	MetricsAddr     string        `env:"METRICS_ADDR,required,notEmpty"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"30s"`
}

func (r Runtime) validate(errs *[]string) {
	if r.MetricsAddr != "" {
		if err := checkAddr(r.MetricsAddr); err != nil {
			*errs = append(*errs, config.Invalidf("METRICS_ADDR", "%v", err))
		}
	}
	if r.ShutdownTimeout < time.Second {
		*errs = append(*errs, config.Invalidf("SHUTDOWN_TIMEOUT", "must be at least 1s"))
	}
}

// Database is the PostgreSQL connection of the long-running processes: logitrack_app through
// DATABASE_URL (R66, R87).
type Database struct {
	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`
	MaxConns    int32  `env:"DATABASE_MAX_CONNS"`
	MinConns    int32  `env:"DATABASE_MIN_CONNS"`
}

func (d Database) validate(errs *[]string) {
	if d.DatabaseURL != "" && !strings.HasPrefix(d.DatabaseURL, "postgres://") && !strings.HasPrefix(d.DatabaseURL, "postgresql://") {
		*errs = append(*errs, config.Invalidf("DATABASE_URL", "must be a postgres:// URL"))
	}
	if d.MaxConns < 0 || d.MinConns < 0 || (d.MaxConns > 0 && d.MinConns > d.MaxConns) {
		*errs = append(*errs, "DATABASE_MAX_CONNS, DATABASE_MIN_CONNS: must be >= 0 with MIN <= MAX")
	}
}

// Redis is the Redis connection; every key carries REDIS_KEY_PREFIX = lt:{APP_ENV}: (R26).
type Redis struct {
	RedisURL       string `env:"REDIS_URL,required,notEmpty"`
	RedisKeyPrefix string `env:"REDIS_KEY_PREFIX"`
	RedisTLS       bool   `env:"REDIS_TLS"`
}

func (r *Redis) validate(appEnv string, errs *[]string) {
	if r.RedisURL != "" && !strings.HasPrefix(r.RedisURL, "redis://") && !strings.HasPrefix(r.RedisURL, "rediss://") {
		*errs = append(*errs, config.Invalidf("REDIS_URL", "must be a redis:// or rediss:// URL"))
	}
	want := "lt:" + appEnv + ":"
	if r.RedisKeyPrefix == "" {
		r.RedisKeyPrefix = want
	} else if r.RedisKeyPrefix != want {
		*errs = append(*errs, config.Invalidf("REDIS_KEY_PREFIX", "must be lt:{APP_ENV}: (R26)"))
	}
}

// Auth is the auth configuration of the api process (Appendix C §C.4.16, main spec §16.1).
type Auth struct {
	JWTSigningKeyFile  string        `env:"JWT_SIGNING_KEY_FILE,required,notEmpty"`
	JWTPreviousKeyFile string        `env:"JWT_PREVIOUS_KEY_FILE"`
	JWTActiveKID       string        `env:"JWT_ACTIVE_KID,required,notEmpty"`
	JWTIssuer          string        `env:"JWT_ISSUER,required,notEmpty"`
	JWTAudience        string        `env:"JWT_AUDIENCE,required,notEmpty"`
	JWTAccessTTL       time.Duration `env:"JWT_ACCESS_TTL" envDefault:"15m"`
	RefreshTTLWeb      time.Duration `env:"REFRESH_TOKEN_TTL_WEB" envDefault:"168h"`
	RefreshTTLMobile   time.Duration `env:"REFRESH_TOKEN_TTL_MOBILE" envDefault:"2160h"`
	PasswordResetTTL   time.Duration `env:"PASSWORD_RESET_TTL" envDefault:"30m"`
	PasswordMinLength  int           `env:"PASSWORD_MIN_LENGTH" envDefault:"10"`
	Argon2MemoryKB     uint32        `env:"ARGON2_MEMORY_KB" envDefault:"65536"`
	Argon2Iterations   uint32        `env:"ARGON2_ITERATIONS" envDefault:"3"`
	Argon2Parallelism  uint8         `env:"ARGON2_PARALLELISM" envDefault:"2"`
	ScryptSignerKey    string        `env:"FIREBASE_SCRYPT_SIGNER_KEY"`
	ScryptSaltSep      string        `env:"FIREBASE_SCRYPT_SALT_SEPARATOR"`
	ScryptRounds       int           `env:"FIREBASE_SCRYPT_ROUNDS"`
	ScryptMemCost      int           `env:"FIREBASE_SCRYPT_MEM_COST"`

	// Parsed by Validate.
	Scrypt *firebasescrypt.Params `env:"-"`
}

func (a *Auth) validate(errs *[]string) {
	if a.JWTAccessTTL < time.Minute || a.JWTAccessTTL > time.Hour {
		*errs = append(*errs, config.Invalidf("JWT_ACCESS_TTL", "must be between 1m and 1h"))
	}
	if a.RefreshTTLWeb <= 0 || a.RefreshTTLWeb > auth.WebAbsoluteTTL {
		*errs = append(*errs, config.Invalidf("REFRESH_TOKEN_TTL_WEB", "must be positive and at most 720h (the 30-day absolute cap)"))
	}
	if a.RefreshTTLMobile <= 0 {
		*errs = append(*errs, config.Invalidf("REFRESH_TOKEN_TTL_MOBILE", "must be positive"))
	}
	if a.PasswordResetTTL < time.Minute || a.PasswordResetTTL > 24*time.Hour {
		*errs = append(*errs, config.Invalidf("PASSWORD_RESET_TTL", "must be between 1m and 24h"))
	}
	if a.PasswordMinLength < 8 || a.PasswordMinLength > 128 {
		*errs = append(*errs, config.Invalidf("PASSWORD_MIN_LENGTH", "must be between 8 and 128"))
	}
	if a.Argon2MemoryKB < 8192 || a.Argon2Iterations < 1 || a.Argon2Parallelism < 1 {
		*errs = append(*errs, "ARGON2_MEMORY_KB, ARGON2_ITERATIONS, ARGON2_PARALLELISM: memory >= 8192, iterations and parallelism >= 1")
	}
	sp, err := firebasescrypt.ParseParams(a.ScryptSignerKey, a.ScryptSaltSep, a.ScryptRounds, a.ScryptMemCost)
	if err != nil {
		*errs = append(*errs, err.Error()+" (set all four FIREBASE_SCRYPT_* or none)")
	}
	a.Scrypt = sp
}

// APIConfig is the api process configuration.
type APIConfig struct {
	Common
	Runtime
	Database
	Redis
	Auth
	// RateLimit is RATE_LIMIT_ENABLED and RATE_LIMIT_{LOGIN,PUBLIC_FORMS,EVIDENCE}, parsed once by
	// ratelimit.Config (Validate below) for the rate-limit middleware and the auth buckets alike.
	RateLimit         ratelimit.Config
	InternalAddr      string   `env:"API_INTERNAL_ADDR,required,notEmpty"`
	PublicAddr        string   `env:"API_PUBLIC_ADDR,required,notEmpty"`
	PublicRouteGroups []string `env:"PUBLIC_ROUTE_GROUPS" envSeparator:"," envDefault:"/v1/mobile,/v1/auth,/public/v1,/evidence,/healthz"`
	TrustedProxyCIDRs []string `env:"TRUSTED_PROXY_CIDRS" envSeparator:","`

	// TrustedProxies is TrustedProxyCIDRs parsed by Validate.
	TrustedProxies []netip.Prefix `env:"-"`
}

// Validate implements config.Validator.
func (c *APIConfig) Validate() error {
	var errs []string
	c.Common.validate(&errs)
	c.Runtime.validate(&errs)
	c.Database.validate(&errs)
	c.Redis.validate(c.AppEnv, &errs)
	c.Auth.validate(&errs)
	if err := c.RateLimit.Validate(); err != nil {
		var cerr *config.Error
		if errors.As(err, &cerr) {
			errs = append(errs, cerr.Invalid...)
		} else {
			errs = append(errs, err.Error())
		}
	}
	for _, kv := range [][2]string{{"API_INTERNAL_ADDR", c.InternalAddr}, {"API_PUBLIC_ADDR", c.PublicAddr}} {
		if err := checkAddr(kv[1]); err != nil {
			errs = append(errs, config.Invalidf(kv[0], "%v", err))
		}
	}
	if c.InternalAddr == c.PublicAddr || c.InternalAddr == c.MetricsAddr || c.PublicAddr == c.MetricsAddr {
		errs = append(errs, "API_INTERNAL_ADDR, API_PUBLIC_ADDR, METRICS_ADDR: must be three different addresses")
	}
	if behindEdgeProxy(c.AppEnv) && hasHost(c.PublicAddr) {
		// Caddy dials "api" + API_PUBLIC_ADDR (deploy/Caddyfile), so "0.0.0.0:8081" would become the
		// upstream "api0.0.0.0:8081" and every API_PUBLIC_DOMAIN route a 502 (developer-spec.md §16.1).
		errs = append(errs, config.Invalidf("API_PUBLIC_ADDR",
			"must be :port when APP_ENV is dev or prod (the edge proxy dials api + this value)"))
	}
	groups := map[string]bool{}
	for _, g := range c.PublicRouteGroups {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if !ingress.IsPublicPrefix(g) {
			errs = append(errs, config.Invalidf("PUBLIC_ROUTE_GROUPS",
				"entries must be among %s (widening needs an ADR)", strings.Join(ingress.PublicPrefixes, ", ")))
			break
		}
		groups[g] = true
	}
	c.PublicRouteGroups = c.PublicRouteGroups[:0]
	for _, p := range ingress.PublicPrefixes {
		if groups[p] {
			c.PublicRouteGroups = append(c.PublicRouteGroups, p)
		}
	}
	c.TrustedProxies = nil
	for _, s := range c.TrustedProxyCIDRs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		p, err := parsePrefix(s)
		if err != nil {
			errs = append(errs, config.Invalidf("TRUSTED_PROXY_CIDRS", "entries must be IP addresses or CIDR prefixes"))
			break
		}
		c.TrustedProxies = append(c.TrustedProxies, p)
	}
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}

// WorkerConfig is shared by the worker and scheduler processes until their own
// settings (queues, cron) arrive in T10.
type WorkerConfig struct {
	Common
	Runtime
}

// Validate implements config.Validator.
func (c *WorkerConfig) Validate() error {
	var errs []string
	c.Common.validate(&errs)
	c.Runtime.validate(&errs)
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}

// MigrateConfig is the migrate process configuration: goose runs as logitrack_migrator (R66, R87).
type MigrateConfig struct {
	Common
	DatabaseURL string `env:"MIGRATE_DATABASE_URL,required,notEmpty"`
}

// Validate implements config.Validator.
func (c *MigrateConfig) Validate() error {
	var errs []string
	c.validate(&errs) // Common.validate
	if c.DatabaseURL != "" && !strings.HasPrefix(c.DatabaseURL, "postgres://") && !strings.HasPrefix(c.DatabaseURL, "postgresql://") {
		errs = append(errs, config.Invalidf("MIGRATE_DATABASE_URL", "must be a postgres:// URL"))
	}
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}

// checkAddr accepts "host:port", "[ipv6]:port" or ":port"; port 0 asks the OS
// for a free port (tests). The message never echoes the value.
func checkAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("must be host:port, [ipv6]:port or :port")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("port must be a number between 0 and 65535")
	}
	return nil
}

// behindEdgeProxy reports whether the public listener always runs behind Caddy: dev and prod are
// the compose deployments of §2.7. APP_ENV=local also covers `go run` on the host (README), where
// any listen address is fine.
func behindEdgeProxy(appEnv string) bool {
	return appEnv == "dev" || appEnv == "prod"
}

// hasHost reports whether a valid listen address names a host ("0.0.0.0:8081", "[::]:8081",
// "api:8081") rather than only a port (":8081"). Invalid addresses are reported by checkAddr.
func hasHost(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && host != ""
}

// parsePrefix accepts a CIDR prefix or a single address (as /32 or /128).
func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}
