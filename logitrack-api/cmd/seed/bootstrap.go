package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/cmd/seed/internal/seed"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// The platform bootstrap (T19, Appendix C §C.1.6, §C.5.5; owner addition 2026-10-10). Both commands write through
// ETL_DATABASE_URL in one db.WithSystem transaction, run in every APP_ENV (production included: they are how a
// deployment gets its first platform admin) and never print a password: BOOTSTRAP_ADMIN_PASSWORD is hashed and
// compared in memory only.

// openETL connects ETL_DATABASE_URL as logitrack_etl (R87).
func openETL(ctx context.Context, cfg *app.SeedConfig) (*pgxpool.Pool, error) {
	if cfg.ETLDatabaseURL == "" {
		return nil, &config.Error{Missing: []string{"ETL_DATABASE_URL"}}
	}
	pool, err := db.Open(ctx, cfg.ETLDatabaseURL, "logitrack-seed-bootstrap")
	if err != nil {
		return nil, fmt.Errorf("ETL_DATABASE_URL: %w", err)
	}
	if err := seed.RequireLogin(ctx, pool, db.RoleETL, "ETL_DATABASE_URL"); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// bootstrapPlatformAdmins is `seed bootstrap-platform-admins`: platform_admin for every eligible PLATFORM_ADMIN_EMAILS
// user (idempotent; iam.BootstrapPlatformAdmins); addresses without a user, users refused and platform admins
// outside the list are reported, never changed.
func bootstrapPlatformAdmins(ctx context.Context, cfg *app.SeedConfig, log zerolog.Logger, stdout, stderr io.Writer) int {
	if len(cfg.AdminEmails) == 0 {
		_, _ = fmt.Fprintln(stderr, "seed: bootstrap-platform-admins needs PLATFORM_ADMIN_EMAILS")
		return app.ExitConfigError
	}
	pool, err := openETL(ctx, cfg)
	if err != nil {
		log.Error().Err(err).Msg("dependency unavailable")
		return exitUnreachable
	}
	defer pool.Close()
	var res iam.PlatformAdminsResult
	err = db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) (err error) {
		res, err = iam.BootstrapPlatformAdmins(ctx, tx, cfg.AdminEmails, time.Now().UTC().Truncate(time.Microsecond))
		return err
	})
	if err != nil {
		log.Error().Err(err).Msg("bootstrap-platform-admins failed")
		return app.ExitRuntimeError
	}
	_, _ = fmt.Fprintf(stdout, "seed: platform_admin granted %d, already held %d, no user %d, refused %d\n",
		len(res.Granted), len(res.Held), len(res.Missing), len(res.Refused))
	for _, e := range res.Missing {
		_, _ = fmt.Fprintf(stdout, "seed: no user has the address %s (sign-in first, or import it with etl auth-import)\n", e)
	}
	for _, r := range res.Refused {
		_, _ = fmt.Fprintf(stdout, "seed: warning: %s (user %s) not granted: %s; verify the account, then grant through "+
			"POST /v1/users/{id}/platform-roles\n", r.Email, r.UserID, refusalText(r.Reason))
	}
	for _, e := range res.Outside {
		_, _ = fmt.Fprintf(stdout, "seed: warning: %s holds platform_admin but is not in PLATFORM_ADMIN_EMAILS (kept; revoke through the API if unintended)\n", e)
	}
	return app.ExitOK
}

