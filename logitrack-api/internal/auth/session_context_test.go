package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
)

func TestOrdinaryMemberAdmissionReadsOneLiveRow(t *testing.T) {
	for _, hasTenant := range []bool{false, true} {
		svc, tx, p := supportReadFixture(t)
		p.Platform, p.AccessMode, p.TenantRole = nil, AccessModeMember, authz.Manager
		if !hasTenant {
			p.TenantID, p.TenantRole = nil, ""
		}
		tx.row = func(sql string, _ ...any) pgx.Row {
			if !strings.Contains(sql, "-- name: MemberSessionAdmission") {
				t.Fatalf("ordinary admission reloaded axes: %s", sql)
			}
			return supportTestRow{values: []any{AccessModeMember, p.ContextVersion, p.TenantID, (*time.Time)(nil), svc.clock().Add(time.Hour), p.AuthVersion, "active", string(p.TenantRole), "active", false}}
		}
		if err := svc.checkContext(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		reads := 0
		for _, sql := range tx.statements {
			if strings.Contains(sql, "-- name:") {
				reads++
			}
		}
		if reads != 1 {
			t.Fatalf("ordinary admission: %d queries, want one", reads)
		}
	}
}

func TestMemberContextChangeFallsBackToFullAdmission(t *testing.T) {
	svc, tx, p := supportReadFixture(t)
	p.Platform, p.AccessMode, p.TenantRole = nil, AccessModeMember, authz.Manager
	oldRow := tx.row
	full := false
	tx.row = func(sql string, args ...any) pgx.Row {
		if strings.Contains(sql, "-- name: MemberSessionAdmission") {
			return supportTestRow{values: []any{AccessModeMember, p.ContextVersion + 1, p.TenantID, (*time.Time)(nil), svc.clock().Add(time.Hour), p.AuthVersion, "active", string(p.TenantRole), "active", false}}
		}
		if sql == supportReadSessionContextSQL {
			full = true
			return supportTestRow{values: []any{AccessModeMember, p.ContextVersion + 1, p.TenantID, (*uuid.UUID)(nil), PlatformWeb, AMRPassword, svc.clock().Add(time.Hour), (*time.Time)(nil)}}
		}
		return oldRow(sql, args...)
	}
	if err := svc.checkContext(t.Context(), p); err == nil || !full {
		t.Fatalf("changed version bypassed full admission: %v", err)
	}
}

func TestSupportClaimsAndLegacyContext(t *testing.T) {
	uid, sid, tid := uuid.New(), uuid.New(), uuid.New()
	base := token.Claims{SessionID: sid.String(), Version: 1, AMR: AMRPassword, TenantID: tid.String(), Role: string(authz.Manager)}
	base.Subject, base.ID = uid.String(), "test"
	for _, v := range []string{"", "0", "9007199254740993"} {
		c := base
		c.ContextVersion = v
		p, ok := principalFromClaims(&c)
		if !ok || p.AccessMode != AccessModeMember {
			t.Fatalf("legacy/member version %q rejected", v)
		}
	}
	support := base
	support.AccessMode = AccessModeSupport
	support.Role = string(authz.SupportReadOnly)
	support.Platform = []string{"platform_admin"}
	support.ContextVersion = "1"
	support.SupportAccessVersion = "0"
	if p, ok := principalFromClaims(&support); !ok || p.ContextVersion != 1 || p.AccessMode != AccessModeSupport {
		t.Fatal("valid support claims rejected")
	}
	for _, mutate := range []func(*token.Claims){
		func(c *token.Claims) { c.Platform = []string{"support"} }, func(c *token.Claims) { c.AMR = AMRFirebase }, func(c *token.Claims) { c.ContextVersion = "" },
		func(c *token.Claims) { c.SupportAccessVersion = "-1" }, func(c *token.Claims) { c.Dispatcher = true }, func(c *token.Claims) { c.CustomerScopes = []string{uuid.NewString()} },
		func(c *token.Claims) { c.Role = string(authz.TenantAdmin) }, func(c *token.Claims) { c.TenantID = "" }, func(c *token.Claims) { c.ContextVersion = "01" },
	} {
		c := support
		mutate(&c)
		if _, ok := principalFromClaims(&c); ok {
			t.Fatalf("invalid support claims accepted: %+v", c)
		}
	}
	for _, v := range []string{"-1", "1.0", "+1", "01", "9223372036854775808"} {
		c := base
		c.ContextVersion = v
		if _, ok := principalFromClaims(&c); ok {
			t.Fatalf("bad version %q accepted", v)
		}
	}
}

func TestSessionlessFirebaseAndSupportBridgeRefusal(t *testing.T) {
	s := &Service{}
	p := &authz.Principal{UserID: uuid.New(), AMR: AMRFirebase}
	// A nil pool/store proves that no nonexistent session or Redis state is queried.
	if err := s.CheckSession(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	p.TenantRole = authz.SupportReadOnly
	if err := s.CheckSession(context.Background(), p); err == nil {
		t.Fatal("sessionless support accepted")
	}
	for _, mode := range []BridgeMode{BridgeOff, BridgeWeb, BridgeBoth} {
		s.fb.Mode = mode
		// Nil signer/accounts/pool would panic if refusal reached any bridge work.
		if _, err := s.FirebaseCustomToken(context.Background(), p); err == nil {
			t.Fatalf("support bridge accepted in %s", mode)
		}
	}
	g, err := legacyClaims(context.Background(), nil, authdb.GetBridgeUserRow{}, legacyContext{support: true, platformAdmin: true})
	if err != nil || g.mint || len(g.claims) > 0 {
		t.Fatalf("support mapped to legacy admin: %+v %v", g, err)
	}
}
