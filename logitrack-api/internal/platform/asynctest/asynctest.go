// Package asynctest gives integration tests the asynchronous infrastructure of compose with the images
// of deploy/docker-compose.yml: RabbitMQ (a fresh vhost per test), Redis (a fresh logical database per
// test, compose flags) and Mailpit (one per test). One RabbitMQ and one Redis container per test
// binary (testcontainers-go). Tests that use it carry the "integration" build tag and call Terminate
// from TestMain after pgtest.Main.
package asynctest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.yaml.in/yaml/v3"
)

// composeService reads the image and command of a service of deploy/docker-compose.yml.
func composeService(name string) (image string, command []string, err error) {
	path, err := moduleFile("deploy", "docker-compose.yml")
	if err != nil {
		return "", nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var doc struct {
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", nil, fmt.Errorf("asynctest: parse compose: %w", err)
	}
	node, ok := doc.Services[name]
	if !ok {
		return "", nil, fmt.Errorf("asynctest: compose has no %s service", name)
	}
	var svc struct {
		Image   string   `yaml:"image"`
		Command []string `yaml:"command"`
	}
	if err := node.Decode(&svc); err != nil {
		return "", nil, fmt.Errorf("asynctest: parse compose %s: %w", name, err)
	}
	if svc.Image == "" {
		return "", nil, fmt.Errorf("asynctest: compose %s has no image", name)
	}
	return svc.Image, svc.Command, nil
}

var (
	rabbitOnce sync.Once
	rabbit     *Rabbit
	rabbitErr  error
	redisOnce  sync.Once
	redisSrv   *Redis
	redisErr   error
)

// Terminate stops the shared containers of this binary.
func Terminate() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if rabbit != nil {
		_ = testcontainers.TerminateContainer(rabbit.ctr, testcontainers.StopContext(ctx))
	}
	if redisSrv != nil {
		_ = testcontainers.TerminateContainer(redisSrv.ctr, testcontainers.StopContext(ctx))
	}
}

// Rabbit is the shared RabbitMQ container (management plugin on, as compose runs it).
type Rabbit struct {
	ctr      *testcontainers.DockerContainer
	host     string
	amqpPort string
	httpPort string
	user     string
	password string
	seq      atomic.Int64
}

// SharedRabbit returns the RabbitMQ of this binary, starting it on first use.
func SharedRabbit(tb testing.TB) *Rabbit {
	tb.Helper()
	rabbitOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		rabbit, rabbitErr = startRabbit(ctx)
	})
	if rabbitErr != nil {
		tb.Fatalf("asynctest: %v", rabbitErr)
	}
	return rabbit
}

func startRabbit(ctx context.Context) (*Rabbit, error) {
	image, _, err := composeService("rabbitmq")
	if err != nil {
		return nil, err
	}
	r := &Rabbit{user: "test", password: randomHex()}
	ctr, err := testcontainers.Run(ctx, image,
		testcontainers.WithEnv(map[string]string{"RABBITMQ_DEFAULT_USER": r.user, "RABBITMQ_DEFAULT_PASS": r.password}),
		testcontainers.WithExposedPorts("5672/tcp", "15672/tcp"),
		testcontainers.WithWaitStrategy(wait.ForAll(
			wait.ForListeningPort("5672/tcp"),
			wait.ForHTTP("/api/overview").WithPort("15672/tcp").WithBasicAuth(r.user, r.password),
		).WithDeadline(3*time.Minute)),
	)
	if err != nil {
		if ctr != nil {
			_ = testcontainers.TerminateContainer(ctr)
		}
		return nil, fmt.Errorf("start %s: %w", image, err)
	}
	r.ctr = ctr
	if r.host, err = ctr.Host(ctx); err != nil {
		return nil, err
	}
	ap, err := ctr.MappedPort(ctx, "5672/tcp")
	if err != nil {
		return nil, err
	}
	hp, err := ctr.MappedPort(ctx, "15672/tcp")
	if err != nil {
		return nil, err
	}
	r.amqpPort, r.httpPort = ap.Port(), hp.Port()
	return r, nil
}

