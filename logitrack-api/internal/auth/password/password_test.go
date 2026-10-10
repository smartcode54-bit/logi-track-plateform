package password_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
)

// Small parameters keep the tests fast; production reads ARGON2_* (64 MiB, 3, 2 by default).
var fast = password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1}

func TestHashVerifyAndRehash(t *testing.T) {
	h, err := password.NewHasher(fast)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	phc, err := h.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("PHC string = %s", phc)
	}
	if ok, rehash, err := h.Verify(ctx, "correct horse battery", phc); err != nil || !ok || rehash {
		t.Fatalf("verify: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := h.Verify(ctx, "correct horse batterY", phc); ok {
		t.Fatal("a wrong password verified")
	}
	// Raising ARGON2_ITERATIONS or ARGON2_MEMORY_KB makes the next successful login re-hash (C.4.8).
	stronger, _ := password.NewHasher(password.Params{MemoryKB: 16 * 1024, Iterations: 2, Parallelism: 1})
	if ok, rehash, err := stronger.Verify(ctx, "correct horse battery", phc); err != nil || !ok || !rehash {
		t.Fatalf("weaker stored parameters: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if _, _, err := h.Verify(ctx, "x", "$2a$10$bcrypt"); err == nil {
		t.Fatal("a non-Argon2id hash is an error")
	}
	if err := h.DummyVerify(ctx, strings.Repeat("x", 10_000)); err != nil { // bounded, never panics
		t.Fatal(err)
	}
}

// Hashes are computed over NFC: the composed and decomposed spelling of the same text verify alike.
func TestNFC(t *testing.T) {
	h, _ := password.NewHasher(fast)
	composed, decomposed := "café-password", "café-password"
	phc, _ := h.Hash(context.Background(), composed)
	if ok, _, _ := h.Verify(context.Background(), decomposed, phc); !ok {
		t.Fatal("NFC forms must verify alike")
	}
}

// The gate never lets more than Capacity memory-hard computations run at once, whatever the number of
// callers, and a caller whose context ends while it waits returns at once without hashing.
func TestGateBoundsConcurrentHashes(t *testing.T) {
	h, err := password.NewHasher(fast)
	if err != nil {
		t.Fatal(err)
	}
	limit := h.Capacity()
	if limit < 1 {
		t.Fatalf("capacity %d", limit)
	}
	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup
	for range limit*4 + 3 {
		wg.Go(func() {
			if err := h.Gate(context.Background(), func() {
				n := inFlight.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				inFlight.Add(-1)
			}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if p := peak.Load(); p < 1 || p > int64(limit) {
		t.Fatalf("peak concurrency %d, capacity %d", p, limit)
	}

	// Every slot taken: a cancelled caller returns its context error promptly and computes nothing.
	release := make(chan struct{})
	var held sync.WaitGroup
	held.Add(limit)
	for range limit {
		go func() { _ = h.Gate(context.Background(), func() { held.Done(); <-release }) }()
	}
	held.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := h.Hash(ctx, "a long passphrase"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Hash with every slot taken: %v", err)
	}
	if err := h.DummyVerify(ctx, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DummyVerify after the deadline: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("a cancelled caller waited %v", d)
	}
	close(release)
}

func TestPolicy(t *testing.T) {
	p, err := password.NewPolicy(10)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		pw, email, mobile, want string
	}{
		"ok":                {"a long passphrase", "somchai@logitrack.test", "0812345678", ""},
		"ok thai":           {"รหัสผ่านยาวพอแล้ว", "a@b.c", "", ""},
		"too short":         {"short-pw1", "a@b.c", "", "too_short"},
		"too long":          {strings.Repeat("x", 129), "a@b.c", "", "too_long"},
		"email local part":  {"Somchai.Jaidee", "somchai.jaidee@logitrack.test", "", "equals_email"},
		"mobile digits":     {"0812345678", "a@b.c", "081-234-5678", "equals_mobile"},
		"mobile intl":       {"+66 81 234 5678", "a@b.c", "0812345678", "equals_mobile"},
		"other digits ok":   {"0812345679", "a@b.c", "0812345678", ""},
		"common":            {"Password123", "a@b.c", "", "too_common"},
		"common digits":     {"1234567890", "a@b.c", "", "too_common"},
		"ten runes not ten": {"กขคงจฉชซฌญ", "a@b.c", "", ""},
	}
	for name, tc := range cases {
		v := p.Check(tc.pw, tc.email, tc.mobile)
		got := ""
		if v != nil {
			got = v.Reason
		}
		if got != tc.want {
			t.Errorf("%s: reason %q, want %q", name, got, tc.want)
		}
	}
	if v := p.Check("short", "a@b.c"); v == nil || v.Min != 10 || v.Max != 128 {
		t.Fatalf("too_short carries the limits: %+v", v)
	}
	if _, err := password.NewPolicy(4); err == nil {
		t.Fatal("PASSWORD_MIN_LENGTH below 8 is refused")
	}
}

func TestTemporary(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		p, err := password.Temporary()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != password.TemporaryLength || strings.ContainsAny(p, "0O1Il") {
			t.Fatalf("temporary password %q", p)
		}
		if seen[p] {
			t.Fatal("repeated temporary password")
		}
		seen[p] = true
	}
}
