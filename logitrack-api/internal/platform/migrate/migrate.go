package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/dbq"
)

// Result is one migration applied or rolled back.
type Result struct {
	Version   int64
	Name      string
	Direction string // up | down
	Duration  time.Duration
}

// Status is one migration of the chain and whether the database has it.
type Status struct {
	Version   int64
	Name      string
	Applied   bool
	AppliedAt time.Time // UTC; zero when pending
}

// Runner applies a checked chain through goose. A PostgreSQL session advisory lock serialises
// concurrent runs (two deploys, or compose and a developer), so each version is applied once.
type Runner struct {
	provider *goose.Provider
	files    []File
}

// New checks fsys (Check) and prepares goose on db, which must be logged in as logitrack_migrator
// (RequireMigrator).
func New(sqlDB *sql.DB, fsys fs.FS) (*Runner, error) {
	files, err := Check(fsys)
	if err != nil {
		return nil, err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Runner{provider: p, files: files}, nil
}

// Files returns the checked chain in version order.
func (r *Runner) Files() []File { return r.files }

// Up applies every pending migration.
func (r *Runner) Up(ctx context.Context) ([]Result, error) {
	return results(r.provider.Up(ctx))
}

// UpTo applies pending migrations up to and including version. Production stays at 9 until the
// owner signs off the P1 quarantine report (R59, R88).
func (r *Runner) UpTo(ctx context.Context, version int64) ([]Result, error) {
	if version < 1 {
		return nil, errors.New("migrate: up-to needs a version of at least 1")
	}
	return results(r.provider.UpTo(ctx, version))
}

// Down rolls back the latest applied migration.
func (r *Runner) Down(ctx context.Context) ([]Result, error) {
	res, err := r.provider.Down(ctx)
	if errors.Is(err, goose.ErrNoNextVersion) {
		return nil, nil
	}
	if res == nil {
		return results(nil, err)
	}
	return results([]*goose.MigrationResult{res}, err)
}

// DownTo rolls back every migration above version (0 removes the whole chain).
func (r *Runner) DownTo(ctx context.Context, version int64) ([]Result, error) {
	if version < 0 {
		return nil, errors.New("migrate: down-to needs a version of at least 0")
	}
	return results(r.provider.DownTo(ctx, version))
}

// Status lists every migration of the chain with its state.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	st, err := r.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: status: %w", err)
	}
	out := make([]Status, 0, len(st))
	for _, s := range st {
		out = append(out, Status{
			Version:   s.Source.Version,
			Name:      baseName(s.Source.Path),
			Applied:   s.State == goose.StateApplied,
			AppliedAt: s.AppliedAt.UTC(),
		})
	}
	return out, nil
}

// Version is the highest applied version (0 on an empty database).
func (r *Runner) Version(ctx context.Context) (int64, error) {
	v, err := r.provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("migrate: version: %w", err)
	}
	return v, nil
}

// RequireMigrator fails unless the connection runs as logitrack_migrator without superuser or
// BYPASSRLS (R66): objects must be owned by the migrator, and a superuser login would hide RLS
// mistakes. 0001_preamble repeats the check inside the database.
func RequireMigrator(ctx context.Context, q dbq.DBTX) error {
	l, err := db.CurrentLogin(ctx, q)
	if err != nil {
		return err
	}
	if l.RoleName != db.RoleMigrator || l.Superuser || l.BypassRls {
		return fmt.Errorf("migrate: migrations run as %s through MIGRATE_DATABASE_URL (R66), not as %s (superuser=%t, bypassrls=%t)",
			db.RoleMigrator, l.RoleName, l.Superuser, l.BypassRls)
	}
	return nil
}

func results(in []*goose.MigrationResult, err error) ([]Result, error) {
	if partial, ok := errors.AsType[*goose.PartialError](err); ok {
		in = partial.Applied
		f := partial.Failed
		err = fmt.Errorf("migrate: %s %s failed: %w", f.Direction, baseName(f.Source.Path), partial.Err)
	} else if err != nil {
		err = fmt.Errorf("migrate: %w", err)
	}
	out := make([]Result, 0, len(in))
	for _, m := range in {
		out = append(out, Result{
			Version: m.Source.Version, Name: baseName(m.Source.Path),
			Direction: m.Direction, Duration: m.Duration,
		})
	}
	return out, err
}

func baseName(p string) string { return path.Base(p) }
