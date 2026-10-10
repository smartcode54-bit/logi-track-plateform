package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"slices"
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

// Held are the versions production holds back while later ones apply (R59, R88):
// 0010_d5_unique_constraints waits for the owner's P1 quarantine sign-off, while 0011+ ship in P0
// with Apply (`migrate apply 11`). Only a held version may stay pending below the highest applied
// one; the `up` of the P1 runbook then applies it out of order. Any other gap is refused, as
// goose's own default refuses every out-of-order migration.
var Held = []int64{10}

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
	// Out-of-order is allowed in goose and narrowed to the Held versions by checkGaps.
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
		goose.WithAllowOutofOrder(true),
	)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Runner{provider: p, files: files}, nil
}

// Files returns the checked chain in version order.
func (r *Runner) Files() []File { return r.files }

// Up applies every pending migration, a held one below the applied maximum included (the P1
// runbook's `up` after `apply 11`).
func (r *Runner) Up(ctx context.Context) ([]Result, error) {
	if err := r.checkGaps(ctx, math.MaxInt64); err != nil {
		return nil, err
	}
	return results(r.provider.Up(ctx))
}

// UpTo applies pending migrations up to and including version. Production stays at 9 plus the
// versions it applies with Apply until the owner signs off the P1 quarantine report (R59, R88).
func (r *Runner) UpTo(ctx context.Context, version int64) ([]Result, error) {
	if version < 1 {
		return nil, errors.New("migrate: up-to needs a version of at least 1")
	}
	if err := r.checkGaps(ctx, version); err != nil {
		return nil, err
	}
	return results(r.provider.UpTo(ctx, version))
}

// Apply applies exactly one pending version ahead of a held one: production P0 runs `up-to 9`
// then `apply 11`, so the T11 storage schema ships while 0010 waits for the P1 sign-off. Every
// version below it must be applied or held; an applied version is a no-op (nil results), so a
// deploy job can repeat the step.
func (r *Runner) Apply(ctx context.Context, version int64) ([]Result, error) {
	if !slices.ContainsFunc(r.files, func(f File) bool { return f.Version == version }) {
		return nil, fmt.Errorf("migrate: apply: the chain has no version %d", version)
	}
	st, err := r.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: status: %w", err)
	}
	if slices.ContainsFunc(st, func(s *goose.MigrationStatus) bool {
		return s.Source.Version == version && s.State == goose.StateApplied
	}) {
		return nil, nil
	}
	for _, s := range st {
		if v := s.Source.Version; v < version && s.State != goose.StateApplied && !slices.Contains(Held, v) {
			return nil, fmt.Errorf("migrate: apply %d would skip pending %s; only %v may stay behind (R59, R88), run up-to %d first",
				version, baseName(s.Source.Path), Held, version-1)
		}
	}
	if err := r.checkGaps(ctx, math.MaxInt64); err != nil {
		return nil, err
	}
	res, err := r.provider.ApplyVersion(ctx, version, true)
	if errors.Is(err, goose.ErrAlreadyApplied) {
		return nil, nil // a concurrent run applied it between the status read and the lock
	}
	if res == nil {
		return results(nil, err)
	}
	return results([]*goose.MigrationResult{res}, err)
}

// checkGaps refuses a run while a version that is not Held is pending below the highest applied
// one (and at most target): goose is built with out-of-order allowed so that a held version can
// follow later ones, and this keeps every other gap an error.
func (r *Runner) checkGaps(ctx context.Context, target int64) error {
	st, err := r.provider.Status(ctx)
	if err != nil {
		return fmt.Errorf("migrate: status: %w", err)
	}
	var top int64
	for _, s := range st {
		if s.State == goose.StateApplied {
			top = max(top, s.Source.Version)
		}
	}
	var gaps []string
	for _, s := range st {
		v := s.Source.Version
		if s.State != goose.StateApplied && v < top && v <= target && !slices.Contains(Held, v) {
			gaps = append(gaps, baseName(s.Source.Path))
		}
	}
	if len(gaps) > 0 {
		return fmt.Errorf("migrate: %v pending below applied version %d; only %v may be applied out of order (R59, R88)",
			gaps, top, Held)
	}
	return nil
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
