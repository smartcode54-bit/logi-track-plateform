package main

import (
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
)

// A cut-down §16.1 with one row per consumer the checks distinguish.
const specFixture = "### 16.1 Canonical table\n" +
	"| Name | Secret | Consumed by | Phase | Status | Notes |\n" +
	"|---|---|---|---|---|---|\n" +
	"| `APP_ENV` | no | all-go | P0 / — | MISSING | |\n" +
	"| `DATABASE_URL` | yes | api, worker, scheduler, seed | P0 / — | MISSING | |\n" +
	"| `MIGRATE_DATABASE_URL` | yes | migrate, seed | P0 / — | MISSING | |\n" +
	"| `ETL_DATABASE_URL` | yes | etl, seed | P0 / — | MISSING | |\n" +
	"| `API_PUBLIC_ADDR` | no | api, caddy | P0 / — | MISSING | |\n" +
	"| `JWT_ISSUER` | no | api, web-server | P0 / — | MISSING | |\n" +
	"| `GO_API_INTERNAL_URL` | no | web-server, release | P0 / — | MISSING | |\n" +
	"| `WEB_PUBLIC_ORIGIN` | no | web-server | P0 / — | MISSING | |\n" +
	"| `PORT`, `HOSTNAME` | no | web-server | P0 / — | MISSING | |\n" +
	"| `NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID` | no | web-public | P0 / — | MISSING | |\n" +
	"| `NEXT_PUBLIC_FIREBASE_API_KEY`, `NEXT_PUBLIC_FIREBASE_APP_ID` | no | web-public | today / P6 | DROP | |\n" +
	"| `FIREBASE_PRIVATE_KEY` | yes | web-server | today / P0 | DROP | |\n" +
	"| `WEB_DOMAIN` | no | caddy | P0 / — | MISSING | |\n" +
	"| `API_BASE_URL` | no | mobile | P7a / — | MISSING | |\n" +
	"### 16.2 Missing\n" +
	"**Spellings not adopted**: `HTTP_ADDR`, `AMQP_*`.\n" +
	"### 16.5 Rules\n"

func fixtureInventory(t *testing.T) (inventory, map[string]bool, []string) {
	t.Helper()
	inv, banned, prefixes := parseSpec(specFixture)
	if len(inv.live) != 13 || len(inv.dropped) != 3 {
		t.Fatalf("parseSpec: %d live, %d dropped; want 13, 3", len(inv.live), len(inv.dropped))
	}
	return inv, banned, prefixes
}

func lines(kv ...string) []dotenv.Line {
	var out []dotenv.Line
	for _, s := range kv {
		k, v, _ := strings.Cut(s, "=")
		out = append(out, dotenv.Line{Raw: s, Key: k, Value: v})
	}
	return out
}

var goodEnv = []string{
	"APP_ENV=local", "DATABASE_URL=", "MIGRATE_DATABASE_URL=", "ETL_DATABASE_URL=", "API_PUBLIC_ADDR=:8081",
	"JWT_ISSUER=x", "GO_API_INTERNAL_URL=http://api:8080", "WEB_DOMAIN=http://localhost",
	"WEB_PUBLIC_ORIGIN=http://localhost", "NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID=", "NEXT_PUBLIC_FIREBASE_API_KEY=",
}

func TestEnvExample(t *testing.T) {
	inv, banned, prefixes := fixtureInventory(t)
	if p, _ := checkEnvExample("env", lines(goodEnv...), inv, banned, prefixes); len(p) != 0 {
		t.Fatalf("good env: %v", p)
	}
	cases := map[string]struct {
		env  []string
		want string
	}{
		"mobile name":           {append(clone(goodEnv), "API_BASE_URL=x"), "API_BASE_URL belongs to another consumer"},
		"dropped server secret": {append(clone(goodEnv), "FIREBASE_PRIVATE_KEY="), "FIREBASE_PRIVATE_KEY is a DROP row"},
		"unknown":               {append(clone(goodEnv), "NEXT_PUBLIC_API_URL=x"), "NEXT_PUBLIC_API_URL is not in §16.1"},
		"banned":                {append(clone(goodEnv), "HTTP_ADDR=x"), "HTTP_ADDR is a spelling not adopted"},
		"secret value":          {append(clone(goodEnv[1:]), "APP_ENV=local", "DATABASE_URL=postgres://x"), "DATABASE_URL is a secret"},
		"missing":               {goodEnv[1:], "APP_ENV is missing"},
	}
	for name, c := range cases {
		p, _ := checkEnvExample("env", lines(c.env...), inv, banned, prefixes)
		if !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: want %q in %v", name, c.want, p)
		}
	}
}

