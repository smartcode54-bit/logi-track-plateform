// Package cartrack is the Cartrack Fleet API client of the Go worker (P6, R70; main spec §7.4, Appendix B §B.5.3): GET
// {CARTRACK_API_BASE_URL}/rest/vehicles/status with HTTP Basic auth (CARTRACK_API_USERNAME, CARTRACK_API_PASSWORD) and a
// 20 s timeout (the legacy fetch had none, fn:cartrack.ts:66-72), and the tolerant parser of the legacy sync
// (syncVehicleLocations, fn:cartrack.ts:84-188). The worker's cartrack.sync consumer (internal/fleet/locations) is its
// only caller: the credentials live in the worker alone (main spec §16.1), never in the api.
//
// Nothing here reaches Cartrack unless a deployment sets the base URL and both credentials: none has a default, and the
// tests use an httptest server or a stub of Client. Errors never carry the base URL, the credentials or the response
// body: only the HTTP status, so a job's error column and the probe's answer name nothing secret.
package cartrack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// StatusPath is the vehicle status endpoint below the base URL (legacy CARTRACK_API_URL, fn:cartrack.ts:30, whose
// origin is the production base URL https://fleetapi-th.cartrack.com).
const StatusPath = "/rest/vehicles/status"

// Timeout bounds one fetch (main spec §7.4: 20 s; the legacy fetch had no timeout).
const Timeout = 20 * time.Second

// maxBody bounds the response read: the status of every vehicle of the account, a few hundred bytes each.
const maxBody = 16 << 20

// Vehicle is one entry of the status response as the sync stores it (the CartrackVehicle fields of the legacy sync). A
// value the response does not carry, or carries with the wrong JSON type, is nil.
type Vehicle struct {
	// VehicleID is vehicle_id as text: the key trucks.gps_vehicle_id holds (GPSVehicleId).
	VehicleID           string
	Registration        string
	Lat, Lng            *float64
	SpeedKmh            *float64
	Heading             *float64 // bearing
	EngineOn            *bool    // ignition
	PositionDescription string
	DriverName          string     // driver.first_name + " " + driver.last_name, blanks dropped
	EventAt             *time.Time // event_ts ("2026-03-06 10:18:26+07")
	OdometerKm          *float64   // odometer (metres) / 1000
	FuelLevel           *float64
	FuelPercentage      *float64 // fuel.precentage_left (the API's spelling)
}

// Status is one answer of the status endpoint: the HTTP status and, for a 2xx answer, the vehicles.
type Status struct {
	HTTPStatus int
	Vehicles   []Vehicle
}

// Client fetches the status of every vehicle of the account (the interface the worker depends on; tests use a fake).
// A non-2xx answer returns its Status (HTTPStatus set, no vehicles) with an *HTTPError; a request that got no answer
// returns a zero Status and the transport error.
type Client interface {
	VehicleStatus(ctx context.Context) (Status, error)
}

// HTTPError is a non-2xx answer of the status endpoint.
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("cartrack: API HTTP %d", e.Status) }

// Config configures the HTTP client.
type Config struct {
	// BaseURL is CARTRACK_API_BASE_URL (an origin, optionally with a path prefix: the local stack's WireMock is
	// http://mocks:8080/cartrack). Required.
	BaseURL string
	// Username and Password are CARTRACK_API_USERNAME and CARTRACK_API_PASSWORD (Basic auth). Both required.
	Username, Password string
	// Timeout bounds one fetch (default Timeout).
	Timeout time.Duration
	// HTTP is the client to use (default: http.Client with Timeout).
	HTTP *http.Client
}

// HTTP calls the Cartrack status endpoint.
type HTTP struct {
	url      string
	user     string
	password string
	timeout  time.Duration
	http     *http.Client
}

// ValidateBaseURL checks CARTRACK_API_BASE_URL: an absolute http(s) URL without credentials, query or fragment.
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" ||
		u.User != nil {
		return errors.New("must be an absolute http(s) URL without credentials, query or fragment")
	}
	return nil
}

// ErrNoCredentials is New's error when the username or the password is blank.
var ErrNoCredentials = errors.New("cartrack: CARTRACK_API_USERNAME and CARTRACK_API_PASSWORD are required")

// New builds the client. It does no I/O.
func New(c Config) (*HTTP, error) {
	if err := ValidateBaseURL(c.BaseURL); err != nil {
		return nil, fmt.Errorf("cartrack: base URL %w", err)
	}
	if strings.TrimSpace(c.Username) == "" || c.Password == "" {
		return nil, ErrNoCredentials
	}
	if c.Timeout <= 0 {
		c.Timeout = Timeout
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: c.Timeout}
	}
	return &HTTP{url: strings.TrimRight(strings.TrimSpace(c.BaseURL), "/") + StatusPath, user: c.Username,
		password: c.Password, timeout: c.Timeout, http: c.HTTP}, nil
}

