package idempotency

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	recoverer "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// memStore is Store in memory with the semantics of the SQL (claim fencing by created_at).
type memStore struct {
	mu   sync.Mutex
	now  func() time.Time
	rows map[string]*memRow
}

type memRow struct {
	fp        string
	completed bool
	rec       Record
	createdAt time.Time
	expiresAt time.Time
}

func newMemStore() *memStore { return &memStore{now: time.Now, rows: map[string]*memRow{}} }

func (s *memStore) Claim(_ context.Context, scope, key, fp string, ttl, lock time.Duration) (time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if r, ok := s.rows[scope+"|"+key]; ok && now.Before(r.expiresAt) && (r.completed || !r.createdAt.Before(now.Add(-lock))) {
		return time.Time{}, false, nil
	}
	s.rows[scope+"|"+key] = &memRow{fp: fp, createdAt: now, expiresAt: now.Add(ttl)}
	return now, true, nil
}

func (s *memStore) Get(_ context.Context, scope, key string) (Row, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[scope+"|"+key]
	if !ok || !s.now().Before(r.expiresAt) {
		return Row{}, false, nil
	}
	rec := r.rec
	rec.Fingerprint = r.fp
	return Row{Completed: r.completed, Record: rec, ExpiresAt: r.expiresAt}, true, nil
}

func (s *memStore) Complete(_ context.Context, scope, key string, at time.Time, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rows[scope+"|"+key]; ok && !r.completed && r.createdAt.Equal(at) {
		r.completed, r.rec = true, rec
	}
	return nil
}

func (s *memStore) Release(_ context.Context, scope, key string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rows[scope+"|"+key]; ok && !r.completed && r.createdAt.Equal(at) {
		delete(s.rows, scope+"|"+key)
	}
	return nil
}

type harness struct {
	app   *fiber.App
	calls atomic.Int32
	store *memStore
}

// newHarness mounts POST /v1/mobile/expenses behind the middleware (PostgreSQL-only mode: no Redis).
// The handler answers 201 with an id derived from the call count, 422 for {"bad":true}, 500 for
// {"fail":true} and panics for {"panic":true}.
func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{store: newMemStore()}
	m, err := New(Options{Store: h.store, Scope: func(c fiber.Ctx) string { return c.Get("X-Test-User") }, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	h.app = fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	h.app.Use(httpx.RequestID(), recoverer.New())
	h.app.Post("/v1/mobile/expenses", m.Handler(), func(c fiber.Ctx) error {
		n := h.calls.Add(1)
		var in map[string]any
		_ = json.Unmarshal(c.Body(), &in)
		switch {
		case in["bad"] == true:
			return httpx.NewError(422, "invalid_argument", "bad")
		case in["fail"] == true:
			return c.Status(500).SendString("oops")
		case in["panic"] == true:
			panic("boom")
		}
		return httpx.JSON(c, 201, map[string]any{"id": n, "zeta": 1, "alpha": 2})
	})
	return h
}

type result struct {
	status   int
	body     []byte
	replayed string
	retry    string
}

func (h *harness) do(t *testing.T, user, key, body string) result {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/mobile/expenses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	if key != "" {
		req.Header.Set(Header, key)
	}
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return result{resp.StatusCode, b, resp.Header.Get(HeaderReplayed), resp.Header.Get("Retry-After")}
}

func errCode(t *testing.T, b []byte) (string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("not an error envelope: %s", b)
	}
	return env.Error.Code, env.Error.Details
}

// opID is the outbox operation id the driver app sends as Idempotency-Key.
const opID = "0192f1c2-7d3e-7a10-8b2c-1234567890ab"

func TestReplayReturnsTheIdenticalResponse(t *testing.T) {
	h := newHarness(t)
	first := h.do(t, "u1", opID, `{"amount":100}`)
	if first.status != 201 || first.replayed != "" {
		t.Fatalf("first: %d %s", first.status, first.body)
	}
	for range 2 {
		again := h.do(t, "u1", opID, `{"amount":100}`)
		if again.status != first.status || !bytes.Equal(again.body, first.body) || again.replayed != "true" {
			t.Fatalf("replay: %d %s (replayed %q), want %d %s", again.status, again.body, again.replayed, first.status, first.body)
		}
	}
	if h.calls.Load() != 1 {
		t.Fatalf("handler ran %d times", h.calls.Load())
	}
	// Upper-case spelling of the same uuid is the same key.
	if r := h.do(t, "u1", strings.ToUpper(opID), `{"amount":100}`); r.replayed != "true" {
		t.Fatalf("upper-case key: %d %s", r.status, r.body)
	}
	// Same key, another user: a different scope.
	if r := h.do(t, "u2", opID, `{"amount":100}`); r.status != 201 || r.replayed != "" {
		t.Fatalf("other user: %d %s", r.status, r.body)
	}
}

