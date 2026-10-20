package httpx

import (
	"github.com/google/uuid"
	"reflect"
	"testing"
)

type brokerPathFixture struct {
	Path *string `json:"path"`
}

var brokerPathSchema = NewSchema[brokerPathFixture]("test.broker-path", map[string]Field{"path": {Kind: File, BrokerPath: true}})

func TestBrokerPathsRequireSelectedSupportConsentAndCanonicalPath(t *testing.T) {
	tenant := uuid.NewString()
	other := uuid.NewString()
	good := "/api/go/v1/support/files/" + uuid.NewString() + "/content"
	for _, support := range []bool{false, true} {
		for _, on := range []bool{false, true} {
			for _, owner := range []string{tenant, other, ""} {
				p := projection{audience: ProjectionAudience{Support: support, Platform: true, Tenant: tenant, Consents: map[string]Consent{tenant: {Enabled: on}, other: {Enabled: true}}}, owners: map[string]bool{}}
				v, err := p.walk(reflect.ValueOf(brokerPathFixture{Path: &good}), brokerPathSchema, "/data", owner)
				if err != nil {
					t.Fatal(err)
				}
				got := v.(map[string]any)["path"]
				want := support && on && owner == tenant
				if want && got != good || !want && got != nil {
					t.Fatal("cross-context broker path", support, on, owner, got)
				}
			}
		}
	}
	for _, bad := range []string{"https://private.invalid/signed?token=secret", "private/object-key", good + "?token=secret", good + "/../content", "/api/go/v1/support/files/" + uuid.Nil.String() + "/content", "/api/go/v1/support/files/not-an-id/content"} {
		p := projection{audience: ProjectionAudience{Support: true, Tenant: tenant, Consents: map[string]Consent{tenant: {Enabled: true}}}, owners: map[string]bool{}}
		if _, err := p.walk(reflect.ValueOf(brokerPathFixture{Path: &bad}), brokerPathSchema, "/data", tenant); err == nil {
			t.Fatal("unsafe path admitted", bad)
		}
	}
	if typ := schemaTypeScript(reflect.TypeFor[*string](), Field{Kind: File, BrokerPath: true}); typ != "string | null" {
		t.Fatal(typ)
	}
}
func TestStreamRanges(t *testing.T) {
	for _, tc := range []struct {
		raw           string
		start, length int64
		bad           bool
	}{{"", 0, 10, false}, {"bytes=0-0", 0, 1, false}, {"bytes=2-", 2, 8, false}, {"bytes=-3", 7, 3, false}, {"bytes=4-99", 4, 6, false}, {"bytes=-99", 0, 10, false}, {"bytes=10-", 0, 0, true}, {"bytes=2-1", 0, 0, true}, {"bytes=0-1,3-4", 0, 0, true}, {"bytes=-0", 0, 0, true}, {"items=0-1", 0, 0, true}, {"bytes=99999999999999999999999-", 0, 0, true}} {
		start, length, partial, err := streamRange(tc.raw, 10)
		if tc.bad {
			if err == nil {
				t.Fatal("invalid range admitted", tc.raw)
			}
			continue
		}
		if err != nil || start != tc.start || length != tc.length || partial != (tc.raw != "") {
			t.Fatal(tc, start, length, partial, err)
		}
	}
}
