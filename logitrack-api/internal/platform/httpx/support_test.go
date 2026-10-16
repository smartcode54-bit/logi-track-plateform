package httpx_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

type personal struct {
	Name   *string  `json:"name,omitempty"`
	Amount *float64 `json:"amount"`
	Status string   `json:"status"`
}

var personalSchema = httpx.NewSchema[personal]("test.personal", map[string]httpx.Field{"name": {Kind: httpx.Person}, "amount": {Kind: httpx.Money}, "status": {Kind: httpx.Safe}})

type addedDTO struct {
	Status string `json:"status"`
	Added  string `json:"added,omitempty"`
}

var incompleteSchema = httpx.NewSchema[addedDTO]("test.incomplete", map[string]httpx.Field{"status": {Kind: httpx.Safe}})

type nestedDTO struct {
	Child addedDTO `json:"child"`
}

var _ = httpx.NewSchema[nestedDTO]("test.nested", map[string]httpx.Field{"child": {Kind: httpx.Nested, Schema: incompleteSchema}})

type ownerDTO struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

var _ = httpx.NewSchema[ownerDTO]("test.owner", map[string]httpx.Field{"owner": {Kind: httpx.Safe}, "name": {Kind: httpx.Person}}, func(v ownerDTO) string { return v.Owner })

