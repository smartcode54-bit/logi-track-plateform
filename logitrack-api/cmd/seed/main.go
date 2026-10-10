// Command seed loads the deterministic seed profiles and checks them (developer-spec.md §14, Appendix D):
// uuid v5 ids, the smoke fixture of §D.4, the generated demo and load identities, placeholder media, and the
// twelve --verify invariants. Writes go through ETL_DATABASE_URL as logitrack_etl inside db.WithSystem,
// --reset and the schema check through MIGRATE_DATABASE_URL, the --verify isolation role-play through
// DATABASE_URL as logitrack_app (R66, R87); nothing uses SET ROLE. Profile loads, --reset and --verify refuse
// APP_ENV=prod; dev needs --allow-shared and loads in upsert mode.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/cmd/seed/internal/seed"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// fixtures is the smoke fixture and the id registry (Appendix D §D.4; the appendix is their review copy and
// these files win on conflict).
//
//go:embed testdata/registry.json testdata/smoke/*.json
var fixtures embed.FS

// sarabun draws the overlay of placeholder images (OFL, assets/OFL.txt).
//
//go:embed assets/Sarabun-Regular.ttf
var sarabun []byte

// Exit codes of --verify (Appendix D §D.1.1): 0 pass, 1 invariant violation, 2 dependency unreachable.
const (
	exitViolation   = app.ExitRuntimeError
	exitUnreachable = app.ExitConfigError
)

const usage = `usage: seed [flags]
       seed --bootstrap-admin
       seed bootstrap-platform-admins

  --profile smoke|demo|load  profile to load or verify (default SEED_PROFILE, else smoke)
  --verify                   read-only: the twelve invariants of Appendix D §D.3 and the count diff;
                             exit 0 pass, 1 violation, 2 dependency unreachable
  --reset[=false]            purge seeded state first (objects, TRUNCATE, lt:{APP_ENV}: keys); the default
                             when APP_ENV=local
  --mode insert|upsert       upsert inserts missing rows only (ON CONFLICT DO NOTHING); dev forces it
  --emit-events              leave the seeded outbox rows unpublished so the relay publishes them
  --dry-run                  build the plan, print per-table counts, write nothing
  --allow-shared             required when APP_ENV=dev (a shared database: upsert, no reset)
  --temporary-password-file PATH
                             write the temporary password of the must-change-password fixture user to PATH
                             (mode 0600) when the load inserts it; it is never printed
  --bootstrap-admin          create or update BOOTSTRAP_ADMIN_EMAIL with BOOTSTRAP_ADMIN_PASSWORD (Argon2id, never
                             printed; at least PASSWORD_MIN_LENGTH); platform_admin only when the address is in
                             PLATFORM_ADMIN_EMAILS; idempotent; also in APP_ENV=prod
  bootstrap-platform-admins  platform_admin for the PLATFORM_ADMIN_EMAILS users (idempotent; also in APP_ENV=prod)

Environment (developer-spec.md §16.1): ETL_DATABASE_URL, MIGRATE_DATABASE_URL, DATABASE_URL (--verify),
REDIS_URL, STORAGE_BACKEND and the S3_* / LOCAL_MEDIA_* names, SEED_PROFILE, SEED_RANDOM_SEED,
SEED_ANCHOR_DATE, SEED_NAMESPACE, SEED_DEFAULT_PASSWORD (secret, never printed), OWN_FLEET_TENANT_ID,
ARGON2_*, FIREBASE_SCRYPT_* (public test parameters locally), PLATFORM_ADMIN_EMAILS, BOOTSTRAP_ADMIN_EMAIL,
BOOTSTRAP_ADMIN_PASSWORD (secret, never printed), PASSWORD_MIN_LENGTH.
`

