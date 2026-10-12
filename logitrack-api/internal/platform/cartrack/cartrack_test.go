package cartrack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Placeholder credentials built at run time: never a real Cartrack account.
var (
	testUser = strings.Repeat("u", 8)
	testPass = strings.Repeat("p", 12)
)

const statusBody = `{"data":[
 {"vehicle_id":123456,"registration":"70-1234","speed":61.5,"bearing":182,"ignition":true,"event_ts":"2026-03-06 10:18:26+07",
  "location":{"latitude":13.75,"longitude":100.5,"position_description":" Rama IV "},
  "driver":{"first_name":"Somchai","last_name":""},"odometer":250000500,"fuel":{"level":60.5,"precentage_left":45.25}},
 {"vehicle_id":"654321","location":{"latitude":"13.7","longitude":100.5},"event_ts":"not a time","speed":"12"},
 {"registration":"no id"},
 7,
 {"vehicle_id":"  ","location":{"latitude":1,"longitude":2}}
]}`

func TestParse(t *testing.T) {
	got, err := Parse([]byte(statusBody))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d vehicles, want the two with a vehicle_id: %+v", len(got), got)
	}
	a := got[0]
	if a.VehicleID != "123456" || a.Registration != "70-1234" || *a.Lat != 13.75 || *a.Lng != 100.5 || *a.SpeedKmh != 61.5 ||
		*a.Heading != 182 || !*a.EngineOn || a.PositionDescription != "Rama IV" || a.DriverName != "Somchai" ||
		*a.OdometerKm != 250000.5 || *a.FuelLevel != 60.5 || *a.FuelPercentage != 45.25 {
		t.Fatalf("first vehicle %+v", a)
	}
	if want := time.Date(2026, 3, 6, 3, 18, 26, 0, time.UTC); a.EventAt == nil || !a.EventAt.Equal(want) {
		t.Fatalf("event_ts %v, want %v", a.EventAt, want)
	}
	// A string latitude is not a number (typeof check of the legacy sync), nor a string speed; a bad event_ts is nil.
	b := got[1]
	if b.VehicleID != "654321" || b.Lat != nil || b.Lng == nil || b.SpeedKmh != nil || b.EventAt != nil || b.EngineOn != nil {
		t.Fatalf("second vehicle %+v", b)
	}
}

// A NUL or another control character in a Cartrack string never reaches PostgreSQL (text refuses NUL, SQLSTATE 22021).
func TestParseCleansControlCharacters(t *testing.T) {
	got, err := Parse([]byte(`[{"vehicle_id":1,"location":{"latitude":13.7,"longitude":100.5,"position_description":"Rama IV\u0000"},
		"driver":{"first_name":"Som\u0000chai","last_name":"J\tK\u007f"},"registration":"\u0000"}]`))
	if err != nil || len(got) != 1 {
		t.Fatalf("parse %v %+v", err, got)
	}
	if v := got[0]; v.PositionDescription != "Rama IV" || v.DriverName != "Somchai J K" || v.Registration != "" {
		t.Fatalf("cleaned %+v", v)
	}
	for in, want := range map[string]string{"": "", " a\x00b ": "ab", "a\nb": "a b", "\x00\x00": "", "ถนน\x01พระราม": "ถนน พระราม"} {
		if got := CleanText(in); got != want {
			t.Fatalf("CleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseShapes(t *testing.T) {
	for body, n := range map[string]int{
		`[{"vehicle_id":1},{"vehicle_id":2}]`: 2, // the bare array
		`{"data":[{"vehicle_id":1}]}`:         1,
		`{"vehicles":[{"vehicle_id":1}]}`:     0, // no data array: none
		`{"data":null}`:                       0,
		`42`:                                  0,
		`[]`:                                  0,
	} {
		got, err := Parse([]byte(body))
		if err != nil || len(got) != n {
			t.Fatalf("%s: %d vehicles (%v), want %d", body, len(got), err, n)
		}
	}
	if _, err := Parse([]byte("<html>")); err == nil || strings.Contains(err.Error(), "html") {
		t.Fatalf("non-JSON body: %v", err)
	}
}

func TestEventTimeForms(t *testing.T) {
	want := time.Date(2026, 3, 6, 3, 18, 26, 0, time.UTC)
	for _, s := range []string{"2026-03-06 10:18:26+07", "2026-03-06 10:18:26+07:00", "2026-03-06T10:18:26+07:00", "2026-03-06T03:18:26Z"} {
		if got := eventTime(s); got == nil || !got.Equal(want) {
			t.Fatalf("%s -> %v", s, got)
		}
	}
	for _, s := range []any{"2026-03-06 10:18:26", "", 12, nil} {
		if got := eventTime(s); got != nil {
			t.Fatalf("%v -> %v, want nil", s, got)
		}
	}
}

func TestVehicleStatusOverHTTP(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(statusBody))
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL + "/cartrack/", Username: testUser, Password: testPass})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.VehicleStatus(context.Background())
	if err != nil || st.HTTPStatus != http.StatusOK || len(st.Vehicles) != 2 {
		t.Fatalf("status %+v, %v", st, err)
	}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth(testUser, testPass)
	if gotPath != "/cartrack/rest/vehicles/status" || gotAuth != req.Header.Get("Authorization") {
		t.Fatalf("path %q, auth header sent as Basic: %v", gotPath, gotAuth == req.Header.Get("Authorization"))
	}
}

func TestVehicleStatusErrorsNameNothingSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow"+StatusPath {
			time.Sleep(200 * time.Millisecond)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad credentials for ` + testUser + `"}`))
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, Username: testUser, Password: testPass})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.VehicleStatus(context.Background())
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusUnauthorized || st.HTTPStatus != http.StatusUnauthorized || len(st.Vehicles) != 0 {
		t.Fatalf("401: %+v %v", st, err)
	}
	if err.Error() != "cartrack: API HTTP 401" {
		t.Fatalf("message %q", err)
	}
	// A timeout names neither the URL nor the credentials.
	slow, err := New(Config{BaseURL: srv.URL + "/slow", Username: testUser, Password: testPass, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = slow.VehicleStatus(context.Background())
	if err == nil || strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), testUser) || strings.Contains(err.Error(), testPass) {
		t.Fatalf("timeout error %v", err)
	}
}

func TestNewRefusesBadConfig(t *testing.T) {
	for _, c := range []Config{
		{BaseURL: "", Username: testUser, Password: testPass},
		{BaseURL: "ftp://x", Username: testUser, Password: testPass},
		{BaseURL: "https://user:pw@example.test", Username: testUser, Password: testPass},
		{BaseURL: "https://example.test?x=1", Username: testUser, Password: testPass},
		{BaseURL: "https://example.test", Username: " ", Password: testPass},
		{BaseURL: "https://example.test", Username: testUser, Password: ""},
	} {
		if _, err := New(c); err == nil {
			t.Fatalf("accepted %q", c.BaseURL)
		} else if strings.Contains(err.Error(), testPass) {
			t.Fatalf("error names the password: %v", err)
		}
	}
}

// The local stack's WireMock answer (deploy/mocks/mappings/cartrack.json) parses: two vehicles with a position, one
// without (bad coordinates, skipped by the sync), and the 401 mapping for a request without credentials.
func TestWireMockMapping(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/mocks/mappings/cartrack.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Mappings []struct {
			Request struct {
				URLPath string `json:"urlPath"`
			} `json:"request"`
			Response struct {
				Status   int             `json:"status"`
				JSONBody json.RawMessage `json:"jsonBody"`
			} `json:"response"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || len(file.Mappings) != 2 {
		t.Fatalf("mapping file: %v", err)
	}
	ok := file.Mappings[0]
	if ok.Request.URLPath != "/cartrack"+StatusPath || ok.Response.Status != http.StatusOK {
		t.Fatalf("first mapping %+v", ok.Request)
	}
	vs, err := Parse(ok.Response.JSONBody)
	if err != nil || len(vs) != 3 || vs[0].Lat == nil || vs[1].Lat == nil || vs[2].Lat != nil {
		t.Fatalf("mock vehicles %+v (%v)", vs, err)
	}
	if file.Mappings[1].Response.Status != http.StatusUnauthorized {
		t.Fatal("second mapping is the 401 without credentials")
	}
}
