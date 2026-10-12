// Command etl moves Firestore and Cloud Storage into PostgreSQL 18 and MinIO (developer-spec.md §13, Appendix A
// §A.3): dump, load, reconcile, export-back, media-copy, rewrite-urls, quarantine list|resolve and status; and the
// users ETL of T19 (Appendix C §C.5): auth-import and auth-weak-scan; and the role matrix of T51 (Appendix C §C.2.6):
// auth-rbac. It runs
// as logitrack_etl through ETL_DATABASE_URL (BYPASSRLS, R66) inside db.WithSystem with app.etl_load on, and refuses
// to start without OWN_FLEET_TENANT_ID (R56).
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/objstore"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/webcfg"
)

// fixtures is the committed fixture dump (`--fixtures`, make etl-fixtures): no production data.
//
//go:embed testdata/firestore-fixtures
var fixtures embed.FS

const fixturesDir = "testdata/firestore-fixtures"

const usage = `usage: etl <command> [flags]      (OWN_FLEET_TENANT_ID is required by every command, R56)

Firestore (ETL_FIRESTORE_PROJECT_ID, FIRESTORE_DATABASE_ID, GOOGLE_APPLICATION_CREDENTIALS):
  dump --collections=all|a,b [--groups=mobile_installations,messages] --out=DIR|s3
        read Firestore into NDJSON.gz + manifest.json; s3 writes S3_BUCKET etl/dumps/{ts}/ (R74); all also
        reads every collection reconcile --finance requires, an empty one included

Database (ETL_DATABASE_URL, logged in as logitrack_etl):
  load (--dump=DIR|s3:etl/dumps/{ts} | --fixtures) [--collections=a,b] [--dry-run] [--report=FILE]
       [--since=RFC3339|watermark] [--force]
        upsert by legacy_doc_id, only documents whose _updateTime is newer; --dry-run writes the report and
        commits nothing; --force re-applies unchanged documents (after a mapping change); --since=watermark
        looks back 10 minutes; --fixtures without --dry-run is refused unless APP_ENV=local
  reconcile (--dump=... | --fixtures) [--period=YYYY-MM] [--out=DIR] [--accept-findings=NAME]
       [--finance [--months=3] [--through=YYYY-MM]]
        counts and money against the dump, open findings per reason code against the last matching run (a new
        code or a higher count blocks); exit 2 on a mismatch (etl.reconciliation_runs); --accept-findings records
        the owner's sign-off of the open findings, so a matching run becomes the next baseline; --finance adds the
        billing cut-over checks (T41): rate tables and voids, the D5 fee dedupe, legacy statements (WHT 0.0100),
        counters, fuel snapshots, and the parity dry run of the last --months closed Bangkok months ending at
        --through (default: the month before now; nothing written or locked); --out also writes parity-{ts}.csv
  export-back --collection=customers --frozen-at=RFC3339 [--overwrite-after-freeze] [--out=DIR]
        class A rollback to Firestore in the legacy shape (--out writes the documents to a dump instead)
  media-copy [--limit=N] [--verify]
        GCS ETL_GCS_BUCKET -> S3_BUCKET (the APK: S3_PUBLIC_BUCKET) under the identical key; --verify compares
        size and sha256
  rewrite-urls [--apply]
        list columns that still hold Firebase Storage URLs; exit 2 when any does; --apply first points
        settings(mobile_app).apkDownloadUrl at S3_PUBLIC_BASE_URL once media-copy committed the APK
  mirror --owned=none|all|DOMAINS [--collections=a,b]
        one etl.sync sweep by hand (main spec §13.7; also needs the Firestore names): the collections the
        PostgreSQL-owned domains leave to Firestore, whole, and the 3.x app's fields of the owned ones; --owned is the
        deployment's PG_OWNED_DOMAINS; --collections pins like ETL_MIRROR_COLLECTIONS (default: that variable); exit 2
        when a billable document cannot be applied (R19)
  project --owned=none|all|DOMAINS [--collections=a,b]
        one full firestore.shadow pass (main spec §13.8): every row of the projected collections, only the fields that
        differ, document ids minted (R25), and the documents of holidays deleted in PostgreSQL meanwhile deleted; run it
        before COMPAT_PROJECTION_ENABLED is turned on, or back on
  reconcile-sync --owned=none|all|DOMAINS [--dump=DIR|s3:etl/dumps/{ts}] [--out=DIR] [--accept-findings=NAME]
        the daily coexistence reconciliation (live Firestore, or a dump of it): the mirror caught up, the projected
        documents equivalent to their rows, open findings against the baseline; exit 2 on a mismatch
  quarantine list [--collection=C] [--reason=R] [--all]
  quarantine resolve --collection=C --doc=PATH --action=retry|skip|rehome [--tenant=UUID] [--by=NAME]
  status
        watermarks, lag, outcome counts and open findings per collection

Users (T19, Appendix C §C.5; PLATFORM_ADMIN_EMAILS, FIREBASE_SCRYPT_* for the scan):
  auth-import --export=FILE [--dump=DIR|s3:etl/dumps/{ts} | --fixtures] [--default-member-domain=DOMAIN]
       [--exported-at=RFC3339] [--report=FILE] [--refresh-legacy] [--dry-run]
        firebase auth:export + users documents -> users, identities, memberships, scopes, driver links, device
        tokens; migration_users_report.csv; idempotent (existing uids are skipped); never grants platform_admin
  auth-weak-scan (--candidates-file=FILE | --with-mobile [--dump=DIR|s3:etl/dumps/{ts}]) [--report=FILE] [--dry-run]
        flags every account whose legacy hash matches a candidate (must_change_password); run it before the P0
        cut-over (a Go sign-in replaces the legacy hash); --dump gives --with-mobile the drivers not loaded yet;
        never prints a candidate
  auth-rbac (--dump=DIR|s3:etl/dumps/{ts} | --fixtures) [--actor=EMAIL] [--report=FILE] [--dry-run]
        permissions_config -> own-fleet rows of role_capability_overrides (T51, Appendix C §C.2.6), where the saved
        value differs from the new default; unknown keys -> etl.quarantine unknown_capability_key;
        migration_rbac_report.csv; --actor (default: the first PLATFORM_ADMIN_EMAILS address) is updated_by

Exit codes: 0 ok, 1 runtime error, 2 configuration error, mismatch, refusal or an aborted load (R19).
`

