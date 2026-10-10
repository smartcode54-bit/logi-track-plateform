// Package migrations embeds the goose chain of Appendix A (developer-spec.md §3.5; R31, R59).
//
// One file per version, NNNN_name.sql, each with "-- +goose Up" and "-- +goose Down". A merged
// file is never edited: changes go into the next number (make migrate-new NAME=...). `migrate
// check` enforces the rules; cmd/migrate applies the files as logitrack_migrator through
// MIGRATE_DATABASE_URL (R66, R87).
package migrations

import "embed"

// FS holds every migration file of the chain.
//
//go:embed *.sql
var FS embed.FS
