// Package storagetest gives integration tests the MinIO of deploy/docker-compose.yml: the same image, command
// and server-level CORS (MINIO_API_CORS_ALLOW_ORIGIN from the CORS_ALLOWED_ORIGINS of .env.example), one
// container per test binary and fresh buckets per test. Tests that use it carry the "integration" build tag and
// call Terminate from TestMain after pgtest.Main.
package storagetest

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// MinIO is the shared MinIO container.
type MinIO struct {
	ctr *testcontainers.DockerContainer
	// Endpoint is http://host:port of the S3 API as the test process reaches it.
	Endpoint string
	// CORSOrigins is the server-level CORS allow-list the container runs with.
	CORSOrigins []string
	user, pass  string
	seq         atomic.Int64
}

var (
	once     sync.Once
	shared   *MinIO
	startErr error
)

// Terminate stops the shared container of this binary.
func Terminate() {
	if shared != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = testcontainers.TerminateContainer(shared.ctr, testcontainers.StopContext(ctx))
	}
}

// Shared returns the MinIO of this binary, starting it on first use.
func Shared(tb testing.TB) *MinIO {
	tb.Helper()
	once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		shared, startErr = start(ctx)
	})
	if startErr != nil {
		tb.Fatalf("storagetest: %v", startErr)
	}
	return shared
}

// envExampleValue reads one value of .env.example (the local stack's non-secret defaults).
func envExampleValue(name string) (string, error) {
	path, err := asynctest.ModuleFile(".env.example")
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), name+"="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("%s is not in .env.example", name)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func start(ctx context.Context) (*MinIO, error) {
	image, command, err := asynctest.ComposeService("minio")
	if err != nil {
		return nil, err
	}
	cors, err := envExampleValue("CORS_ALLOWED_ORIGINS")
	if err != nil {
		return nil, err
	}
	m := &MinIO{user: "test-" + randomHex(4), pass: randomHex(16), CORSOrigins: strings.Split(cors, ",")}
	ctr, err := testcontainers.Run(ctx, image,
		testcontainers.WithCmd(command...),
		testcontainers.WithEnv(map[string]string{"MINIO_ROOT_USER": m.user, "MINIO_ROOT_PASSWORD": m.pass,
			"MINIO_API_CORS_ALLOW_ORIGIN": cors}),
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/minio/health/live").WithPort("9000/tcp").WithStartupTimeout(3*time.Minute)),
	)
	if err != nil {
		if ctr != nil {
			_ = testcontainers.TerminateContainer(ctr)
		}
		return nil, fmt.Errorf("start %s: %w", image, err)
	}
	m.ctr = ctr
	host, err := ctr.Host(ctx)
	if err != nil {
		return nil, err
	}
	port, err := ctr.MappedPort(ctx, "9000/tcp")
	if err != nil {
		return nil, err
	}
	m.Endpoint = "http://" + net.JoinHostPort(host, port.Port())
	return m, nil
}

// Config returns an S3 configuration on fresh bucket names (created by S3.Bootstrap) whose presigned URLs are
// signed for presignEndpoint ("" = the container endpoint itself). The credentials are the container's root
// pair: test-only values that never leave the test process.
func (m *MinIO) Config(presignEndpoint string) storage.S3Config {
	n := m.seq.Add(1)
	if presignEndpoint == "" {
		presignEndpoint = m.Endpoint
	}
	return storage.S3Config{
		Endpoint: m.Endpoint, PresignEndpoint: presignEndpoint, Region: "us-east-1",
		AccessKeyID: m.user, SecretAccessKey: m.pass, PathStyle: true,
		Bucket: fmt.Sprintf("t%03d-logitrack", n), PublicBucket: fmt.Sprintf("t%03d-logitrack-public", n),
		PublicBaseURL: presignEndpoint + fmt.Sprintf("/t%03d-logitrack-public", n), CORSOrigins: m.CORSOrigins,
	}
}