func TestSameKeyDifferentBodyIsAConflict(t *testing.T) {
	h := newHarness(t)
	h.do(t, "u1", opID, `{"amount":100}`)
	r := h.do(t, "u1", opID, `{"amount":999}`)
	code, details := errCode(t, r.body)
	if r.status != 409 || code != CodeIdempotencyConflict || details["reason"] != "fingerprint_mismatch" {
		t.Fatalf("got %d %s", r.status, r.body)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("handler ran %d times", h.calls.Load())
	}
}

func TestFailuresReleaseTheKey(t *testing.T) {
	for _, body := range []string{`{"bad":true}`, `{"fail":true}`, `{"panic":true}`} {
		h := newHarness(t)
		r1 := h.do(t, "u1", opID, body)
		r2 := h.do(t, "u1", opID, body)
		if r1.status < 400 || r2.status != r1.status || r2.replayed != "" || h.calls.Load() != 2 {
			t.Fatalf("%s: %d then %d (replayed %q), calls %d: a failed request must run again", body, r1.status, r2.status, r2.replayed, h.calls.Load())
		}
		if len(h.store.rows) != 0 {
			t.Fatalf("%s left a claim", body)
		}
	}
}

func TestHeaderAndScopeChecks(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		user, key, code, reason string
		status                  int
	}{
		{"u1", "", "bad_request", "missing", 400},
		{"u1", "not-a-uuid", "bad_request", "malformed", 400},
		{"u1", "0192f1c27d3e7a108b2c1234567890ab", "bad_request", "malformed", 400},
		{"", opID, "unauthenticated", "", 401},
	} {
		r := h.do(t, tc.user, tc.key, `{}`)
		code, details := errCode(t, r.body)
		if r.status != tc.status || code != tc.code || (tc.reason != "" && details["reason"] != tc.reason) {
			t.Errorf("user %q key %q: %d %s", tc.user, tc.key, r.status, r.body)
		}
	}
	if h.calls.Load() != 0 {
		t.Fatal("handler ran for a rejected request")
	}
}

func TestInFlightDuplicateIsTurnedAway(t *testing.T) {
	h := newHarness(t)
	// A live claim of another request with the same fingerprint.
	fp := Fingerprint("POST", "/v1/mobile/expenses", []byte(`{"amount":1}`))
	if _, ok, _ := h.store.Claim(context.Background(), "u1", opID, fp, time.Hour, LockTTL); !ok {
		t.Fatal("claim")
	}
	r := h.do(t, "u1", opID, `{"amount":1}`)
	code, details := errCode(t, r.body)
	if r.status != 409 || code != CodeIdempotencyConflict || details["reason"] != "in_flight" || r.retry != "1" {
		t.Fatalf("got %d %s retry %q", r.status, r.body, r.retry)
	}
	// An abandoned claim (older than the lock) is taken over.
	h.store.mu.Lock()
	for _, row := range h.store.rows {
		row.createdAt = row.createdAt.Add(-time.Minute)
	}
	h.store.mu.Unlock()
	if r := h.do(t, "u1", opID, `{"amount":1}`); r.status != 201 {
		t.Fatalf("takeover: %d %s", r.status, r.body)
	}
}

func TestRecordKeepsBytes(t *testing.T) {
	for _, body := range [][]byte{[]byte(`{"b":1,"a":[2,1]}  `), {0xff, 0x00, 0xfe}, nil} {
		rec := NewRecord("fp", 200, "application/json", body)
		b, _ := json.Marshal(rec)
		var back Record
		if err := json.Unmarshal(b, &back); err != nil || !bytes.Equal(back.Bytes(), body) {
			t.Fatalf("round trip of %q: %q %v", body, back.Bytes(), err)
		}
	}
}

func TestFingerprint(t *testing.T) {
	a := Fingerprint("POST", "/v1/mobile/expenses", []byte(`{}`))
	for _, other := range []string{
		Fingerprint("PUT", "/v1/mobile/expenses", []byte(`{}`)),
		Fingerprint("POST", "/v1/mobile/expenses?x=1", []byte(`{}`)),
		Fingerprint("POST", "/v1/mobile/expenses", []byte(`{ }`)),
	} {
		if other == a {
			t.Fatal("fingerprint ignores method, path or body")
		}
	}
	if len(a) != 64 {
		t.Fatalf("fingerprint %q", a)
	}
}

func TestConfig(t *testing.T) {
	cfg, err := config.LoadFrom[Config](nil)
	if err != nil || cfg.TTL != 168*time.Hour {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	if _, err := config.LoadFrom[Config]([]string{"IDEMPOTENCY_TTL=30m"}); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_TTL") {
		t.Fatalf("30m accepted: %v", err)
	}
}
