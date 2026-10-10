package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testSigningKey is a test-only LOCAL_MEDIA_SIGNING_KEY (low entropy on purpose).
var testSigningKey = bytes.Repeat([]byte("t"), 40)

func newTestLocal(t *testing.T, now func() time.Time) (*Local, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := NewLocal(LocalConfig{Dir: dir, PublicBaseURL: "https://logi.example.test/media/", SigningKey: testSigningKey,
		APIUploadPath: LocalUploadPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, dir
}

func TestNewLocalRefusesBadConfig(t *testing.T) {
	dir := t.TempDir()
	for name, cfg := range map[string]LocalConfig{
		"relative dir":  {Dir: "media"},
		"short key":     {Dir: dir, SigningKey: []byte("short")},
		"base with ?":   {Dir: dir, PublicBaseURL: "https://x.test/media?a=1"},
		"base not http": {Dir: dir, PublicBaseURL: "ftp://x.test/media"},
	} {
		if l, err := NewLocal(cfg); err == nil {
			_ = l.Close()
			t.Errorf("%s accepted", name)
		} else if strings.Contains(err.Error(), "short") {
			t.Errorf("%s: error carries the key", name)
		}
	}
}

func TestLocalWriteIsAtomicAndPrivate(t *testing.T) {
	l, dir := newTestLocal(t, nil)
	body := []byte("\xff\xd8\xff jpeg bytes")
	info, err := l.Write("trips/t1/seal-1.jpg", bytes.NewReader(body), "image/jpeg", 1024)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if info.Size != int64(len(body)) || info.SHA256 != hex.EncodeToString(sum[:]) || info.ContentType != "image/jpeg" {
		t.Fatalf("info %+v", info)
	}
	fi, err := os.Stat(filepath.Join(dir, "private", "trips", "t1", "seal-1.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v, want 0640", fi.Mode().Perm())
	}
	if d, _ := os.Stat(filepath.Join(dir, "private", "trips", "t1")); d.Mode().Perm() != 0o750 {
		t.Fatalf("dir mode %v, want 0750", d.Mode().Perm())
	}
	got, err := l.Stat(context.Background(), Object{Key: "trips/t1/seal-1.jpg"})
	if err != nil || got != info {
		t.Fatalf("stat %+v %v", got, err)
	}
	// No partial file is left behind, and an upload over the limit leaves nothing at the key.
	if _, err := l.Write("trips/t1/big.jpg", bytes.NewReader(make([]byte, 11)), "image/jpeg", 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := l.Stat(context.Background(), Object{Key: "trips/t1/big.jpg"}); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("partial object visible: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, tmpDir)); len(entries) != 0 {
		t.Fatalf("temp files left: %d", len(entries))
	}
	// Public keys live in their own tree.
	if _, err := l.Write("app_releases/prod/a.apk", strings.NewReader("apk"), "application/vnd.android.package-archive", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "public", "app_releases", "prod", "a.apk")); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(context.Background(), Object{Key: "trips/t1/seal-1.jpg"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(context.Background(), Object{Key: "trips/t1/seal-1.jpg"}); err != nil {
		t.Fatalf("deleting a missing object: %v", err)
	}
	if _, err := l.Stat(context.Background(), Object{Key: "trips/t1/seal-1.jpg"}); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("deleted object: %v", err)
	}
}

func TestLocalRefusesKeysOutsideTheDirectory(t *testing.T) {
	l, dir := newTestLocal(t, nil)
	outside := filepath.Join(filepath.Dir(dir), "outside-"+filepath.Base(dir))
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	for _, k := range []string{"../escape.jpg", "../../etc/passwd", "a/../../b", "/abs.jpg",
		// Filesystem limits of the local backend only: a 256-byte directory, a 251-byte last segment (its
		// sidecar would need 256).
		"a/" + strings.Repeat("b", 256) + "/c.jpg", "a/" + strings.Repeat("b", 247) + ".jpg"} {
		if _, err := l.Write(k, strings.NewReader("x"), "image/jpeg", 10); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("write %q: %v", k, err)
		}
		if _, _, err := l.Open(k); !errors.Is(err, ErrObjectNotFound) {
			t.Errorf("open %q: %v", k, err)
		}
		if _, err := l.PresignPut(context.Background(), Object{Key: k}, "image/jpeg", 1, time.Minute, PutOptions{}); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("presign %q: %v", k, err)
		}
	}
	// 250 bytes is the longest last segment: written, sidecar included.
	if _, err := l.Write("a/"+strings.Repeat("b", 246)+".jpg", strings.NewReader("x"), "image/jpeg", 10); err != nil {
		t.Fatalf("250-byte last segment: %v", err)
	}
	// A symlink planted inside the tree cannot lead out of it (os.Root).
	if err := os.Symlink(outside, filepath.Join(dir, "private", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write("link/x.jpg", strings.NewReader("x"), "image/jpeg", 10); err == nil {
		t.Fatal("write through a symlink to outside succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "x.jpg")); err == nil {
		t.Fatal("a file was written outside LOCAL_MEDIA_DIR")
	}
	// A directory is never an object.
	if _, err := l.Write("trips/t/x.jpg", strings.NewReader("x"), "image/jpeg", 10); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Open("trips/t"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("a directory opened as an object: %v", err)
	}
}

func TestLocalSignedURLs(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	l, _ := newTestLocal(t, clock)
	ctx := context.Background()
	key := "trips/t1/สติกเกอร์ 1.jpg"

	put, err := l.PresignPut(ctx, Object{Key: key}, "image/jpeg", 10, 15*time.Minute, PutOptions{APIPath: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(put.URL, "/v1/uploads/local/trips/t1/") || put.Method != "PUT" || put.Headers["Content-Type"] != "image/jpeg" ||
		!put.Expires.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("api put %+v", put)
	}
	media, err := l.PresignPut(ctx, Object{Key: key}, "image/jpeg", 10, time.Minute, PutOptions{})
	if err != nil || !strings.HasPrefix(media.URL, "https://logi.example.test/media/trips/t1/") {
		t.Fatalf("media put %+v %v", media, err)
	}
	u, err := url.Parse(put.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/v1/uploads/local/")); got != key {
		t.Fatalf("escaped key round trip %q", got)
	}
	q := u.Query()
	verify := func(method, k, ct, n, disp string) error {
		return l.Verify(method, k, ct, n, disp, q.Get(QueryExpires), q.Get(QuerySignature))
	}
	if err := verify("PUT", key, "image/jpeg", "10", ""); err != nil {
		t.Fatalf("valid upload signature: %v", err)
	}
	for name, err := range map[string]error{
		"other method":       verify("GET", key, "image/jpeg", "10", ""),
		"other key":          verify("PUT", "trips/t1/other.jpg", "image/jpeg", "10", ""),
		"other content type": verify("PUT", key, "text/html", "10", ""),
		"other size":         verify("PUT", key, "image/jpeg", "11", ""),
		"chunked body":       verify("PUT", key, "image/jpeg", "", ""),
		"tampered expiry":    l.Verify("PUT", key, "image/jpeg", "10", "", "9999999999", q.Get(QuerySignature)),
		"tampered signature": l.Verify("PUT", key, "image/jpeg", "10", "", q.Get(QueryExpires), q.Get(QuerySignature)[1:]+"A"),
		"no signature":       l.Verify("PUT", key, "image/jpeg", "10", "", q.Get(QueryExpires), ""),
	} {
		if !errors.Is(err, ErrSignatureInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	get, exp, err := l.PresignGet(ctx, Object{Key: key}, time.Hour, GetOptions{ContentDisposition: `attachment; filename="a.jpg"`})
	if err != nil || !exp.Equal(now.Add(time.Hour)) || !strings.HasPrefix(get, "https://logi.example.test/media/") {
		t.Fatalf("get %s %v %v", get, exp, err)
	}
	gq := mustQuery(t, get)
	if err := l.Verify("GET", key, "", "", gq.Get(QueryDisposition), gq.Get(QueryExpires), gq.Get(QuerySignature)); err != nil {
		t.Fatalf("valid download signature: %v", err)
	}
	if err := l.Verify("GET", key, "", "", "inline", gq.Get(QueryExpires), gq.Get(QuerySignature)); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("changed disposition: %v", err)
	}
	// The URL expires after its TTL.
	now = now.Add(time.Hour + time.Second)
	if err := l.Verify("GET", key, "", "", gq.Get(QueryDisposition), gq.Get(QueryExpires), gq.Get(QuerySignature)); !errors.Is(err, ErrSignatureExpired) {
		t.Fatalf("expired download: %v", err)
	}

	if pub, err := l.PublicURL(Object{Key: "app_releases/prod/logitrack-prod-v3.5.0.apk"}); err != nil ||
		pub != "https://logi.example.test/media/app_releases/prod/logitrack-prod-v3.5.0.apk" {
		t.Fatalf("public url %s %v", pub, err)
	}
	if _, err := l.PublicURL(Object{Key: key}); !errors.Is(err, ErrNotPublic) {
		t.Fatalf("private key public url: %v", err)
	}
	// A worker-side backend (no key, no base) signs nothing.
	w, err := NewLocal(LocalConfig{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if _, _, err := w.PresignGet(ctx, Object{Key: key}, time.Minute, GetOptions{}); err == nil {
		t.Fatal("signed without a key")
	}
	if err := w.Verify("GET", key, "", "", "", q.Get(QueryExpires), q.Get(QuerySignature)); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("verified without a key: %v", err)
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestLocalPutAndSweep(t *testing.T) {
	now := time.Now()
	l, dir := newTestLocal(t, func() time.Time { return now })
	ctx := context.Background()
	if err := l.Put(ctx, Object{Key: "documents/statements/s/bundle.zip"}, strings.NewReader("zip"), 3, "application/zip"); err != nil {
		t.Fatal(err)
	}
	if err := l.Put(ctx, Object{Key: "documents/statements/s/short.zip"}, strings.NewReader("zi"), 3, "application/zip"); err == nil {
		t.Fatal("short write accepted")
	}
	f, info, err := l.Open("documents/statements/s/bundle.zip")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	_ = f.Close()
	if string(b) != "zip" || info.ContentType != "application/zip" {
		t.Fatalf("open %q %+v", b, info)
	}
	stale := filepath.Join(dir, tmpDir, "stale")
	if err := os.WriteFile(stale, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, tmpDir, "fresh")
	if err := os.WriteFile(fresh, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if n, err := l.SweepTemp(time.Hour); err != nil || n != 1 {
		t.Fatalf("sweep %d %v", n, err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("a fresh partial upload was swept")
	}
	if err := l.Check(ctx); err != nil {
		t.Fatal(err)
	}
}