// VHost creates an empty vhost for one test (deleted afterwards) and returns its AMQP URL and name.
func (r *Rabbit) VHost(tb testing.TB) (amqpURL, vhost string) {
	tb.Helper()
	vhost = fmt.Sprintf("t%03d", r.seq.Add(1))
	if err := r.API(http.MethodPut, "/api/vhosts/"+url.PathEscape(vhost), nil); err != nil {
		tb.Fatalf("asynctest: create vhost: %v", err)
	}
	perm := []byte(`{"configure":".*","write":".*","read":".*"}`)
	if err := r.API(http.MethodPut, "/api/permissions/"+url.PathEscape(vhost)+"/"+url.PathEscape(r.user), perm); err != nil {
		tb.Fatalf("asynctest: grant vhost: %v", err)
	}
	tb.Cleanup(func() { _ = r.API(http.MethodDelete, "/api/vhosts/"+url.PathEscape(vhost), nil) })
	u := url.URL{Scheme: "amqp", User: url.UserPassword(r.user, r.password),
		Host: net.JoinHostPort(r.host, r.amqpPort), Path: "/" + vhost}
	return u.String(), vhost
}

// ImportDefinitions loads a definitions document into vhost, as deploy/rabbitmq/rabbitmq-init.sh does
// for "/" in compose.
func (r *Rabbit) ImportDefinitions(tb testing.TB, vhost string, defs []byte) {
	tb.Helper()
	if err := r.API(http.MethodPost, "/api/definitions/"+url.PathEscape(vhost), defs); err != nil {
		tb.Fatalf("asynctest: import definitions: %v", err)
	}
}

// API calls the management HTTP API.
func (r *Rabbit) API(method, path string, body []byte) error {
	req, err := http.NewRequest(method, "http://"+net.JoinHostPort(r.host, r.httpPort)+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(r.user, r.password)
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, b)
	}
	return nil
}

// Redis is the shared Redis container with the compose flags.
type Redis struct {
	ctr  *testcontainers.DockerContainer
	addr string
	seq  atomic.Int64
}

const redisDatabases = 256

// SharedRedis returns the Redis of this binary, starting it on first use.
func SharedRedis(tb testing.TB) *Redis {
	tb.Helper()
	redisOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		redisSrv, redisErr = startRedis(ctx)
	})
	if redisErr != nil {
		tb.Fatalf("asynctest: %v", redisErr)
	}
	return redisSrv
}

func startRedis(ctx context.Context) (*Redis, error) {
	image, command, err := composeService("redis")
	if err != nil {
		return nil, err
	}
	cmd := append(slices.Clone(command), "--databases", fmt.Sprint(redisDatabases))
	ctr, err := testcontainers.Run(ctx, image,
		testcontainers.WithCmd(cmd...),
		testcontainers.WithExposedPorts("6379/tcp"),
		testcontainers.WithWaitStrategy(wait.ForLog("Ready to accept connections").WithStartupTimeout(time.Minute)),
	)
	if err != nil {
		if ctr != nil {
			_ = testcontainers.TerminateContainer(ctr)
		}
		return nil, fmt.Errorf("start %s: %w", image, err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		return nil, err
	}
	port, err := ctr.MappedPort(ctx, "6379/tcp")
	if err != nil {
		return nil, err
	}
	return &Redis{ctr: ctr, addr: net.JoinHostPort(host, port.Port())}, nil
}

// URL reserves a fresh logical database and returns its redis:// URL (no password: test only).
func (r *Redis) URL(tb testing.TB) string {
	tb.Helper()
	return fmt.Sprintf("redis://%s/%d", r.addr, r.next(tb))
}

func (r *Redis) next(tb testing.TB) int {
	tb.Helper()
	n := int(r.seq.Add(1))
	if n >= redisDatabases {
		tb.Fatalf("asynctest: more than %d Redis databases in one binary", redisDatabases-1)
	}
	return n
}

// Client opens a client on a fresh, flushed logical database, closed when the test ends.
func (r *Redis) Client(tb testing.TB) *redis.Client {
	tb.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: r.addr, DB: r.next(tb)})
	tb.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		tb.Fatalf("asynctest: flushdb: %v", err)
	}
	return rdb
}

