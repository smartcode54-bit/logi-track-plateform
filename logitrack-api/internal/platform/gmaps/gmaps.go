// Package gmaps is the server-side Google Maps Platform client of the Go processes (main spec §16.1,
// GOOGLE_MAPS_SERVER_API_KEY): the Distance Matrix API of the distances.compute job (T21, Appendix B §B.5.3). It
// replaces distanceMatrixRequest of fn:distances.ts. Reverse geocoding (GET /v1/mobile/geo/reverse, P7a) joins it
// later.
//
// The key travels only in the request URL (the API has no header form). It is never logged and never part of an
// error: transport errors are reported without the URL, and the API's own error message is cleaned of it.
package gmaps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the Distance Matrix endpoint.
const DefaultBaseURL = "https://maps.googleapis.com/maps/api/distancematrix/json"

// Request limits of the Distance Matrix API (and of the legacy job, .vibe-rules.md Hub-SOC Distance section):
// at most 25 origins, 25 destinations and 100 elements (origins x destinations) per request.
const (
	MaxOrigins      = 25
	MaxDestinations = 25
	MaxElements     = 100
)

// maxBody bounds the response read (100 elements are a few kilobytes).
const maxBody = 4 << 20

// LatLng is one point.
type LatLng struct {
	Lat, Lng float64
}

// Element is one origin -> destination cell. OK is the element status OK with both values present (parseElement of
// fn:core/distances.ts); any other cell carries its Status and no values.
type Element struct {
	Status          string
	OK              bool
	DistanceMeters  int
	DurationSeconds int
}

// Matrix holds Rows[i][j] for origin i and destination j.
type Matrix struct {
	Rows [][]Element
}

// DistanceMatrix computes driving distances (the interface the job depends on; tests use a fake).
type DistanceMatrix interface {
	DistanceMatrix(ctx context.Context, origins, destinations []LatLng) (Matrix, error)
}

// Config configures Client.
type Config struct {
	// APIKey is GOOGLE_MAPS_SERVER_API_KEY.
	APIKey string
	// BaseURL overrides DefaultBaseURL (tests).
	BaseURL string
	// HTTP is the client to use (default: 30 s timeout).
	HTTP *http.Client
}

// Client calls the Distance Matrix API.
type Client struct {
	key  string
	base string
	http *http.Client
}

// New builds a client. It does no I/O.
func New(c Config) (*Client, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, errors.New("gmaps: GOOGLE_MAPS_SERVER_API_KEY is not set")
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("gmaps: the base URL must be an absolute http(s) URL")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{key: c.APIKey, base: base, http: hc}, nil
}

// apiResponse is the JSON of the Distance Matrix API.
type apiResponse struct {
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
	Rows         []struct {
		Elements []struct {
			Status   string `json:"status"`
			Distance *struct {
				Value *float64 `json:"value"`
			} `json:"distance"`
			Duration *struct {
				Value *float64 `json:"value"`
			} `json:"duration"`
		} `json:"elements"`
	} `json:"rows"`
}

// Error is a refusal of the API: an HTTP status other than 200 or a top-level status other than OK.
type Error struct {
	HTTPStatus int
	Status     string // the API status (REQUEST_DENIED, OVER_QUERY_LIMIT, ...), empty for an HTTP failure
	Message    string // error_message, without the key
}

func (e *Error) Error() string {
	if e.Status == "" {
		return fmt.Sprintf("Distance Matrix API HTTP %d", e.HTTPStatus)
	}
	if e.Message != "" {
		return "Distance Matrix API: " + e.Status + ": " + e.Message
	}
	return "Distance Matrix API: " + e.Status
}

// DistanceMatrix implements DistanceMatrix with mode=driving.
func (c *Client) DistanceMatrix(ctx context.Context, origins, destinations []LatLng) (Matrix, error) {
	if err := CheckLimits(len(origins), len(destinations)); err != nil {
		return Matrix{}, err
	}
	q := url.Values{}
	q.Set("origins", joinPoints(origins))
	q.Set("destinations", joinPoints(destinations))
	q.Set("mode", "driving")
	q.Set("key", c.key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"?"+q.Encode(), nil)
	if err != nil {
		return Matrix{}, errors.New("gmaps: cannot build the request")
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return Matrix{}, fmt.Errorf("gmaps: Distance Matrix request failed: %w", stripURL(err))
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return Matrix{}, fmt.Errorf("gmaps: read the Distance Matrix response: %w", stripURL(err))
	}
	if res.StatusCode != http.StatusOK {
		return Matrix{}, &Error{HTTPStatus: res.StatusCode}
	}
	var out apiResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return Matrix{}, errors.New("gmaps: the Distance Matrix response is not JSON")
	}
	if out.Status != "OK" {
		status := out.Status
		if status == "" {
			status = "UNKNOWN_ERROR"
		}
		return Matrix{}, &Error{HTTPStatus: res.StatusCode, Status: status, Message: c.clean(out.ErrorMessage)}
	}
	if len(out.Rows) != len(origins) {
		return Matrix{}, fmt.Errorf("gmaps: %d rows for %d origins", len(out.Rows), len(origins))
	}
	m := Matrix{Rows: make([][]Element, len(out.Rows))}
	for i, r := range out.Rows {
		if len(r.Elements) != len(destinations) {
			return Matrix{}, fmt.Errorf("gmaps: row %d has %d elements for %d destinations", i, len(r.Elements), len(destinations))
		}
		row := make([]Element, len(r.Elements))
		for j, el := range r.Elements {
			e := Element{Status: el.Status}
			if el.Status == "OK" && el.Distance != nil && el.Distance.Value != nil && el.Duration != nil && el.Duration.Value != nil &&
				*el.Distance.Value >= 0 && *el.Duration.Value >= 0 {
				e.OK, e.DistanceMeters, e.DurationSeconds = true, int(*el.Distance.Value), int(*el.Duration.Value)
			}
			row[j] = e
		}
		m.Rows[i] = row
	}
	return m, nil
}

// CheckLimits refuses a request the API would refuse (MaxOrigins, MaxDestinations, MaxElements) or an empty one.
func CheckLimits(origins, destinations int) error {
	switch {
	case origins < 1 || destinations < 1:
		return errors.New("gmaps: a request needs at least one origin and one destination")
	case origins > MaxOrigins || destinations > MaxDestinations || origins*destinations > MaxElements:
		return fmt.Errorf("gmaps: %d x %d is over the Distance Matrix limits (%d origins, %d destinations, %d elements)",
			origins, destinations, MaxOrigins, MaxDestinations, MaxElements)
	}
	return nil
}

func joinPoints(ps []LatLng) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = strconv.FormatFloat(p.Lat, 'f', -1, 64) + "," + strconv.FormatFloat(p.Lng, 'f', -1, 64)
	}
	return strings.Join(parts, "|")
}

// stripURL drops the request URL (which carries the key) from a transport error.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// clean removes the key from an API message, should the API ever echo it.
func (c *Client) clean(msg string) string {
	msg = strings.TrimSpace(msg)
	if c.key != "" {
		msg = strings.ReplaceAll(msg, c.key, "[redacted]")
	}
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300])
	}
	return msg
}