func audience(enabled bool) httpx.ProjectionAudience {
	consents := map[string]httpx.Consent{"A": {Enabled: enabled, Version: "7"}, "B": {Enabled: false, Version: "3"}}
	return httpx.ProjectionAudience{Support: true, Tenant: "A", Actor: "actor", Session: "session", ConsentVersion: "7", Consents: consents,
		Finalize: func(_ context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
			return fn(consents)
		}}
}
func policy(method, path, schema string) httpx.EndpointPolicy {
	return httpx.EndpointPolicy{ResolveTarget: func(c fiber.Ctx) (httpx.PolicyTarget, error) {
		p := httpx.EndpointPolicyFrom(c)
		a := httpx.ProjectionAudienceFrom(c)
		target := httpx.PolicyTarget{Plane: p.Plane}
		if p.Target == "selected_tenant" {
			target.TenantID = a.Tenant
		}
		if p.Target == "authenticated_caller" {
			target.ActorID = a.Actor
		}
		return target, nil
	}, ID: method + " " + path, Method: method, Route: path, Class: httpx.PolicyTenantRead, Phase: 1, Plane: "tenant", Target: "selected_tenant", PredicateID: "test-exact", SchemaID: schema, Owner: "test"}
}
func request(t *testing.T, app *fiber.App, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}
func setup(a httpx.ProjectionAudience, mount func(fiber.Router)) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(httpx.RequestID(), httpx.AdmissionGuard(nil))
	ingress.Mount(app, ingress.Internal, []ingress.Group{{Prefix: "/v1", Mount: func(r fiber.Router) {
		r = r.Group("", func(c fiber.Ctx) error { httpx.SetProjectionAudience(c, a); return c.Next() })
		mount(r)
	}}}, nil)
	return app
}
func TestProjectionCoverageAndFinalBytes(t *testing.T) {
	name := "PERSON_SENTINEL"
	amount := 123.45
	cached := personal{&name, &amount, "active"}
	for _, tc := range []struct {
		name, schema string
		handler      fiber.Handler
		want         int
	}{
		{"projected", "test.personal", func(c fiber.Ctx) error { return httpx.JSON(c, 200, cached) }, 200},
		{"direct JSON", "test.personal", func(c fiber.Ctx) error { return c.JSON(cached) }, 503},
		{"raw fasthttp", "test.personal", func(c fiber.Ctx) error { c.Response().SetBodyString(name); return nil }, 503},
		{"redirect", "test.personal", func(c fiber.Ctx) error { return c.Redirect().To("/" + name) }, 503},
		{"hijack", "test.personal", func(c fiber.Ctx) error {
			c.RequestCtx().Hijack(func(conn net.Conn) { _ = conn.Close(); t.Error("unregistered hijack reached the connection") })
			return nil
		}, 503},
		{"direct Send", "test.personal", func(c fiber.Ctx) error { return c.SendString(name) }, 503},
		{"direct stream", "test.personal", func(c fiber.Ctx) error { return c.SendStream(strings.NewReader(name)) }, 503},
		{"new DTO", "test.personal", func(c fiber.Ctx) error { return httpx.JSON(c, 200, addedDTO{}) }, 503},
		{"omitted unclassified field", "test.incomplete", func(c fiber.Ctx) error { return httpx.JSON(c, 200, addedDTO{}) }, 503},
		{"nested omitted unclassified field", "test.nested", func(c fiber.Ctx) error { return httpx.JSON(c, 200, nestedDTO{}) }, 503},
		{"body replaced", "test.personal", func(c fiber.Ctx) error {
			if err := httpx.JSON(c, 200, cached); err != nil {
				return err
			}
			return c.SendString(name)
		}, 503},
		{"header replaced", "test.personal", func(c fiber.Ctx) error {
			if err := httpx.JSON(c, 200, cached); err != nil {
				return err
			}
			c.Set("Location", name)
			return nil
		}, 503},
		{"error branch", "test.personal", func(c fiber.Ctx) error {
			c.Set("Location", name)
			return httpx.ErrBadRequest(name).WithDetails(map[string]any{"leak": name})
		}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := setup(audience(false), func(r fiber.Router) { ingress.Bind(r, "/data", policy("GET", "/v1/data", tc.schema), tc.handler) })
			status, body := request(t, app, "GET", "/v1/data", "")
			if status != tc.want || strings.Contains(body, name) {
				t.Fatalf("%d %s", status, body)
			}
			if tc.want == 200 && (!strings.Contains(body, `"amount":null`) || !strings.Contains(body, `/data/name`) || !strings.Contains(body, `"version":"7"`)) {
				t.Fatal(body)
			}
		})
	}
	if *cached.Name != name || *cached.Amount != amount {
		t.Fatal("projection mutated a member/cache object")
	}
}
func TestConsentOnStillClassifiesAndTargetsOwners(t *testing.T) {
	a := audience(true)
	a.Platform = true
	app := setup(a, func(r fiber.Router) {
		p := policy("GET", "/v1/data", "test.owner")
		p.Plane = "platform"
		p.Class = httpx.PolicyPlatform
		p.Target = "explicit_target_owners"
		ingress.Bind(r, "/data", p, func(c fiber.Ctx) error {
			return httpx.JSON(c, 200, []ownerDTO{{"A", "VISIBLE"}, {"B", "B_SENTINEL"}, {"", "GLOBAL_SENTINEL"}})
		})
	})
	status, body := request(t, app, "GET", "/v1/data", "")
	if status != 200 || !strings.Contains(body, "VISIBLE") || strings.Contains(body, "SENTINEL") {
		t.Fatalf("%d %s", status, body)
	}
	// Platform-only responses get the same target rule without selected-tenant metadata.
	a.Support = false
	a.Tenant = ""
	app = setup(a, func(r fiber.Router) {
		p := policy("GET", "/v1/data", "test.owner")
		p.Plane = "platform"
		p.Class = httpx.PolicyPlatform
		p.Target = "explicit_target_owners"
		ingress.Bind(r, "/data", p, func(c fiber.Ctx) error { return httpx.JSON(c, 200, []ownerDTO{{"B", "B_SENTINEL"}}) })
	})
	status, body = request(t, app, "GET", "/v1/data", "")
	if status != 200 || strings.Contains(body, "SENTINEL") || strings.Contains(body, "supportAccess") {
		t.Fatalf("%d %s", status, body)
	}
}
func TestDefaultDenyAndMutationPrecedence(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		calls := 0
		app := setup(audience(enabled), func(r fiber.Router) {
			for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
				r.Add([]string{method}, "/deferred", func(c fiber.Ctx) error { calls++; return c.SendString("SENTINEL") })
			}
		})
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			status, body := request(t, app, method, "/v1/deferred", "")
			want := "support_read_only"
			if method == "GET" {
				want = "support_view_unavailable"
			}
			if status != 403 || !strings.Contains(body, want) {
				t.Fatalf("%s %d %s", method, status, body)
			}
		}
		if calls != 0 {
			t.Fatal("deferred handler ran")
		}
	}
}
func TestMixedPredicates(t *testing.T) {
	key := httpx.APIKeyPredicate(func(k string) string {
		if k == "global:read" {
			return "global"
		}
		return "tenant"
	})
	for _, tc := range []struct {
		method, path, body string
		predicate          func(fiber.Ctx) (httpx.PolicyClass, error)
		want               int
	}{
		{"GET", "/v1/matrix?layer=tenant&tenantId=018f47a0-7b31-7c22-8b10-9733e2bba819", "", httpx.MatrixPredicate, 403},
		{"GET", "/v1/matrix?layer=platform&layer=platform", "", httpx.MatrixPredicate, 403},
		{"GET", "/v1/matrix?layer=platform&tenantId=", "", httpx.MatrixPredicate, 403},
		{"GET", "/v1/matrix?layer=unknown", "", httpx.MatrixPredicate, 403},
		{"GET", "/v1/matrix", "", httpx.MatrixPredicate, 403},
		{"PUT", "/v1/matrix?layer=tenant&tenantId=018f47a0-7b31-7c22-8b10-9733e2bba819", "{}", httpx.MatrixPredicate, 403},
		{"POST", "/v1/matrix", `{"scope":"script","name":"key","tenantId":"A","capabilities":["global:read"]}`, key, 403},
		{"POST", "/v1/matrix", `{"scope":"script","name":"key","tenantId":null,"tenantId":null,"capabilities":["global:read"]}`, key, 403},
		{"POST", "/v1/matrix", `{"scope":"script","name":"key","capabilities":["tenant:read"]}`, key, 403},
		{"POST", "/v1/matrix", `{"scope":"script","name":"key","capabilities":["global:read"],"extra":1}`, key, 403},
		{"PATCH", "/v1/matrix", `{"status":"active","reason":"x","name":"tenant profile"}`, httpx.LifecyclePredicate, 403},
		{"PATCH", "/v1/matrix", `{"status":"active","status":"suspended","reason":"x"}`, httpx.LifecyclePredicate, 403},
	} {
		app := setup(audience(true), func(r fiber.Router) {
			p := policy(tc.method, "/v1/matrix", "test.personal")
			p.Class = httpx.PolicyPlatform
			p.Plane = "platform"
			p.Predicate = tc.predicate
			ingress.Bind(r, "/matrix", p, func(c fiber.Ctx) error { t.Fatal("invalid predicate reached handler"); return nil })
		})
		status, body := request(t, app, tc.method, tc.path, tc.body)
		if status != tc.want {
			t.Fatalf("%s %s %d %s", tc.method, tc.path, status, body)
		}
		if tc.method != "GET" && !strings.Contains(body, "support_read_only") {
			t.Fatal(body)
		}
	}
}
func TestTypedInputDecodedOnce(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Post("/", func(c fiber.Ctx) error {
		_, err := httpx.StrictBody[input](c)
		if err != nil {
			return err
		}
		c.Request().SetBodyString(`{"name":"REPARSED"}`)
		var in input
		if err := httpx.DecodeJSON(c, &in); err != nil {
			return err
		}
		return c.JSON(in)
	})
	status, body := request(t, app, "POST", "/", `{"name":"VALIDATED"}`)
	if status != 200 || !strings.Contains(body, "VALIDATED") {
		t.Fatal(status, body)
	}
	for _, body := range []string{`{"name":"a","name":"a"}`, `{"name":"a","Name":"b"}`, `{"name":"a","unknown":1}`, `{"name":"a"} {}`, `[]`} {
		status, _ := request(t, app, "POST", "/", body)
		if status != 400 {
			t.Fatal(status, body)
		}
	}
}
func TestProjectionFreshnessFailure(t *testing.T) {
	a := audience(true)
	a.ConsentVersion = "6"
	app := setup(a, func(r fiber.Router) {
		ingress.Bind(r, "/data", policy("GET", "/v1/data", "test.personal"), func(c fiber.Ctx) error { return httpx.JSON(c, 200, personal{}) })
	})
	status, body := request(t, app, "GET", "/v1/data", "")
	if status != 401 || !strings.Contains(body, "claims_changed") {
		t.Fatalf("%d %s", status, body)
	}
	var value any
	if json.Unmarshal([]byte(body), &value) != nil {
		t.Fatal(body)
	}
}

