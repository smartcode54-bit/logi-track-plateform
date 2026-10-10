package cache_test

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/idempotency"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

// TestEnvExampleShipsTheDesignDefaults: .env.example (the local and CI stack's values, main spec
// §16) carries the same cache lifetimes, idempotency TTL and rate limits as the code defaults, which
// are the design values of Appendix B §B.6.2 and §B.6.3, so the file and the code cannot drift apart.
func TestEnvExampleShipsTheDesignDefaults(t *testing.T) {
	environ := envExample(t)
	ttls, err := config.LoadFrom[cache.TTLs](environ)
	if err != nil || *ttls != cache.DefaultTTLs {
		t.Errorf(".env.example cache TTLs %+v (%v), want the defaults %+v", ttls, err, cache.DefaultTTLs)
	}
	idem, err := config.LoadFrom[idempotency.Config](environ)
	if def, _ := config.LoadFrom[idempotency.Config](nil); err != nil || *idem != *def {
		t.Errorf(".env.example IDEMPOTENCY_TTL %+v (%v), want %+v", idem, err, def)
	}
	rl, err := config.LoadFrom[ratelimit.Config](environ)
	if err != nil {
		t.Fatal(err)
	}
	if !rl.Enabled {
		t.Error(".env.example disables rate limiting")
	}
	for _, b := range ratelimit.Buckets {
		if b.Env != "" && rl.Limit(b) != b.Default {
			t.Errorf(".env.example %s = %v, want the design value %v", b.Env, rl.Limit(b), b.Default)
		}
	}
}

// envExample reads the NAME=value lines of the module's .env.example (non-secret local defaults).
func envExample(t *testing.T) []string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
	f, err := os.Open(filepath.Join(dir, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
