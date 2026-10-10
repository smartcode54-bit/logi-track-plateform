package app_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

func baseEnv() []string {
	return []string{
		"APP_ENV=local",
		"API_INTERNAL_ADDR=127.0.0.1:8080",
		"API_PUBLIC_ADDR=127.0.0.1:8081",
		"METRICS_ADDR=127.0.0.1:9090",
		// Placeholders: config loading never connects or reads the key file (BuildAPI does).
		"DATABASE_URL=postgres://logitrack_app@localhost:5432/logitrack",
		"REDIS_URL=redis://localhost:6379/0",
		"JWT_SIGNING_KEY_FILE=/nonexistent/jwt.pem",
		"JWT_ACTIVE_KID=test-kid",
		"JWT_ISSUER=http://localhost:8080",
		"JWT_AUDIENCE=logitrack-test",
	}
}

func TestAPIConfigDefaults(t *testing.T) {
	cfg, err := config.LoadFrom[app.APIConfig](baseEnv())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.PublicRouteGroups, ","); got != "/v1/mobile,/v1/auth,/public/v1,/evidence,/healthz" {
		t.Fatalf("default allow-list = %s", got)
	}
	if cfg.LogLevel != "info" || cfg.LogFormat != "json" || cfg.ShutdownTimeout.Seconds() != 30 {
		t.Fatalf("defaults: %+v", cfg)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Fatalf("no proxy is trusted by default: %v", cfg.TrustedProxies)
	}
}

func TestAPIConfigMissingRequired(t *testing.T) {
	_, err := config.LoadFrom[app.APIConfig]([]string{"APP_ENV=local"})
	if err == nil {
		t.Fatal("want error")
	}
	for _, n := range []string{"API_INTERNAL_ADDR", "API_PUBLIC_ADDR", "METRICS_ADDR", "DATABASE_URL", "REDIS_URL",
		"JWT_SIGNING_KEY_FILE", "JWT_ACTIVE_KID", "JWT_ISSUER", "JWT_AUDIENCE"} {
		if !strings.Contains(err.Error(), n) {
			t.Fatalf("error does not name %s: %v", n, err)
		}
	}
}