// deps are the outside world; tests replace the Google client.
type deps struct {
	google *http.Client // nil: a default client
	now    func() time.Time
}

func main() {
	ctx, stop := app.SignalContext()
	code := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr, deps{})
	stop()
	os.Exit(code)
}

type env struct {
	ctx    context.Context
	cfg    *app.ETLConfig
	log    zerolog.Logger
	stdout io.Writer
	stderr io.Writer
	deps   deps
}

func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return app.ExitConfigError
	}
	cmd, rest := args[0], args[1:]
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		_, _ = fmt.Fprint(stdout, usage)
		return app.ExitOK
	}
	if cmd == "quarantine" {
		if len(rest) == 0 {
			return usageError(stderr, "quarantine needs list or resolve")
		}
		cmd, rest = "quarantine "+rest[0], rest[1:]
	}
	commands := map[string]func(*env, []string) int{
		"dump": cmdDump, "load": cmdLoad, "reconcile": cmdReconcile, "export-back": cmdExportBack, "media-copy": cmdMediaCopy,
		"rewrite-urls": cmdRewriteURLs, "quarantine list": cmdQuarantineList, "quarantine resolve": cmdQuarantineResolve,
		"status": cmdStatus, "auth-import": cmdAuthImport, "auth-weak-scan": cmdAuthWeakScan, "auth-rbac": cmdAuthRBAC,
		"mirror": cmdMirror, "project": cmdProject, "reconcile-sync": cmdReconcileSync,
	}
	fn, ok := commands[cmd]
	if !ok {
		return usageError(stderr, fmt.Sprintf("unknown command %q", cmd))
	}
	cfg, err := config.LoadFrom[app.ETLConfig](environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "etl: %v\n", err)
		return app.ExitConfigError
	}
	log, err := app.NewLogger(cfg.Common, "etl", stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "etl: %v\n", err)
		return app.ExitConfigError
	}
	app.LogConfig[app.ETLConfig](log)
	if d.now == nil {
		d.now = time.Now
	}
	return fn(&env{ctx: ctx, cfg: cfg, log: log, stdout: stdout, stderr: stderr, deps: d}, rest)
}

func usageError(stderr io.Writer, msg string) int {
	_, _ = fmt.Fprintf(stderr, "etl: %s\n\n%s", msg, usage)
	return app.ExitConfigError
}

func (e *env) fail(code int, err error, msg string) int {
	e.log.Error().Err(err).Msg(msg)
	return code
}

// engine opens the ETL pool and checks the login (R66).
func (e *env) engine() (*etl.Engine, func(), int) {
	if err := e.cfg.RequireDB(); err != nil {
		_, _ = fmt.Fprintf(e.stderr, "etl: %v\n", err)
		return nil, nil, app.ExitConfigError
	}
	pool, err := db.NewPool(e.ctx, e.cfg.DatabaseURL, "logitrack-etl", db.Options{MaxConns: e.cfg.MaxConns, MinConns: e.cfg.MinConns})
	if err != nil {
		return nil, nil, e.fail(app.ExitConfigError, err, "ETL_DATABASE_URL rejected")
	}
	l, err := db.CurrentLogin(e.ctx, pool)
	if err != nil {
		pool.Close()
		return nil, nil, e.fail(app.ExitRuntimeError, err, "database unavailable")
	}
	if l.RoleName != db.RoleETL || l.Superuser {
		pool.Close()
		_, _ = fmt.Fprintf(e.stderr, "etl: runs as %s through ETL_DATABASE_URL (R66), not as %s\n", db.RoleETL, l.RoleName)
		return nil, nil, app.ExitConfigError
	}
	eng, err := etl.New(etl.Config{Tx: systemTx(pool), OwnFleetTenantID: e.cfg.OwnFleet, Bucket: e.cfg.S3Bucket,
		PublicBucket: e.cfg.S3PublicBucket, Log: e.log, Now: e.deps.now})
	if err != nil {
		pool.Close()
		return nil, nil, e.fail(app.ExitConfigError, err, "etl configuration")
	}
	return eng, pool.Close, app.ExitOK
}

