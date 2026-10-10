package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl"
)

// cmdAuthImport is `etl auth-import` (T19, main spec §4.11, Appendix C §C.5): the Firebase Auth export joined
// with the Firestore users documents of a dump into users, identities, memberships, scopes, driver links and
// device tokens, plus migration_users_report.csv. The export holds password hashes: it is read from a path the
// operator gives and never copied, printed or logged; stdout carries counts only.
func cmdAuthImport(e *env, args []string) int {
	fl := flag.NewFlagSet("auth-import", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	export := fl.String("export", "", "firebase auth:export JSON file (a secret: hashes and salts)")
	src := fl.String("dump", "", "dump directory or s3:etl/dumps/{ts} with the users (and drivers) collections")
	useFixtures := fl.Bool("fixtures", false, "join the committed fixture dump")
	domain := fl.String("default-member-domain", "", "grant memberships(own_fleet, user) to role-less users of this email domain")
	exportedAt := fl.String("exported-at", "", "RFC 3339 time of the export (disabled_at of disabled accounts; default: the file's modification time)")
	reportPath := fl.String("report", "", "write migration_users_report.csv to this file ('-': stdout)")
	refresh := fl.Bool("refresh-legacy", false, "replace the legacy hash of users that have no Argon2id hash yet (before P7a)")
	dry := fl.Bool("dry-run", false, "write the report, commit nothing")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 || *export == "" {
		return usageError(e.stderr, "auth-import takes --export")
	}
	f, err := os.Open(*export)
	if err != nil {
		_, _ = fmt.Fprintln(e.stderr, "etl: auth-import: the export file cannot be opened")
		return app.ExitConfigError
	}
	st, statErr := f.Stat()
	exp, err := etl.ReadAuthExport(f)
	_ = f.Close()
	if err != nil {
		_, _ = fmt.Fprintf(e.stderr, "etl: %v\n", err)
		return app.ExitConfigError
	}
	at := e.deps.now()
	if statErr == nil {
		at = st.ModTime()
	}
	if *exportedAt != "" {
		if at, err = time.Parse(time.RFC3339Nano, *exportedAt); err != nil {
			return usageError(e.stderr, "--exported-at must be an RFC 3339 instant")
		}
	}
	o := etl.AuthImportOptions{Export: exp, DefaultMemberDomain: *domain, PlatformAdminEmails: e.cfg.AdminEmails,
		ExportedAt: at, RefreshLegacy: *refresh, DryRun: *dry}
	if *src != "" || *useFixtures {
		d, cleanup, code := e.openDump(*src, *useFixtures)
		defer cleanup()
		if code != app.ExitOK {
			return code
		}
		o.Dump = d
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	rep, err := eng.AuthImport(e.ctx, o)
	if err != nil {
		return e.abortCode(err, "auth-import failed")
	}
	outcomes, flags := rep.Counts()
	mode := "committed"
	if *dry {
		mode = "dry run: rolled back"
	}
	_, _ = fmt.Fprintf(e.stdout, "auth-import: %d account(s) (%s): %s; %d device token(s)\n", len(rep.Lines), mode,
		countList(outcomes), rep.DeviceTokens)
	if len(flags) > 0 {
		_, _ = fmt.Fprintf(e.stdout, "flags: %s\n", countList(flags))
	}
	if flags[etl.FlagPlatformAdmin] > 0 {
		_, _ = fmt.Fprintln(e.stdout, "platform_admin is granted by `seed bootstrap-platform-admins`, never by the import")
	}
	if *reportPath != "" {
		if err := writeTo(*reportPath, e.stdout, rep.WriteCSV); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write report")
		}
	}
	return app.ExitOK
}

// cmdAuthWeakScan is `etl auth-weak-scan` (Appendix C §C.5.7): flags (must_change_password) driver accounts
// whose legacy Firebase hash matches a candidate of the operator's file. Candidates and matches are never
// printed: stdout carries counts, the report the flagged accounts per tenant.
func cmdAuthWeakScan(e *env, args []string) int {
	fl := flag.NewFlagSet("auth-weak-scan", flag.ContinueOnError)
	fl.SetOutput(e.stderr)
	candidates := fl.String("candidates-file", "", "candidate passwords: 'key<TAB>password' (key = uid or email) or a bare password for every driver")
	withMobile := fl.Bool("with-mobile", false, "also try each driver's own mobile digits")
	reportPath := fl.String("report", "", "write the per-tenant report (CSV) to this file ('-': stdout)")
	dry := fl.Bool("dry-run", false, "write the report, flag nobody")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 || (*candidates == "" && !*withMobile) {
		return usageError(e.stderr, "auth-weak-scan takes --candidates-file and/or --with-mobile")
	}
	if err := e.cfg.RequireScrypt(); err != nil {
		_, _ = fmt.Fprintf(e.stderr, "etl: %v\n", err)
		return app.ExitConfigError
	}
	var cands []etl.WeakCandidate
	if *candidates != "" {
		f, err := os.Open(*candidates)
		if err != nil {
			_, _ = fmt.Fprintln(e.stderr, "etl: auth-weak-scan: the candidates file cannot be opened")
			return app.ExitConfigError
		}
		cands, err = etl.ReadWeakCandidates(f)
		_ = f.Close()
		if err != nil {
			_, _ = fmt.Fprintf(e.stderr, "%v\n", err)
			return app.ExitConfigError
		}
	}
	eng, closeDB, code := e.engine()
	if code != app.ExitOK {
		return code
	}
	defer closeDB()
	rep, err := eng.WeakScan(e.ctx, etl.WeakScanOptions{Candidates: cands, Params: *e.cfg.Scrypt, WithMobile: *withMobile, DryRun: *dry})
	if err != nil {
		return e.fail(app.ExitRuntimeError, err, "auth-weak-scan failed")
	}
	mode := "committed"
	if *dry {
		mode = "dry run: rolled back"
	}
	_, _ = fmt.Fprintf(e.stdout, "auth-weak-scan: %d driver account(s) (%s): %s\n", len(rep.Lines), mode, countList(rep.Counts()))
	if *candidates != "" {
		_, _ = fmt.Fprintln(e.stdout, "delete the candidates file now (Appendix C §C.5.7)")
	}
	if *reportPath != "" {
		if err := writeTo(*reportPath, e.stdout, rep.WriteCSV); err != nil {
			return e.fail(app.ExitRuntimeError, err, "write report")
		}
	}
	return app.ExitOK
}

func countList(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, m[k])
	}
	return strings.Join(parts, ", ")
}
