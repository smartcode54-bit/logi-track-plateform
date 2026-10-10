package ratelimit

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

func TestParseLimit(t *testing.T) {
	ok := map[string]Limit{
		"10/1m": {10, time.Minute}, " 5 / 15m ": {5, 15 * time.Minute}, "60/1m": {60, time.Minute}, "1/24h": {1, 24 * time.Hour},
	}
	for in, want := range ok {
		got, err := ParseLimit(in)
		if err != nil || got != want {
			t.Errorf("ParseLimit(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "10", "10/", "/1m", "0/1m", "-1/1m", "10/0s", "10/-1m", "ten/1m", "10/1x", "10/1d"} {
		if _, err := ParseLimit(in); err == nil {
			t.Errorf("ParseLimit(%q) accepted", in)
		}
	}
}

func TestBucketCatalog(t *testing.T) {
	names := map[string]bool{}
	for _, b := range Buckets {
		if names[b.Name] {
			t.Errorf("duplicate bucket %s", b.Name)
		}
		names[b.Name] = true
		if b.Name != "webhook" && !b.Default.valid() {
			t.Errorf("%s has no valid default", b.Name)
		}
	}
	// Appendix B §B.6.3 design values.
	for b, want := range map[Bucket]Limit{
		LoginIP: {10, time.Minute}, LoginFail: {5, 15 * time.Minute}, PublicFormIP: {5, time.Hour},
		PublicFormEmail: {1, 24 * time.Hour}, EvidenceIP: {60, time.Minute}, HeartbeatInstall: {1, 30 * time.Second},
		User: {600, time.Minute}, APIKey: {600, time.Minute},
	} {
		if b.Default != want {
			t.Errorf("%s default %v, want %v", b.Name, b.Default, want)
		}
	}
	var envs []string
	for _, b := range Buckets {
		if b.Env != "" {
			envs = append(envs, b.Env)
		}
	}
	slices.Sort(envs)
	if want := []string{"RATE_LIMIT_EVIDENCE", "RATE_LIMIT_LOGIN", "RATE_LIMIT_PUBLIC_FORMS"}; !slices.Equal(envs, want) {
		t.Errorf("configurable buckets %v, want %v", envs, want)
	}
}

func TestConfig(t *testing.T) {
	cfg, err := config.LoadFrom[Config]([]string{"RATE_LIMIT_LOGIN=20/1m"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Limit(LoginIP) != (Limit{20, time.Minute}) || cfg.Limit(PublicFormIP) != (Limit{5, time.Hour}) ||
		cfg.Limit(EvidenceIP) != (Limit{60, time.Minute}) || cfg.Limit(User) != User.Default {
		t.Fatalf("cfg = %+v", cfg)
	}
	_, err = config.LoadFrom[Config]([]string{"RATE_LIMIT_PUBLIC_FORMS=secret-looking-value", "RATE_LIMIT_ENABLED=false"})
	if err == nil || !strings.Contains(err.Error(), "RATE_LIMIT_PUBLIC_FORMS") || strings.Contains(err.Error(), "secret-looking-value") {
		t.Fatalf("invalid value: %v", err)
	}
}

func TestSubjectKeyHidesTheSubject(t *testing.T) {
	k := SubjectKey("someone@example.com")
	if len(k) != 32 || strings.Contains(k, "example") || k != SubjectKey("someone@example.com") || k == SubjectKey("other@example.com") {
		t.Fatalf("SubjectKey = %q", k)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for d, want := range map[time.Duration]int{0: 1, time.Millisecond: 1, time.Second: 1, 1500 * time.Millisecond: 2, time.Minute: 60} {
		if got := (Decision{RetryAfter: d}).RetryAfterSeconds(); got != want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", d, got, want)
		}
	}
}
