// Package pgtest gives integration tests a real PostgreSQL 18: one postgres:18-alpine container per
// test binary (testcontainers-go, R34), with deploy/postgres-init/00-roles.sql run by the image's
// init hook exactly as in compose (R66), and a fresh database per test owned by logitrack_migrator.
// Tests that use it carry the "integration" build tag (make test-integration) and call Main from
// TestMain so the container stops with the binary.
package pgtest

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// Image is the PostgreSQL image of compose, testcontainers and CI (R34).
const Image = "postgres:18-alpine"

// loginRoles get a random password once the container is up (00-roles.sql sets none).
var loginRoles = []string{db.RoleMigrator, db.RoleApp, db.RoleETL, db.RoleReadonly}

// Cluster is one running container.
type Cluster struct {
	ctr       *tcpostgres.PostgresContainer
	host      string
	port      string
	superURL  string
	passwords map[string]string
	seq       atomic.Int64
}

var (
	sharedOnce sync.Once
	shared     *Cluster
	sharedErr  error
)

// Main runs the tests of a package and stops the shared container afterwards.
func Main(m *testing.M) int {
	code := m.Run()
	if shared != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = shared.Terminate(ctx)
	}
	return code
}

// Shared returns the container of this test binary, starting it on first use.
func Shared(tb testing.TB) *Cluster {
	tb.Helper()
	sharedOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		shared, sharedErr = Start(ctx)
	})
	if sharedErr != nil {
		tb.Fatalf("pgtest: %v", sharedErr)
	}
	return shared
}

// Start launches a container with the R66 roles and random LOGIN passwords.
func Start(ctx context.Context) (*Cluster, error) {
	roles, err := moduleFile("deploy", "postgres-init", "00-roles.sql")
	if err != nil {
		return nil, err
	}
	superPW := randomHex()
	ctr, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("logitrack"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(superPW),
		tcpostgres.WithInitScripts(roles),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		if ctr != nil {
			_ = testcontainers.TerminateContainer(ctr)
		}
		return nil, fmt.Errorf("start %s: %w", Image, err)
	}
	c := &Cluster{ctr: ctr, passwords: map[string]string{}}
	if c.host, err = ctr.Host(ctx); err != nil {
		return nil, c.fail(err)
	}
	p, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, c.fail(err)
	}
	c.port = p.Port()
	c.superURL = c.url("postgres", superPW, "postgres")
	conn, err := pgx.Connect(ctx, c.superURL)
	if err != nil {
		return nil, c.fail(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, r := range loginRoles {
		pw := randomHex()
		// Test-only passwords on an ephemeral container; the hex value needs no escaping.
		if _, err := conn.Exec(ctx, fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", pgx.Identifier{r}.Sanitize(), pw)); err != nil {
			return nil, c.fail(fmt.Errorf("set password of %s: %w", r, err))
		}
		c.passwords[r] = pw
	}
	return c, nil
}

func (c *Cluster) fail(err error) error {
	_ = testcontainers.TerminateContainer(c.ctr)
	return err
}

// Terminate stops and removes the container.
func (c *Cluster) Terminate(ctx context.Context) error {
	return testcontainers.TerminateContainer(c.ctr, testcontainers.StopContext(ctx))
}

// SuperExec runs statements as the superuser on the maintenance database (role changes and other
// cluster-wide setup that only tests do).
func (c *Cluster) SuperExec(ctx context.Context, sql string) error {
	conn, err := pgx.Connect(ctx, c.superURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, sql)
	return err
}

func (c *Cluster) url(user, password, dbName string) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort(c.host, c.port),
		Path:     "/" + dbName,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

// Database is an empty database owned by logitrack_migrator.
type Database struct {
	Name    string
	cluster *Cluster
}

var unsafeChars = regexp.MustCompile(`[^a-z0-9_]+`)

// NewDatabase creates a database for one test on the shared container and drops it afterwards.
func NewDatabase(tb testing.TB) *Database {
	tb.Helper()
	c := Shared(tb)
	name := fmt.Sprintf("t%03d_%s", c.seq.Add(1), unsafeChars.ReplaceAllString(strings.ToLower(tb.Name()), "_"))
	if len(name) > 63 {
		name = name[:63]
	}
	ctx := context.Background()
	ident := pgx.Identifier{name}.Sanitize()
	if err := c.SuperExec(ctx, "CREATE DATABASE "+ident+" OWNER "+db.RoleMigrator); err != nil {
		tb.Fatalf("pgtest: create database: %v", err)
	}
	tb.Cleanup(func() {
		if err := c.SuperExec(context.Background(), "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			tb.Errorf("pgtest: drop database %s: %v", name, err)
		}
	})
	return &Database{Name: name, cluster: c}
}

// Cluster returns the container the database lives on.
func (d *Database) Cluster() *Cluster { return d.cluster }

// URL is the connection URL of a login role (db.RoleMigrator, RoleApp, RoleETL, RoleReadonly).
func (d *Database) URL(role string) string {
	pw, ok := d.cluster.passwords[role]
	if !ok {
		panic("pgtest: no login role " + role)
	}
	return d.cluster.url(role, pw, d.Name)
}

// SuperURL connects as the superuser; only for assertions a login role cannot make.
func (d *Database) SuperURL() string {
	u, _ := url.Parse(d.cluster.superURL)
	u.Path = "/" + d.Name
	return u.String()
}

// Pool opens a pool as role, closed when the test ends.
func (d *Database) Pool(tb testing.TB, role string) *pgxpool.Pool {
	tb.Helper()
	p, err := db.Open(context.Background(), d.URL(role), "pgtest")
	if err != nil {
		tb.Fatalf("pgtest: open %s: %v", role, err)
	}
	tb.Cleanup(p.Close)
	return p
}

// DumpSchema is `pg_dump --schema-only` (client 18, inside the container) without the per-run
// \restrict lines, so two dumps of the same schema compare equal.
func (d *Database) DumpSchema(ctx context.Context) (string, error) {
	code, out, err := d.cluster.ctr.Exec(ctx,
		[]string{"pg_dump", "--schema-only", "--username=postgres", "--dbname=" + d.Name}, tcexec.Multiplexed())
	if err != nil {
		return "", err
	}
	raw, err := io.ReadAll(out)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("pg_dump exited %d: %s", code, raw)
	}
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, `\restrict `) || strings.HasPrefix(line, `\unrestrict `) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String(), sc.Err()
}

// moduleFile resolves a path below the logitrack-api module root (the directory holding go.mod),
// searched upwards from the working directory of the test.
func moduleFile(parts ...string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			p := filepath.Join(append([]string{dir}, parts...)...)
			if _, err := os.Stat(p); err != nil {
				return "", fmt.Errorf("pgtest: %w", err)
			}
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("pgtest: go.mod not found above the working directory")
		}
		dir = parent
	}
}

func randomHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