func TestInterpolation(t *testing.T) {
	seen := map[string]bool{"WEB_DOMAIN": true}
	raw := "a: ${WEB_DOMAIN:-}\nb: ${GHOST_NAME:-x}\nc: ${GHOST_NAME}\nd: $${NOT_INTERPOLATED}\n"
	p := checkInterpolation("compose.yml", raw, seen)
	if len(p) != 1 || !strings.Contains(p[0], "${GHOST_NAME}") {
		t.Fatalf("got %v", p)
	}
}

func str(s string) *string { return &s }

func goodCompose() composeConfig {
	var cfg composeConfig
	cfg.Services = map[string]struct {
		Environment map[string]*string `json:"environment"`
		Build       *struct {
			Args map[string]*string `json:"args"`
		} `json:"build"`
	}{}
	set := func(svc string, env map[string]*string, args map[string]*string) {
		s := cfg.Services[svc]
		s.Environment = env
		if args != nil {
			s.Build = &struct {
				Args map[string]*string `json:"args"`
			}{Args: args}
		}
		cfg.Services[svc] = s
	}
	set("api", map[string]*string{"APP_ENV": str(""), "DATABASE_URL": str(""), "API_PUBLIC_ADDR": str(""), "JWT_ISSUER": str("")}, nil)
	set("worker", map[string]*string{"APP_ENV": str(""), "DATABASE_URL": str("")}, nil)
	set("scheduler", map[string]*string{"APP_ENV": str(""), "DATABASE_URL": str("")}, nil)
	set("migrate", map[string]*string{"APP_ENV": str(""), "MIGRATE_DATABASE_URL": str("")}, nil)
	set("seed", map[string]*string{"APP_ENV": str(""), "DATABASE_URL": str(""), "MIGRATE_DATABASE_URL": str(""), "ETL_DATABASE_URL": str("")}, nil)
	set("etl", map[string]*string{"APP_ENV": str(""), "ETL_DATABASE_URL": str("")}, nil)
	set("web",
		map[string]*string{"JWT_ISSUER": str(""), "GO_API_INTERNAL_URL": str(""), "WEB_PUBLIC_ORIGIN": str(""), "PORT": str("3000"), "HOSTNAME": str("0.0.0.0")},
		map[string]*string{"NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID": str(""), "NEXT_PUBLIC_FIREBASE_API_KEY": str("")})
	set("caddy", map[string]*string{"API_PUBLIC_ADDR": str(""), "WEB_DOMAIN": str("")}, nil)
	return cfg
}

func TestCompose(t *testing.T) {
	inv, _, _ := fixtureInventory(t)
	if p := checkCompose(goodCompose(), inv); len(p) != 0 {
		t.Fatalf("good compose: %v", p)
	}
	cases := map[string]struct {
		mutate func(c composeConfig)
		want   string
	}{
		"web gets a Go-only name":   {func(c composeConfig) { c.Services["web"].Environment["DATABASE_URL"] = str("") }, "web receives DATABASE_URL"},
		"web misses a server name":  {func(c composeConfig) { delete(c.Services["web"].Environment, "WEB_PUBLIC_ORIGIN") }, "web does not receive WEB_PUBLIC_ORIGIN"},
		"server value as build arg": {func(c composeConfig) { c.Services["web"].Build.Args["GO_API_INTERNAL_URL"] = str("") }, "build arg GO_API_INTERNAL_URL is not"},
		"missing web-public arg":    {func(c composeConfig) { delete(c.Services["web"].Build.Args, "NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID") }, "does not receive NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID"},
		"caddy gets more":           {func(c composeConfig) { c.Services["caddy"].Environment["JWT_ISSUER"] = str("") }, "caddy receives JWT_ISSUER"},
		"caddy misses upstream":     {func(c composeConfig) { delete(c.Services["caddy"].Environment, "API_PUBLIC_ADDR") }, "caddy does not receive API_PUBLIC_ADDR"},
		"api gets every DB URL":     {func(c composeConfig) { c.Services["api"].Environment["ETL_DATABASE_URL"] = str("") }, "api DB URLs"},
	}
	for name, c := range cases {
		cfg := goodCompose()
		c.mutate(cfg)
		if p := checkCompose(cfg, inv); !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: want %q in %v", name, c.want, p)
		}
	}
}

func clone(s []string) []string { return append([]string(nil), s...) }
