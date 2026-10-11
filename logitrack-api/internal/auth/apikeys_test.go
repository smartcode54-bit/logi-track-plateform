package auth

import (
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// AllowAPIKeys refuses at registration what no route may admit: cf_shim keys (the shim targets, M2) and
// scopes outside the CHECK list.
func TestAllowAPIKeysPanics(t *testing.T) {
	for _, s := range []authz.APIKeyScope{authz.KeyCFShim, "admin", ""} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("AllowAPIKeys(%q) did not panic", s)
				}
			}()
			AllowAPIKeys(s)
		}()
	}
	var p authPolicy
	AllowAPIKeys(authz.KeyIntegration, authz.KeyScript, authz.KeyIntegration)(&p)
	AllowAPIKeys(authz.KeyReleasePublisher)(&p)
	if len(p.keys) != 3 {
		t.Fatalf("policy keys = %v", p.keys)
	}
}

// The scope routing of R82 (Appendix C §C.4.11): a key passes only on the internal listener, on a route that
// admits its scope; cf_shim never passes here.
func TestKeyScopeRefusal(t *testing.T) {
	var none, machine, publisher authPolicy
	AllowAPIKeys(authz.KeyIntegration, authz.KeyScript)(&machine)
	AllowAPIKeys(authz.KeyReleasePublisher)(&publisher)
	cases := []struct {
		scope    authz.APIKeyScope
		listener string
		pol      authPolicy
		pass     bool
	}{
		{authz.KeyIntegration, ingress.Internal, machine, true},
		{authz.KeyScript, ingress.Internal, machine, true},
		{authz.KeyIntegration, ingress.Public, machine, false},
		{authz.KeyIntegration, ingress.Internal, none, false},
		{authz.KeyIntegration, ingress.Internal, publisher, false},
		{authz.KeyReleasePublisher, ingress.Internal, publisher, true},
		{authz.KeyReleasePublisher, ingress.Internal, machine, false},
		{authz.KeyReleasePublisher, ingress.Public, publisher, false},
		{authz.KeyCFShim, ingress.Public, machine, false},
		{authz.KeyCFShim, ingress.Internal, publisher, false},
		{authz.KeyIntegration, "", machine, false}, // no listener recorded: refused
	}
	for _, tc := range cases {
		msg := keyScopeRefusal(tc.scope, tc.listener, tc.pol)
		if (msg == "") != tc.pass {
			t.Errorf("%s on %q with %v: refusal %q, want pass=%v", tc.scope, tc.listener, tc.pol.keys, msg, tc.pass)
		}
	}
}
