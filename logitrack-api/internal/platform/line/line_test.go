package line_test

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

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/line"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/line/linetest"
)

const body = `{"to":"Cgroup","messages":[{"type":"text","text":"hi"}]}`

// A push is accepted once per retry key: the retry of a push whose answer was lost is LINE's 409 with the accepted
// request id (Replayed), never a second card.
func TestPushRetryKey(t *testing.T) {
	s := linetest.New(t)
	c := s.Client()
	ctx := context.Background()
	r, err := c.Push(ctx, "key-1", []byte(body))
	if err != nil || r.Replayed || r.RequestID == "" {
		t.Fatalf("first push: %+v %v", r, err)
	}
	again, err := c.Push(ctx, "key-1", []byte(body))
	if err != nil || !again.Replayed || again.RequestID != r.RequestID {
		t.Fatalf("retry: %+v %v, want replayed %s", again, err, r.RequestID)
	}
	if n := len(s.Sent()); n != 1 {
		t.Fatalf("%d pushes accepted, want 1", n)
	}
	sent := s.Sent()[0]
	if sent.To != "Cgroup" || sent.RetryKey != "key-1" || string(sent.Body) != body {
		t.Fatalf("sent %+v", sent)
	}

	// The answer of an accepted push is lost: transient, and the retry is the 409 replay.
	s.LoseNextAnswer()
	if _, err := c.Push(ctx, "key-2", []byte(body)); err == nil || !line.IsTransient(err) {
		t.Fatalf("lost answer: %v, want a transient error", err)
	}
	if r, err := c.Push(ctx, "key-2", []byte(body)); err != nil || !r.Replayed {
		t.Fatalf("retry after a lost answer: %+v %v", r, err)
	}
	if n := len(s.Sent()); n != 2 {
		t.Fatalf("%d pushes accepted, want 2", n)
	}
}

// 429 and 5xx retry; other 4xx are permanent; errors carry the status and LINE's message, never the token.
func TestPushErrors(t *testing.T) {
	s := linetest.New(t)
	c := s.Client()
	ctx := context.Background()
	for _, tc := range []struct {
		status    int
		transient bool
	}{{429, true}, {500, true}, {503, true}, {400, false}, {403, false}, {404, false}} {
		s.FailNext(tc.status, "The property, 'to', in the request body is invalid")
		_, err := c.Push(ctx, "k", []byte(body))
		var he *line.HTTPError
		if !errors.As(err, &he) || he.Status != tc.status || line.IsTransient(err) != tc.transient {
			t.Errorf("HTTP %d: %v (transient %v), want transient %v", tc.status, err, line.IsTransient(err), tc.transient)
		}
		if strings.Contains(err.Error(), linetest.Token) || !strings.Contains(err.Error(), "'to'") {
			t.Errorf("error text %q", err.Error())
		}
	}
	bad, err := line.New(line.Config{BaseURL: s.URL(), Token: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bad.Push(ctx, "k", []byte(body))
	var he *line.HTTPError
	if !errors.As(err, &he) || he.Status != 401 || line.IsTransient(err) || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("bad token: %v", err)
	}
	if len(s.Sent()) != 0 {
		t.Fatal("a refused push was recorded")
	}
}

func TestNewAndRedirects(t *testing.T) {
	if _, err := line.New(line.Config{Token: " "}); !errors.Is(err, line.ErrNoToken) {
		t.Fatalf("blank token: %v", err)
	}
	for _, u := range []string{"ftp://x", "https://u:p@x", "https://x?q=1", "x"} {
		if _, err := line.New(line.Config{BaseURL: u, Token: "t"}); err == nil {
			t.Errorf("base %q accepted", u)
		}
	}
	if _, err := line.New(line.Config{Token: "t"}); err != nil {
		t.Fatalf("default base URL: %v", err)
	}
	var auth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { auth = r.Header.Get("Authorization") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+line.PushPath, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c, err := line.New(line.Config{BaseURL: redirect.URL, Token: "secret-token", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Push(context.Background(), "k", []byte(body))
	var he *line.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusTemporaryRedirect || auth != "" {
		t.Fatalf("redirect: %v, the token reached the target: %q", err, auth)
	}
	if !line.IsTransient(errors.New("dial tcp: connection refused")) || line.IsTransient(line.ErrConfig) {
		t.Fatal("IsTransient of transport and configuration errors")
	}
}

// The local stack's WireMock answer (deploy/mocks, profile mocks; .env.example points LINE_API_BASE_URL at
// http://mocks:8080/line) is a 200 to the push path of this client, with a bearer token and JSON: the client accepts it.
func TestLocalMockAnswers(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/mocks/mappings/line.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Request struct {
			Method  string `json:"method"`
			URLPath string `json:"urlPath"`
		} `json:"request"`
		Response struct {
			Status   int               `json:"status"`
			Headers  map[string]string `json:"headers"`
			JSONBody json.RawMessage   `json:"jsonBody"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != m.Request.Method || r.URL.Path != m.Request.URLPath || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.NotFound(w, r)
			return
		}
		for k, v := range m.Response.Headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(m.Response.Status)
		_, _ = w.Write(m.Response.JSONBody)
	}))
	t.Cleanup(srv.Close)
	c, err := line.New(line.Config{BaseURL: srv.URL + "/line", Token: "placeholder"})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := c.Push(context.Background(), "key", []byte(body)); err != nil || r.RequestID != "mock-line-request-0001" {
		t.Fatalf("the mock's answer: %+v %v", r, err)
	}
}