// Mailpit is one Mailpit container (SMTP on SMTPHost:SMTPPort, API on its HTTP port).
type Mailpit struct {
	SMTPHost string
	SMTPPort int
	api      string
}

// StartMailpit starts a Mailpit for one test and removes it afterwards.
func StartMailpit(tb testing.TB) *Mailpit {
	tb.Helper()
	image, _, err := composeService("mailpit")
	if err != nil {
		tb.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ctr, err := testcontainers.Run(ctx, image,
		// No reverse-DNS lookup of the client: on Docker networks it can stall the SMTP greeting.
		testcontainers.WithEnv(map[string]string{"MP_SMTP_DISABLE_RDNS": "true"}),
		testcontainers.WithExposedPorts("1025/tcp", "8025/tcp"),
		testcontainers.WithWaitStrategy(wait.ForAll(
			wait.ForListeningPort("1025/tcp"),
			wait.ForHTTP("/api/v1/info").WithPort("8025/tcp"),
		).WithDeadline(time.Minute)),
	)
	if err != nil {
		if ctr != nil {
			_ = testcontainers.TerminateContainer(ctr)
		}
		tb.Fatalf("asynctest: start %s: %v", image, err)
	}
	tb.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	host, err := ctr.Host(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	sp, err := ctr.MappedPort(ctx, "1025/tcp")
	if err != nil {
		tb.Fatal(err)
	}
	hp, err := ctr.MappedPort(ctx, "8025/tcp")
	if err != nil {
		tb.Fatal(err)
	}
	m := &Mailpit{SMTPHost: host, api: "http://" + net.JoinHostPort(host, hp.Port())}
	_, _ = fmt.Sscan(sp.Port(), &m.SMTPPort)
	return m
}

// Mail is a message as Mailpit stored it.
type Mail struct {
	ID      string `json:"ID"`
	Subject string `json:"Subject"`
	Text    string `json:"Text"`
	HTML    string `json:"HTML"`
	From    struct {
		Name    string `json:"Name"`
		Address string `json:"Address"`
	} `json:"From"`
	To []struct {
		Address string `json:"Address"`
	} `json:"To"`
}

// Messages returns every stored message, newest first, with bodies.
func (m *Mailpit) Messages(tb testing.TB) []Mail {
	tb.Helper()
	var list struct {
		Messages []struct {
			ID string `json:"ID"`
		} `json:"messages"`
	}
	m.get(tb, "/api/v1/messages?limit=200", &list)
	out := make([]Mail, 0, len(list.Messages))
	for _, s := range list.Messages {
		var full Mail
		m.get(tb, "/api/v1/message/"+url.PathEscape(s.ID), &full)
		out = append(out, full)
	}
	return out
}

// WaitMessages polls until n messages are stored or the timeout passes.
func (m *Mailpit) WaitMessages(tb testing.TB, n int, timeout time.Duration) []Mail {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := m.Messages(tb)
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (m *Mailpit) get(tb testing.TB, path string, v any) {
	tb.Helper()
	resp, err := http.Get(m.api + path)
	if err != nil {
		tb.Fatalf("asynctest: mailpit: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		tb.Fatalf("asynctest: mailpit %s: %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		tb.Fatalf("asynctest: mailpit %s: %v", path, err)
	}
}

// Eventually polls cond every 50 ms until it holds or timeout passes.
func Eventually(tb testing.TB, timeout time.Duration, what string, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			tb.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ModuleFile resolves a path below the logitrack-api module root.
func ModuleFile(parts ...string) (string, error) { return moduleFile(parts...) }

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
			return "", errors.New("asynctest: go.mod not found above the working directory")
		}
		dir = parent
	}
}

func randomHex() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Contains reports whether s contains every part.
func Contains(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
