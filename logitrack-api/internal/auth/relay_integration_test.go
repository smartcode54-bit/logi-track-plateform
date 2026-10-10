//go:build integration

// The auth side of the outbox (T10): auth emits through outbox.Append, so its rows carry traceparent,
// and the relay's RevocationHook repairs a post-commit hook that a dying replica lost (Appendix C
// §C.4.7: at-least-once delivery of the revocation markers and the version raise).
package auth_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// The outbox rows of POST /v1/auth/password/forgot carry the caller's trace context and request id
// (Appendix B §B.5.2), so the worker's consumer span joins the forgot request's trace.
func TestForgotOutboxCarriesTraceparent(t *testing.T) {
	h := newHarness(t)
	h.user("trace@logitrack.test")
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{}) // what telemetry.SetupTracing installs
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	if err := h.svc.Forgot(ctx, auth.ForgotInput{Email: "trace@logitrack.test", IP: "198.51.100.7", RequestID: "req-trace"}); err != nil {
		t.Fatal(err)
	}
	rows, err := h.etl.Query(context.Background(), `SELECT routing_key, headers FROM outbox_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]string{}
	for rows.Next() {
		var rk string
		var headers map[string]string
		if err := rows.Scan(&rk, &headers); err != nil {
			t.Fatal(err)
		}
		got[rk] = headers
	}
	const want = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	for _, rk := range []string{auth.RoutePasswordResetAsked, auth.RouteSecurityEvent} {
		if hd := got[rk]; hd["traceparent"] != want || hd["requestId"] != "req-trace" {
			t.Fatalf("%s headers %v, want traceparent %s and requestId", rk, hd, want)
		}
	}
}

type nopPublisher struct{ keys []string }

func (p *nopPublisher) Publish(_ context.Context, _, key string, _ amqp.Publishing) error {
	p.keys = append(p.keys, key)
	return nil
}

// relayDrain runs the scheduler's relay over the harness outbox with the auth hook registered, as
// internal/app wires it.
func (h *harness) relayDrain() *nopPublisher {
	h.t.Helper()
	pub := &nopPublisher{}
	relay := outbox.NewRelay(h.pool, func() (outbox.Publisher, error) { return pub, nil },
		realtime.NewWriter(h.rdb, realtime.NewKeys(h.prefix), 1000, time.Hour), outbox.RelayOptions{
			Log: zerolog.Nop(),
			Hooks: map[string]outbox.Hook{
				auth.RouteSessionsRevoked: auth.RevocationHook(h.pool, auth.NewStore(h.rdb, h.prefix), 15*time.Minute, zerolog.Nop()),
			},
		})
	if _, err := relay.Drain(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	return pub
}

// A revocation whose post-commit writes were lost (Redis unreachable, then the replica stopped before
// its retrier landed them) is repaired when the relay publishes user.sessions_revoked: the revoked
// marker for an admin revoke, the version raise for a claims change.
func TestRelayReappliesLostRevocation(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	victim, staff := h.user("victim@logitrack.test"), h.user("staff@logitrack.test")
	h.member(victim, own, "operator")
	h.member(staff, own, "manager")
	ctx := context.Background()

	revoked := h.mustLogin("victim@logitrack.test", "web", "")
	stale := h.mustLogin("staff@logitrack.test", "web", "")
	for _, s := range []session{revoked, stale} {
		if r := h.get("/v1/me", s.access); r.status != http.StatusOK { // warms auth:user:ver
			t.Fatalf("me: %d %s", r.status, r.raw)
		}
	}

	lost, f, _ := h.replica(h.cfg())
	f.down.Store(true)
	if _, err := lost.Revoke(ctx, auth.Revocation{UserID: uuid.MustParse(victim), Reason: auth.RevokeAdmin}); err != nil {
		t.Fatal(err)
	}
	var pc *auth.PostCommit
	if err := db.WithSystem(ctx, h.pool, nil, func(tx pgx.Tx) error {
		var err error
		pc, _, err = lost.RevokeInTx(ctx, tx, auth.Revocation{UserID: uuid.MustParse(staff), Reason: auth.RevokeClaimsChanged, BumpVersion: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	lost.Apply(ctx, pc)
	lost.Close() // SIGTERM during a rolling deploy: the retrier dies with the replica
	f.down.Store(false)

	// The gap the hook closes: Redis still says the session is live and the version is current.
	if r := h.get("/v1/me", revoked.access); r.status != http.StatusOK {
		t.Fatalf("before the relay the revoked session answers %d; the test no longer reproduces the lost hook", r.status)
	}
	if r := h.get("/v1/me", stale.access); r.status != http.StatusOK {
		t.Fatalf("before the relay the stale token answers %d; the test no longer reproduces the lost raise", r.status)
	}

	pub := h.relayDrain()
	if n := countKey(pub.keys, auth.RouteSessionsRevoked); n != 2 {
		t.Fatalf("relay published %v, want two user.sessions_revoked", pub.keys)
	}
	ttl := h.rdb.PTTL(ctx, h.prefix+"auth:sess:revoked:"+revoked.sid).Val()
	if ttl <= 15*time.Minute || ttl > 15*time.Minute+30*time.Second {
		t.Fatalf("revoked marker TTL %v, want JWT_ACCESS_TTL + 30 s", ttl)
	}
	var ver string
	if err := h.etl.QueryRow(ctx, `SELECT auth_version::text FROM users WHERE id = $1`, staff).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if cached := h.rdb.Get(ctx, h.prefix+"auth:user:ver:"+staff).Val(); cached != ver {
		t.Fatalf("cached auth_version %q, want users.auth_version %s", cached, ver)
	}
	if r := h.get("/v1/me", revoked.access); r.status != http.StatusUnauthorized || r.code() != "session_revoked" {
		t.Fatalf("after the relay the revoked session answers %d %s", r.status, r.code())
	}
	if r := h.get("/v1/me", stale.access); r.status != http.StatusUnauthorized || r.code() != "token_expired" ||
		r.details()["reason"] != "claims_changed" {
		t.Fatalf("after the relay the stale token answers %d %s %v", r.status, r.code(), r.details())
	}
	// Idempotent: a redelivery of the same rows changes nothing.
	if _, err := h.pool.Exec(ctx, `UPDATE outbox_events SET published_at = NULL WHERE routing_key = $1`, auth.RouteSessionsRevoked); err != nil {
		t.Fatal(err)
	}
	if pub := h.relayDrain(); countKey(pub.keys, auth.RouteSessionsRevoked) != 2 {
		t.Fatalf("redelivery published %v", pub.keys)
	}
	if cached := h.rdb.Get(ctx, h.prefix+"auth:user:ver:"+staff).Val(); cached != ver {
		t.Fatalf("a second pass moved the cached version to %q", cached)
	}
}

func countKey(keys []string, key string) int {
	n := 0
	for _, k := range keys {
		if k == key {
			n++
		}
	}
	return n
}