// VehicleStatus fetches and parses the status of every vehicle (fetchCartrackVehicles). A non-2xx answer is an
// *HTTPError naming the status only ("cartrack: API HTTP 401"); a 2xx body that is not JSON is an error too. A body
// without a recognisable list has no vehicles, which is not an error.
func (h *HTTP) VehicleStatus(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
	if err != nil {
		return Status{}, errors.New("cartrack: cannot build the request")
	}
	req.SetBasicAuth(h.user, h.password)
	req.Header.Set("Accept", "application/json")
	res, err := h.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL is configuration, not part of the message
		}
		return Status{}, fmt.Errorf("cartrack: request failed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	st := Status{HTTPStatus: res.StatusCode}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxBody))
		return st, &HTTPError{Status: res.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return st, fmt.Errorf("cartrack: read response: %w", err)
	}
	if len(body) > maxBody {
		return st, errors.New("cartrack: response too large")
	}
	if st.Vehicles, err = Parse(body); err != nil {
		return st, err
	}
	return st, nil
}

// Parse reads a status response the way the legacy sync did: a top-level array of vehicles, or an object whose data
// array holds them ("API may return array directly or { data: [...] }"); anything else has none. Entries that are not
// objects or have no vehicle_id are dropped. Only a body that is not JSON is an error.
func Parse(body []byte) ([]Vehicle, error) {
	var raw any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, errors.New("cartrack: response is not JSON")
	}
	var list []any
	switch v := raw.(type) {
	case []any:
		list = v
	case map[string]any:
		list, _ = v["data"].([]any)
	}
	out := make([]Vehicle, 0, len(list))
	for _, e := range list {
		obj, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := vehicle(obj); ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func vehicle(o map[string]any) (Vehicle, bool) {
	id := idText(o["vehicle_id"])
	if id == "" {
		return Vehicle{}, false
	}
	v := Vehicle{VehicleID: id, Registration: text(o["registration"]), SpeedKmh: number(o["speed"]),
		Heading: number(o["bearing"]), EngineOn: boolean(o["ignition"]), EventAt: eventTime(o["event_ts"])}
	if loc, ok := o["location"].(map[string]any); ok {
		v.Lat, v.Lng = number(loc["latitude"]), number(loc["longitude"])
		v.PositionDescription = text(loc["position_description"])
	}
	if d, ok := o["driver"].(map[string]any); ok {
		var parts []string
		for _, k := range []string{"first_name", "last_name"} {
			if s := text(d[k]); s != "" {
				parts = append(parts, s)
			}
		}
		v.DriverName = strings.Join(parts, " ")
	}
	if m := number(o["odometer"]); m != nil {
		km := *m / 1000
		v.OdometerKm = &km
	}
	if f, ok := o["fuel"].(map[string]any); ok {
		v.FuelLevel, v.FuelPercentage = number(f["level"]), number(f["precentage_left"])
	}
	return v, true
}

// idText is String(v.vehicle_id) for the ids the account returns: a JSON number (integral) or a non-blank string.
func idText(v any) string {
	switch x := v.(type) {
	case json.Number:
		return strings.TrimSpace(x.String())
	case string:
		return strings.TrimSpace(x)
	}
	return ""
}

// number is a finite JSON number (typeof === "number" in the legacy checks); anything else is nil.
func number(v any) *float64 {
	n, ok := v.(json.Number)
	if !ok {
		return nil
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}

func boolean(v any) *bool {
	b, ok := v.(bool)
	if !ok {
		return nil
	}
	return &b
}

func text(v any) string {
	s, _ := v.(string)
	return CleanText(s)
}

// CleanText is a Cartrack string as the sync stores it: NUL removed (PostgreSQL text holds none, SQLSTATE 22021, and
// one such value would fail the whole sync transaction for every truck), every other C0 control and DEL replaced by a
// space, then trimmed. JSON decoding already replaced invalid UTF-8 by U+FFFD, so NUL is the one byte PostgreSQL would
// refuse.
func CleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == 0:
			return -1
		case r < 0x20 || r == 0x7f:
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// eventLayouts are the event_ts forms the API sends ("2026-03-06 10:18:26+07") and RFC 3339.
var eventLayouts = []string{"2006-01-02 15:04:05-07", "2006-01-02 15:04:05-07:00", "2006-01-02 15:04:05Z07:00",
	time.RFC3339Nano, "2006-01-02T15:04:05-07"}

// eventTime parses event_ts; a value without a zone or in another form is nil (the sync then stamps its own time, as
// the legacy sync did for a missing value).
func eventTime(v any) *time.Time {
	s := text(v)
	if s == "" {
		return nil
	}
	for _, l := range eventLayouts {
		if t, err := time.Parse(l, s); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}
