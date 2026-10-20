//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestContextBoundUploadsLocalAndS3(t *testing.T) {
	for _, backend := range []string{storage.BackendLocal, storage.BackendS3} {
		t.Run(backend, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			var svc *storage.Service
			if backend == storage.BackendLocal {
				svc = f.service(t, backend, f.local(t))
			} else {
				s3, cfg := s3Backend(t)
				svc = f.serviceOn(t, backend, cfg, s3)
			}
			p := &authz.Principal{UserID: f.staff, TenantID: &f.own, TenantRole: authz.Operator, SessionID: uuid.New(), AMR: authz.AMRPassword}
			var version int32
			if err := f.etl.QueryRow(ctx, `SELECT auth_version FROM users WHERE id=$1`, f.staff).Scan(&version); err != nil {
				t.Fatal(err)
			}
			p.AuthVersion = version
			if _, err := f.etl.Exec(ctx, `INSERT INTO sessions(id,user_id,platform,amr,active_tenant_id,absolute_expires_at)VALUES($1,$2,'web','pwd',$3,$4)`, p.SessionID, p.UserID, f.own, f.now.Add(24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := f.etl.Exec(ctx, `INSERT INTO refresh_tokens(session_id,family_id,token_hash,expires_at)VALUES($1,$2,$3,$4)`, p.SessionID, uuid.New(), bytes.Repeat([]byte{1}, 32), f.now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			caller := f.caller(f.staff, f.own, true)
			caller.Principal = p
			app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
			ingress.Mount(app, ingress.Internal, svc.Groups(storage.HTTPOptions{Auth: func(c fiber.Ctx) error {
				if c.Get("Authorization") != "Bearer web-session" {
					return httpx.ErrUnauthenticated()
				}
				authz.SetPrincipal(c, p)
				return c.Next()
			}, Caller: func(fiber.Ctx) (storage.Caller, bool) { return caller, true }}), nil)
			body := []byte("ORIGINAL_PENDING_BYTES")
			grant, err := svc.Presign(ctx, caller, storage.PresignInput{Purpose: "driver_profile", EntityID: uuid.NewString(), ContentType: "image/jpeg", SizeBytes: int64(len(body))}, storage.PutOptions{APIPath: true})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(grant.URL, "/v1/uploads/") || strings.ContainsAny(grant.URL, "?") {
				t.Fatal("bearer PUT emitted", grant.URL)
			}
			send := func(target, token string, raw []byte) int {
				req := httptest.NewRequest("PUT", target, bytes.NewReader(raw))
				req.Header.Set("Content-Type", "image/jpeg")
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				res, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = res.Body.Close() }()
				_, _ = io.Copy(io.Discard, res.Body)
				return res.StatusCode
			}
			if got := send(grant.URL, "", body); got != 401 {
				t.Fatal("anonymous grant", got)
			}
			if got := send(grant.URL, "web-session", body); got != 200 {
				t.Fatal("valid grant", got)
			}
			// The same pending key is protected from every legacy unsigned transport.
			if backend == storage.BackendLocal {
				l := svc.Local()
				put, err := l.PresignPut(ctx, storage.Object{Key: grant.Key}, "image/jpeg", int64(len(body)), time.Minute, storage.PutOptions{APIPath: true})
				if err != nil {
					t.Fatal(err)
				}
				if got := send(put.URL, "", body); got != 403 {
					t.Fatal("legacy signature bypass", got)
				}
			}
			// Transition invalidation is authoritative even if the incoming caller has old claims.
			if _, err := f.etl.Exec(ctx, `UPDATE sessions SET access_mode='support',context_version=context_version+1 WHERE id=$1`, p.SessionID); err != nil {
				t.Fatal(err)
			}
			if got := send(grant.URL, "web-session", bytes.Repeat([]byte("X"), len(body))); got != 403 {
				t.Fatal("old grant survived entry", got)
			}
			if _, err := f.etl.Exec(ctx, `UPDATE sessions SET access_mode='member',context_version=context_version+1 WHERE id=$1`, p.SessionID); err != nil {
				t.Fatal(err)
			}
			p.ContextVersion = 2
			if got := send(grant.URL, "web-session", body); got != 403 {
				t.Fatal("old grant revived after leave", got)
			}
			if _, err := svc.Presign(ctx, caller, storage.PresignInput{Key: grant.Key}, storage.PutOptions{APIPath: true}); err == nil {
				t.Fatal("old grant rebound to new context")
			}
			err = db.WithSystem(ctx, f.pool, &f.own, func(tx pgx.Tx) error {
				_, err := svc.Commit(ctx, tx, storage.CommitInput{Key: grant.Key, UserID: f.staff, TenantID: &f.own, OwnerID: uuid.New(), Purposes: []string{"driver_profile"}})
				return err
			})
			if err == nil {
				t.Fatal("stale pending bytes committed")
			}
			caller.Principal.AccessMode = "support"
			if _, err := svc.Presign(ctx, caller, storage.PresignInput{}, storage.PutOptions{}); err == nil {
				t.Fatal("direct support presign")
			}
		})
	}
}