func TestGroupAuthInlineAuthAndHEAD(t *testing.T) {
	for _, inline := range []bool{false, true} {
		for _, head := range []bool{false, true} {
			server := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
			server.Use(httpx.AdmissionGuard(nil))
			identity := fiber.Handler(func(c fiber.Ctx) error { httpx.SetProjectionAudience(c, audience(false)); return c.Next() })
			ingress.Mount(server, ingress.Internal, []ingress.Group{{Prefix: "/v1/me", Mount: func(r fiber.Router) {
				if !inline {
					r = r.Group("", identity)
				}
				p := policy("GET", "/v1/me/:id", "test.personal")
				p.Class = httpx.PolicyOwnAccount
				p.Plane = "self"
				p.Target = "authenticated_caller"
				p.AllowHEAD = head
				handlers := []any{func(c fiber.Ctx) error { return httpx.JSON(c, 200, personal{Status: c.Params("id")}) }}
				if inline {
					handlers = append([]any{identity}, handlers...)
				}
				ingress.Bind(r, "/:id", p, handlers[0], handlers[1:]...)
			}}}, nil)
			status, body := request(t, server, "GET", "/v1/me/caller", "")
			if status != 200 || !strings.Contains(body, "caller") {
				t.Fatal(status, body)
			}
			status, _ = request(t, server, "HEAD", "/v1/me/caller", "")
			want := 403
			if head {
				want = 200
			}
			if status != want {
				t.Fatal("HEAD", inline, head, status)
			}
		}
	}
}

