// Package linetest is an in-process stand-in of the LINE Messaging API push endpoint for tests: the real line.HTTP
// client talks to it over HTTP, so the wire format, the bearer token and the X-Line-Retry-Key semantics are exercised
// without reaching LINE. A push is accepted once per retry key: a second request with the same key answers 409 with
// X-Line-Accepted-Request-Id, as LINE does for 24 hours. Failures are injected per request (an HTTP status, or an
// accepted push whose answer is lost).
package linetest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/line"
)

// Token is the channel access token the stand-in accepts.
const Token = "linetest-channel-token"

// Push is one accepted push as the group would show it.
type Push struct {
	To        string
	RetryKey  string
	RequestID string
	Body      []byte          // the request body as sent
	Messages  json.RawMessage // its "messages"
}

// Server is the stand-in.
type Server struct {
	t   testing.TB
	srv *httptest.Server

	mu       sync.Mutex
	accepted map[string]string // retry key -> request id
	sent     []Push
	requests int
	failures []failure
}

type failure struct {
	status   int
	message  string
	loseBody bool // accept the push, then drop the connection before answering
}

// New starts the stand-in; it stops when the test ends.
func New(t testing.TB) *Server {
	s := &Server{t: t, accepted: map[string]string{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the base URL to configure (LINE_API_BASE_URL).
func (s *Server) URL() string { return s.srv.URL }

// Client is a line.HTTP client of the stand-in with the accepted token.
func (s *Server) Client() *line.HTTP {
	c, err := line.New(line.Config{BaseURL: s.srv.URL, Token: Token})
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}

// FailNext answers the next request with status (and LINE's error message), without accepting it.
func (s *Server) FailNext(status int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure{status: status, message: message})
}

// LoseNextAnswer accepts the next push and closes the connection without answering: the sender cannot tell whether
// the card was posted (a timeout or reset after the request was written).
func (s *Server) LoseNextAnswer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure{loseBody: true})
}

// ForgetRetryKeys ends the retry-key window of every accepted push (LINE keeps a key 24 hours): a later push with the
// same key is accepted again.
func (s *Server) ForgetRetryKeys() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accepted = map[string]string{}
}

// Sent returns the accepted pushes in order.
func (s *Server) Sent() []Push {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Push(nil), s.sent...)
}

// Requests counts every request received, refused ones included.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	var f *failure
	if len(s.failures) > 0 {
		f = &s.failures[0]
		s.failures = s.failures[1:]
	}
	s.mu.Unlock()
	if r.Method != http.MethodPost || r.URL.Path != line.PushPath {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+Token {
		writeError(w, http.StatusUnauthorized, "Authentication failed due to the following reason: invalid token.")
		return
	}
	if f != nil && !f.loseBody {
		writeError(w, f.status, f.message)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unreadable body")
		return
	}
	var body struct {
		To       string          `json:"to"`
		Messages json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil || !strings.HasPrefix(body.To, "C") && !strings.HasPrefix(body.To, "U") &&
		!strings.HasPrefix(body.To, "R") {
		writeError(w, http.StatusBadRequest, "The property, 'to', in the request body is invalid")
		return
	}
	key := r.Header.Get(line.HeaderRetryKey)
	s.mu.Lock()
	if id, ok := s.accepted[key]; ok && key != "" {
		s.mu.Unlock()
		w.Header().Set(line.HeaderAcceptedRequest, id)
		writeError(w, http.StatusConflict, "The retry key is already accepted")
		return
	}
	id := fmt.Sprintf("req-%d", len(s.sent)+1)
	if key != "" {
		s.accepted[key] = id
	}
	s.sent = append(s.sent, Push{To: body.To, RetryKey: key, RequestID: id, Body: raw, Messages: body.Messages})
	s.mu.Unlock()
	if f != nil && f.loseBody {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		panic(http.ErrAbortHandler)
	}
	w.Header().Set(line.HeaderRequestID, id)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"sentMessages":[{"id":"` + id + `"}]}`))
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]string{"message": message})
	_, _ = w.Write(b)
}
