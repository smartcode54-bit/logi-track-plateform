package operations

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

func firstViolation(t *testing.T, err error) (code, field, reason string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) {
		t.Fatalf("%v is not an API error", err)
	}
	if f, ok := he.Details["fields"].([]httpx.FieldViolation); ok && len(f) > 0 {
		return he.Code, f[0].Field, f[0].Reason
	}
	return he.Code, "", ""
}

// The body of the T28 slice of PATCH /v1/tasks/{id}: tenantId required, driverId a uuid or null, every other field
// refused rather than dropped.
func TestParseReassign(t *testing.T) {
	to, drv := uuid.New(), uuid.New()
	in, err := ParseReassign([]byte(`{"tenantId":"` + to.String() + `"}`))
	if err != nil || in.TenantID != to || in.DriverSet || in.DriverID != nil {
		t.Fatalf("tenant only: %+v %v", in, err)
	}
	in, err = ParseReassign([]byte(` {"tenantId":"` + to.String() + `","driverId":"` + drv.String() + `"} `))
	if err != nil || !in.DriverSet || in.DriverID == nil || *in.DriverID != drv {
		t.Fatalf("with a driver: %+v %v", in, err)
	}
	in, err = ParseReassign([]byte(`{"tenantId":"` + to.String() + `","driverId":null}`))
	if err != nil || !in.DriverSet || in.DriverID != nil {
		t.Fatalf("unassign: %+v %v", in, err)
	}
	for body, want := range map[string]string{
		``:                  "tenantId required",
		`{}`:                "tenantId required",
		`{"tenantId":null}`: "tenantId required",
		`{"tenantId":"x"}`:  "tenantId invalid",
		`{"tenantId":7}`:    "tenantId invalid",
		`{"tenantId":"` + uuid.Nil.String() + `"}`:                   "tenantId invalid",
		`{"tenantId":"` + to.String() + `","driverId":"x"}`:          "driverId invalid",
		`{"tenantId":"` + to.String() + `","date":"2026-10-11"}`:     "date not_supported",
		`{"tenantId":"` + to.String() + `","helperDriverId":null}`:   "helperDriverId not_supported",
		`{"jobCategory":"PRIMARY","tenantId":"` + to.String() + `"}`: "jobCategory not_supported",
	} {
		_, err := ParseReassign([]byte(body))
		code, field, reason := firstViolation(t, err)
		if code != httpx.CodeInvalidArgument || field+" "+reason != want {
			t.Errorf("%s: %s %s %s, want %s", body, code, field, reason, want)
		}
	}
	for _, body := range []string{`[]`, `"x"`, `{"tenantId":`, `{} {}`} {
		_, err := ParseReassign([]byte(body))
		var he *httpx.Error
		if !errors.As(err, &he) || he.Code != httpx.CodeBadRequest {
			t.Errorf("%s: %v, want 400 bad_request", body, err)
		}
	}
}

type fakeMover struct {
	calls int
	got   Reassign
}

func (f *fakeMover) ReassignTask(_ context.Context, _ *authz.Principal, _ string, id uuid.UUID, in Reassign) (Reassigned, error) {
	f.calls++
	f.got = in
	return Reassigned{ID: id, TenantID: in.TenantID, TenantSource: string(tenancy.SourceForm), Moved: true}, nil
}

// Only a dispatcher holding operations:manage_tasks or a platform admin holding platform:cross_tenant_write reaches the
// reassignment; the Reassigner learns which authority the request used.
func TestReassignAuthorization(t *testing.T) {
	tid := uuid.New()
	manage := authz.NewCapSet(authz.OperationsManageTasks)
	for name, c := range map[string]struct {
		p        *authz.Principal
		allowed  bool
		platform bool
	}{
		"dispatcher":                      {&authz.Principal{TenantID: &tid, TenantRole: authz.OperationStaff, Dispatcher: true, Caps: manage}, true, false},
		"dispatcher without manage_tasks": {&authz.Principal{TenantID: &tid, TenantRole: authz.Operator, Dispatcher: true}, false, false},
		"tenant admin, no grant":          {&authz.Principal{TenantID: &tid, TenantRole: authz.TenantAdmin, Steward: true, Caps: manage}, false, false},
		"platform admin":                  {&authz.Principal{Platform: []authz.PlatformRole{authz.PlatformAdmin}, Caps: authz.NewCapSet(authz.PlatformCrossTenantWrite)}, false, false},
		"platform admin without write":    {&authz.Principal{Platform: []authz.PlatformRole{authz.PlatformAdmin}, Caps: authz.NewCapSet(authz.PlatformCrossTenantRead)}, false, false},
		"support with write":              {&authz.Principal{Platform: []authz.PlatformRole{authz.Support}, Caps: authz.NewCapSet(authz.PlatformCrossTenantWrite)}, false, false},
		"an API key":                      {&authz.Principal{AMR: authz.AMRAPIKey, TenantID: &tid, Dispatcher: true, Caps: manage}, false, false},
	} {
		m := &fakeMover{}
		s := &Service{mover: m}
		_, err := s.ReassignTask(context.Background(), c.p, "rid", uuid.New(), Reassign{TenantID: uuid.New()})
		if c.allowed != (err == nil) || (m.calls == 1) != c.allowed {
			t.Errorf("%s: %v (calls %d)", name, err, m.calls)
			continue
		}
		if !c.allowed {
			var he *httpx.Error
			if !errors.As(err, &he) || he.Code != authz.CodePermissionDenied || he.Details["reason"] != ReasonDispatcherOrPlatform {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if m.got.Platform != c.platform {
			t.Errorf("%s: Platform = %v", name, m.got.Platform)
		}
	}
	if _, err := (&Service{}).ReassignTask(context.Background(), &authz.Principal{}, "", uuid.New(), Reassign{}); err == nil {
		t.Fatal("a service without a Reassigner answered")
	}
}

// Every table Stamp serves has a chain in tenancy.Resolve, and the Go vocabulary of tenant_source is the resolver's.
func TestStampTables(t *testing.T) {
	for table, collection := range stampCollections {
		if !tenancy.IsCollection(collection) {
			t.Errorf("%s -> %s has no chain", table, collection)
		}
	}
	if len(TenantSources) != len(tenancy.Sources) {
		t.Fatalf("TenantSources %v, tenancy.Sources %v", TenantSources, tenancy.Sources)
	}
	for i, s := range tenancy.Sources {
		if TenantSources[i] != string(s) {
			t.Fatalf("TenantSources %v, tenancy.Sources %v", TenantSources, tenancy.Sources)
		}
	}
	if _, err := Stamp(context.Background(), nil, &authz.Principal{}, "vehicle_expenses", Links{}); err == nil {
		t.Fatal("Stamp served a table without an operations chain")
	}
}
