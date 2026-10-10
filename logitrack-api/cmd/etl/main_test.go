package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The embedded fixture dump is committed only into a developer's own database: with APP_ENV dev or prod,
// `load --fixtures` is refused before any database is opened, while --dry-run passes the guard (and then needs
// ETL_DATABASE_URL like any load).
func TestLoadFixturesRefusedOutsideLocal(t *testing.T) {
	for _, appEnv := range []string{"dev", "prod"} {
		env := []string{"APP_ENV=" + appEnv, "LOG_FORMAT=json", "OWN_FLEET_TENANT_ID=01900000-0000-7000-8000-000000000001"}
		var out, errb bytes.Buffer
		code := run(context.Background(), []string{"load", "--fixtures"}, env, &out, &errb, deps{})
		if code != 2 || !strings.Contains(errb.String(), "load --fixtures commits synthetic documents; refused when APP_ENV="+appEnv) {
			t.Fatalf("APP_ENV=%s: exit %d\n%s", appEnv, code, errb.String())
		}
		out.Reset()
		errb.Reset()
		code = run(context.Background(), []string{"load", "--fixtures", "--dry-run"}, env, &out, &errb, deps{})
		if strings.Contains(errb.String(), "refused") || !strings.Contains(errb.String(), "ETL_DATABASE_URL") {
			t.Fatalf("APP_ENV=%s --dry-run must pass the guard and stop at the database: exit %d\n%s", appEnv, code, errb.String())
		}
	}
}
