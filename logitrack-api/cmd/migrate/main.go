// Command migrate applies the goose chain embedded from migrations/ (developer-spec.md §3.5; R31,
// R59) as logitrack_migrator through MIGRATE_DATABASE_URL (R66, R87), and lints or creates
// migration files on the host.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const usage = `usage: migrate <command> [arguments]

Database commands (MIGRATE_DATABASE_URL, logged in as logitrack_migrator):
  up                    apply every pending migration (dev, CI and seeded databases)
  up-to VERSION         apply pending migrations up to VERSION; production stays at 9
                        until the P1 runbook applies 0010 (R59, R88)
  down                  roll back the latest migration (refused when APP_ENV=prod)
  down-to VERSION       roll back every migration above VERSION; 0 removes the chain
                        (refused when APP_ENV=prod)
  status [-fail-on-pending]
                        list every migration and its state; the flag exits 1 when one is pending
  version               print the applied version

File commands (no database):
  check [-dir DIR]      lint the chain (R31); default: the files embedded in this binary
  create [-dir DIR] NAME
                        write the next NNNN_NAME.sql into DIR (default: migrations)
`

func main() {
	ctx, stop := app.SignalContext()
	code := run(ctx, os.Args[1:], os.Environ(), migrations.FS, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args, environ []string, chain fs.FS, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return app.ExitConfigError
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "-h", "-help", "--help", "help":
		_, _ = fmt.Fprint(stdout, usage)
		return app.ExitOK
	case "check":
		return runCheck(rest, chain, stdout, stderr)
	case "create":
		return runCreate(rest, stdout, stderr)
	case "up", "up-to", "down", "down-to", "status", "version":
		return runDB(ctx, cmd, rest, environ, chain, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "migrate: unknown command %q\n\n%s", cmd, usage)
		return app.ExitConfigError
	}
}

func runCheck(args []string, chain fs.FS, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("check", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dir := fl.String("dir", "", "directory to lint instead of the embedded files")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return usageError(stderr, "check takes only -dir")
	}
	fsys, where := chain, "embedded chain"
	if *dir != "" {
		fsys, where = os.DirFS(*dir), *dir
	}
	files, err := migrate.Check(fsys)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "migrate: %s: %v\n", where, err)
		return app.ExitRuntimeError
	}
	_, _ = fmt.Fprintf(stdout, "migrate check: ok (%s: %d migration file(s), latest %04d, round-trip floor %d)\n",
		where, len(files), files[len(files)-1].Version, migrate.RoundTripFloor(files))
	return app.ExitOK
}

func runCreate(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("create", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dir := fl.String("dir", "migrations", "directory of the migration files")
	if err := fl.Parse(args); err != nil || fl.NArg() != 1 {
		return usageError(stderr, "create takes one NAME")
	}
	p, err := migrate.Create(*dir, fl.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return app.ExitConfigError
	}
	_, _ = fmt.Fprintf(stdout, "created %s: write the Up and Down sections, then run make migrate-check\n", p)
	return app.ExitOK
}

func runDB(ctx context.Context, cmd string, args, environ []string, chain fs.FS, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fl.SetOutput(stderr)
	failOnPending := false
	if cmd == "status" {
		fl.BoolVar(&failOnPending, "fail-on-pending", false, "exit 1 when a migration is pending")
	}
	if err := fl.Parse(args); err != nil {
		return usageError(stderr, cmd+": bad flags")
	}
	var target int64
	switch cmd {
	case "up-to", "down-to":
		if fl.NArg() != 1 {
			return usageError(stderr, cmd+" takes one VERSION")
		}
		v, err := strconv.ParseInt(fl.Arg(0), 10, 64)
		if err != nil || v < 0 || (cmd == "up-to" && v < 1) {
			return usageError(stderr, cmd+": VERSION must be a whole number (at least 1 for up-to)")
		}
		target = v
	default:
		if fl.NArg() != 0 {
			return usageError(stderr, cmd+" takes no arguments")
		}
	}

	cfg, err := config.LoadFrom[app.MigrateConfig](environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "migrate: %v\n", err)
		return app.ExitConfigError
	}
	if (cmd == "down" || cmd == "down-to") && cfg.AppEnv == "prod" {
		_, _ = fmt.Fprintln(stderr, "migrate: down is refused when APP_ENV=prod; production never rolls back with goose (expand/contract, developer-spec.md §17)")
		return app.ExitConfigError
	}
	log, err := app.NewLogger(cfg.Common, "migrate", stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "migrate: %v\n", err)
		return app.ExitConfigError
	}
	app.LogConfig[app.MigrateConfig](log)

	pool, err := db.Open(ctx, cfg.DatabaseURL, "logitrack-migrate")
	if err != nil {
		log.Error().Err(err).Msg("database unavailable")
		return app.ExitRuntimeError
	}
	defer pool.Close()
	if err := migrate.RequireMigrator(ctx, pool); err != nil {
		log.Error().Err(err).Msg("wrong database login")
		return app.ExitRuntimeError
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()
	r, err := migrate.New(sqlDB, chain)
	if err != nil {
		log.Error().Err(err).Msg("migration chain rejected")
		return app.ExitRuntimeError
	}

	switch cmd {
	case "status":
		return status(ctx, r, failOnPending, stdout, stderr, log)
	case "version":
		v, err := r.Version(ctx)
		if err != nil {
			log.Error().Err(err).Msg("version failed")
			return app.ExitRuntimeError
		}
		_, _ = fmt.Fprintln(stdout, v)
		return app.ExitOK
	}

	var res []migrate.Result
	switch cmd {
	case "up":
		res, err = r.Up(ctx)
	case "up-to":
		res, err = r.UpTo(ctx, target)
	case "down":
		res, err = r.Down(ctx)
	case "down-to":
		res, err = r.DownTo(ctx, target)
	}
	for _, m := range res {
		log.Info().Int64("migration", m.Version).Str("file", m.Name).Str("direction", m.Direction).
			Dur("duration", m.Duration).Msg("migration done")
	}
	if err != nil {
		log.Error().Err(err).Msg(cmd + " failed")
		return app.ExitRuntimeError
	}
	v, err := r.Version(ctx)
	if err != nil {
		log.Error().Err(err).Msg("version failed")
		return app.ExitRuntimeError
	}
	log.Info().Str("command", cmd).Int("changed", len(res)).Int64("schema_version", v).Msg("schema version")
	return app.ExitOK
}

func status(ctx context.Context, r *migrate.Runner, failOnPending bool, stdout, stderr io.Writer, log zerolog.Logger) int {
	st, err := r.Status(ctx)
	if err != nil {
		log.Error().Err(err).Msg("status failed")
		return app.ExitRuntimeError
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "VERSION\tSTATE\tAPPLIED AT (UTC)\tFILE")
	pending := 0
	for _, s := range st {
		state, at := "applied", s.AppliedAt.Format("2006-01-02 15:04:05")
		if !s.Applied {
			state, at = "pending", "-"
			pending++
		}
		_, _ = fmt.Fprintf(tw, "%04d\t%s\t%s\t%s\n", s.Version, state, at, s.Name)
	}
	_ = tw.Flush()
	if failOnPending && pending > 0 {
		_, _ = fmt.Fprintf(stderr, "migrate: %d migration(s) pending\n", pending)
		return app.ExitRuntimeError
	}
	return app.ExitOK
}

func usageError(stderr io.Writer, msg string) int {
	_, _ = fmt.Fprintf(stderr, "migrate: %s\n\n%s", msg, usage)
	return app.ExitConfigError
}