func TestAPIConfigValidation(t *testing.T) {
	cases := map[string]struct {
		env  []string
		want string
	}{
		"bad APP_ENV":          {[]string{"APP_ENV=staging"}, "APP_ENV: must be one of local, dev, prod"},
		"widened allow-list":   {[]string{"PUBLIC_ROUTE_GROUPS=/v1/mobile,/v1/me"}, "PUBLIC_ROUTE_GROUPS"},
		"bad proxy CIDR":       {[]string{"TRUSTED_PROXY_CIDRS=10.0.0.0/8,caddy"}, "TRUSTED_PROXY_CIDRS"},
		"same addresses":       {[]string{"API_PUBLIC_ADDR=127.0.0.1:8080"}, "must be three different addresses"},
		"bad log format":       {[]string{"LOG_FORMAT=xml"}, "LOG_FORMAT"},
		"sampler out of range": {[]string{"OTEL_TRACES_SAMPLER_ARG=2"}, "OTEL_TRACES_SAMPLER_ARG"},
		"shutdown too short":   {[]string{"SHUTDOWN_TIMEOUT=10ms"}, "SHUTDOWN_TIMEOUT"},
		"address without port": {[]string{"API_PUBLIC_ADDR=localhost"}, "API_PUBLIC_ADDR"},
		"bare IPv6":            {[]string{"API_INTERNAL_ADDR=::1"}, "API_INTERNAL_ADDR"},
		"port out of range":    {[]string{"METRICS_ADDR=127.0.0.1:99999"}, "METRICS_ADDR"},
		"port not a number":    {[]string{"API_PUBLIC_ADDR=localhost:abc"}, "API_PUBLIC_ADDR"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := override(baseEnv(), tc.env)
			_, err := config.LoadFrom[app.APIConfig](env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// Caddy dials "api" + API_PUBLIC_ADDR, so behind the edge proxy (APP_ENV dev, prod) only ":port"
// works; "0.0.0.0:8081" would adapt to the upstream "api0.0.0.0:8081" (502 on every public route)
// and "[::]:8081" would stop Caddy from starting. Local runs (go run on the host) keep host:port.
func TestAPIConfigPublicAddrBehindEdgeProxy(t *testing.T) {
	for _, appEnv := range []string{"dev", "prod"} {
		for _, addr := range []string{"0.0.0.0:8081", "[::]:8081", "api:8081", "127.0.0.1:8081"} {
			t.Run(appEnv+" "+addr, func(t *testing.T) {
				_, err := config.LoadFrom[app.APIConfig](override(baseEnv(), []string{"APP_ENV=" + appEnv, "API_PUBLIC_ADDR=" + addr}))
				if err == nil || !strings.Contains(err.Error(), "API_PUBLIC_ADDR: must be :port when APP_ENV is dev or prod") {
					t.Fatalf("want the :port error, got %v", err)
				}
				if strings.Contains(err.Error(), addr) {
					t.Fatalf("the error echoes the value: %v", err)
				}
			})
		}
		t.Run(appEnv+" :8081", func(t *testing.T) {
			if _, err := config.LoadFrom[app.APIConfig](override(baseEnv(), []string{"APP_ENV=" + appEnv, "API_PUBLIC_ADDR=:8081"})); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := config.LoadFrom[app.APIConfig](override(baseEnv(), []string{"APP_ENV=local", "API_PUBLIC_ADDR=0.0.0.0:8081"})); err != nil {
		t.Fatalf("local keeps host:port: %v", err)
	}
}

func TestAPIConfigParsesProxies(t *testing.T) {
	cfg, err := config.LoadFrom[app.APIConfig](override(baseEnv(), []string{
		"TRUSTED_PROXY_CIDRS=172.18.0.0/16, 10.1.2.3 ,::1",
		"PUBLIC_ROUTE_GROUPS=/healthz,/v1/mobile",
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, p := range cfg.TrustedProxies {
		got = append(got, p.String())
	}
	if strings.Join(got, ",") != "172.18.0.0/16,10.1.2.3/32,::1/128" {
		t.Fatalf("proxies = %v", got)
	}
	// Normalised to the canonical order of ingress.PublicPrefixes.
	if strings.Join(cfg.PublicRouteGroups, ",") != "/v1/mobile,/healthz" {
		t.Fatalf("groups = %v", cfg.PublicRouteGroups)
	}
}

func override(base, extra []string) []string {
	m := map[string]string{}
	order := []string{}
	for _, kv := range append(append([]string{}, base...), extra...) {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := m[k]; !ok {
			order = append(order, k)
		}
		m[k] = kv
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, m[k])
	}
	return out
}

func TestAPIConfigAcceptsBracketedIPv6(t *testing.T) {
	if _, err := config.LoadFrom[app.APIConfig](override(baseEnv(), []string{"METRICS_ADDR=[::1]:9091"})); err != nil {
		t.Fatal(err)
	}
}

func TestAPIConfigAuthDefaults(t *testing.T) {
	cfg, err := config.LoadFrom[app.APIConfig](baseEnv())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JWTAccessTTL != 15*time.Minute || cfg.RefreshTTLWeb != 168*time.Hour || cfg.RefreshTTLMobile != 2160*time.Hour ||
		cfg.PasswordResetTTL != 30*time.Minute || cfg.PasswordMinLength != 10 {
		t.Fatalf("lifetimes: %+v", cfg.Auth)
	}
	if cfg.Argon2MemoryKB != 65536 || cfg.Argon2Iterations != 3 || cfg.Argon2Parallelism != 2 {
		t.Fatalf("argon2: %+v", cfg.Auth)
	}
	if !cfg.RateLimit.Enabled || cfg.RateLimit.Limit(ratelimit.LoginIP) != (ratelimit.Limit{Count: 10, Window: time.Minute}) ||
		cfg.RateLimit.Limit(ratelimit.PublicFormIP) != (ratelimit.Limit{Count: 5, Window: time.Hour}) ||
		cfg.RateLimit.Limit(ratelimit.EvidenceIP) != (ratelimit.Limit{Count: 60, Window: time.Minute}) {
		t.Fatalf("rate limits: %+v", cfg.RateLimit)
	}
	if cfg.RedisKeyPrefix != "lt:local:" {
		t.Fatalf("redis prefix derived from APP_ENV: %q", cfg.RedisKeyPrefix)
	}
	if cfg.Scrypt != nil {
		t.Fatal("no FIREBASE_SCRYPT_* means no legacy verification")
	}
}

func TestAPIConfigAuthValidation(t *testing.T) {
	cases := map[string]struct {
		env  []string
		want string
	}{
		"database scheme":     {[]string{"DATABASE_URL=mysql://x"}, "DATABASE_URL: must be a postgres:// URL"},
		"redis scheme":        {[]string{"REDIS_URL=http://x"}, "REDIS_URL"},
		"redis prefix":        {[]string{"REDIS_KEY_PREFIX=lt:prod:"}, "REDIS_KEY_PREFIX: must be lt:{APP_ENV}:"},
		"access ttl":          {[]string{"JWT_ACCESS_TTL=5h"}, "JWT_ACCESS_TTL"},
		"web refresh ttl":     {[]string{"REFRESH_TOKEN_TTL_WEB=1000h"}, "REFRESH_TOKEN_TTL_WEB"},
		"password min length": {[]string{"PASSWORD_MIN_LENGTH=4"}, "PASSWORD_MIN_LENGTH"},
		"argon2 memory":       {[]string{"ARGON2_MEMORY_KB=1024"}, "ARGON2_MEMORY_KB"},
		"login rate":          {[]string{"RATE_LIMIT_LOGIN=ten"}, "RATE_LIMIT_LOGIN"},
		"login rate interval": {[]string{"RATE_LIMIT_LOGIN=1001/1ms"}, "RATE_LIMIT_LOGIN"},
		"public forms rate":   {[]string{"RATE_LIMIT_PUBLIC_FORMS=5/1d"}, "RATE_LIMIT_PUBLIC_FORMS"},
		"evidence rate":       {[]string{"RATE_LIMIT_EVIDENCE=-5/1m"}, "RATE_LIMIT_EVIDENCE"},
		"rate limit switch":   {[]string{"RATE_LIMIT_ENABLED=maybe"}, "RATE_LIMIT_ENABLED"},
		"scrypt partial":      {[]string{"FIREBASE_SCRYPT_ROUNDS=8"}, "FIREBASE_SCRYPT_SIGNER_KEY"},
		"scrypt bad base64":   {[]string{"FIREBASE_SCRYPT_SIGNER_KEY=not*base64!", "FIREBASE_SCRYPT_SALT_SEPARATOR=Bw==", "FIREBASE_SCRYPT_ROUNDS=8", "FIREBASE_SCRYPT_MEM_COST=14"}, "FIREBASE_SCRYPT_SIGNER_KEY: must be base64"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := config.LoadFrom[app.APIConfig](override(baseEnv(), tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
			for _, kv := range tc.env {
				if _, v, _ := strings.Cut(kv, "="); len(v) > 3 && strings.Contains(err.Error(), v) {
					t.Fatalf("the error echoes a value: %v", err)
				}
			}
		})
	}
}

func TestAPIConfigParsesScryptParams(t *testing.T) {
	// A placeholder, not a key: the bytes 0..63 in unpadded URL-safe base64, built at run time so the
	// source holds no literal a secret scanner could take for one.
	placeholder := make([]byte, 64)
	for i := range placeholder {
		placeholder[i] = byte(i)
	}
	cfg, err := config.LoadFrom[app.APIConfig](override(baseEnv(), []string{
		"FIREBASE_SCRYPT_SIGNER_KEY=" + base64.RawURLEncoding.EncodeToString(placeholder),
		"FIREBASE_SCRYPT_SALT_SEPARATOR=Bw==", "FIREBASE_SCRYPT_ROUNDS=8", "FIREBASE_SCRYPT_MEM_COST=14",
		"RATE_LIMIT_LOGIN=20/30s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scrypt == nil || !bytes.Equal(cfg.Scrypt.SignerKey, placeholder) || cfg.Scrypt.Rounds != 8 || cfg.Scrypt.MemCost != 14 {
		t.Fatalf("scrypt params: %+v", cfg.Scrypt)
	}
	if cfg.RateLimit.Limit(ratelimit.LoginIP) != (ratelimit.Limit{Count: 20, Window: 30 * time.Second}) {
		t.Fatalf("login rate: %+v", cfg.RateLimit)
	}
}