func main() {
	ctx, stop := app.SignalContext()
	code := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// options are the parsed flags.
type options struct {
	profile                                  string
	verify, reset, resetSet, emitEvents, dry bool
	allowShared                              bool
	mode                                     string
	temporaryPasswordFile                    string
	bootstrapAdmin                           bool
	explicit                                 []string // the flags given on the command line
}

func parseFlags(args []string) (options, error) {
	var o options
	fl := flag.NewFlagSet("seed", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	fl.StringVar(&o.profile, "profile", "", "")
	fl.BoolVar(&o.verify, "verify", false, "")
	fl.BoolVar(&o.reset, "reset", false, "")
	fl.StringVar(&o.mode, "mode", "", "")
	fl.BoolVar(&o.emitEvents, "emit-events", false, "")
	fl.BoolVar(&o.dry, "dry-run", false, "")
	fl.BoolVar(&o.allowShared, "allow-shared", false, "")
	fl.StringVar(&o.temporaryPasswordFile, "temporary-password-file", "", "")
	fl.BoolVar(&o.bootstrapAdmin, "bootstrap-admin", false, "")
	if err := fl.Parse(args); err != nil {
		return o, err
	}
	if fl.NArg() != 0 {
		return o, fmt.Errorf("unexpected argument %q", fl.Arg(0))
	}
	fl.Visit(func(f *flag.Flag) {
		o.explicit = append(o.explicit, f.Name)
		if f.Name == "reset" {
			o.resetSet = true
		}
	})
	if o.bootstrapAdmin {
		for _, f := range o.explicit {
			if f != "bootstrap-admin" {
				return o, fmt.Errorf("--bootstrap-admin runs alone (not with --%s)", f)
			}
		}
	}
	return o, nil
}

func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "-help", "--help", "help":
			_, _ = fmt.Fprint(stdout, usage)
			return app.ExitOK
		case "bootstrap-platform-admins":
			if len(args) > 1 {
				_, _ = fmt.Fprintf(stderr, "seed: bootstrap-platform-admins takes no arguments\n\n%s", usage)
				return app.ExitConfigError
			}
			cfg, log, code := loadConfig(environ, stderr)
			if code != app.ExitOK {
				return code
			}
			return bootstrapPlatformAdmins(ctx, cfg, log, stdout, stderr)
		}
	}
	o, err := parseFlags(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n\n%s", err, usage)
		return app.ExitConfigError
	}
	if o.bootstrapAdmin {
		cfg, log, code := loadConfig(environ, stderr)
		if code != app.ExitOK {
			return code
		}
		return bootstrapAdmin(ctx, cfg, log, stdout, stderr)
	}
	cfg, err := config.LoadFrom[app.SeedConfig](environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return app.ExitConfigError
	}
	profileName := o.profile
	if profileName == "" {
		profileName = cfg.SeedProfile
	}
	if profileName == "" {
		profileName = string(seed.ProfileSmoke)
	}
	profile, err := seed.ParseProfile(profileName)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: --profile: %v\n", err)
		return app.ExitConfigError
	}
	mode, reset, msg := decideMode(cfg.AppEnv, o)
	if msg != "" {
		_, _ = fmt.Fprintf(stderr, "seed: %s\n", msg)
		return app.ExitConfigError
	}
	log, err := app.NewLogger(cfg.Common, "seed", stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return app.ExitConfigError
	}
	app.LogConfig[app.SeedConfig](log)
	fx := seed.FixtureFS{FS: fixtures, Registry: "testdata/registry.json", Smoke: "testdata/smoke"}
	switch {
	case o.dry:
		return dryRun(ctx, cfg, fx, profile, o, stdout, stderr)
	case o.verify:
		return verify(ctx, cfg, fx, profile, log, stdout, stderr)
	}
	return load(ctx, cfg, fx, profile, mode, reset, o.emitEvents, o.temporaryPasswordFile, log, stdout, stderr)
}

// loadConfig reads the configuration and the logger of the bootstrap commands.
func loadConfig(environ []string, stderr io.Writer) (*app.SeedConfig, zerolog.Logger, int) {
	cfg, err := config.LoadFrom[app.SeedConfig](environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return nil, zerolog.Nop(), app.ExitConfigError
	}
	log, err := app.NewLogger(cfg.Common, "seed", stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return nil, zerolog.Nop(), app.ExitConfigError
	}
	app.LogConfig[app.SeedConfig](log)
	return cfg, log, app.ExitOK
}

