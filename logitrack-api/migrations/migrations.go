// Package migrations embeds the goose chain of Appendix A (developer-spec.md §3.5; R31, R59).
//
// One file per version, NNNN_name.sql, each with "-- +goose Up" and "-- +goose Down". Once a file
// is applied to a database that is kept (from the P0 deploy on) it is never edited: changes go into
// the next number (make migrate-new NAME=...). Before P0 the baseline may still be corrected in place
// (0009_infra stays the single GRANT site, R66); goose records versions only, so a local or dev
// database migrated before such an edit does not see it and is reset (make reset), as the README
// says. `migrate check` enforces the rules; cmd/migrate applies the files as logitrack_migrator
// through MIGRATE_DATABASE_URL (R66, R87).
package migrations

import "embed"

// FS holds every migration file of the chain.
//
//go:embed *.sql
var FS embed.FS
