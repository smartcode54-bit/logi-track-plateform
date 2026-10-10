// Package cachetest gives integration tests a real Redis: the image and server flags of the redis
// service in deploy/docker-compose.yml (AOF, maxmemory-policy noeviction), so tests run against the
// configuration compose runs. One container per test binary (testcontainers-go), a fresh logical
// database per test, and a separate container for tests that stop Redis. Tests that use it carry the
// "integration" build tag (make test-integration).
package cachetest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.yaml.in/yaml/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

// AppEnv is the APP_ENV of the test keyspace: keys are lt:local:...
const AppEnv = "local"

// testDatabases is appended to the compose flags so every test of a binary gets its own logical
// database; it changes nothing else about the server.
const testDatabases = 256

// ComposeRedis reads the image and command of the redis service from deploy/docker-compose.yml.
func ComposeRedis() (image string, command []string, err error) {
	path, err := moduleFile("deploy", "docker-compose.yml")
	if err != nil {
		return "", nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	// Other services may write command as a string: decode only the redis service.
	var doc struct {
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", nil, fmt.Errorf("cachetest: parse compose: %w", err)
	}
	node, ok := doc.Services["redis"]
	var svc struct {
		Image   string   `yaml:"image"`
		Command []string `yaml:"command"`
	}
	if ok {
		if err := node.Decode(&svc); err != nil {
			return "", nil, fmt.Errorf("cachetest: parse compose redis service: %w", err)
		}
	}
	if svc.Image == "" || len(svc.Command) == 0 {
		return "", nil, errors.New("cachetest: compose has no redis service with an image and a command list")
	}
	return svc.Image, svc.Command, nil
}

// Server is one running Redis container.
type Server struct {
	ctr  *testcontainers.DockerContainer
	addr string
	seq  atomic.Int64
}

var (
	sharedOnce sync.Once
	shared     *Server
	sharedErr  error
)

// Main runs the tests of a package and stops the shared container afterwards.
func Main(m *testing.M) int {
	code := m.Run()
	TerminateShared()
	return code
}

// TerminateShared stops the shared container; for TestMain functions that also use pgtest.Main.
func TerminateShared() {
	if shared != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = shared.Terminate(ctx)
	}
}

// Shared returns the container of this test binary, starting it on first use.
func Shared(tb testing.TB) *Server {
	tb.Helper()
	sharedOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		shared, sharedErr = Start(ctx)
	})
	if sharedErr != nil {
		tb.Fatalf("cachetest: %v", sharedErr)
	}
	return shared
}

// Start launches a container with the compose image and flags.
func Start(ctx context.Context) (*Server, error) {
	image, command, err := ComposeRedis()
	if err != nil {
		return nil, err
	}
	cmd := append(slices.Clone(command), "--databases", fmt.Sprint(testDatabases))
	ctr, err := testcontainers.Run(ctx, image,
		testcontainers.WithCmd(cmd...),
		testcontainers.WithExposedPorts("6379/tcp"),
		testcontainers.WithWaitStrategy(wait.ForLog("Ready to accept connections").WithStartupTimeout(time.Minute)),
	)
	if err != nil {
		if ctr != nil {
			_ = testcontainers.TerminateContainer(ctr)
		}
		return nil, fmt.Errorf("cachetest: start %s: %w", image, err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)
		return nil, err
	}
	port, err := ctr.MappedPort(ctx, "6379/tcp")
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)
		return nil, err
	}
	return &Server{ctr: ctr, addr: net.JoinHostPort(host, port.Port())}, nil
}

// StartDedicated starts a container for one test (one that stops Redis) and removes it afterwards.
func StartDedicated(tb testing.TB) *Server {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, err := Start(ctx)
	if err != nil {
		tb.Fatalf("cachetest: %v", err)
	}
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = s.Terminate(ctx)
	})
	return s
}

// Addr is host:port of the server.
func (s *Server) Addr() string { return s.addr }

// URL is the redis:// URL of a logical database (no password: test containers run without one).
func (s *Server) URL(db int) string { return fmt.Sprintf("redis://%s/%d", s.addr, db) }

// Stop stops the container (Redis down); the address then refuses connections.
func (s *Server) Stop(ctx context.Context) error {
	timeout := 5 * time.Second
	return s.ctr.Stop(ctx, &timeout)
}

// Terminate stops and removes the container.
func (s *Server) Terminate(ctx context.Context) error {
	return testcontainers.TerminateContainer(s.ctr, testcontainers.StopContext(ctx))
}

// Client opens a client on a fresh, empty logical database of s, closed when the test ends. It is
// built by cache.Open, so it carries the money-path guard like the processes' clients.
func (s *Server) Client(tb testing.TB) (*redis.Client, cache.Keyspace) {
	tb.Helper()
	n := int(s.seq.Add(1))
	if n >= testDatabases {
		tb.Fatalf("cachetest: more than %d databases requested in one binary", testDatabases-1)
	}
	rdb, ks, err := cache.Open(cache.Options{URL: s.URL(n), AppEnv: AppEnv})
	if err != nil {
		tb.Fatalf("cachetest: %v", err)
	}
	tb.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		tb.Fatalf("cachetest: flushdb: %v", err)
	}
	return rdb, ks
}

// NewClient is Shared(tb).Client(tb).
func NewClient(tb testing.TB) (*redis.Client, cache.Keyspace) {
	tb.Helper()
	return Shared(tb).Client(tb)
}

// Keys lists every key of the client's database.
func Keys(tb testing.TB, rdb redis.UniversalClient) []string {
	tb.Helper()
	var out []string
	ctx := context.Background()
	it := rdb.Scan(ctx, 0, "*", 1000).Iterator()
	for it.Next(ctx) {
		out = append(out, it.Val())
	}
	if err := it.Err(); err != nil {
		tb.Fatalf("cachetest: scan: %v", err)
	}
	slices.Sort(out)
	return out
}

// AssertKeyspace fails the test for any key of the database outside ks's prefix or outside the
// namespaces of Appendix B §B.6.1, and returns the keys it checked.
func AssertKeyspace(tb testing.TB, rdb redis.UniversalClient, ks cache.Keyspace) []string {
	tb.Helper()
	keys := Keys(tb, rdb)
	for _, k := range keys {
		if _, ok := ks.Namespace(k); !ok {
			tb.Errorf("key %q is outside %s{%v}:", k, ks.Prefix(), cache.Namespaces)
		}
	}
	return keys
}

// moduleFile resolves a path below the logitrack-api module root, searched upwards from the working
// directory of the test.
func moduleFile(parts ...string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(append([]string{dir}, parts...)...), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("cachetest: go.mod not found above the working directory")
		}
		dir = parent
	}
}
