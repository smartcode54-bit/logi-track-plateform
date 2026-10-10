package seed

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Backends resolves the storage backend of a file_objects row (its storage_backend column, migration 0011).
type Backends map[string]storage.Backend

// For returns the backend named by a row.
func (b Backends) For(name string) (storage.Backend, error) {
	be, ok := b[name]
	if !ok || be == nil {
		return nil, fmt.Errorf("seed: storage backend %q is not configured", name)
	}
	return be, nil
}

// RequireLogin checks that a pool logs in as role (R66, R87): the seed writes only as logitrack_etl, resets
// only as logitrack_migrator and role-plays only as logitrack_app, never through SET ROLE.
func RequireLogin(ctx context.Context, pool *pgxpool.Pool, role, variable string) error {
	l, err := db.CurrentLogin(ctx, pool)
	if err != nil {
		return err
	}
	if l.RoleName != role || l.Superuser {
		return fmt.Errorf("seed: %s must log in as %s (R87), not as %s", variable, role, l.RoleName)
	}
	return nil
}

// SchemaCurrent checks through MIGRATE_DATABASE_URL that every embedded migration is applied: the fixture
// follows the full chain (dev, CI and seeded databases apply it at once, §3.5).
func SchemaCurrent(ctx context.Context, migrator *pgxpool.Pool, chain fs.FS) error {
	if err := migrate.RequireMigrator(ctx, migrator); err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(migrator)
	defer func() { _ = sqlDB.Close() }()
	r, err := migrate.New(sqlDB, chain)
	if err != nil {
		return err
	}
	st, err := r.Status(ctx)
	if err != nil {
		return err
	}
	var pending []string
	for _, s := range st {
		if !s.Applied {
			pending = append(pending, s.Name)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("seed: the schema is not current, %d migration(s) pending (%s): run make migrate first",
			len(pending), strings.Join(pending, ", "))
	}
	return nil
}

// ResetResult reports a reset.
type ResetResult struct {
	Objects   int // objects removed from the store
	Tables    int // tables truncated
	Tenants   int // tenant rows deleted (the quarantine row survives)
	RedisKeys int // keys unlinked under lt:{APP_ENV}:
}

// Reset purges seeded state before a load (Appendix D §D.1.7): (1) every object listed in file_objects is
// removed from its backend; (2) through MIGRATE_DATABASE_URL every public table except tenants and goose's
// goose_db_version, and the etl tables, are truncated in one statement with RESTART IDENTITY CASCADE (the
// ETL login holds no TRUNCATE), then through ETL_DATABASE_URL every tenant but the quarantine row of
// migration 0002 is deleted; (3) Redis keys under the prefix are unlinked with SCAN, never FLUSHDB.
func Reset(ctx context.Context, etl, migrator *pgxpool.Pool, rdb redis.UniversalClient, prefix string, be Backends) (ResetResult, error) {
	var res ResetResult
	type obj struct{ bucket, key, backend string }
	var objs []obj
	err := db.WithSystem(ctx, etl, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT bucket, object_key, storage_backend FROM file_objects WHERE status <> 'missing_at_source'`)
		if err != nil {
			return err
		}
		return scanAll(rows, func(r pgx.Rows) error {
			var o obj
			if err := r.Scan(&o.bucket, &o.key, &o.backend); err != nil {
				return err
			}
			objs = append(objs, o)
			return nil
		})
	})
	if err != nil {
		return res, fmt.Errorf("seed: reset: list objects: %w", err)
	}
	for _, o := range objs {
		b, err := be.For(o.backend)
		if err != nil {
			return res, err
		}
		if err := b.Delete(ctx, storage.Object{Bucket: o.bucket, Key: o.key}); err != nil {
			return res, fmt.Errorf("seed: reset: remove %s: %w", o.key, err)
		}
		res.Objects++
	}
	var tables []string
	rows, err := migrator.Query(ctx, `SELECT format('%I.%I', schemaname, tablename) FROM pg_tables
		WHERE schemaname IN ('public', 'etl')
		  AND NOT (schemaname = 'public' AND tablename IN ('tenants', 'goose_db_version'))
		ORDER BY schemaname, tablename`)
	if err != nil {
		return res, fmt.Errorf("seed: reset: list tables: %w", err)
	}
	if err := scanAll(rows, func(r pgx.Rows) error {
		var t string
		if err := r.Scan(&t); err != nil {
			return err
		}
		tables = append(tables, t)
		return nil
	}); err != nil {
		return res, fmt.Errorf("seed: reset: list tables: %w", err)
	}
	if len(tables) == 0 {
		return res, errors.New("seed: reset: no table to truncate: run make migrate first")
	}
	if _, err := migrator.Exec(ctx, "TRUNCATE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
		return res, fmt.Errorf("seed: reset: truncate: %w", err)
	}
	res.Tables = len(tables)
	err = db.WithSystem(ctx, etl, nil, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM tenants WHERE id <> app_quarantine_tenant_id()`)
		res.Tenants = int(tag.RowsAffected())
		return err
	})
	if err != nil {
		return res, fmt.Errorf("seed: reset: delete tenants: %w", err)
	}
	n, err := PurgeRedis(ctx, rdb, prefix)
	res.RedisKeys = n
	if err != nil {
		return res, err
	}
	return res, nil
}

// PurgeRedis unlinks every key under prefix (lt:{APP_ENV}:, R26) with SCAN + UNLINK. FLUSHDB is never used:
// the Redis may serve other environments or services.
func PurgeRedis(ctx context.Context, rdb redis.UniversalClient, prefix string) (int, error) {
	if rdb == nil {
		return 0, nil
	}
	if prefix == "" || !strings.HasPrefix(prefix, "lt:") || !strings.HasSuffix(prefix, ":") {
		return 0, fmt.Errorf("seed: refusing to purge Redis keys under %q (want lt:{APP_ENV}:)", prefix)
	}
	match := globEscape(prefix) + "*"
	n := 0
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, match, 500).Result()
		if err != nil {
			return n, fmt.Errorf("seed: redis scan: %w", err)
		}
		if len(keys) > 0 {
			if err := rdb.Unlink(ctx, keys...).Err(); err != nil {
				return n, fmt.Errorf("seed: redis unlink: %w", err)
			}
			n += len(keys)
		}
		if next == 0 {
			return n, nil
		}
		cursor = next
	}
}

// globEscape escapes the SCAN MATCH metacharacters.
func globEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`*?[]\^`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
