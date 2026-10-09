package app_test

import (
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

func baseEnv() []string {
	return []string{
		"APP_ENV=local",
		"API_INTERNAL_ADDR=127.0.0.1:8080",
		"API_PUBLIC_ADDR=127.0.0.1:8081",
		"METRICS_ADDR=127.0.0.1:9090",
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
	for _, n := range []string{"API_INTERNAL_ADDR", "API_PUBLIC_ADDR", "METRICS_ADDR"} {
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
