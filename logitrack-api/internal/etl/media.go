package etl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/objstore"
)

// media-copy (main spec §13.6, §9.8): every file_objects row the load registered as 'missing_at_source' is copied
// from GCS (ETL_GCS_BUCKET) to its bucket under the identical key, then committed with size, sha256 and type. An
// object GCS does not have stays 'missing_at_source' with a file_missing_at_source finding (the referencing row
// keeps its FK). --verify re-reads committed copies and compares size and sha256.

// ObjectSource reads the legacy store (gcp.GCS).
type ObjectSource interface {
	Open(ctx context.Context, bucket, name string) (*gcp.Object, error)
}

// ObjectStore is the target store (objstore.S3).
type ObjectStore interface {
	Put(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	Stat(ctx context.Context, bucket, key string) (objstore.Info, error)
}

// MediaResult counts a media-copy run.
type MediaResult struct {
	Copied, Missing, Mismatched int
	MismatchKeys                []string
}

type mediaRow struct {
	id                uuid.UUID
	bucket, key       string
	size              *int64
	sha               *string
	legacyURL, urlBkt string
}

// MediaCopy copies up to limit missing objects (0: all) from gcsBucket.
func (e *Engine) MediaCopy(ctx context.Context, src ObjectSource, dst ObjectStore, gcsBucket string, limit int) (*MediaResult, error) {
	if gcsBucket == "" {
		return nil, errors.New("etl: media-copy needs ETL_GCS_BUCKET")
	}
	rows, err := e.mediaRows(ctx, `status = 'missing_at_source'`, limit)
	if err != nil {
		return nil, err
	}
	res := &MediaResult{}
	for _, r := range rows {
		if r.urlBkt != "" && r.urlBkt != gcsBucket {
			e.cfg.Log.Warn().Str("key", r.key).Msg("legacy URL names another bucket than ETL_GCS_BUCKET; copied from ETL_GCS_BUCKET under the same key")
		}
		obj, err := src.Open(ctx, gcsBucket, r.key)
		if errors.Is(err, gcp.ErrNotFound) {
			res.Missing++
			if err := e.markMissing(ctx, r); err != nil {
				return res, err
			}
			continue
		}
		if err != nil {
			return res, fmt.Errorf("etl: media-copy %s: %w", r.key, err)
		}
		h := sha256.New()
		counter := &countingReader{r: io.TeeReader(obj.Body, h)}
		ctype := obj.ContentType
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		err = dst.Put(ctx, r.bucket, r.key, counter, obj.Size, ctype)
		_ = obj.Body.Close()
		if err != nil {
			return res, fmt.Errorf("etl: media-copy %s: %w", r.key, err)
		}
		if obj.Size >= 0 && counter.n != obj.Size {
			return res, fmt.Errorf("etl: media-copy %s: read %d of %d bytes", r.key, counter.n, obj.Size)
		}
		sum := hex.EncodeToString(h.Sum(nil))
		err = e.cfg.Tx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE file_objects SET status = 'committed', committed_at = now(), size_bytes = $2, sha256 = $3,
				content_type = $4 WHERE id = $1 AND status = 'missing_at_source'`, r.id, counter.n, sum, ctype); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE etl.quarantine SET resolved_at = now(), resolved_by = 'etl media-copy', resolution = 'retried'
				WHERE collection = 'file_objects' AND doc_path = $1 AND reason_code = 'file_missing_at_source' AND resolved_at IS NULL`, r.key)
			return err
		})
		if err != nil {
			return res, fmt.Errorf("etl: media-copy %s: commit: %w", r.key, err)
		}
		res.Copied++
	}
	return res, nil
}

// MediaVerify compares every committed legacy copy with the store (size, sha256).
func (e *Engine) MediaVerify(ctx context.Context, dst ObjectStore, limit int) (*MediaResult, error) {
	rows, err := e.mediaRows(ctx, `status = 'committed'`, limit)
	if err != nil {
		return nil, err
	}
	res := &MediaResult{}
	for _, r := range rows {
		rc, err := dst.Get(ctx, r.bucket, r.key)
		if errors.Is(err, objstore.ErrNotFound) {
			res.Mismatched++
			res.MismatchKeys = append(res.MismatchKeys, r.key)
			continue
		}
		if err != nil {
			return res, err
		}
		h := sha256.New()
		n, err := io.Copy(h, rc)
		_ = rc.Close()
		if err != nil {
			return res, fmt.Errorf("etl: verify %s: %w", r.key, err)
		}
		if r.size == nil || *r.size != n || r.sha == nil || *r.sha != hex.EncodeToString(h.Sum(nil)) {
			res.Mismatched++
			res.MismatchKeys = append(res.MismatchKeys, r.key)
			continue
		}
		res.Copied++
	}
	slices.Sort(res.MismatchKeys)
	return res, nil
}

func (e *Engine) mediaRows(ctx context.Context, where string, limit int) ([]mediaRow, error) {
	var out []mediaRow
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		q := `SELECT id, bucket, object_key, size_bytes, sha256, legacy_url FROM file_objects
			WHERE ` + where + ` AND storage_backend = 's3' AND legacy_url IS NOT NULL ORDER BY created_at, id`
		args := []any{}
		if limit > 0 {
			q += ` LIMIT $1`
			args = append(args, limit)
		}
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r mediaRow
			if err := rows.Scan(&r.id, &r.bucket, &r.key, &r.size, &r.sha, &r.legacyURL); err != nil {
				return err
			}
			r.urlBkt, _, _, _ = ParseStorageURL(r.legacyURL)
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

func (e *Engine) markMissing(ctx context.Context, r mediaRow) error {
	return e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		return writeFindings(ctx, tx, "file_objects", r.key, []Finding{{Reason: ReasonFileMissingAtSource,
			Detail: "GCS has no object under this key; the row stays missing_at_source", Raw: r.legacyURL}})
	})
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// URLHit is a column that still holds Firebase Storage URLs.
type URLHit struct {
	Table, Column string
	Rows          int64
}

// RewriteURLs reports every text, citext or jsonb column of schema public that still holds a Firebase Storage URL
// after the load mapped URL fields to *_file_id (main spec §13.6). file_objects.legacy_url is the one place such
// URLs belong (token stripped) and is skipped.
func (e *Engine) RewriteURLs(ctx context.Context) ([]URLHit, error) {
	var hits []URLHit
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.table_name::text, c.column_name::text FROM information_schema.columns c
			JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
			WHERE c.table_schema = 'public' AND c.udt_name IN ('text', 'citext', 'varchar', 'jsonb')
			  AND NOT (c.table_name = 'file_objects' AND c.column_name = 'legacy_url') AND c.table_name <> 'goose_db_version'
			ORDER BY 1, 2`)
		if err != nil {
			return err
		}
		var cols [][2]string
		for rows.Next() {
			var t, c string
			if err := rows.Scan(&t, &c); err != nil {
				return err
			}
			cols = append(cols, [2]string{t, c})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, tc := range cols {
			var n int64
			q := `SELECT count(*) FROM ` + pgx.Identifier{tc[0]}.Sanitize() + ` WHERE ` + pgx.Identifier{tc[1]}.Sanitize() + `::text LIKE $1`
			if err := tx.QueryRow(ctx, q, "%"+FirebaseStorageHost+"%").Scan(&n); err != nil {
				return fmt.Errorf("etl: rewrite-urls %s.%s: %w", tc[0], tc[1], err)
			}
			if n > 0 {
				hits = append(hits, URLHit{Table: tc[0], Column: tc[1], Rows: n})
			}
		}
		return nil
	})
	return hits, err
}
