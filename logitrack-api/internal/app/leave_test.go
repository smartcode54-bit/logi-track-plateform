package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// Leave decisions write PostgreSQL only once every domain is PostgreSQL-owned (T45): hr alone is not enough, since the
// 3.x APKs keep writing leave requests in Firestore until P7b.
func TestLeavePGOwned(t *testing.T) {
	for v, want := range map[string]bool{
		"all": true,
		"auth,masterdata,operations,billing,hr,comms,security,mobile_release,dashboard":  true,
		"dashboard, mobile_release,security,comms,hr,billing,operations,masterdata,auth": true,
		"auth,masterdata,billing,hr": false,
		"hr":                         false,
		"":                           false,
		"all,hr":                     false, // invalid: refused by the config, never all-owned
	} {
		if got := leavePGOwned(v); got != want {
			t.Errorf("leavePGOwned(%q) = %v, want %v", v, got, want)
		}
	}
}

// writeBackConfig loads a startable api environment (placeholders: loading the configuration reads no file and dials
// nothing) plus extra.
func writeBackConfig(t *testing.T, extra ...string) *APIConfig {
	t.Helper()
	env := []string{"APP_ENV=local", "API_INTERNAL_ADDR=127.0.0.1:8080", "API_PUBLIC_ADDR=127.0.0.1:8081",
		"METRICS_ADDR=127.0.0.1:9090", "DATABASE_URL=postgres://logitrack_app@localhost:5432/logitrack",
		"REDIS_URL=redis://localhost:6379/0", "JWT_SIGNING_KEY_FILE=/nonexistent/jwt.pem", "JWT_ACTIVE_KID=test-kid",
		"JWT_ISSUER=http://localhost:8080", "JWT_AUDIENCE=logitrack-test", "S3_ENDPOINT=http://minio:9000",
		"S3_PRESIGN_ENDPOINT=http://localhost:9000", "S3_REGION=us-east-1", "S3_ACCESS_KEY_ID=placeholder",
		"S3_SECRET_ACCESS_KEY=placeholder"}
	cfg, err := config.LoadFrom[APIConfig](append(env, extra...))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// testCaches is a cache over a Redis client that is never dialled (go-redis connects on the first command).
func testCaches(t *testing.T) *cache.Cache {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = rdb.Close() })
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	return cache.New(rdb, ks)
}

// noPool fails every transaction: a decision that reaches the database is not the one the test expects.
type noPool struct{}

func (noPool) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("no database in this test")
}

