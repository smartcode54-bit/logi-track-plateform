package httpx

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
)

type fleetProvenanceFixture struct {
	ID           string  `json:"id"`
	Private      *string `json:"private"`
	Foreign      string  `json:"foreign"`
	Label        string  `json:"label"`
	Organization bool    `json:"-"`
}

var fleetProvenanceSchema = NewSchema[fleetProvenanceFixture]("test.fleet-provenance", map[string]Field{
	"id": {Kind: Safe}, "private": {Kind: Person, NullMasked: true}, "foreign": {Kind: Person, Owner: func(any) string { return "" }}, "label": {Kind: Person, PublicWhen: func(v any) bool { return v.(fleetProvenanceFixture).Organization }},
}, func(fleetProvenanceFixture) string { return "selected" })

func TestFleetPrivateNullAndJoinedOwnership(t *testing.T) {
	secret := "PRIVATE_SENTINEL"
	for _, on := range []bool{false, true} {
		for _, org := range []bool{false, true} {
			s := fleetProvenanceFixture{ID: "opaque", Private: &secret, Foreign: "FOREIGN_SENTINEL", Label: "ORG_OR_PERSON", Organization: org}
			if err := validateSchema(fleetProvenanceSchema.typ, fleetProvenanceSchema, map[reflect.Type]bool{}); err != nil {
				t.Fatal(err)
			}
			p := projection{audience: ProjectionAudience{Support: true, Tenant: "selected", Consents: map[string]Consent{"selected": {Enabled: on}}}, owners: map[string]bool{}}
			v, err := p.walk(reflect.ValueOf(s), fleetProvenanceSchema, "/data", "")
			if err != nil {
				t.Fatal(err)
			}
			out := v.(map[string]any)
			if out["foreign"] != "****" {
				t.Fatal("selected consent exposed global join", out)
			}
			if !on && out["private"] != nil || on && out["private"] != secret {
				t.Fatal(out)
			}
			if !on && !org && out["label"] != "****" || (on || org) && out["label"] != "ORG_OR_PERSON" {
				t.Fatal(out)
			}
			if s.Foreign != "FOREIGN_SENTINEL" || *s.Private != secret {
				t.Fatal("projection mutated source")
			}
		}
	}
}

func TestSupportPageCursorIsOpaqueAndBoundToEveryAudienceAxis(t *testing.T) {
	audience := ProjectionAudience{Support: true, Actor: "actor", Session: "session", Tenant: "selected", ContextVersion: "1", ConsentVersion: "7", Consents: map[string]Consent{"selected": {Version: "7"}}}
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(zerolog.Nop())})
	app.Use(AdmissionGuard(nil), func(c fiber.Ctx) error {
		a := audience
		a.Finalize = func(_ context.Context, _ []string, fn func(map[string]Consent) error) error {
			return fn(map[string]Consent{a.Tenant: {Version: a.ConsentVersion}})
		}
		SetProjectionAudience(c, a)
		return c.Next()
	})
	handler := func(c fiber.Ctx) error {
		p := TenantReadPolicy(EndpointPolicy{ID: c.Path(), Method: "GET", Route: c.Path(), Class: PolicyTenantRead, Phase: 1, Plane: "tenant", Target: "selected_tenant", PredicateID: "test", Owner: "test"}, "test.fleet-provenance", "", false, "limit", "cursor", "status")
		if err := EndpointGate(c, p); err != nil {
			return err
		}
		raw, err := SupportCursor(c)
		if err != nil {
			return err
		}
		if c.Query("cursor") != "" && raw != "RAW_POSITION_SENTINEL" {
			t.Fatal(raw)
		}
		return SupportPage(c, "test.fleet-provenance", []fleetProvenanceFixture{}, "RAW_POSITION_SENTINEL", nil)
	}
	app.Get("/records", handler)
	app.Get("/other", handler)
	request := func(path string) (int, string, string) {
		t.Helper()
		r, e := app.Test(httptest.NewRequest("GET", path, nil))
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = r.Body.Close() }()
		b, e := io.ReadAll(r.Body)
		if e != nil {
			t.Fatal(e)
		}
		var out struct {
			Next string `json:"nextCursor"`
		}
		_ = json.Unmarshal(b, &out)
		return r.StatusCode, out.Next, string(b)
	}
	code, next, body := request("/records?limit=1")
	if code != 200 || next == "" || strings.Contains(body, "RAW_POSITION_SENTINEL") {
		t.Fatal(code, body)
	}
	if code, _, body = request("/records?cursor=" + next + "&limit=1"); code != 200 {
		t.Fatal(code, body)
	}
	for _, path := range []string{"/records?limit=2&cursor=" + next, "/other?limit=1&cursor=" + next, "/records?limit=1&status=active&cursor=" + next, "/records?limit=1&cursor=" + next + "x", "/records?limit=1&cursor=RAW_POSITION_SENTINEL"} {
		if code, _, body = request(path); code != 403 {
			t.Fatal(path, code, body)
		}
	}
	baseline := audience
	for _, axis := range []string{"actor", "session", "tenant", "context", "consent"} {
		audience = baseline
		switch axis {
		case "actor":
			audience.Actor = "other"
		case "session":
			audience.Session = "other"
		case "tenant":
			audience.Tenant = "other"
		case "context":
			audience.ContextVersion = "2"
		case "consent":
			audience.ConsentVersion = "8"
		}
		if code, _, body = request("/records?limit=1&cursor=" + next); code != 403 {
			t.Fatal(axis, code, body)
		}
	}
}

type fleetOptionalPII struct {
	Name *string `json:"name"`
	Key  *string `json:"key"`
}
type fleetOptionalRow struct{ *fleetOptionalPII }

var fleetOptionalSchema = NewSchema[fleetOptionalRow]("test.fleet-optional", map[string]Field{"name": {Kind: Person}, "key": {Kind: File}})

func TestFleetNilEmbeddedSensitiveTextStillUsesMask(t *testing.T) {
	p := projection{audience: ProjectionAudience{Support: true, Tenant: "selected"}, owners: map[string]bool{}}
	result, err := p.walk(reflect.ValueOf(fleetOptionalRow{}), fleetOptionalSchema, "/data", "selected")
	if err != nil {
		t.Fatal(err)
	}
	out := result.(map[string]any)
	if out["name"] != "****" || out["key"] != nil {
		t.Fatal("missing PII lost classified wire shape", out)
	}
}