func TestRegisteredStreamsAreSeparateFromRawWriters(t *testing.T) {
	checks := 0
	adapter := httpx.NewStreamAdapter("test.identifiers", "fleet", []string{"text/event-stream"}, true, func(c fiber.Ctx) (httpx.StreamSource, error) {
		return httpx.StreamSource{Tenant: "A", ContentType: "text/event-stream", Check: func(context.Context) error { checks++; return nil }, Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("event: identifier\n\n")), nil
		}}, nil
	})
	app := setup(audience(false), func(r fiber.Router) {
		p := policy("GET", "/v1/data", "stream:test.identifiers")
		ingress.Bind(r, "/data", p, func(c fiber.Ctx) error { return httpx.Stream(c, adapter) })
	})
	status, body := request(t, app, "GET", "/v1/data", "")
	if status != 200 || !strings.Contains(body, "event: identifier") || checks < 2 {
		t.Fatal(status, body, checks)
	}
}

type mapDTO struct {
	Labels  map[string]string `json:"labels"`
	Names   []string          `json:"names"`
	Amounts []*float64        `json:"amounts"`
}

var _ = httpx.NewSchema[mapDTO]("test.map", map[string]httpx.Field{
	"labels": {Kind: httpx.Person, MapKeys: []string{"first", "second"}}, "names": {Kind: httpx.Person}, "amounts": {Kind: httpx.Money},
})

type metadataDTO struct {
	Note  string `json:"note"`
	Count int    `json:"count"`
}

var _ = httpx.NewSchema[metadataDTO]("test.metadata", map[string]httpx.Field{"note": {Kind: httpx.FreeText}, "count": {Kind: httpx.Safe}})

func TestArraysMapKeysAndMetadata(t *testing.T) {
	data := mapDTO{Labels: map[string]string{"first": "PERSON_A", "second": "PERSON_B"}, Names: []string{"PERSON_A", "PERSON_B"}, Amounts: []*float64{nil, nil}}
	app := setup(audience(false), func(r fiber.Router) {
		p := policy("GET", "/v1/data", "test.map")
		p.MetaSchemaID = "test.metadata"
		ingress.Bind(r, "/data", p, func(c fiber.Ctx) error { return httpx.Page(c, data, "", metadataDTO{"FREE_TEXT_SENTINEL", 2}) })
	})
	status, body := request(t, app, "GET", "/v1/data", "")
	for _, expected := range []string{`"names":["****","****"]`, `"amounts":[null,null]`, `/data/amounts/0`, `/meta/note`, `"count":2`} {
		if status != 200 || !strings.Contains(body, expected) {
			t.Fatalf("%d missing %s: %s", status, expected, body)
		}
	}
	if strings.Contains(body, "PERSON_") || strings.Contains(body, "SENTINEL") {
		t.Fatal(body)
	}
	data.Labels["SENSITIVE_NAME_KEY"] = "PERSON_C"
	status, body = request(t, app, "GET", "/v1/data", "")
	if status != 503 || strings.Contains(body, "SENSITIVE_NAME_KEY") {
		t.Fatal(status, body)
	}
}
func TestBodylessPolicyReceipt(t *testing.T) {
	app := setup(audience(false), func(r fiber.Router) {
		p := policy("DELETE", "/v1/data", "empty")
		p.Class = httpx.PolicyOwnAccount
		p.Plane = "self"
		p.Target = "authenticated_caller"
		ingress.Bind(r, "/data", p, func(c fiber.Ctx) error { return httpx.Empty(c, 204) })
	})
	status, body := request(t, app, "DELETE", "/v1/data", "")
	if status != 204 || body != "" {
		t.Fatal(status, body)
	}
}

