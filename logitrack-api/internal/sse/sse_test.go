package sse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestParseLastEventID(t *testing.T) {
	for in, want := range map[string]struct {
		after       int64
		replay, bad bool
	}{
		"":                       {0, false, false},
		" 42 ":                   {42, true, false},
		"0":                      {0, true, false},
		"-1":                     {0, false, true},
		"abc":                    {0, false, true},
		"1-0":                    {0, false, true},
		"9999999999999999999999": {0, false, true},
	} {
		after, replay, bad := parseLastEventID(in)
		if after != want.after || replay != want.replay || bad != want.bad {
			t.Errorf("%q: %d %v %v", in, after, replay, bad)
		}
	}
}

func TestSplitTopics(t *testing.T) {
	chat := "chat:" + uuid.NewString()
	got, err := splitTopics(" " + chat + ",," + chat + ",global ")
	if err != nil || len(got) != 2 || got[0] != chat || got[1] != "global" {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"tenant:x:payroll", "chat:not-a-uuid", "rt:global", "global:x"} {
		if _, err := splitTopics(bad); err == nil || httpx.AsError(err).Status != http.StatusBadRequest {
			t.Errorf("%q: %v", bad, err)
		}
	}
	var many []string
	for range MaxExplicitTopics + 1 {
		many = append(many, "chat:"+uuid.NewString())
	}
	if _, err := splitTopics(strings.Join(many, ",")); err == nil {
		t.Fatal("too many explicit topics accepted")
	}
}

func TestCheckQueryRefusesTicketsAndUnknownParameters(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error { return httpx.WriteError(c, httpx.AsError(err)) }})
	app.Get("/web", func(c fiber.Ctx) error {
		if err := checkQuery(c, "topics"); err != nil {
			return err
		}
		return c.SendStatus(http.StatusNoContent)
	})
	for path, want := range map[string]string{
		"/web":               "",
		"/web?topics=global": "",
		"/web?ticket=x":      "ticket_not_accepted",
		"/web?access_token=": "unknown",
	} {
		res, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		switch want {
		case "":
			if res.StatusCode != http.StatusNoContent {
				t.Errorf("%s: %d", path, res.StatusCode)
			}
		case "unknown":
			if res.StatusCode != http.StatusBadRequest || body.Error.Code != "bad_request" {
				t.Errorf("%s: %d %+v", path, res.StatusCode, body)
			}
		default:
			if res.StatusCode != http.StatusBadRequest || body.Error.Details["reason"] != want {
				t.Errorf("%s: %d %+v", path, res.StatusCode, body)
			}
		}
	}
}

func TestRevokesOnlyItsOwnSessionOrClaimsChanged(t *testing.T) {
	sid := uuid.New()
	st := &stream{sessionID: sid}
	for data, want := range map[string]bool{
		`{"userId":"u","sessionIds":["` + sid.String() + `"],"reason":"logout_all"}`: true,
		`{"sessionIds":["` + uuid.NewString() + `"],"reason":"logout"}`:              false,
		`{"sessionIds":[],"reason":"claims_changed"}`:                                true,
		`{"reason":"password_changed","sessionIds":["` + uuid.NewString() + `"]}`:    false,
		`not json`: true,
	} {
		if got := st.revokes(json.RawMessage(data)); got != want {
			t.Errorf("%s: %v", data, got)
		}
	}
}

func TestControlFrames(t *testing.T) {
	if got := string(reconnectFrame(ReasonTokenExpiring)); got != "event: reconnect\ndata: {\"reason\":\"token_expiring\"}\n\n" {
		t.Fatalf("%q", got)
	}
	if got := string(resyncFrame(0, "trimmed")); got != "id: 0\nevent: resync\ndata: {\"reason\":\"trimmed\"}\n\n" {
		t.Fatalf("%q", got)
	}
}