// bootstrapAdmin is `seed --bootstrap-admin`: creates or updates BOOTSTRAP_ADMIN_EMAIL with the Argon2id hash of
// BOOTSTRAP_ADMIN_PASSWORD (at least PASSWORD_MIN_LENGTH, the password policy of Appendix C §C.4.8), without
// must_change_password, and grants platform_admin only when the address is in PLATFORM_ADMIN_EMAILS. Running it
// again with the same password changes nothing.
func bootstrapAdmin(ctx context.Context, cfg *app.SeedConfig, log zerolog.Logger, stdout, stderr io.Writer) int {
	var missing []string
	if strings.TrimSpace(cfg.BootstrapAdminEmail) == "" {
		missing = append(missing, "BOOTSTRAP_ADMIN_EMAIL")
	}
	if cfg.BootstrapAdminPassword == "" {
		missing = append(missing, "BOOTSTRAP_ADMIN_PASSWORD")
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "seed: --bootstrap-admin needs %s\n", strings.Join(missing, ", "))
		return app.ExitConfigError
	}
	emails, bad := iam.ParseEmails(cfg.BootstrapAdminEmail)
	if len(bad) > 0 || len(emails) != 1 {
		_, _ = fmt.Fprintln(stderr, "seed: BOOTSTRAP_ADMIN_EMAIL must be one email address")
		return app.ExitConfigError
	}
	email := emails[0]
	policy, err := password.NewPolicy(cfg.PasswordMinLength)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "seed: PASSWORD_MIN_LENGTH must be between 8 and 128")
		return app.ExitConfigError
	}
	if v := policy.Check(cfg.BootstrapAdminPassword, email); v != nil {
		// The reason only: never the value, never its length.
		_, _ = fmt.Fprintf(stderr, "seed: BOOTSTRAP_ADMIN_PASSWORD is refused by the password policy (%s, at least %d characters)\n",
			v.Reason, cfg.PasswordMinLength)
		return app.ExitConfigError
	}
	hasher, err := password.NewHasher(password.Params{MemoryKB: cfg.Argon2MemoryKB, Iterations: cfg.Argon2Iterations,
		Parallelism: cfg.Argon2Parallelism})
	if err != nil {
		log.Error().Err(err).Msg("password hasher")
		return app.ExitConfigError
	}
	hash, err := hasher.Hash(ctx, cfg.BootstrapAdminPassword)
	if err != nil {
		log.Error().Err(err).Msg("password hash")
		return app.ExitRuntimeError
	}
	pool, err := openETL(ctx, cfg)
	if err != nil {
		log.Error().Err(err).Msg("dependency unavailable")
		return exitUnreachable
	}
	defer pool.Close()
	platform := false
	for _, e := range cfg.AdminEmails {
		platform = platform || e == email
	}
	var res iam.BootstrapAdminResult
	err = db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) (err error) {
		res, err = iam.ApplyBootstrapAdmin(ctx, tx, iam.BootstrapAdmin{
			Email: email, Hash: hash, PlatformAdmin: platform, Now: time.Now().UTC().Truncate(time.Microsecond),
			Matches: func(phc string) (bool, error) {
				ok, _, err := hasher.Verify(ctx, cfg.BootstrapAdminPassword, phc)
				return ok, err
			},
		})
		return err
	})
	if err != nil {
		log.Error().Err(err).Msg("bootstrap-admin failed")
		return app.ExitRuntimeError
	}
	what := "unchanged"
	switch {
	case res.Created:
		what = "created"
	case res.PasswordSet:
		what = "password set, sessions revoked"
	case res.Reactivated:
		what = "made usable again"
	}
	role := "not granted: the address is not in PLATFORM_ADMIN_EMAILS"
	switch {
	case res.RoleGranted:
		role = "granted"
	case res.RoleHeld:
		role = "already held"
	case res.RoleRefused != "":
		role = "refused (warning): " + refusalText(res.RoleRefused) + "; verify the account, then grant through " +
			"POST /v1/users/{id}/platform-roles"
	case res.RoleOutsideList:
		role = "held but the address is not in PLATFORM_ADMIN_EMAILS (warning: kept; revoke through the API if unintended)"
	}
	_, _ = fmt.Fprintf(stdout, "seed: bootstrap admin %s: %s; platform_admin %s\n", email, what, role)
	return app.ExitOK
}

// refusalText explains an iam.Refuse* reason to the operator.
func refusalText(reason string) string {
	switch reason {
	case iam.RefuseAddressUnproven:
		return "its address was not imported from Firebase, set by the bootstrap or set by a platform admin (address_unproven)"
	case iam.RefuseOtherTenant:
		return "it belongs to a tenant other than the own fleet (carrier_membership)"
	case iam.RefuseScope:
		return "it holds a customer scope or a dispatcher grant (scope_holder)"
	}
	return reason
}