// decideMode applies the safety guard of Appendix D §D.1.1: prod refuses everything but the bootstrap; dev
// needs --allow-shared and forces upsert without reset; local resets by default.
func decideMode(appEnv string, o options) (seed.Mode, bool, string) {
	if appEnv == "prod" {
		return "", false, "refused: APP_ENV=prod (profile loads, --reset and --verify never run against production)"
	}
	mode := seed.Mode(o.mode)
	switch mode {
	case "":
		mode = seed.ModeInsert
	case seed.ModeInsert, seed.ModeUpsert:
	default:
		return "", false, "--mode must be insert or upsert"
	}
	reset := o.reset
	if appEnv == "dev" {
		if !o.allowShared {
			return "", false, "refused: APP_ENV=dev is a shared database; pass --allow-shared (loads in upsert mode, never resets)"
		}
		if o.resetSet && o.reset {
			return "", false, "refused: --reset on a shared database (APP_ENV=dev)"
		}
		return seed.ModeUpsert, false, ""
	}
	if !o.resetSet {
		reset = appEnv == "local" && mode == seed.ModeInsert
	}
	return mode, reset, ""
}

// buildOptions maps the configuration onto the plan options.
func buildOptions(cfg *app.SeedConfig, profile seed.Profile) seed.Options {
	o := seed.Options{
		Profile: profile, Namespace: cfg.Namespace, OwnFleet: cfg.OwnFleet, RandomSeed: cfg.RandomSeed,
		Anchor: cfg.Anchor, Bucket: cfg.S3Bucket, PublicBucket: cfg.S3PublicBucket, Backend: cfg.StorageBackend,
	}
	if o.RandomSeed == 0 {
		o.RandomSeed = seed.DefaultRandomSeed
	}
	return o
}

func dryRun(ctx context.Context, cfg *app.SeedConfig, fx seed.FixtureFS, profile seed.Profile, o options, stdout, stderr io.Writer) int {
	opts := buildOptions(cfg, profile)
	opts.EmitEvents = o.emitEvents
	p, err := seed.Build(ctx, fx, opts)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return app.ExitRuntimeError
	}
	_, _ = fmt.Fprintf(stdout, "seed --dry-run: profile %s, %d rows in %d tables, %d objects (nothing written)\n",
		profile, p.Count(), len(p.Tables), len(p.Objects))
	tables := make([]string, 0, len(p.Expected))
	for t := range p.Expected {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		_, _ = fmt.Fprintf(stdout, "  %-32s %8d\n", t, p.Expected[t])
	}
	return app.ExitOK
}

// conns are the connections a command opened.
type conns struct {
	etl, migrator, app *pgxpool.Pool
	rdb                *redis.Client
	keys               cache.Keyspace
	backends           seed.Backends
}

func (c *conns) close() {
	for _, p := range []*pgxpool.Pool{c.etl, c.migrator, c.app} {
		if p != nil {
			p.Close()
		}
	}
	if c.rdb != nil {
		_ = c.rdb.Close()
	}
	for _, b := range c.backends {
		if l, ok := b.(*storage.Local); ok {
			_ = l.Close()
		}
	}
}

// open connects what a command needs; every pool is checked against the role its variable stands for.
func open(ctx context.Context, cfg *app.SeedConfig, needMigrator, needApp, needRedis bool) (*conns, error) {
	c := &conns{}
	fail := func(err error) (*conns, error) { c.close(); return nil, err }
	required := map[string]string{"ETL_DATABASE_URL": cfg.ETLDatabaseURL}
	if needMigrator {
		required["MIGRATE_DATABASE_URL"] = cfg.MigrateDatabaseURL
	}
	if needApp {
		required["DATABASE_URL"] = cfg.DatabaseURL
	}
	if needRedis {
		required["REDIS_URL"] = cfg.RedisURL
	}
	var missing []string
	for name, v := range required {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, &config.Error{Missing: missing}
	}
	var err error
	if c.etl, err = db.Open(ctx, cfg.ETLDatabaseURL, "logitrack-seed"); err != nil {
		return fail(fmt.Errorf("ETL_DATABASE_URL: %w", err))
	}
	if err := seed.RequireLogin(ctx, c.etl, db.RoleETL, "ETL_DATABASE_URL"); err != nil {
		return fail(err)
	}
	if needMigrator {
		if c.migrator, err = db.Open(ctx, cfg.MigrateDatabaseURL, "logitrack-seed"); err != nil {
			return fail(fmt.Errorf("MIGRATE_DATABASE_URL: %w", err))
		}
	}
	if needApp {
		if c.app, err = db.Open(ctx, cfg.DatabaseURL, "logitrack-seed-verify"); err != nil {
			return fail(fmt.Errorf("DATABASE_URL: %w", err))
		}
		if err := seed.RequireLogin(ctx, c.app, db.RoleApp, "DATABASE_URL"); err != nil {
			return fail(err)
		}
	}
	if cfg.RedisURL != "" {
		if c.rdb, c.keys, err = cache.Open(cache.Options{URL: cfg.RedisURL, Prefix: cfg.RedisKeyPrefix, AppEnv: cfg.AppEnv}); err != nil {
			return fail(err)
		}
		if err := c.rdb.Ping(ctx).Err(); err != nil {
			return fail(fmt.Errorf("REDIS_URL: %w", err))
		}
	}
	be, err := cfg.Backends()
	if err != nil {
		return fail(err)
	}
	c.backends = be
	active, err := c.backends.For(cfg.StorageBackend)
	if err != nil {
		return fail(err)
	}
	if err := active.Check(ctx); err != nil {
		return fail(fmt.Errorf("object store (%s): %w", cfg.StorageBackend, err))
	}
	return c, nil
}