// The write-backs the api builds (T31, T45), without I/O: one Firestore client of WRITEBACK_FIRESTORE_PROJECT_ID shared
// by the operations writes and the leave decisions, built only when one of them is in use; every domain owned is the
// PostgreSQL path with no writer; without a project the leave decisions answer 503 write_back_unconfigured.
func TestBuildWriteBacks(t *testing.T) {
	caches := testCaches(t)
	key := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(key, gcptest.New(t, "logitrack-writeback-test").ServiceAccountJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "secret-sa-name.json")
	project := "WRITEBACK_FIRESTORE_PROJECT_ID=logitrack-writeback-test"
	build := func(t *testing.T, extra ...string) (writeBacks, string, error) {
		t.Helper()
		var logs bytes.Buffer
		wbs, err := buildWriteBacks(writeBackConfig(t, extra...), caches, zerolog.New(&logs))
		return wbs, logs.String(), err
	}

	t.Run("every domain owned: PostgreSQL, no writer", func(t *testing.T) {
		// The key file is missing: it is never read, since no client is built.
		wbs, logs, err := build(t, "PG_OWNED_DOMAINS=all", project, "GOOGLE_APPLICATION_CREDENTIALS="+missing)
		if err != nil || !wbs.LeavePG || wbs.Leave != nil || wbs.Operations != nil {
			t.Fatalf("%+v %v", wbs, err)
		}
		if !strings.Contains(logs, "leave decisions: PostgreSQL") || !strings.Contains(logs, "operations writes: PostgreSQL") {
			t.Errorf("logs %s", logs)
		}
	})

	t.Run("project unset: no writer, decisions 503 write_back_unconfigured", func(t *testing.T) {
		for _, owned := range []string{"", "auth,masterdata,operations,billing,hr"} {
			wbs, logs, err := build(t, "PG_OWNED_DOMAINS="+owned)
			if err != nil || wbs.LeavePG || wbs.Leave != nil || wbs.Operations != nil {
				t.Fatalf("%q: %+v %v", owned, wbs, err)
			}
			if !strings.Contains(logs, "WRITEBACK_FIRESTORE_PROJECT_ID is unset (POST /v1/leave-requests/{id}/* answer 503") ||
				strings.Contains(logs, "builds no leave write-back") {
				t.Errorf("%q: logs %s", owned, logs)
			}
			svc, err := hr.New(hr.Deps{Pool: noPool{}, LeavePGOwned: wbs.LeavePG, LeaveWriteBack: wbs.Leave, Log: zerolog.Nop()})
			if err != nil {
				t.Fatal(err)
			}
			tenant := uuid.New()
			_, err = svc.DecideLeave(context.Background(), &authz.Principal{UserID: uuid.New(), TenantID: &tenant,
				TenantRole: authz.TenantAdmin}, "", uuid.New(), hr.ActionApprove, hr.LeaveDecision{})
			var he *httpx.Error
			if !errors.As(err, &he) || he.Status != http.StatusServiceUnavailable || he.Details["reason"] != hr.ReasonWriteBackUnconfigured {
				t.Fatalf("%q: decision %v", owned, err)
			}
		}
	})

	t.Run("project set: the leave write-back over the shared client", func(t *testing.T) {
		wbs, logs, err := build(t, "PG_OWNED_DOMAINS=", project, "GOOGLE_APPLICATION_CREDENTIALS="+key)
		if err != nil || wbs.LeavePG || wbs.Leave == nil || wbs.Operations == nil {
			t.Fatalf("%+v %v", wbs, err)
		}
		lw, ok := wbs.Leave.Writer.(writeback.Firestore)
		ow, ok2 := wbs.Operations.Writer.(writeback.Firestore)
		if !ok || !ok2 || lw.FS == nil || lw.FS != ow.FS {
			t.Fatalf("not one shared Firestore client: %#v %#v", wbs.Leave.Writer, wbs.Operations.Writer)
		}
		if acks, ok := wbs.Leave.Acks.(*cache.Cache); !ok || acks != caches || wbs.Leave.Wait != hr.DefaultMirrorWait {
			t.Errorf("acks %#v wait %v", wbs.Leave.Acks, wbs.Leave.Wait)
		}
		if !strings.Contains(logs, "leave decisions: Firestore write-back") || strings.Contains(logs, "PRIVATE KEY") {
			t.Errorf("logs %s", logs)
		}
		// operations owned but not every domain: the leave decisions alone still get the writer (before T45's
		// wiring no client was built then).
		wbs, _, err = build(t, "PG_OWNED_DOMAINS=auth,masterdata,operations,billing,hr", project, "GOOGLE_APPLICATION_CREDENTIALS="+key)
		if err != nil || wbs.LeavePG || wbs.Operations != nil || wbs.Leave == nil {
			t.Fatalf("operations owned: %+v %v", wbs, err)
		}
		if w, ok := wbs.Leave.Writer.(writeback.Firestore); !ok || w.FS == nil {
			t.Fatalf("writer %#v", wbs.Leave.Writer)
		}
	})

	t.Run("an unreadable key file names the variable, never the path", func(t *testing.T) {
		_, _, err := build(t, "PG_OWNED_DOMAINS=auth,masterdata,operations", project, "GOOGLE_APPLICATION_CREDENTIALS="+missing)
		var ce *config.Error
		if !errors.As(err, &ce) || !strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS") || strings.Contains(err.Error(), "secret-sa-name") {
			t.Fatalf("%v", err)
		}
	})
}
