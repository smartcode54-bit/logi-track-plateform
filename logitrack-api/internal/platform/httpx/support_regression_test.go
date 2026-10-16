package httpx_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

type coordinates struct {
	Lat      float64           `json:"lat"`
	Lng      *float64          `json:"lng"`
	Names    []string          `json:"names"`
	Labels   map[string]string `json:"labels"`
	Children []personal        `json:"children"`
}

var coordinatesSchema = httpx.NewSchema[coordinates]("test.coordinates", map[string]httpx.Field{
	"lat": {Kind: httpx.Address}, "lng": {Kind: httpx.Address}, "names": {Kind: httpx.Person},
	"labels": {Kind: httpx.Person, MapKeys: []string{"label"}}, "children": {Kind: httpx.Nested, Schema: personalSchema},
})

func TestCoordinateAndAbsentCollectionProjection(t *testing.T) {
	for _, on := range []bool{false, true} {
		for _, present := range []bool{false, true} {
			value := coordinates{Lat: 13.5}
			if present {
				lng := 100.5
				value.Lng = &lng
			}
			app := setup(audience(on), func(r fiber.Router) {
				ingress.Bind(r, "/coords", policy("GET", "/v1/coords", "test.coordinates"), func(c fiber.Ctx) error { return httpx.JSON(c, 200, value) })
			})
			status, body := request(t, app, "GET", "/v1/coords", "")
			var wire struct {
				Data map[string]any       `json:"data"`
				Meta httpx.ProjectionMeta `json:"meta"`
			}
			if status != 200 || json.Unmarshal([]byte(body), &wire) != nil {
				t.Fatal(status, body)
			}
			if on {
				if wire.Data["lat"] != 13.5 || (present && wire.Data["lng"] != 100.5) || len(wire.Meta.Redacted) != 0 {
					t.Fatal(body)
				}
			} else {
				if wire.Data["lat"] != nil || wire.Data["lng"] != nil {
					t.Fatal(body)
				}
				for _, path := range []string{"/data/lat", "/data/lng", "/data/names", "/data/labels"} {
					found := false
					for _, field := range wire.Meta.Redacted {
						if field.Path == path {
							found = true
						}
					}
					if !found {
						t.Fatalf("missing %s: %s", path, body)
					}
				}
			}
			for _, name := range []string{"names", "labels", "children"} {
				if wire.Data[name] != nil {
					t.Fatal(body)
				}
			}
			for _, field := range wire.Meta.Redacted {
				if field.Kind == httpx.Nested {
					t.Fatal(body)
				}
			}
		}
	}
}

func TestProjectedErrorFields(t *testing.T) {
	httpx.RegisterSafeErrorCode("reviewed_test_error")
	for _, on := range []bool{false, true} {
		for _, code := range []string{httpx.CodeInvalidArgument, "masked_query_not_allowed", "reviewed_test_error", "unreviewed_domain_error"} {
			app := setup(audience(on), func(r fiber.Router) {
				ingress.Bind(r, "/error", policy("GET", "/v1/error", "test.personal"), func(c fiber.Ctx) error {
					return httpx.NewError(422, code, "PERSON_SENTINEL").WithDetails(map[string]any{"fields": []httpx.FieldViolation{{Field: "lat", Reason: "required", Params: map[string]any{"value": "SECRET_SENTINEL"}}}, "value": "SECRET_SENTINEL"})
				})
			})
			status, body := request(t, app, "GET", "/v1/error", "")
			if strings.Contains(body, "SENTINEL") {
				t.Fatal(body)
			}
			var wire struct {
				Error struct {
					Code    string                     `json:"code"`
					Details map[string]json.RawMessage `json:"details"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(body), &wire) != nil {
				t.Fatal(body)
			}
			if code == "unreviewed_domain_error" {
				if status != 503 || wire.Error.Code != httpx.CodeSupportProjectionUnavailable {
					t.Fatal(body)
				}
				continue
			}
			if status != 422 || wire.Error.Code != code || string(wire.Error.Details["fields"]) != `[{"field":"lat","reason":"required"}]` {
				t.Fatal(body)
			}
			if len(wire.Error.Details) != 1 {
				t.Fatal(body)
			}
		}
	}
}