// systemTx runs every ETL statement in the system context (Appendix C §C.3.2) with app.etl_load on (db.WithETL), which
// the frozen-tenant, driver-link and file-registry triggers accept from the ETL load path only.
func systemTx(pool *pgxpool.Pool) etl.TxFunc {
	return func(ctx context.Context, fn func(pgx.Tx) error) error {
		return db.WithETL(ctx, pool, fn)
	}
}

func (e *env) googleClient() *http.Client {
	if e.deps.google != nil {
		return e.deps.google
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

func (e *env) firestore() (*gcp.Firestore, int) {
	if err := e.cfg.RequireFirestore(); err != nil {
		_, _ = fmt.Fprintf(e.stderr, "etl: %v\n", err)
		return nil, app.ExitConfigError
	}
	sa, err := firebase.LoadServiceAccount(e.cfg.GoogleCredentials)
	if err != nil {
		return nil, e.fail(app.ExitConfigError, err, "service account")
	}
	f, err := gcp.NewFirestore(e.cfg.FirestoreProjectID, e.cfg.FirestoreDatabaseID, e.googleClient(), sa.TokenSource(e.googleClient(), gcp.FirestoreScope))
	if err != nil {
		return nil, e.fail(app.ExitConfigError, err, "firestore client")
	}
	return f, app.ExitOK
}

func (e *env) store() (*objstore.S3, int) {
	if err := e.cfg.RequireS3(); err != nil {
		_, _ = fmt.Fprintf(e.stderr, "etl: %v\n", err)
		return nil, app.ExitConfigError
	}
	s, err := objstore.New(objstore.Config{Endpoint: e.cfg.S3Endpoint, UseSSL: e.cfg.S3UseSSL, Region: e.cfg.S3Region,
		AccessKeyID: e.cfg.S3AccessKeyID, SecretAccessKey: e.cfg.S3SecretAccessKey, PathStyle: e.cfg.S3UsePathStyle})
	if err != nil {
		return nil, e.fail(app.ExitConfigError, err, "object store")
	}
	return s, app.ExitOK
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// dumpPrefix is the S3 prefix of a dump (R74).
func dumpPrefix(exportedAt time.Time) string {
	return "etl/dumps/" + exportedAt.UTC().Format("20060102T150405Z") + "/"
}

func cmdDump(e *env, args []string) int {
	fl := flag.NewFlagSet("dump", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	colls := fl.String("collections", "", "all, or a comma list of top-level collections")
	groups := fl.String("groups", "mobile_installations,messages", "collection groups (subcollections) read with --collections=all")
	out := fl.String("out", "", "output directory, or s3 for S3_BUCKET etl/dumps/{ts}/")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 || *colls == "" || *out == "" {
		return usageError(e.stderr, "dump takes --collections and --out")
	}
	f, code := e.firestore()
	if code != app.ExitOK {
		return code
	}
	var s3 *objstore.S3
	if *out == "s3" {
		if s3, code = e.store(); code != app.ExitOK {
			return code
		}
	}
	type coll struct {
		name  string
		group bool
	}
	var list []coll
	if *colls == "all" {
		ids, err := f.CollectionIDs(e.ctx)
		if err != nil {
			return e.fail(app.ExitRuntimeError, err, "list collections")
		}
		// Firestore lists no empty collection: the ones `reconcile --finance` requires are always read, so a collection
		// empty at source still has its manifest entry (count 0).
		for _, n := range etl.FinanceCollections {
			if !slices.Contains(ids, n) {
				ids = append(ids, n)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			list = append(list, coll{id, false})
		}
		for _, g := range splitList(*groups) {
			list = append(list, coll{g, true})
		}
	} else {
		for _, c := range splitList(*colls) {
			list = append(list, coll{c, false})
		}
	}
	exportedAt := e.deps.now().UTC().Truncate(time.Second)
	dir := *out
	if s3 != nil {
		tmp, err := os.MkdirTemp("", "etl-dump-")
		if err != nil {
			return e.fail(app.ExitRuntimeError, err, "temp dir")
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		dir = tmp
	}
	w, err := dump.NewWriter(dir, exportedAt, e.cfg.FirestoreProjectID, e.cfg.FirestoreDatabaseID)
	if err != nil {
		return e.fail(app.ExitRuntimeError, err, "dump directory")
	}
	for _, c := range list {
		docs, err := f.Documents(e.ctx, c.name, c.group)
		if err != nil {
			return e.fail(app.ExitRuntimeError, err, "read "+c.name)
		}
		if err := w.WriteCollection(c.name, c.group, docs); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write "+c.name)
		}
		_, _ = fmt.Fprintf(e.stdout, "%s\t%d\n", c.name, len(docs))
	}
	if err := w.Close(); err != nil {
		return e.fail(app.ExitRuntimeError, err, "manifest")
	}
	where := dir
	if s3 != nil {
		prefix := dumpPrefix(exportedAt)
		for _, name := range w.Files() {
			fh, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				return e.fail(app.ExitRuntimeError, err, "read dump file")
			}
			st, _ := fh.Stat()
			ctype := "application/gzip"
			if name == dump.ManifestName {
				ctype = "application/json"
			}
			err = s3.Put(e.ctx, e.cfg.S3Bucket, prefix+name, fh, st.Size(), ctype)
			_ = fh.Close()
			if err != nil {
				return e.fail(app.ExitRuntimeError, err, "upload dump")
			}
		}
		where = "s3:" + prefix
	}
	_, _ = fmt.Fprintf(e.stdout, "dump written: %s\n", where)
	return app.ExitOK
}

// openDump opens --dump (a directory or s3:PREFIX) or the embedded fixtures.
func (e *env) openDump(src string, useFixtures bool) (*dump.Dump, func(), int) {
	noop := func() {}
	switch {
	case useFixtures && src != "":
		return nil, noop, usageError(e.stderr, "--dump and --fixtures are exclusive")
	case useFixtures:
		sub, err := fs.Sub(fixtures, fixturesDir)
		if err != nil {
			return nil, noop, e.fail(app.ExitRuntimeError, err, "fixtures")
		}
		d, err := dump.Open(sub)
		if err != nil {
			return nil, noop, e.fail(app.ExitRuntimeError, err, "fixtures")
		}
		return d, noop, app.ExitOK
	case src == "":
		return nil, noop, usageError(e.stderr, "--dump or --fixtures is required")
	case strings.HasPrefix(src, "s3:"):
		s3, code := e.store()
		if code != app.ExitOK {
			return nil, noop, code
		}
		prefix := strings.TrimSuffix(strings.TrimPrefix(src, "s3:"), "/") + "/"
		if !strings.HasPrefix(prefix, "etl/dumps/") {
			return nil, noop, usageError(e.stderr, "an s3 dump lives under etl/dumps/")
		}
		tmp, err := os.MkdirTemp("", "etl-dump-")
		if err != nil {
			return nil, noop, e.fail(app.ExitRuntimeError, err, "temp dir")
		}
		cleanup := func() { _ = os.RemoveAll(tmp) }
		keys, err := s3.List(e.ctx, e.cfg.S3Bucket, prefix)
		if err != nil {
			cleanup()
			return nil, noop, e.fail(app.ExitRuntimeError, err, "list dump")
		}
		for _, k := range keys {
			name := path.Base(k)
			if k != prefix+name {
				continue
			}
			if err := download(e.ctx, s3, e.cfg.S3Bucket, k, filepath.Join(tmp, name)); err != nil {
				cleanup()
				return nil, noop, e.fail(app.ExitRuntimeError, err, "download dump")
			}
		}
		d, err := dump.OpenDir(tmp)
		if err != nil {
			cleanup()
			return nil, noop, e.fail(app.ExitRuntimeError, err, "open dump")
		}
		return d, cleanup, app.ExitOK
	default:
		d, err := dump.OpenDir(src)
		if err != nil {
			return nil, noop, e.fail(app.ExitRuntimeError, err, "open dump")
		}
		return d, noop, app.ExitOK
	}
}

func download(ctx context.Context, s3 *objstore.S3, bucket, key, to string) error {
	rc, err := s3.Get(ctx, bucket, key)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	f, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, rc); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// abortCode maps a run error to its exit code: an aborted load (R19) is 2, anything else 1.
func (e *env) abortCode(err error, msg string) int {
	if ae, ok := errors.AsType[*etl.AbortError](err); ok {
		_, _ = fmt.Fprintf(e.stderr, "etl: %v\n", ae)
		return app.ExitConfigError
	}
	return e.fail(app.ExitRuntimeError, err, msg)
}

func cmdLoad(e *env, args []string) int {
	fl := flag.NewFlagSet("load", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	src := fl.String("dump", "", "dump directory or s3:etl/dumps/{ts}")
	useFixtures := fl.Bool("fixtures", false, "load the committed fixture dump")
	colls := fl.String("collections", "", "comma list (default: every collection of the dump)")
	dry := fl.Bool("dry-run", false, "write the report, commit nothing")
	reportPath := fl.String("report", "", "write the quarantine report to this file ('-': stdout)")
	since := fl.String("since", "", "RFC 3339 instant, or watermark")
	force := fl.Bool("force", false, "re-apply documents whose _updateTime is not newer")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return usageError(e.stderr, "load: bad flags")
	}
	// The fixture dump ships in every etl binary: committing its synthetic carriers, tasks and billable trips is
	// for a developer's own database only (no delete path would take them out of a shared one again).
	if *useFixtures && !*dry && e.cfg.AppEnv != "local" {
		_, _ = fmt.Fprintf(e.stderr, "etl: load --fixtures commits synthetic documents; refused when APP_ENV=%s (use --dry-run, or APP_ENV=local)\n", e.cfg.AppEnv)
		return app.ExitConfigError
	}
	o := etl.LoadOptions{Collections: splitList(*colls), DryRun: *dry, Force: *force}
	switch *since {
	case "":
	case "watermark":
		o.SinceWatermark = true
	default:
		t, err := time.Parse(time.RFC3339Nano, *since)
		if err != nil {
			return usageError(e.stderr, "--since must be an RFC 3339 instant or watermark")
		}
		o.Since = t
	}
	d, cleanup, code := e.openDump(*src, *useFixtures)
	defer cleanup()
	if code != app.ExitOK {
		return code
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	rep, err := eng.Load(e.ctx, d, o)
	if err != nil {
		return e.abortCode(err, "load failed")
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "collection\tdocs\tunchanged\tbefore-mark\tloaded\tquarantined\trejected\tdropped\tpending")
	for _, c := range rep.Collections {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n", c.Name, c.Total, c.Unchanged, c.BeforeMark, c.Outcomes[etl.Loaded],
			c.Outcomes[etl.Quarantined], c.Outcomes[etl.Rejected], c.Outcomes[etl.Dropped], c.Outcomes[etl.Pending])
	}
	_ = tw.Flush()
	mode := "committed"
	if *dry {
		mode = "dry run: rolled back"
	}
	_, _ = fmt.Fprintf(e.stdout, "%d document(s) applied (%s)\n", rep.Applied(), mode)
	if *reportPath != "" {
		if err := writeTo(*reportPath, e.stdout, rep.Sections().Write); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write report")
		}
	}
	return app.ExitOK
}

func writeTo(p string, stdout io.Writer, write func(io.Writer) error) error {
	if p == "-" {
		return write(stdout)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func cmdReconcile(e *env, args []string) int {
	fl := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	src := fl.String("dump", "", "dump directory or s3:etl/dumps/{ts}")
	useFixtures := fl.Bool("fixtures", false, "reconcile against the committed fixture dump")
	period := fl.String("period", "", "YYYY-MM (Asia/Bangkok) for the money checks")
	out := fl.String("out", "", "directory for reconcile-{ts}.md and .csv")
	accept := fl.String("accept-findings", "", "owner name: accept the open findings (new codes and deltas) as the next baseline")
	finance := fl.Bool("finance", false, "add the billing cut-over checks and the parity dry run (T41)")
	months := fl.Int("months", 3, "--finance: closed Bangkok months the parity dry run recomputes")
	through := fl.String("through", "", "--finance: the last of them, YYYY-MM (default: the month before now)")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return usageError(e.stderr, "reconcile: bad flags")
	}
	if !*finance && (*through != "" || *months != 3) {
		return usageError(e.stderr, "reconcile: --months and --through go with --finance")
	}
	if *finance && (*months < 1 || *months > 24) {
		return usageError(e.stderr, "reconcile: --months must be 1-24")
	}
	d, cleanup, code := e.openDump(*src, *useFixtures)
	defer cleanup()
	if code != app.ExitOK {
		return code
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	rec, err := eng.Reconcile(e.ctx, d, etl.ReconcileOptions{Period: *period, AcceptFindingsBy: *accept, Finance: *finance,
		Months: *months, Through: *through})
	if err != nil {
		if errors.Is(err, etl.ErrBadWindow) {
			_, _ = fmt.Fprintf(e.stderr, "etl: %v\n", err)
			return app.ExitConfigError
		}
		return e.fail(app.ExitRuntimeError, err, "reconcile failed")
	}
	if *out != "" {
		if err := os.MkdirAll(*out, 0o750); err != nil {
			return e.fail(app.ExitRuntimeError, err, "report directory")
		}
		base := filepath.Join(*out, "reconcile-"+e.deps.now().UTC().Format("20060102T150405Z"))
		if err := writeTo(base+".md", e.stdout, rec.WriteMarkdown); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write report")
		}
		if err := writeTo(base+".csv", e.stdout, rec.WriteCSV); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write report")
		}
		if rec.Finance != nil {
			parity := filepath.Join(*out, "parity-"+e.deps.now().UTC().Format("20060102T150405Z")+".csv")
			if err := writeTo(parity, e.stdout, rec.WriteParityCSV); err != nil {
				return e.fail(app.ExitRuntimeError, err, "write parity report")
			}
		}
	}
	if rec.Finance != nil {
		n := map[string]int{}
		for _, p := range rec.Finance.Parity {
			n[p.Outcome]++
		}
		_, _ = fmt.Fprintf(e.stdout, "parity %s: %d recomputed, %d exact, %d allow-listed, %d mismatched\n",
			strings.Join(rec.Finance.Window.Months, " "), len(rec.Finance.Parity), n[etl.ParityExact], n[etl.ParityAllowed], n[etl.ParityMismatch])
	}
	for _, c := range rec.Mismatches() {
		_, _ = fmt.Fprintf(e.stdout, "MISMATCH\t%s\t%s\tsource %s\ttarget %s\t%s\n", c.Name, c.Key, c.Source, c.Target, c.Note)
	}
	if !rec.Match {
		_, _ = fmt.Fprintf(e.stdout, "reconcile: %d mismatch(es)\n", len(rec.Mismatches()))
		return app.ExitConfigError
	}
	_, _ = fmt.Fprintf(e.stdout, "reconcile: match (%d checks)\n", len(rec.Checks))
	return app.ExitOK
}

func cmdExportBack(e *env, args []string) int {
	fl := flag.NewFlagSet("export-back", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	coll := fl.String("collection", "", "class A collection: "+strings.Join(etl.ExportCollections(), ", "))
	frozen := fl.String("frozen-at", "", "RFC 3339 instant of the class A freeze")
	overwrite := fl.Bool("overwrite-after-freeze", false, "also overwrite documents changed in Firestore after the freeze")
	out := fl.String("out", "", "write the encoded documents to this dump directory instead of Firestore")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 || *coll == "" {
		return usageError(e.stderr, "export-back takes --collection and --frozen-at")
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	if *out != "" {
		docs, err := eng.Encode(e.ctx, *coll)
		if err != nil {
			return e.fail(app.ExitConfigError, err, "encode")
		}
		w, err := dump.NewWriter(*out, e.deps.now(), "", "")
		if err != nil {
			return e.fail(app.ExitRuntimeError, err, "output directory")
		}
		var dd []dump.Doc
		for _, x := range docs {
			id := path.Base(x.Path)
			dd = append(dd, dump.Doc{ID: id, Path: x.Path, UpdateTime: e.deps.now().UTC(), Fields: x.Fields})
		}
		if err := w.WriteCollection(*coll, false, dd); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write")
		}
		if err := w.Close(); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write")
		}
		_, _ = fmt.Fprintf(e.stdout, "export-back: %d document(s) encoded into %s (nothing written to Firestore)\n", len(dd), *out)
		return app.ExitOK
	}
	t, err := time.Parse(time.RFC3339Nano, *frozen)
	if err != nil {
		return usageError(e.stderr, "--frozen-at must be an RFC 3339 instant")
	}
	f, code := e.firestore()
	if code != app.ExitOK {
		return code
	}
	res, err := eng.ExportBack(e.ctx, f, etl.ExportOptions{Collection: *coll, FrozenAt: t, OverwriteAfterFreeze: *overwrite})
	if res != nil {
		_, _ = fmt.Fprintf(e.stdout, "export-back: %d written, %d refused\n", len(res.Written), len(res.Refused))
		for _, p := range res.Refused {
			_, _ = fmt.Fprintf(e.stdout, "REFUSED\t%s\tchanged in Firestore after the freeze; rerun with --overwrite-after-freeze to replace it\n", p)
		}
	}
	if err != nil {
		return e.fail(app.ExitRuntimeError, err, "export-back failed")
	}
	if len(res.Refused) > 0 {
		return app.ExitConfigError
	}
	return app.ExitOK
}

func cmdMediaCopy(e *env, args []string) int {
	fl := flag.NewFlagSet("media-copy", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	limit := fl.Int("limit", 0, "copy at most N objects (0: all)")
	verify := fl.Bool("verify", false, "compare committed copies with the store (size, sha256)")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 || *limit < 0 {
		return usageError(e.stderr, "media-copy: bad flags")
	}
	s3, code := e.store()
	if code != app.ExitOK {
		return code
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	if *verify {
		res, err := eng.MediaVerify(e.ctx, s3, *limit)
		if err != nil {
			return e.fail(app.ExitRuntimeError, err, "verify failed")
		}
		for _, k := range res.MismatchKeys {
			_, _ = fmt.Fprintf(e.stdout, "MISMATCH\t%s\n", k)
		}
		_, _ = fmt.Fprintf(e.stdout, "media-copy --verify: %d ok, %d mismatched\n", res.Copied, res.Mismatched)
		if res.Mismatched > 0 {
			return app.ExitConfigError
		}
		return app.ExitOK
	}
	if err := e.cfg.RequireMedia(); err != nil {
		_, _ = fmt.Fprintf(e.stderr, "etl: %v\n", err)
		return app.ExitConfigError
	}
	sa, err := firebase.LoadServiceAccount(e.cfg.GoogleCredentials)
	if err != nil {
		return e.fail(app.ExitConfigError, err, "service account")
	}
	g, err := gcp.NewGCS(e.googleClient(), sa.TokenSource(e.googleClient(), gcp.StorageReadScope))
	if err != nil {
		return e.fail(app.ExitConfigError, err, "storage client")
	}
	res, err := eng.MediaCopy(e.ctx, g, s3, e.cfg.GCSBucket, *limit)
	if res != nil {
		_, _ = fmt.Fprintf(e.stdout, "media-copy: %d copied, %d missing at source\n", res.Copied, res.Missing)
	}
	if err != nil {
		return e.fail(app.ExitRuntimeError, err, "media-copy failed")
	}
	return app.ExitOK
}

func cmdRewriteURLs(e *env, args []string) int {
	fl := flag.NewFlagSet("rewrite-urls", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	apply := fl.Bool("apply", false, "point settings(mobile_app).apkDownloadUrl at S3_PUBLIC_BASE_URL once the APK is committed")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return usageError(e.stderr, "rewrite-urls takes only --apply")
	}
	if *apply && e.cfg.S3PublicBaseURL == "" {
		_, _ = fmt.Fprintln(e.stderr, "etl: rewrite-urls --apply needs S3_PUBLIC_BASE_URL")
		return app.ExitConfigError
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	if *apply {
		res, err := eng.ApplyURLRewrites(e.ctx, e.cfg.S3PublicBaseURL)
		if err != nil {
			return e.fail(app.ExitRuntimeError, err, "rewrite-urls --apply failed")
		}
		for _, p := range res.Pending {
			_, _ = fmt.Fprintf(e.stdout, "PENDING\t%s (not committed yet: run media-copy)\n", p)
		}
		_, _ = fmt.Fprintf(e.stdout, "rewrite-urls --apply: %d rewritten\n", res.Rewritten)
	}
	hits, err := eng.RewriteURLs(e.ctx)
	if err != nil {
		return e.fail(app.ExitRuntimeError, err, "rewrite-urls failed")
	}
	for _, h := range hits {
		_, _ = fmt.Fprintf(e.stdout, "UNMAPPED\t%s.%s\t%d row(s)\n", h.Table, h.Column, h.Rows)
	}
	if len(hits) > 0 {
		return app.ExitConfigError
	}
	_, _ = fmt.Fprintln(e.stdout, "rewrite-urls: no Firebase Storage URL outside file_objects.legacy_url")
	return app.ExitOK
}

func cmdQuarantineList(e *env, args []string) int {
	fl := flag.NewFlagSet("quarantine list", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	coll := fl.String("collection", "", "only this collection")
	reason := fl.String("reason", "", "only this reason code")
	all := fl.Bool("all", false, "include resolved findings")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return usageError(e.stderr, "quarantine list: bad flags")
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	rows, err := eng.ListFindings(e.ctx, etl.FindingFilter{Collection: *coll, Reason: *reason, OpenOnly: !*all})
	if err != nil {
		return e.fail(app.ExitRuntimeError, err, "list failed")
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "id\tcollection\tdoc_path\tfield\treason_code\tresolution\tdetail")
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Collection, r.DocPath, orDash(r.Field), r.Reason, orDash(r.Resolution), r.Detail)
	}
	_ = tw.Flush()
	return app.ExitOK
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdQuarantineResolve(e *env, args []string) int {
	fl := flag.NewFlagSet("quarantine resolve", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	coll := fl.String("collection", "", "collection of the document")
	doc := fl.String("doc", "", "document path (collection/id)")
	action := fl.String("action", "", "retry, skip or rehome")
	tenant := fl.String("tenant", "", "rehome: the target tenant id")
	by := fl.String("by", "etl-cli", "operator label stored with the resolution")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 || *coll == "" || *doc == "" || *action == "" {
		return usageError(e.stderr, "quarantine resolve takes --collection, --doc and --action")
	}
	o := etl.ResolveOptions{Collection: *coll, DocPath: *doc, Action: *action, By: *by}
	if *action == etl.ActionRehome {
		t, err := uuid.Parse(*tenant)
		if err != nil {
			return usageError(e.stderr, "--action=rehome needs --tenant=<uuid>")
		}
		o.Tenant = t
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	res, err := eng.Resolve(e.ctx, o)
	if err != nil {
		if errors.Is(err, etl.ErrNoDocument) {
			_, _ = fmt.Fprintf(e.stderr, "etl: %v\n", err)
			return app.ExitConfigError
		}
		return e.abortCode(err, "resolve failed")
	}
	_, _ = fmt.Fprintf(e.stdout, "quarantine resolve: %d finding(s) resolved", res.Resolved)
	if res.Outcome != "" {
		_, _ = fmt.Fprintf(e.stdout, ", outcome %s", res.Outcome)
	}
	if len(res.Moved) > 0 {
		files := 0
		for _, m := range res.Moved {
			if m.Table == tenancy.FileObjects {
				files++
			}
		}
		_, _ = fmt.Fprintf(e.stdout, ", %d row(s) and %d file(s) re-homed", len(res.Moved)-files, files)
	}
	_, _ = fmt.Fprintln(e.stdout)
	return app.ExitOK
}

func cmdStatus(e *env, args []string) int {
	if len(args) != 0 {
		return usageError(e.stderr, "status takes no arguments")
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	st, err := eng.Status(e.ctx)
	if err != nil {
		return e.fail(app.ExitRuntimeError, err, "status failed")
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "collection\twatermark\tlag\tloaded\tquarantined\trejected\tdropped\tpending\topen findings")
	for _, s := range st {
		mark, lag := "-", "-"
		if s.Watermark != nil {
			mark, lag = s.Watermark.UTC().Format(time.RFC3339), s.Lag.Truncate(time.Second).String()
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\n", s.Collection, mark, lag, s.Counts["loaded"], s.Counts["quarantined"],
			s.Counts["rejected"], s.Counts["dropped"], s.Counts["pending"], s.OpenFindings)
	}
	_ = tw.Flush()
	return app.ExitOK
}

// ownedDomains parses --owned: the deployment's PG_OWNED_DOMAINS ("none" for an empty value, "all", or a comma list).
// It is required: a sweep or a projection planned for the wrong owner would write the other side.
func ownedDomains(v string) (map[string]bool, error) {
	if v == "" {
		return nil, errors.New("--owned is required (the deployment's PG_OWNED_DOMAINS: none, all or a comma list)")
	}
	if v == "none" {
		v = ""
	}
	list, err := webcfg.ParseOwned(v)
	if err != nil {
		return nil, fmt.Errorf("--owned: %w", err)
	}
	out := map[string]bool{}
	for _, d := range list {
		out[d] = true
	}
	return out, nil
}

func cmdMirror(e *env, args []string) int {
	fl := flag.NewFlagSet("mirror", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	ownedFl := fl.String("owned", "", "the deployment's PG_OWNED_DOMAINS: none, all or a comma list")
	colls := fl.String("collections", e.cfg.MirrorCollections, "pin: a comma list of collections (default ETL_MIRROR_COLLECTIONS)")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return usageError(e.stderr, "mirror: bad flags")
	}
	owned, err := ownedDomains(*ownedFl)
	if err != nil {
		return usageError(e.stderr, err.Error())
	}
	plan, err := etl.PlanMirror(owned, splitList(*colls))
	if err != nil {
		return usageError(e.stderr, err.Error())
	}
	f, code := e.firestore()
	if code != app.ExitOK {
		return code
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	rep, err := eng.Mirror(e.ctx, f, plan)
	if err != nil {
		return e.abortCode(err, "mirror failed")
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "collection\tmode\tread\tunchanged\tbefore watermark\tapplied\theld")
	held := 0
	for _, c := range rep.Collections {
		n := 0
		for _, v := range c.Outcomes {
			n += v
		}
		held += c.Held
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\n", c.Name, c.Mode, c.Total, c.Unchanged, c.BeforeMark, n, c.Held)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(e.stdout, "mirror: %d document(s) applied\n", rep.Applied())
	if held > 0 {
		// A billable document the sweep could not apply (R19) is held, the rest committed: name it for the fix at source.
		for _, d := range rep.Docs {
			if d.Outcome == etl.Pending && len(d.Findings) > 0 {
				_, _ = fmt.Fprintf(e.stderr, "etl: held %s (%s): fix it at the source; every sweep retries it\n", d.Path, d.Findings[0].Reason)
			}
		}
		return app.ExitConfigError
	}
	return app.ExitOK
}

func cmdProject(e *env, args []string) int {
	fl := flag.NewFlagSet("project", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	ownedFl := fl.String("owned", "", "the deployment's PG_OWNED_DOMAINS: none, all or a comma list")
	colls := fl.String("collections", "", "pin: a comma list of projected collections")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return usageError(e.stderr, "project: bad flags")
	}
	owned, err := ownedDomains(*ownedFl)
	if err != nil {
		return usageError(e.stderr, err.Error())
	}
	plan, err := etl.PlanProjection(owned, splitList(*colls))
	if err != nil {
		return usageError(e.stderr, err.Error())
	}
	if len(plan) == 0 {
		_, _ = fmt.Fprintln(e.stdout, "project: nothing to project (no projected collection is PostgreSQL-owned under --owned)")
		return app.ExitOK
	}
	f, code := e.firestore()
	if code != app.ExitOK {
		return code
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	stats, err := eng.Projector(f, plan, owned).ProjectAll(e.ctx)
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "collection\twritten\tunchanged\tskipped\tdeleted")
	var total etl.ProjectStats
	for _, c := range plan {
		st, ok := stats[c]
		if !ok {
			continue
		}
		total.Written += st.Written
		total.Deleted += st.Deleted
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\n", c, st.Written, st.Unchanged, st.Skipped, st.Deleted)
	}
	_ = tw.Flush()
	if err != nil {
		return e.fail(app.ExitRuntimeError, err, "project failed")
	}
	_, _ = fmt.Fprintf(e.stdout, "project: %d document(s) written, %d deleted (the documents of rows deleted in PostgreSQL)\n", total.Written, total.Deleted)
	return app.ExitOK
}

func cmdReconcileSync(e *env, args []string) int {
	fl := flag.NewFlagSet("reconcile-sync", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	ownedFl := fl.String("owned", "", "the deployment's PG_OWNED_DOMAINS: none, all or a comma list")
	src := fl.String("dump", "", "compare with a dump instead of the live Firestore")
	out := fl.String("out", "", "directory for reconcile-sync-{ts}.md and .csv")
	accept := fl.String("accept-findings", "", "owner name: accept the open findings (new codes and deltas) as the next baseline")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		return usageError(e.stderr, "reconcile-sync: bad flags")
	}
	owned, err := ownedDomains(*ownedFl)
	if err != nil {
		return usageError(e.stderr, err.Error())
	}
	mirror, err := etl.PlanMirror(owned, splitList(e.cfg.MirrorCollections))
	if err != nil {
		return usageError(e.stderr, "ETL_MIRROR_COLLECTIONS: "+err.Error())
	}
	projection, _ := etl.PlanProjection(owned, nil)
	var source etl.DocSource
	if *src != "" {
		d, cleanup, code := e.openDump(*src, false)
		defer cleanup()
		if code != app.ExitOK {
			return code
		}
		source = etl.DumpSource{Dump: d}
	} else {
		f, code := e.firestore()
		if code != app.ExitOK {
			return code
		}
		source = f
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	rec, err := eng.ReconcileSync(e.ctx, source, etl.SyncReconcileOptions{Mirror: mirror, Projection: projection, AcceptFindingsBy: *accept})
	if err != nil {
		return e.fail(app.ExitRuntimeError, err, "reconcile-sync failed")
	}
	if *out != "" {
		if err := os.MkdirAll(*out, 0o750); err != nil {
			return e.fail(app.ExitRuntimeError, err, "report directory")
		}
		base := filepath.Join(*out, "reconcile-sync-"+e.deps.now().UTC().Format("20060102T150405Z"))
		if err := writeTo(base+".md", e.stdout, rec.WriteMarkdown); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write report")
		}
		if err := writeTo(base+".csv", e.stdout, rec.WriteCSV); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write report")
		}
	}
	for _, c := range rec.Mismatches() {
		_, _ = fmt.Fprintf(e.stdout, "MISMATCH\t%s\t%s\tsource %s\ttarget %s\t%s\n", c.Name, c.Key, c.Source, c.Target, c.Note)
	}
	if !rec.Match {
		_, _ = fmt.Fprintf(e.stdout, "reconcile-sync: %d mismatch(es)\n", len(rec.Mismatches()))
		return app.ExitConfigError
	}
	_, _ = fmt.Fprintf(e.stdout, "reconcile-sync: match (%d checks)\n", len(rec.Checks))
	return app.ExitOK
}
