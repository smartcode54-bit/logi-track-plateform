package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
)

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
