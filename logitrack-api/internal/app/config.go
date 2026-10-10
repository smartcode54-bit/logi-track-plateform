// Package app wires processes together: configuration, logger, telemetry and
// the HTTP listeners. Env names follow main spec §16; values are never logged.
package app

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// AppEnvs are the allowed APP_ENV values (part of the Redis prefix, R26).
var AppEnvs = []string{"local", "dev", "prod"}

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
	if c.AppEnv != "" && !contains(AppEnvs, c.AppEnv) {
		*errs = append(*errs, config.Invalidf("APP_ENV", "must be one of %s", strings.Join(AppEnvs, ", ")))
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

// APIConfig is the api process configuration.
type APIConfig struct {
	Common
	Runtime
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
	for _, kv := range [][2]string{{"API_INTERNAL_ADDR", c.InternalAddr}, {"API_PUBLIC_ADDR", c.PublicAddr}} {
		if err := checkAddr(kv[1]); err != nil {
			errs = append(errs, config.Invalidf(kv[0], "%v", err))
		}
	}
	if c.InternalAddr == c.PublicAddr || c.InternalAddr == c.MetricsAddr || c.PublicAddr == c.MetricsAddr {
		errs = append(errs, "API_INTERNAL_ADDR, API_PUBLIC_ADDR, METRICS_ADDR: must be three different addresses")
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

func contains(list []string, v string) bool {
	return slices.Contains(list, v)
}
