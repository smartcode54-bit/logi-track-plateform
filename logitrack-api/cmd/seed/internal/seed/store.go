package seed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// putParallelism bounds the concurrent object writes.
const putParallelism = 8

// PutObjects writes every drawn object through the storage backends the API uses (internal/storage):
// committed and pending objects alike; missing_at_source rows get none (Appendix D §D.1.6). Objects go to
// their row's bucket: S3_BUCKET, or S3_PUBLIC_BUCKET for app_releases/ only (R23, R74). A re-run writes the
// same bytes under the same keys; an upsert first drops the objects ObjectsToPut keeps.
func PutObjects(ctx context.Context, objs []*Object, be Backends) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, putParallelism)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	n := 0
	for _, o := range objs {
		if o.Status == "missing_at_source" {
			continue
		}
		if o.Body == nil {
			return n, fmt.Errorf("seed: object %s was not drawn", o.Key)
		}
		b, err := be.For(o.Backend)
		if err != nil {
			return n, err
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(o *Object, b storage.Backend) {
			defer func() { <-sem; wg.Done() }()
			err := b.Put(ctx, storage.Object{Bucket: o.Bucket, Key: o.Key}, bytes.NewReader(o.Body), int64(len(o.Body)), o.ContentType)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("seed: put %s: %w", o.Key, err)
				cancel()
			}
			if err == nil {
				n++
			}
		}(o, b)
	}
	wg.Wait()
	return n, firstErr
}

// ObjectsToPut picks the drawn objects an upsert writes, so it never leaves a file_objects row it does not
// update describing other bytes than the store holds. An object without a row is put (its row is about to be
// inserted); one whose row records the drawn sha256 is put again (identical bytes; this restores an object
// that went missing from the store); one whose row records another sha256 is kept as stored and listed in
// kept: JPEG and PNG bytes are stable for one Go toolchain and renderer only (Appendix D §D.1.6), and the
// placeholder statement PDFs change when documents.render lands (T39). Insert mode puts every object.
func ObjectsToPut(ctx context.Context, etl db.Beginner, objs []*Object, mode Mode) (put []*Object, kept []string, err error) {
	if mode != ModeUpsert {
		return objs, nil, nil
	}
	keys := make([]string, 0, len(objs))
	for _, o := range objs {
		keys = append(keys, o.Key)
	}
	recorded := map[[2]string]string{} // (bucket, key) -> recorded sha256 ("" when NULL)
	err = db.WithSystem(ctx, etl, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT bucket, object_key, coalesce(sha256, '') FROM file_objects WHERE object_key = ANY($1::text[])`, keys)
		if err != nil {
			return err
		}
		return scanAll(rows, func(r pgx.Rows) error {
			var bucket, key, sum string
			if err := r.Scan(&bucket, &key, &sum); err != nil {
				return err
			}
			recorded[[2]string{bucket, key}] = sum
			return nil
		})
	})
	if err != nil {
		return nil, nil, fmt.Errorf("seed: read file_objects: %w", err)
	}
	for _, o := range objs {
		if sum, ok := recorded[[2]string{o.Bucket, o.Key}]; ok && o.Status != "missing_at_source" && sum != o.SHA256 {
			kept = append(kept, o.Key)
			continue
		}
		put = append(put, o)
	}
	return put, kept, nil
}

// RemoveObjects deletes the objects of a plan (a load that failed after --reset); a missing object is fine.
func RemoveObjects(ctx context.Context, objs []*Object, be Backends) (int, error) {
	n := 0
	for _, o := range objs {
		if o.Status == "missing_at_source" {
			continue
		}
		b, err := be.For(o.Backend)
		if err != nil {
			return n, err
		}
		if err := b.Delete(ctx, storage.Object{Bucket: o.Bucket, Key: o.Key}); err != nil {
			return n, fmt.Errorf("seed: remove %s: %w", o.Key, err)
		}
		n++
	}
	return n, nil
}

// objectRow is one file_objects row as invariant 9 checks it.
type objectRow struct {
	bucket, key, status, backend string
	size                         *int64
	sha                          *string
}

// checkObjects is the Go half of invariant 9: every committed or pending object exists with the recorded
// size and sha256 (hashed from the stored bytes), every missing_at_source row has no object.
func checkObjects(ctx context.Context, tx pgx.Tx, be Backends) ([]string, int, error) {
	rows, err := tx.Query(ctx, `SELECT bucket, object_key, status, storage_backend, size_bytes, sha256
		FROM file_objects WHERE deleted_at IS NULL ORDER BY object_key`)
	if err != nil {
		return nil, 0, err
	}
	var list []objectRow
	if err := scanAll(rows, func(r pgx.Rows) error {
		var o objectRow
		if err := r.Scan(&o.bucket, &o.key, &o.status, &o.backend, &o.size, &o.sha); err != nil {
			return err
		}
		list = append(list, o)
		return nil
	}); err != nil {
		return nil, 0, err
	}
	var problems []string
	for _, o := range list {
		b, err := be.For(o.backend)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", o.key, err))
			continue
		}
		obj := storage.Object{Bucket: o.bucket, Key: o.key}
		info, err := b.Stat(ctx, obj)
		switch {
		case o.status == "missing_at_source":
			if err == nil {
				problems = append(problems, o.key+": missing_at_source but the object exists")
			} else if !errors.Is(err, storage.ErrObjectNotFound) {
				return nil, 0, fmt.Errorf("stat %s: %w", o.key, err)
			}
			continue
		case errors.Is(err, storage.ErrObjectNotFound):
			problems = append(problems, fmt.Sprintf("%s: %s but no object", o.key, o.status))
			continue
		case err != nil:
			return nil, 0, fmt.Errorf("stat %s: %w", o.key, err)
		}
		if o.size == nil || info.Size != *o.size {
			problems = append(problems, fmt.Sprintf("%s: size %d, recorded %s", o.key, info.Size, showInt(o.size)))
			continue
		}
		sum, err := hashObject(ctx, b, obj)
		if err != nil {
			return nil, 0, fmt.Errorf("read %s: %w", o.key, err)
		}
		if o.sha == nil || sum != *o.sha {
			problems = append(problems, fmt.Sprintf("%s: sha256 %s, recorded %s", o.key, sum, show(o.sha)))
		}
	}
	return problems, len(list), nil
}

func hashObject(ctx context.Context, b storage.Backend, o storage.Object) (string, error) {
	r, ok := b.(storage.Reader)
	if !ok {
		return "", fmt.Errorf("backend %s cannot read objects", b.Name())
	}
	rc, err := r.Read(ctx, o)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func showInt(v *int64) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprint(*v)
}
