//go:build integration

// Public uploads of T52 (main spec §9.7): an APK under app_releases/ in the public bucket, presigned by the release
// routes (no client purpose), uploaded through the internal API route on the local backend or straight to S3 with
// Cache-Control and metadata signed, and committed with its sha256 verified (the local sidecar, or read back from S3).
package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

func apkBody(n int, b byte) ([]byte, string) {
	body := append([]byte("PK\x03\x04"), bytes.Repeat([]byte{b}, n-4)...)
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:])
}

func (f *fixture) commitPublic(t *testing.T, s *storage.Service, key string, owner uuid.UUID) (storage.PublicCommitted, error) {
	t.Helper()
	var out storage.PublicCommitted
	err := db.WithSystem(context.Background(), f.pool, nil, func(tx pgx.Tx) error {
		var err error
		out, err = s.CommitPublic(context.Background(), tx, key, "apk", owner)
		return err
	})
	return out, err
}

func TestPublicUploadLocal(t *testing.T) {
	f := setup(t)
	svc := f.service(t, storage.BackendLocal, f.local(t)) // UPLOAD_MAX_BYTES 1 MiB
	base := serveStorage(t, svc)
	ctx := context.Background()
	const key = "app_releases/dev/logitrack-dev-v3.6.0.apk"
	body, sum := apkBody(2<<20, 7) // above UPLOAD_MAX_BYTES: an APK may be (MaxReleaseBytes)
	in := storage.PublicUpload{Key: key, Purpose: "apk", ContentType: storage.ReleaseContentType, SizeBytes: int64(len(body)), SHA256: sum}

	p, err := svc.PresignPublic(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.URL, storage.LocalUploadPath+"/"+key+"?") || p.PublicURL != "http://media.test/media/"+key || p.StorageBackend != "local" {
		t.Fatalf("presign %+v", p)
	}
	var vis, bucket string
	var uploader *uuid.UUID
	if err := f.etl.QueryRow(ctx, `SELECT visibility, bucket, uploaded_by FROM file_objects WHERE object_key = $1`, key).Scan(&vis, &bucket, &uploader); err != nil ||
		vis != "public" || bucket != "logitrack-public" || uploader != nil {
		t.Fatalf("row: %s %s %v %v", vis, bucket, uploader, err)
	}
	// The public /media route takes no APK, signed or not: release uploads stay on the internal listener (R43).
	media := *p
	media.URL = strings.Replace(p.URL, storage.LocalUploadPath, storage.MediaPrefix, 1)
	if r := put(t, &media.Presigned, base, body, ""); r.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT /media of an APK: %d", r.StatusCode)
	}
	// A second presign of the pending key needs Replace.
	var pending *storage.PendingUploadError
	if _, err := svc.PresignPublic(ctx, in); !errors.As(err, &pending) {
		t.Fatalf("second presign: %v", err)
	}
	// Bytes of the declared size but another digest: the commit refuses them.
	bad, _ := apkBody(len(body), 8)
	if r := put(t, &p.Presigned, base, bad, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d", r.StatusCode)
	}
	if _, err := f.commitPublic(t, svc, key, uuid.New()); err == nil {
		t.Fatal("a wrong digest committed")
	} else {
		wantField(t, err, "key", "checksum_mismatch")
	}
	// Replace discards the upload; the right bytes commit.
	in.Replace = true
	if p, err = svc.PresignPublic(ctx, in); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.status(t, key); st != "pending" {
		t.Fatalf("status %s", st)
	}
	if r := put(t, &p.Presigned, base, body, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d", r.StatusCode)
	}
	owner := uuid.New()
	c, err := f.commitPublic(t, svc, key, owner)
	if err != nil || c.SHA256 != sum || c.SizeBytes != int64(len(body)) || c.StorageBackend != "local" || c.Bucket != "logitrack-public" {
		t.Fatalf("commit %+v %v", c, err)
	}
	var kind string
	var ownerID uuid.UUID
	if err := f.etl.QueryRow(ctx, `SELECT owner_kind, owner_id FROM file_objects WHERE object_key = $1 AND status = 'committed'`, key).
		Scan(&kind, &ownerID); err != nil || kind != "release" || ownerID != owner {
		t.Fatalf("owner %s %s %v", kind, ownerID, err)
	}
	// Committed: immutable, Replace or not.
	if _, err := svc.PresignPublic(ctx, in); !errors.Is(err, storage.ErrCommitted) {
		t.Fatalf("presign after commit: %v", err)
	}
	// Served unsigned with the immutable cache header.
	res, err := http.Get(base + storage.MediaPrefix + "/" + key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !bytes.Equal(got, body) || res.Header.Get("Cache-Control") != storage.PublicCacheControl ||
		res.Header.Get("Content-Type") != storage.ReleaseContentType {
		t.Fatalf("GET: %d %d bytes %v", res.StatusCode, len(got), res.Header)
	}
	// An ordinary upload above UPLOAD_MAX_BYTES is still refused on the same route.
	big := bytes.Repeat([]byte{1}, 2<<20)
	ph, err := svc.Presign(ctx, f.caller(f.driver, f.own, false), storage.PresignInput{Purpose: "trip_photo", EntityID: uuid.NewString(),
		ContentType: "image/jpeg", SizeBytes: 1 << 20, Variant: "loading"}, storage.PutOptions{APIPath: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := put(t, ph, base, big, ""); r.StatusCode == http.StatusOK {
		t.Fatal("a 2 MiB photo was taken")
	}
}

func TestPublicUploadS3(t *testing.T) {
	f := setup(t)
	s3, cfg := s3Backend(t)
	svc := f.serviceOn(t, storage.BackendS3, cfg, s3)
	ctx := context.Background()
	const key = "app_releases/prod/logitrack-prod-v3.6.0.apk"
	body, sum := apkBody(64<<10, 3)
	uploader := f.staff
	in := storage.PublicUpload{Key: key, Purpose: "apk", ContentType: storage.ReleaseContentType, SizeBytes: int64(len(body)), SHA256: sum,
		UploadedBy: &uploader, Metadata: map[string]string{"sha256": sum, "build-number": "4", "flavor": "prod"}}
	p, err := svc.PresignPublic(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.URL, cfg.PresignEndpoint+"/") || p.Headers["Cache-Control"] != storage.PublicCacheControl ||
		p.Headers["X-Amz-Meta-Build-Number"] != "4" || p.PublicURL != cfg.PublicBaseURL+"/"+key {
		t.Fatalf("presign %+v", p)
	}
	// Without the signed Cache-Control the PUT fails the signature.
	stripped := *p
	stripped.Headers = map[string]string{}
	for k, v := range p.Headers {
		if k != "Cache-Control" {
			stripped.Headers[k] = v
		}
	}
	if r := put(t, &stripped.Presigned, "", body, ""); r.StatusCode == http.StatusOK {
		t.Fatal("a PUT without the signed Cache-Control was stored")
	}
	if r := put(t, &p.Presigned, "", body, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d", r.StatusCode)
	}
	// The object carries the headers; the public URL serves it anonymously.
	admin, err := minio.New(strings.TrimPrefix(cfg.Endpoint, "http://"), &minio.Options{
		Creds: credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""), Region: cfg.Region, BucketLookup: minio.BucketLookupPath})
	if err != nil {
		t.Fatal(err)
	}
	oi, err := admin.StatObject(ctx, cfg.PublicBucket, key, minio.StatObjectOptions{})
	if err != nil || oi.Metadata.Get("Cache-Control") != storage.PublicCacheControl || oi.UserMetadata["Sha256"] != sum ||
		oi.ContentType != storage.ReleaseContentType {
		t.Fatalf("object %+v %v", oi, err)
	}
	res, err := http.Get(strings.Replace(p.PublicURL, cfg.PresignEndpoint, cfg.Endpoint, 1))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("anonymous GET of the public URL: %d", res.StatusCode)
	}
	// S3 keeps no digest of a presigned PUT: the commit reads the object back.
	c, err := f.commitPublic(t, svc, key, uuid.New())
	if err != nil || c.SHA256 != sum || c.StorageBackend != "s3" {
		t.Fatalf("commit %+v %v", c, err)
	}

	// A pending upload whose bytes differ from the declared digest is refused by the read-back.
	const key2 = "app_releases/prod/logitrack-prod-v3.6.1.apk"
	_, sum2 := apkBody(len(body), 4)
	p2, err := svc.PresignPublic(ctx, storage.PublicUpload{Key: key2, Purpose: "apk", ContentType: storage.ReleaseContentType,
		SizeBytes: int64(len(body)), SHA256: sum2})
	if err != nil {
		t.Fatal(err)
	}
	if r := put(t, &p2.Presigned, "", body, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d", r.StatusCode)
	}
	if _, err := f.commitPublic(t, svc, key2, uuid.New()); err == nil {
		t.Fatal("a wrong digest committed")
	} else {
		wantField(t, err, "key", "checksum_mismatch")
	}
	// Replace removes the object of the never-published upload, so the create-only PUT can store new bytes.
	body3, sum3 := apkBody(len(body), 4)
	p3, err := svc.PresignPublic(ctx, storage.PublicUpload{Key: key2, Purpose: "apk", ContentType: storage.ReleaseContentType,
		SizeBytes: int64(len(body3)), SHA256: sum3, Replace: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := put(t, &p3.Presigned, "", body3, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("PUT after Replace: %d", r.StatusCode)
	}
	if _, err := f.commitPublic(t, svc, key2, uuid.New()); err != nil {
		t.Fatal(err)
	}
}
