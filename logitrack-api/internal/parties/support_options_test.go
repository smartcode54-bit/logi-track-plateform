package parties

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties/partiesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestSupportCustomerPredicate(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		query string
		ok    bool
	}{
		{"ids=" + id, true}, {"codes=ONE,TWO", true}, {"", false}, {"ids=" + id + "&codes=ONE", false},
		{"ids=" + id + "&ids=" + id, false}, {"other=ONE", false}, {"codes=ONE,ONE", false},
		{"codes=ONE,%20TWO", false}, {"codes=", false}, {"ids=bad", false}, {"ids=" + uuid.Nil.String(), false},
		{"codes=" + strings.Repeat("X,", 100) + "Y", false}, {"ids=" + id + "," + id, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
			app.Get("/", func(c fiber.Ctx) error {
				class, err := supportCustomerPredicate(c)
				if (err == nil) != tc.ok {
					t.Fatalf("query %s class %s err %v", tc.query, class, err)
				}
				if tc.ok {
					in, ok := httpx.PolicyInput[SupportCustomerOptionsInput](c)
					if !ok || class != httpx.PolicyTenantRead || len(in.IDs)+len(in.Codes) == 0 {
						t.Fatal(in)
					}
				}
				return c.SendStatus(204)
			})
			res, err := app.Test(httptest.NewRequest("GET", "/?"+tc.query, nil))
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
		})
	}
	codes := make([]string, 100)
	for i := range codes {
		codes[i] = uuid.NewString()
	}
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := supportCustomerPredicate(c)
		if err != nil {
			t.Fatal(err)
		}
		return c.SendStatus(204)
	})
	res, err := app.Test(httptest.NewRequest("GET", "/?codes="+url.QueryEscape(strings.Join(codes, ",")), nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
}

func TestSupportCustomerHandlerScopeAndReviewedCodes(t *testing.T) {
	selected, other, reviewed, legacy := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, on := range []bool{false, true} {
		for _, mode := range []string{"ids", "codes"} {
			calls := 0
			h := handlers{&Service{reviewedCustomerCode: func(id uuid.UUID, code string) bool { return id == reviewed && code == "REVIEWED" }}}
			read := func(_ context.Context, p *authz.Principal, in partiesdb.ListSupportCustomerOptionsParams) ([]partiesdb.ListSupportCustomerOptionsRow, error) {
				calls++
				if in.TenantID != selected || p.TenantID == nil || *p.TenantID != other {
					t.Fatal("selected tenant scope lost", in, p)
				}
				return []partiesdb.ListSupportCustomerOptionsRow{{ID: reviewed, Code: "REVIEWED"}, {ID: legacy, Code: "PERSON_SENTINEL"}}, nil
			}
			app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
			app.Use(httpx.AdmissionGuard(nil))
			app.Get("/", func(c fiber.Ctx) error {
				p := &authz.Principal{TenantID: &other, TenantRole: authz.SupportReadOnly, Caps: authz.NewCapSet(authz.DriversView)}
				p.MarkResolved()
				authz.SetPrincipal(c, p)
				consents := map[string]httpx.Consent{selected.String(): {Enabled: on, Version: "1"}}
				httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Support: true, Tenant: selected.String(), ConsentVersion: "1", Consents: consents, Finalize: func(_ context.Context, _ []string, fn func(map[string]httpx.Consent) error) error {
					return fn(consents)
				}})
				policy := httpx.EndpointPolicy{ID: "GET /", Method: "GET", Route: "/", Class: httpx.PolicyTenantRead, Phase: 1, Plane: "tenant", Target: "selected_tenant", SchemaID: "support.customer_option", Owner: "P1.0", PredicateID: "test"}
				if err := httpx.EndpointGate(c, policy); err != nil {
					return err
				}
				in := SupportCustomerOptionsInput{IDs: []uuid.UUID{reviewed, legacy}}
				if mode == "codes" {
					in.IDs = nil
					in.Codes = []string{"REVIEWED", "PERSON_SENTINEL"}
				}
				httpx.SetPolicyInput(c, in)
				return h.serveSupportCustomerOptions(c, read)
			})
			res, err := app.Test(httptest.NewRequest("GET", "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(res.Body)
			res.Body.Close()
			var wire struct {
				Data []SupportCustomerOption `json:"data"`
			}
			if res.StatusCode != 200 || json.Unmarshal(data, &wire) != nil || calls != 1 {
				t.Fatal(res.StatusCode, string(data), calls)
			}
			expected := 2
			if mode == "codes" {
				expected = 1
			}
			if len(wire.Data) != expected || wire.Data[0].Code == nil || *wire.Data[0].Code != "REVIEWED" {
				t.Fatal(string(data))
			}
			if mode == "ids" && wire.Data[1].Code != nil {
				t.Fatal(string(data))
			}
		}
	}
}