func load(ctx context.Context, cfg *app.SeedConfig, fx seed.FixtureFS, profile seed.Profile, mode seed.Mode, reset,
	emitEvents bool, temporaryPasswordFile string, log zerolog.Logger, stdout, stderr io.Writer) int {
	start := time.Now()
	if cfg.SeedDefaultPassword == "" {
		_, _ = fmt.Fprintln(stderr, "seed: SEED_DEFAULT_PASSWORD is required to load a profile (make env sets a local one)")
		return app.ExitConfigError
	}
	c, err := open(ctx, cfg, true, false, reset)
	if err != nil {
		log.Error().Err(err).Msg("dependency unavailable")
		return exitUnreachable
	}
	defer c.close()
	if err := seed.SchemaCurrent(ctx, c.migrator, migrations.FS); err != nil {
		log.Error().Err(err).Msg("schema check failed")
		return app.ExitRuntimeError
	}
	hasher, err := password.NewHasher(password.Params{MemoryKB: cfg.Argon2MemoryKB, Iterations: cfg.Argon2Iterations,
		Parallelism: cfg.Argon2Parallelism})
	if err != nil {
		log.Error().Err(err).Msg("password hasher")
		return app.ExitConfigError
	}
	media, err := seed.NewMedia(sarabun)
	if err != nil {
		log.Error().Err(err).Msg("overlay font")
		return app.ExitRuntimeError
	}
	opts := buildOptions(cfg, profile)
	opts.EmitEvents, opts.Materialize, opts.Hasher, opts.DefaultPassword = emitEvents, true, hasher, cfg.SeedDefaultPassword
	opts.Scrypt, opts.Media = cfg.Scrypt, media
	opts.PublicURL = func(o storage.Object) (string, error) {
		b, err := c.backends.For(cfg.StorageBackend)
		if err != nil {
			return "", err
		}
		return b.PublicURL(o)
	}
	plan, err := seed.Build(ctx, fx, opts)
	if err != nil {
		log.Error().Err(err).Msg("plan failed")
		return app.ExitRuntimeError
	}
	for _, w := range plan.Warnings {
		log.Warn().Msg(w)
	}
	var rr seed.ResetResult
	if reset {
		if rr, err = seed.Reset(ctx, c.etl, c.migrator, c.rdb, c.keys.Prefix(), c.backends); err != nil {
			log.Error().Err(err).Msg("reset failed")
			return app.ExitRuntimeError
		}
		log.Info().Int("objects", rr.Objects).Int("tables", rr.Tables).Int("tenants", rr.Tenants).
			Int("redis_keys", rr.RedisKeys).Msg("reset done")
	}
	if !reset {
		if err := seed.CheckNamespace(ctx, c.etl, plan); err != nil {
			log.Error().Err(err).Msg("load refused")
			return app.ExitRuntimeError
		}
	}
	toPut, kept, err := seed.ObjectsToPut(ctx, c.etl, plan.Objects, mode)
	if err != nil {
		log.Error().Err(err).Msg("objects failed")
		return app.ExitRuntimeError
	}
	for _, k := range kept {
		log.Warn().Str("object_key", k).Msg("the existing file_objects row records other bytes than this load draws " +
			"(a toolchain or renderer change): the stored object and its row are kept as they are")
	}
	put, err := seed.PutObjects(ctx, toPut, c.backends)
	if err != nil {
		log.Error().Err(err).Msg("objects failed")
		return app.ExitRuntimeError
	}
	res, err := seed.Load(ctx, c.etl, plan, mode)
	if err != nil {
		log.Error().Err(err).Msg("load failed")
		if reset {
			// After a reset no row references these objects any more: remove them so a failed load leaves
			// nothing behind. An upsert keeps them, since existing rows may point at the same keys.
			if n, derr := seed.RemoveObjects(ctx, plan.Objects, c.backends); derr != nil {
				log.Warn().Err(derr).Int("removed", n).Msg("objects of the failed load not all removed")
			}
		}
		return app.ExitRuntimeError
	}
	rows := 0
	for _, n := range res.Inserted {
		rows += n
	}
	log.Info().Str("profile", string(profile)).Str("mode", string(mode)).Bool("reset", reset).Int("rows", rows).
		Int("objects", put).Int("objects_kept", len(kept)).Int("task_number_counters", res.Counters).
		Dur("load", res.Duration).Dur("total", time.Since(start)).Msg("seed loaded")
	keptNote := ""
	if len(kept) > 0 {
		keptNote = fmt.Sprintf(" (%d existing kept: their rows record other bytes)", len(kept))
	}
	_, _ = fmt.Fprintf(stdout, "seed: profile %s loaded in %s (%s mode%s): %d rows in %d tables, %d objects%s, %d task counters\n",
		profile, time.Since(start).Round(time.Millisecond), mode, resetNote(reset), rows, len(res.Inserted), put, keptNote, res.Counters)
	// The must-change-password fixture user's temporary password (crypto/rand) is never printed: stdout and
	// stderr reach CI logs (owner addition to T19). A local tester who needs it passes --temporary-password-file;
	// the load that inserts the user writes it there with mode 0600. SEED_DEFAULT_PASSWORD is never printed either.
	switch {
	case plan.TemporaryPassword == "":
	case res.TemporaryUserInserted && temporaryPasswordFile != "":
		if err := writeSecretFile(temporaryPasswordFile, plan.TemporaryPassword); err != nil {
			log.Error().Err(err).Msg("--temporary-password-file not written")
			return app.ExitRuntimeError
		}
		_, _ = fmt.Fprintf(stdout, "seed: %s was created with a temporary password (must change at the first sign-in); written to %s (mode 0600)\n",
			plan.TemporaryEmail, temporaryPasswordFile)
	case res.TemporaryUserInserted:
		_, _ = fmt.Fprintf(stdout, "seed: %s was created with a temporary password (must change at the first sign-in); it is not printed: pass --temporary-password-file PATH to keep it\n",
			plan.TemporaryEmail)
	default:
		_, _ = fmt.Fprintf(stdout, "seed: %s already exists: its password is unchanged\n", plan.TemporaryEmail)
	}
	return app.ExitOK
}

