//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type brokerReadBackend struct {
	storage.Backend
	source    storage.Reader
	afterRead func()
}

func (b *brokerReadBackend) Read(ctx context.Context, o storage.Object) (io.ReadCloser, error) {
	reader, err := b.source.Read(ctx, o)
	if err != nil {
		return nil, err
	}
	return &brokerReader{ReadCloser: reader, afterRead: b.afterRead}, nil
}

type brokerReader struct {
	io.ReadCloser
	afterRead func()
}

func (r *brokerReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.afterRead != nil {
		fn := r.afterRead
		r.afterRead = nil
		fn()
	}
	return n, err
}

func TestSupportBrokerLocalAndS3HTTPContracts(t *testing.T) {
	for _, kind := range []string{storage.BackendLocal, storage.BackendS3} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			setConsent := func(enabled bool) error {
				tx, err := f.etl.Begin(ctx)
				if err != nil {
					return err
				}
				defer func() { _ = tx.Rollback(ctx) }()
				if _, err = tx.Exec(ctx, `SELECT set_config('app.role','system',true),set_config('app.bypass_tenant','on',true)`); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, `UPDATE tenants SET support_access_enabled=$3,support_access_version=support_access_version+1,support_access_changed_at=now(),support_access_changed_by=$2 WHERE id=$1`, f.own, f.staff, enabled); err != nil {
					return err
				}
				return tx.Commit(ctx)
			}
			bucket := "logitrack"
			var base storage.Backend
			var svc *storage.Service
			var wrapped *brokerReadBackend
			if kind == storage.BackendLocal {
				base = f.local(t)
				wrapped = &brokerReadBackend{Backend: base, source: base.(storage.Reader)}
				svc = f.service(t, kind, wrapped)
			} else {
				s3, cfg := s3Backend(t)
				base = s3
				bucket = cfg.Bucket
				wrapped = &brokerReadBackend{Backend: base, source: s3}
				svc = f.serviceOn(t, kind, cfg, wrapped)
			}
			var driver uuid.UUID
			if err := f.etl.QueryRow(ctx, `INSERT INTO drivers(tenant_id,tenant_source,first_name,last_name,mobile)VALUES($1,'self','Private','Driver','0800000001')RETURNING id`, f.own).Scan(&driver); err != nil {
				t.Fatal(err)
			}
			body := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("PRIVATE_FILE_SENTINEL"), 4096)...)
			body = append(body, []byte("\n%%EOF")...)
			key := "drivers/" + driver.String() + "/license-" + uuid.NewString() + ".pdf"
			if err := base.Put(ctx, storage.Object{Bucket: bucket, Key: key}, bytes.NewReader(body), int64(len(body)), "application/pdf"); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(body)
			var file uuid.UUID
			if err := f.etl.QueryRow(ctx, `INSERT INTO file_objects(bucket,object_key,tenant_id,purpose,owner_kind,owner_id,status,committed_at,storage_backend,content_type,size_bytes,sha256)VALUES($1,$2,$3,'driver_license','driver',$4,'committed',now(),$5,'application/pdf',$6,$7)RETURNING id`, bucket, key, f.own, driver, kind, len(body), hex.EncodeToString(sum[:])).Scan(&file); err != nil {
				t.Fatal(err)
			}
			if _, err := f.etl.Exec(ctx, `UPDATE drivers SET license_file_id=$2 WHERE id=$1`, driver, file); err != nil {
				t.Fatal(err)
			}
			tenant := f.own.String()
			consents := map[string]httpx.Consent{tenant: {Version: "0"}}
			principal := &authz.Principal{UserID: f.staff, SessionID: uuid.New(), TenantID: &f.own, AccessMode: "support", TenantRole: authz.SupportReadOnly, TokenExpiresAt: time.Now().Add(time.Hour)}
			audience := httpx.ProjectionAudience{Support: true, Platform: true, Tenant: tenant, Actor: f.staff.String(), Session: principal.SessionID.String(), ContextVersion: "1", ConsentVersion: "0", Consents: consents}
			audience.Finalize = func(ctx context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
				var enabled bool
				var version int64
				if err := f.etl.QueryRow(ctx, `SELECT support_access_enabled,support_access_version FROM tenants WHERE id=$1`, f.own).Scan(&enabled, &version); err != nil {
					return err
				}
				return fn(map[string]httpx.Consent{tenant: {Enabled: enabled, Version: strconv.FormatInt(version, 10)}})
			}
			app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
			app.Use(httpx.RequestID(), httpx.AdmissionGuard(nil))
			ingress.Mount(app, ingress.Internal, svc.Groups(storage.HTTPOptions{Auth: func(c fiber.Ctx) error {
				if c.Get("Authorization") != "Bearer fixture-support" {
					return httpx.ErrUnauthenticated()
				}
				authz.SetPrincipal(c, principal)
				httpx.SetProjectionAudience(c, audience)
				return c.Next()
			}}), nil)
			target := "/v1/support/files/" + file.String() + "/content"
			send := func(method, path, token, rangeValue string) (int, []byte, map[string]string) {
				req := httptest.NewRequest(method, path, nil)
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				if rangeValue != "" {
					req.Header.Set("Range", rangeValue)
				}
				res, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = res.Body.Close() }()
				raw, _ := io.ReadAll(res.Body)
				headers := map[string]string{}
				for _, name := range []string{"Cache-Control", "Content-Security-Policy", "X-Content-Type-Options", "Content-Disposition", "Content-Range", "Content-Length", "Location", "ETag"} {
					headers[name] = res.Header.Get(name)
				}
				return res.StatusCode, raw, headers
			}
			if status, _, _ := send("GET", target, "", ""); status != 401 {
				t.Fatal("anonymous", status)
			}
			if status, raw, _ := send("GET", target, "fixture-support", ""); status != 403 || bytes.Contains(raw, []byte("PRIVATE_FILE")) {
				t.Fatal("OFF", status, string(raw))
			}
			if err := setConsent(true); err != nil {
				t.Fatal(err)
			}
			audience.ConsentVersion = "1"
			consents[tenant] = httpx.Consent{Enabled: true, Version: "1"}
			for _, tc := range []struct {
				method, rangeValue string
				status             int
				want               []byte
			}{{"GET", "", 200, body}, {"HEAD", "", 200, nil}, {"GET", "bytes=0-3", 206, []byte("%PDF")}, {"HEAD", "bytes=0-3", 206, nil}} {
				status, raw, head := send(tc.method, target, "fixture-support", tc.rangeValue)
				if status != tc.status || !bytes.Equal(raw, tc.want) || head["Location"] != "" || head["ETag"] != "" || head["Content-Security-Policy"] != "sandbox; default-src 'none'" || head["Cache-Control"] != "private, no-store" || head["X-Content-Type-Options"] != "nosniff" || head["Content-Disposition"] != "attachment; filename=\"document.pdf\"" {
					t.Fatal(tc.method, tc.rangeValue, status, len(raw), head)
				}
			}
			if _, err := f.etl.Exec(ctx, `UPDATE drivers SET license_file_id=NULL WHERE id=$1`, driver); err != nil {
				t.Fatal(err)
			}
			if status, _, _ := send("GET", target, "fixture-support", ""); status != 404 {
				t.Fatal("unlinked file", status)
			}
			if _, err := f.etl.Exec(ctx, `UPDATE drivers SET license_file_id=$2 WHERE id=$1`, driver, file); err != nil {
				t.Fatal(err)
			}
			wrapped.afterRead = func() {
				if err := setConsent(false); err != nil {
					t.Fatal(err)
				}
			}
			status, raw, head := send("GET", target, "fixture-support", "")
			if status != 403 || strings.Contains(string(raw), "PRIVATE_FILE") || head["Location"] != "" {
				t.Fatal("revocation during storage read", status, string(raw), head)
			}
		})
	}
}
