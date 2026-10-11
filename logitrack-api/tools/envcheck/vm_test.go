package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
)

var goodVMEnv = []string{
	"# per-process (ecosystem.config.cjs): API_PUBLIC_ADDR GO_API_INTERNAL_URL PORT HOSTNAME",
	"APP_ENV=dev", "DATABASE_URL=", "MIGRATE_DATABASE_URL=", "ETL_DATABASE_URL=", "JWT_ISSUER=https://api.example.test",
	"WEB_PUBLIC_ORIGIN=https://web.example.test",
}

// vmLines keeps comment lines, as dotenv.Read does.
func vmLines(kv ...string) []dotenv.Line {
	var out []dotenv.Line
	for _, s := range kv {
		if strings.HasPrefix(s, "#") {
			out = append(out, dotenv.Line{Raw: s})
			continue
		}
		out = append(out, lines(s)...)
	}
	return out
}

func TestVMEnv(t *testing.T) {
	inv, banned, prefixes := fixtureInventory(t)
	p, perProcess := checkVMEnv("vm", vmLines(goodVMEnv...), inv, banned, prefixes)
	if len(p) != 0 {
		t.Fatalf("good VM env: %v", p)
	}
	if len(perProcess) != 4 || !perProcess["GO_API_INTERNAL_URL"] {
		t.Fatalf("per-process names: %v", perProcess)
	}
	cases := map[string]struct {
		env  []string
		want string
	}{
		"secret value":          {replace(goodVMEnv, "DATABASE_URL=postgres://u:p@h/db"), "DATABASE_URL is a secret and must be blank"},
		"missing name":          {goodVMEnv[:len(goodVMEnv)-1], "WEB_PUBLIC_ORIGIN is missing"},
		"per-process as a key":  {append(clone(goodVMEnv), "PORT=3300"), "PORT is per-process"},
		"not declared anywhere": {append([]string{"# per-process (ecosystem.config.cjs): API_PUBLIC_ADDR PORT HOSTNAME"}, goodVMEnv[1:]...), "GO_API_INTERNAL_URL is missing"},
		"caddy name":            {append(clone(goodVMEnv), "WEB_DOMAIN=x"), "WEB_DOMAIN belongs to no program"},
		"unknown":               {append(clone(goodVMEnv), "NEXT_PUBLIC_API_URL=x"), "NEXT_PUBLIC_API_URL is not in §16.1"},
		"dropped":               {append(clone(goodVMEnv), "FIREBASE_PRIVATE_KEY="), "FIREBASE_PRIVATE_KEY is a DROP row"},
		"build arg in the .env": {append(clone(goodVMEnv), "NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID=x"), "NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID belongs to no program"},
		"banned":                {append(clone(goodVMEnv), "HTTP_ADDR=x"), "HTTP_ADDR is not in §16.1"},
		"secret per-process":    {append([]string{goodVMEnv[0] + " DATABASE_URL"}, goodVMEnv[1:]...), "per-process name DATABASE_URL is a secret"},
	}
	for name, c := range cases {
		p, _ := checkVMEnv("vm", vmLines(c.env...), inv, banned, prefixes)
		if !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: want %q in %v", name, c.want, p)
		}
	}
}

func TestVMNames(t *testing.T) {
	inv, _, _ := fixtureInventory(t)
	good := map[string]string{
		"api":       "# api\nAPP_ENV\nDATABASE_URL\nAPI_PUBLIC_ADDR\nJWT_ISSUER\n",
		"worker":    "APP_ENV\nDATABASE_URL\n",
		"scheduler": "APP_ENV\nDATABASE_URL\n",
		"migrate":   "APP_ENV\nMIGRATE_DATABASE_URL\n",
		"seed":      "APP_ENV\nDATABASE_URL\nMIGRATE_DATABASE_URL\nETL_DATABASE_URL\n",
		"web":       "JWT_ISSUER\nGO_API_INTERNAL_URL\nWEB_PUBLIC_ORIGIN\nPORT\nHOSTNAME\n",
		"release":   "GO_API_INTERNAL_URL\n",
	}
	write := func(t *testing.T, files map[string]string) string {
		dir := t.TempDir()
		for p, c := range files {
			if err := os.WriteFile(filepath.Join(dir, p+".txt"), []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	if p := checkVMNames(write(t, good), inv); len(p) != 0 {
		t.Fatalf("good names: %v", p)
	}
	cases := map[string]struct {
		mutate func(m map[string]string)
		want   string
	}{
		"web gets a Go secret":  {func(m map[string]string) { m["web"] += "DATABASE_URL\n" }, "web.txt: DATABASE_URL is not a §16.1 name of web"},
		"api misses a name":     {func(m map[string]string) { m["api"] = "APP_ENV\nDATABASE_URL\nAPI_PUBLIC_ADDR\n" }, "api.txt: JWT_ISSUER is missing"},
		"worker gets every URL": {func(m map[string]string) { m["worker"] += "ETL_DATABASE_URL\n" }, "worker.txt: ETL_DATABASE_URL is not a §16.1 name"},
		"missing file":          {func(m map[string]string) { delete(m, "seed") }, "seed.txt is missing"},
		"unknown program":       {func(m map[string]string) { m["etl"] = "APP_ENV\n" }, "etl.txt is not a program"},
		"not a name":            {func(m map[string]string) { m["migrate"] += "APP_ENV=dev\n" }, "expected one variable name per line"},
	}
	for name, c := range cases {
		files := map[string]string{}
		for k, v := range good {
			files[k] = v
		}
		c.mutate(files)
		if p := checkVMNames(write(t, files), inv); !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: want %q in %v", name, c.want, p)
		}
	}
}