// writeSecretFile writes a secret to path with mode 0600 (also when the file existed with a wider mode); the
// value is never echoed.
func writeSecretFile(path, value string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(value + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func resetNote(reset bool) string {
	if reset {
		return ", after --reset"
	}
	return ""
}

func verify(ctx context.Context, cfg *app.SeedConfig, fx seed.FixtureFS, profile seed.Profile, log zerolog.Logger,
	stdout, stderr io.Writer) int {
	c, err := open(ctx, cfg, false, true, true)
	if err != nil {
		log.Error().Err(err).Msg("dependency unreachable")
		return exitUnreachable
	}
	defer c.close()
	plan, err := seed.Build(ctx, fx, buildOptions(cfg, profile))
	if err != nil {
		log.Error().Err(err).Msg("plan failed")
		return app.ExitRuntimeError
	}
	v := &seed.Verifier{ETL: c.etl, App: c.app, Redis: c.rdb, Keys: c.keys, Backends: c.backends, Plan: plan, Log: log}
	rep, err := v.Run(ctx)
	if err != nil {
		log.Error().Err(err).Msg("verify could not run")
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return exitUnreachable
	}
	rep.Write(stdout)
	if !rep.OK() {
		_, _ = fmt.Fprintln(stderr, "seed: --verify found violations")
		return exitViolation
	}
	return app.ExitOK
}
