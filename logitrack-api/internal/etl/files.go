package etl

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// FirebaseStorageHost is the host of legacy download URLs.
const FirebaseStorageHost = "firebasestorage.googleapis.com"

// ParseStorageURL turns a Firebase Storage download URL
// https://firebasestorage.googleapis.com/v0/b/{bucket}/o/{encodedPath}?alt=media&token=... into its bucket, the
// object key (urldecode of the path after /o/) and the URL without its token (Appendix A §A.3.0 "File URL ->
// object key"). Anything else (a Google profile photo, a data URL) is not ours: ok is false.
func ParseStorageURL(raw string) (bucket, key, legacyURL string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host != FirebaseStorageHost {
		return "", "", "", false
	}
	rest, found := strings.CutPrefix(u.EscapedPath(), "/v0/b/")
	if !found {
		return "", "", "", false
	}
	b, enc, found := strings.Cut(rest, "/o/")
	if !found || b == "" || enc == "" {
		return "", "", "", false
	}
	if bucket, err = url.PathUnescape(b); err != nil {
		return "", "", "", false
	}
	if key, err = url.PathUnescape(enc); err != nil || key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "//") {
		return "", "", "", false
	}
	q := u.Query()
	q.Del("token")
	u.RawQuery = q.Encode()
	return bucket, key, u.String(), true
}

// fileRef registers the object behind a legacy URL field in file_objects (R1) and returns its id. The row is
// 'missing_at_source' until `etl media-copy` copies the object from GCS under the identical key and commits it;
// the referencing row keeps its FK either way (§13.6). One row per key: a key referenced twice (the check-in
// screenshot a trip photo copies, ADR 0019) keeps its first owner. A URL that is not a Firebase Storage download
// URL is NULL + url_unparseable.
func (c *docCtx) fileRef(field string, value any, purpose, ownerKind string, ownerID, tenant uuid.UUID) (*uuid.UUID, error) {
	s, _ := value.(string)
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	_, key, legacy, ok := ParseStorageURL(s)
	if !ok {
		c.find(field, ReasonURLUnparseable, "not a Firebase Storage download URL", s)
		return nil, nil
	}
	var tid *uuid.UUID
	if tenant != uuid.Nil {
		tid = &tenant
	}
	var id uuid.UUID
	err := c.tx.QueryRow(c.ctx, `WITH ins AS (
		  INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, owner_kind, owner_id, visibility, status, legacy_url,
		                            storage_backend)
		  VALUES ($1, $2, $3, $4, $5, $6, 'private', 'missing_at_source', $7, 's3')
		  ON CONFLICT (bucket, object_key) DO NOTHING RETURNING id)
		SELECT id FROM ins UNION ALL SELECT id FROM file_objects WHERE bucket = $1 AND object_key = $2 LIMIT 1`,
		c.e.cfg.Bucket, key, tid, purpose, ownerKind, ownerID, legacy).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("etl: %s: register %s: %w", c.doc.Path, field, err)
	}
	return &id, nil
}
