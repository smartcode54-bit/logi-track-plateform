package gmaps

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeKey is a placeholder; no request leaves the test process.
const fakeKey = "test-key-not-a-secret"

func newClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := New(Config{APIKey: fakeKey, BaseURL: base, HTTP: &http.Client{Timeout: 5 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDistanceMatrixParsesTheGrid(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"OK","rows":[
			{"elements":[{"status":"OK","distance":{"value":12345},"duration":{"value":1800}},{"status":"ZERO_RESULTS"}]},
			{"elements":[{"status":"OK","distance":{"value":0},"duration":{"value":0}},{"status":"OK","distance":{"value":5}}]}]}`)
	}))
	defer srv.Close()
	m, err := newClient(t, srv.URL).DistanceMatrix(context.Background(),
		[]LatLng{{13.75, 100.5}, {13.8, 100.55}}, []LatLng{{13.7, 100.6}, {14, 101}})
	if err != nil {
		t.Fatal(err)
	}
	q := got.URL.Query()
	if q.Get("origins") != "13.75,100.5|13.8,100.55" || q.Get("destinations") != "13.7,100.6|14,101" ||
		q.Get("mode") != "driving" || q.Get("key") != fakeKey {
		t.Fatalf("query: %v", q)
	}
	want := [][]Element{
		{{Status: "OK", OK: true, DistanceMeters: 12345, DurationSeconds: 1800}, {Status: "ZERO_RESULTS"}},
		{{Status: "OK", OK: true}, {Status: "OK"}}, // no duration: not usable (parseElement)
	}
	for i := range want {
		for j := range want[i] {
			if m.Rows[i][j] != want[i][j] {
				t.Errorf("[%d][%d] = %+v, want %+v", i, j, m.Rows[i][j], want[i][j])
			}
		}
	}
}

func TestDistanceMatrixRefusalsNeverCarryTheKey(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"status": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"status":"REQUEST_DENIED","error_message":"The provided API key %s is invalid."}`, fakeKey)
		},
		"http": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"json": func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `<html>`) },
		"rows": func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `{"status":"OK","rows":[]}`) },
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			_, err := newClient(t, srv.URL).DistanceMatrix(context.Background(), []LatLng{{1, 2}}, []LatLng{{3, 4}})
			if err == nil {
				t.Fatal("no error")
			}
			if strings.Contains(err.Error(), fakeKey) {
				t.Fatalf("the error carries the key: %v", err)
			}
		})
	}
	// A transport error: net/http's *url.Error would print the URL, and with it the key.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close()
	_, err := newClient(t, base).DistanceMatrix(context.Background(), []LatLng{{1, 2}}, []LatLng{{3, 4}})
	if err == nil || strings.Contains(err.Error(), fakeKey) || strings.Contains(err.Error(), "key=") {
		t.Fatalf("transport error: %v", err)
	}
	var apiErr *Error
	srv2 := httptest.NewServer(cases["status"])
	defer srv2.Close()
	_, err = newClient(t, srv2.URL).DistanceMatrix(context.Background(), []LatLng{{1, 2}}, []LatLng{{3, 4}})
	if !errors.As(err, &apiErr) || apiErr.Status != "REQUEST_DENIED" || !strings.Contains(apiErr.Message, "[redacted]") {
		t.Fatalf("api error: %#v", err)
	}
}

func TestLimitsAndConfig(t *testing.T) {
	for _, c := range []struct {
		o, d int
		ok   bool
	}{{25, 4, true}, {4, 25, true}, {1, 1, true}, {25, 5, false}, {26, 1, false}, {1, 26, false}, {0, 3, false}, {11, 10, false}} {
		if err := CheckLimits(c.o, c.d); (err == nil) != c.ok {
			t.Errorf("%dx%d: %v", c.o, c.d, err)
		}
	}
	if _, err := New(Config{}); err == nil {
		t.Error("a client without a key")
	}
	if _, err := New(Config{APIKey: fakeKey, BaseURL: "ftp://x"}); err == nil {
		t.Error("a non-http base URL")
	}
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	if _, err := newClient(t, srv.URL).DistanceMatrix(context.Background(), make([]LatLng, 26), []LatLng{{}}); err == nil || called {
		t.Fatalf("over the limits: %v, called %v", err, called)
	}
}
