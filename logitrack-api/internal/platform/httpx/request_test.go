package httpx_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestDecodeJSONAndInvalidArgument(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(httpx.RequestID())
	app.Post("/in", func(c fiber.Ctx) error {
		var in struct {
			Name string `json:"name"`
		}
		if err := httpx.DecodeJSON(c, &in); err != nil {
			return err
		}
		if in.Name == "" {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "name", Reason: "required", Params: map[string]any{"min": 1}})
		}
		return httpx.JSON(c, 200, in)
	})
	cases := []struct {
		body string
		code int
		want string
	}{
		{`{"name":"x","extra":1}`, 200, `"name":"x"`},
		{``, 422, `"fields":[{"field":"name","reason":"required","params":{"min":1}}]`},
		{`   `, 422, `"invalid_argument"`},
		{`[1]`, 400, `"bad_request"`},
		{`"text"`, 400, `"bad_request"`},
		{`{"name":`, 400, `"bad_request"`},
		{`{"name":"x"} {"name":"y"}`, 400, `"bad_request"`},
		{`{"name":5}`, 400, `"bad_request"`},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("POST", "/in", strings.NewReader(tc.body))
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != tc.code || !strings.Contains(string(b), tc.want) {
			t.Errorf("%q: %d %s", tc.body, resp.StatusCode, b)
		}
		if tc.code >= 400 {
			var env map[string]map[string]any
			if err := json.Unmarshal(b, &env); err != nil || len(env["error"]) != 4 {
				t.Errorf("%q: envelope %s", tc.body, b)
			}
		}
	}
}
