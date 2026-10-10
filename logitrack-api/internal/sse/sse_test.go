package sse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	rt "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
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
	// A bad value of the known parameter is 422 invalid_argument naming the field (Appendix B §B.1.5),
	// like the jobs API; 400 is for an unknown parameter (checkQuery).
	violation := func(err error) (int, string, httpx.FieldViolation) {
		t.Helper()
		e := httpx.AsError(err)
		fields, _ := e.Details["fields"].([]httpx.FieldViolation)
		if len(fields) != 1 {
			t.Fatalf("details %+v", e.Details)
		}
		return e.Status, e.Code, fields[0]
	}
	for _, bad := range []string{"tenant:x:payroll", "chat:not-a-uuid", "rt:global", "global:x"} {
		_, err := splitTopics(bad)
		if err == nil {
			t.Fatalf("%q accepted", bad)
		}
		status, code, f := violation(err)
		if status != http.StatusUnprocessableEntity || code != httpx.CodeInvalidArgument || f.Field != "topics" ||
			f.Reason != "unknown_topic" || f.Params["topic"] != bad {
			t.Errorf("%q: %d %s %+v", bad, status, code, f)
		}
	}
	var many []string
	for range MaxExplicitTopics + 1 {
		many = append(many, "chat:"+uuid.NewString())
	}
	_, err = splitTopics(strings.Join(many, ","))
	if err == nil {
		t.Fatal("too many explicit topics accepted")
	}
	if status, code, f := violation(err); status != http.StatusUnprocessableEntity || code != httpx.CodeInvalidArgument ||
		f.Field != "topics" || f.Reason != "too_many" || f.Params["max"] != MaxExplicitTopics {
		t.Fatalf("too many: %d %s %+v", status, code, f)
	}
}

func TestCheckQueryRefusesTicketsAndUnknownParameters(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error { return httpx.WriteError(c, httpx.AsError(err)) }})
	// The parameter sets of handleWeb and handleMobile.
	app.Get("/web", func(c fiber.Ctx) error {
		if err := checkQuery(c, "topics", QueryLastEventID); err != nil {
			return err
		}
		return c.SendStatus(http.StatusNoContent)
	})
	app.Get("/mobile", func(c fiber.Ctx) error {
		if err := checkQuery(c, "ticket", "topics"); err != nil {
			return err
		}
		return c.SendStatus(http.StatusNoContent)
	})
	for path, want := range map[string]string{
		"/web":                                   "",
		"/web?topics=global":                     "",
		"/web?topics=global&lastEventId=812":     "",
		"/web?ticket=x":                          "ticket_not_accepted",
		"/web?access_token=":                     "unknown",
		"/mobile?ticket=x&topics=global":         "",
		"/mobile?ticket=x&lastEventId=812":       "unknown",
		"/mobile?ticket=x&topics=g&access_token": "unknown",
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

// The Last-Event-ID header wins over ?lastEventId=: the browser's own reconnect of a URL that carries
// lastEventId sends a newer header (main spec §10.3).
func TestLastEventIDPrefersTheHeader(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error { return c.SendString(lastEventID(c)) })
	for _, tc := range []struct{ query, header, want string }{
		{"", "", ""},
		{"?lastEventId=812", "", "812"},
		{"?lastEventId=812", "900", "900"},
		{"", "900", "900"},
		{"?lastEventId=812", "  ", "812"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/"+tc.query, nil)
		if tc.header != "" {
			req.Header.Set(HeaderLastEventID, tc.header)
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(res.Body)
		if b.String() != tc.want {
			t.Errorf("query %q header %q: %q, want %q", tc.query, tc.header, b.String(), tc.want)
		}
	}
}

// After a resync the stream records the position the resync frame gave the client, as after a replay:
// a late ephemeral event at or below it (relayed live even below the snapshot) goes out without an id
// line, so the client's Last-Event-ID never moves back.
func TestOpeningFramesRecordThePosition(t *testing.T) {
	vehicles := rt.TenantTopic(uuid.NewString(), rt.FamilyVehicleLocations)
	late := rt.Message{ID: 7, Topic: vehicles, Type: "vehicle_locations.updated", EventID: uuid.NewString(), Data: json.RawMessage(`[]`)}
	for name, snap := range map[string]rt.Snapshot{
		"resync":  {Seq: 10, Resync: rt.ResyncTrimmed},
		"connect": {Seq: 10},
		"replay":  {Seq: 10, Events: []rt.Message{{ID: 9, Topic: "global", Type: "hubs.changed", EventID: "e", Data: json.RawMessage(`{}`)}}},
	} {
		st := &stream{s: &Service{resyncs: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "x"}, []string{"reason"})}, snap: snap}
		var out bytes.Buffer
		w := bufio.NewWriter(&out)
		if err := st.open(w); err != nil {
			t.Fatal(err)
		}
		if st.lastID != 10 {
			t.Errorf("%s: lastID %d, want the snapshot's 10", name, st.lastID)
		}
		out.Reset()
		if _, err := st.deliver(w, late); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); strings.Contains(got, "id:") || !strings.HasPrefix(got, "event: vehicle_locations.updated\n") {
			t.Errorf("%s: late ephemeral frame %q moves the position back", name, got)
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