func TestValidMixedControlsKeepTheirExactClass(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		predicate          func(fiber.Ctx) (httpx.PolicyClass, error)
	}{
		{"GET", "/v1/data?layer=platform", "", httpx.MatrixPredicate},
		{"PUT", "/v1/data?layer=platform", `{"overrides":[]}`, httpx.MatrixPredicate},
		{"POST", "/v1/data", `{"name":"release key","scope":"script","capabilities":["global:read"]}`, httpx.APIKeyPredicate(func(string) string { return "global" })},
	} {
		called := false
		app := setup(audience(false), func(r fiber.Router) {
			p := policy(tc.method, "/v1/data", "test.personal")
			p.Class = httpx.PolicyPlatform
			p.Plane = "platform"
			p.Predicate = tc.predicate
			ingress.Bind(r, "/data", p, func(c fiber.Ctx) error { called = true; return httpx.JSON(c, 200, personal{Status: "control"}) })
		})
		status, body := request(t, app, tc.method, tc.path, tc.body)
		if status != 200 || !called || !strings.Contains(body, "control") {
			t.Fatalf("%s %d %s", tc.method, status, body)
		}
	}
}

func TestForeignOwnerConsentDependsOnTheEndpointPlane(t *testing.T) {
	for _, plane := range []string{"tenant", "platform"} {
		a := audience(true)
		a.Consents["B"] = httpx.Consent{Enabled: true, Version: "3"}
		app := setup(a, func(r fiber.Router) {
			p := policy("GET", "/v1/data", "test.owner")
			p.Plane = plane
			if plane == "platform" {
				p.Class = httpx.PolicyPlatform
				p.Target = "explicit_target_owners"
			}
			ingress.Bind(r, "/data", p, func(c fiber.Ctx) error { return httpx.JSON(c, 200, ownerDTO{"B", "B_PERSON_SENTINEL"}) })
		})
		status, body := request(t, app, "GET", "/v1/data", "")
		if status != 200 {
			t.Fatal(status, body)
		}
		if strings.Contains(body, "B_PERSON_SENTINEL") != (plane == "platform") {
			t.Fatal(plane, body)
		}
	}
}

func TestOrdinaryPagesAndTypedVariantsFailClosed(t *testing.T) {
	name := "PERSON_SENTINEL"
	for _, tc := range []struct {
		name     string
		handler  fiber.Handler
		status   int
		contains string
	}{
		{"raw ordinary response", func(c fiber.Ctx) error { return httpx.OrdinaryPage(c, []personal{{Name: &name}}, "", nil) }, 403, "support_view_unavailable"},
		{"classified variant", func(c fiber.Ctx) error {
			return httpx.PageAs(c, "test.personal", []personal{{Name: &name, Status: "active"}}, "", nil)
		}, 200, "****"},
		{"unknown variant", func(c fiber.Ctx) error {
			return httpx.PageAs(c, "unregistered.variant", []personal{{Name: &name}}, "", nil)
		}, 503, "support_projection_unavailable"},
		{"wrong concrete variant", func(c fiber.Ctx) error { return httpx.PageAs(c, "test.owner", []personal{{Name: &name}}, "", nil) }, 503, "support_projection_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := setup(audience(false), func(r fiber.Router) {
				ingress.Bind(r, "/variant", policy("GET", "/v1/variant", "test.personal"), tc.handler)
			})
			status, body := request(t, app, "GET", "/v1/variant", "")
			if status != tc.status || !strings.Contains(body, tc.contains) || strings.Contains(body, name) {
				t.Fatalf("%d %s", status, body)
			}
		})
	}
}
