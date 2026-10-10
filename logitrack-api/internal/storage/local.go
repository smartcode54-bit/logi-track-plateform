package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// Query parameters of a local signed URL. The names say what they are; the values are an expiry in Unix seconds,
// an HMAC-SHA256 (base64url) and, for downloads, the Content-Disposition to answer with.
const (
	QueryExpires     = "X-LT-Expires"
	QuerySignature   = "X-LT-Signature"
	QueryDisposition = "response-content-disposition"
)

// Local file layout under LOCAL_MEDIA_DIR: data under private/ or public/ (public only for app_releases/), the
// content type and digest of each object under .meta/, partial uploads under .tmp/ (same filesystem, so the final
// rename is atomic). Every access goes through one os.Root, which refuses names that leave the directory, symlinks
// included.
const (
	areaPrivate = "private"
	areaPublic  = "public"
	metaDir     = ".meta"
	tmpDir      = ".tmp"
	fileMode    = 0o640
	dirMode     = 0o750
)

// NAME_MAX of the filesystems LOCAL_MEDIA_DIR lives on: a directory segment of a key becomes a directory name,
// the last one a file name and, with ".json", the sidecar's name.
const (
	maxNameBytes     = 255
	maxLastNameBytes = maxNameBytes - len(".json")
)

// validLocalKey is ValidateKey plus the local backend's filesystem limits: a directory segment of at most 255
// bytes and a last segment of at most 250 (its sidecar adds ".json"). Every key the local backend writes comes
// from a purpose template and is far shorter; legacy keys with longer Thai file names stay on s3 (§9.11).
func validLocalKey(key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	segs := strings.Split(key, "/")
	for i, seg := range segs {
		limit := maxNameBytes
		if i == len(segs)-1 {
			limit = maxLastNameBytes
		}
		if len(seg) > limit {
			return ErrInvalidKey
		}
	}
	return nil
}

// MinSigningKeyBytes is the shortest LOCAL_MEDIA_SIGNING_KEY accepted (HMAC-SHA256 key).
const MinSigningKeyBytes = 32

// Signature errors of the local backend; both answer 403 permission_denied.
var (
	ErrSignatureExpired = errors.New("storage: signed URL expired")
	ErrSignatureInvalid = errors.New("storage: signature missing or invalid")
)

// LocalConfig configures the local backend.
type LocalConfig struct {
	// Dir is LOCAL_MEDIA_DIR: an absolute directory outside any web root; created (0750) when missing.
	Dir string
	// PublicBaseURL is LOCAL_MEDIA_PUBLIC_BASE_URL, the origin and path that reach GET /media on the public
	// listener (deployment: https://logi.showkhun.co/media). Empty: no URL can be built (worker).
	PublicBaseURL string
	// SigningKey is LOCAL_MEDIA_SIGNING_KEY; nil: nothing can be signed or verified (worker).
	SigningKey []byte
	// APIUploadPath is the path of the upload route on the Go API that the web reaches through the BFF.
	APIUploadPath string
	// Now is the clock (tests); nil = time.Now.
	Now func() time.Time
}

// Local is the disk backend.
type Local struct {
	root    *os.Root
	base    *url.URL
	key     []byte
	apiPath string
	now     func() time.Time
}

var _ Backend = (*Local)(nil)

// NewLocal opens (and creates) the directory tree. Errors never carry the signing key.
func NewLocal(cfg LocalConfig) (*Local, error) {
	if cfg.Dir == "" || !strings.HasPrefix(cfg.Dir, "/") {
		return nil, errors.New("storage: LOCAL_MEDIA_DIR must be an absolute path")
	}
	if err := os.MkdirAll(cfg.Dir, dirMode); err != nil {
		return nil, fmt.Errorf("storage: LOCAL_MEDIA_DIR: %w", err)
	}
	root, err := os.OpenRoot(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("storage: LOCAL_MEDIA_DIR: %w", err)
	}
	for _, d := range []string{areaPrivate, areaPublic, metaDir + "/" + areaPrivate, metaDir + "/" + areaPublic, tmpDir} {
		if err := root.MkdirAll(d, dirMode); err != nil {
			_ = root.Close()
			return nil, fmt.Errorf("storage: LOCAL_MEDIA_DIR: %w", err)
		}
	}
	l := &Local{root: root, apiPath: strings.TrimSuffix(cfg.APIUploadPath, "/"), now: cfg.Now}
	if l.now == nil {
		l.now = time.Now
	}
	if cfg.PublicBaseURL != "" {
		u, err := url.Parse(strings.TrimSuffix(cfg.PublicBaseURL, "/"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			_ = root.Close()
			return nil, errors.New("storage: LOCAL_MEDIA_PUBLIC_BASE_URL must be an absolute http(s) URL without query")
		}
		l.base = u
	}
	if cfg.SigningKey != nil {
		if len(cfg.SigningKey) < MinSigningKeyBytes {
			_ = root.Close()
			return nil, fmt.Errorf("storage: LOCAL_MEDIA_SIGNING_KEY must be at least %d bytes", MinSigningKeyBytes)
		}
		l.key = append([]byte(nil), cfg.SigningKey...)
	}
	return l, nil
}

// Close releases the directory handle.
func (l *Local) Close() error { return l.root.Close() }

// Name implements Backend.
func (l *Local) Name() string { return BackendLocal }

func area(key string) string {
	if IsPublicKey(key) {
		return areaPublic
	}
	return areaPrivate
}

func dataName(key string) string { return area(key) + "/" + key }
func metaName(key string) string { return metaDir + "/" + area(key) + "/" + key + ".json" }

// meta is the sidecar of an object: what S3 keeps as object metadata.
type meta struct {
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

// signature is HMAC-SHA256 over length-prefixed fields, so no two field tuples share an input.
func (l *Local) signature(method, key string, exp int64, contentType, contentLength, disposition string) string {
	m := hmac.New(sha256.New, l.key)
	for _, f := range []string{"LT-MEDIA-V1", method, key, strconv.FormatInt(exp, 10), contentType, contentLength, disposition} {
		_, _ = fmt.Fprintf(m, "%d:%s\n", len(f), f)
	}
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Verify checks a signed local URL: method (GET also covers HEAD), key, expiry, the request Content-Type and
// Content-Length of an upload (the declared size: the body is bound to it, like a SigV4 PUT that signs
// Content-Length) and the requested Content-Disposition of a download, as signed.
func (l *Local) Verify(method, key, contentType, contentLength, disposition, expires, sig string) error {
	if l.key == nil || sig == "" || expires == "" {
		return ErrSignatureInvalid
	}
	exp, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || exp <= 0 {
		return ErrSignatureInvalid
	}
	want := l.signature(method, key, exp, contentType, contentLength, disposition)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return ErrSignatureInvalid
	}
	if l.now().Unix() > exp {
		return ErrSignatureExpired
	}
	return nil
}

// escapeKey escapes each segment of a key for a URL path.
func escapeKey(key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

func (l *Local) mediaURL(key string) (string, error) {
	if l.base == nil {
		return "", errors.New("storage: LOCAL_MEDIA_PUBLIC_BASE_URL is not set")
	}
	return l.base.String() + "/" + escapeKey(key), nil
}

func (l *Local) signedQuery(method, key string, ttl time.Duration, contentType, contentLength, disposition string) (url.Values, time.Time, error) {
	if l.key == nil {
		return nil, time.Time{}, errors.New("storage: LOCAL_MEDIA_SIGNING_KEY is not set")
	}
	if err := validLocalKey(key); err != nil {
		return nil, time.Time{}, err
	}
	exp := l.now().Add(ttl).Truncate(time.Second)
	q := url.Values{}
	q.Set(QueryExpires, strconv.FormatInt(exp.Unix(), 10))
	if disposition != "" {
		q.Set(QueryDisposition, disposition)
	}
	q.Set(QuerySignature, l.signature(method, key, exp.Unix(), contentType, contentLength, disposition))
	return q, exp, nil
}

// PresignPut implements Backend: a PUT to the API path (web, through the BFF) or to the media URL (driver app),
// valid for ttl, that must carry exactly Content-Type: contentType and a body of exactly size bytes
// (Content-Length is signed; a chunked body is refused).
func (l *Local) PresignPut(_ context.Context, o Object, contentType string, size int64, ttl time.Duration, opts PutOptions) (PutURL, error) {
	if size <= 0 {
		return PutURL{}, errors.New("storage: an upload needs its size")
	}
	q, exp, err := l.signedQuery("PUT", o.Key, ttl, contentType, strconv.FormatInt(size, 10), "")
	if err != nil {
		return PutURL{}, err
	}
	var u string
	if opts.APIPath {
		if l.apiPath == "" {
			return PutURL{}, errors.New("storage: no API upload path")
		}
		u = l.apiPath + "/" + escapeKey(o.Key)
	} else if u, err = l.mediaURL(o.Key); err != nil {
		return PutURL{}, err
	}
	return PutURL{URL: u + "?" + q.Encode(), Method: "PUT", Headers: map[string]string{"Content-Type": contentType}, Expires: exp}, nil
}

// PresignGet implements Backend: ${LOCAL_MEDIA_PUBLIC_BASE_URL}/<key> signed for ttl.
func (l *Local) PresignGet(_ context.Context, o Object, ttl time.Duration, opts GetOptions) (string, time.Time, error) {
	q, exp, err := l.signedQuery("GET", o.Key, ttl, "", "", opts.ContentDisposition)
	if err != nil {
		return "", time.Time{}, err
	}
	u, err := l.mediaURL(o.Key)
	if err != nil {
		return "", time.Time{}, err
	}
	return u + "?" + q.Encode(), exp, nil
}

// PublicURL implements Backend: unsigned, under app_releases/ only.
func (l *Local) PublicURL(o Object) (string, error) {
	if err := validLocalKey(o.Key); err != nil {
		return "", err
	}
	if !IsPublicKey(o.Key) {
		return "", ErrNotPublic
	}
	return l.mediaURL(o.Key)
}

// Stat implements Backend.
func (l *Local) Stat(_ context.Context, o Object) (Info, error) {
	if err := validLocalKey(o.Key); err != nil {
		return Info{}, err
	}
	fi, err := l.root.Stat(dataName(o.Key))
	if errors.Is(err, fs.ErrNotExist) {
		return Info{}, ErrObjectNotFound
	}
	if err != nil {
		return Info{}, err
	}
	if !fi.Mode().IsRegular() {
		return Info{}, ErrObjectNotFound
	}
	info := Info{Size: fi.Size()}
	if m, err := l.readMeta(o.Key); err == nil {
		info.ContentType, info.SHA256 = m.ContentType, m.SHA256
	}
	return info, nil
}

func (l *Local) readMeta(key string) (meta, error) {
	var m meta
	b, err := l.root.ReadFile(metaName(key))
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

// Put implements Backend: a server-side write of exactly size bytes.
func (l *Local) Put(_ context.Context, o Object, r io.Reader, size int64, contentType string) error {
	st, err := l.Stage(o.Key, r, contentType, size)
	if err != nil {
		return err
	}
	defer st.Discard()
	if st.Info.Size != size {
		return fmt.Errorf("storage: wrote %d bytes, want %d", st.Info.Size, size)
	}
	return st.Publish()
}

// Write stores an upload of at most limit bytes atomically (Stage, then Publish): a reader never sees a partial
// object. Files are 0640.
func (l *Local) Write(key string, r io.Reader, contentType string, limit int64) (Info, error) {
	st, err := l.Stage(key, r, contentType, limit)
	if err != nil {
		return Info{}, err
	}
	defer st.Discard()
	if err := st.Publish(); err != nil {
		return Info{}, err
	}
	return st.Info, nil
}

// Staged is an object written and synced under .tmp/ (data and sidecar) that is not visible yet: Publish renames
// it into place, Discard removes what was not published. The upload route publishes while it holds the pending
// row's lock, so a commit never verifies bytes a concurrent PUT is about to replace.
type Staged struct {
	l                *Local
	key              string
	dataTmp, metaTmp string
	// Info is the object as written: size, content type and sha256.
	Info Info
}

// Stage writes at most limit bytes of r and the sidecar to temporary files and syncs them; ErrTooLarge above
// limit. Nothing is visible at key until Publish.
func (l *Local) Stage(key string, r io.Reader, contentType string, limit int64) (*Staged, error) {
	if err := validLocalKey(key); err != nil {
		return nil, err
	}
	st := &Staged{l: l, key: key}
	ok := false
	defer func() {
		if !ok {
			st.Discard()
		}
	}()
	data, dataTmp, err := l.tempFile()
	if err != nil {
		return nil, err
	}
	st.dataTmp = dataTmp
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(data, h), io.LimitReader(r, limit+1))
	if err == nil && n > limit {
		err = ErrTooLarge
	}
	if err == nil {
		err = data.Sync()
	}
	if cerr := data.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	m := meta{ContentType: contentType, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	mb, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	mf, metaTmp, err := l.tempFile()
	if err != nil {
		return nil, err
	}
	st.metaTmp = metaTmp
	_, err = mf.Write(mb)
	if err == nil {
		err = mf.Sync()
	}
	if cerr := mf.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	st.Info = Info{Size: n, ContentType: contentType, SHA256: m.SHA256}
	ok = true
	return st, nil
}

// Publish renames the staged sidecar, then the data, into place and syncs both directories.
func (st *Staged) Publish() error {
	if st.dataTmp == "" {
		return errors.New("storage: staged object already published or discarded")
	}
	l, key := st.l, st.key
	for _, d := range []string{path.Dir(metaName(key)), path.Dir(dataName(key))} {
		if err := l.root.MkdirAll(d, dirMode); err != nil {
			return err
		}
	}
	if err := l.root.Rename(st.metaTmp, metaName(key)); err != nil {
		return err
	}
	st.metaTmp = ""
	if err := l.root.Rename(st.dataTmp, dataName(key)); err != nil {
		return err
	}
	st.dataTmp = ""
	l.syncDir(path.Dir(dataName(key)))
	l.syncDir(path.Dir(metaName(key)))
	return nil
}

// Discard removes the temporary files that were not published (safe to call more than once).
func (st *Staged) Discard() {
	for _, name := range []*string{&st.dataTmp, &st.metaTmp} {
		if *name != "" {
			_ = st.l.root.Remove(*name)
			*name = ""
		}
	}
}

// tempFile creates a new file under .tmp/ with mode 0640 (the umask cannot widen it; Chmod makes it exact).
func (l *Local) tempFile() (*os.File, string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, "", err
	}
	name := tmpDir + "/" + hex.EncodeToString(b[:])
	f, err := l.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return nil, "", err
	}
	if err := f.Chmod(fileMode); err != nil {
		_ = f.Close()
		_ = l.root.Remove(name)
		return nil, "", err
	}
	return f, name, nil
}

// syncDir makes a rename durable (best effort: some filesystems refuse fsync on a directory).
func (l *Local) syncDir(dir string) {
	if d, err := l.root.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// Delete implements Backend.
func (l *Local) Delete(_ context.Context, o Object) error {
	if err := validLocalKey(o.Key); err != nil {
		return err
	}
	return l.remove(o.Key)
}

func (l *Local) remove(key string) error {
	err := l.root.Remove(dataName(key))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := l.root.Remove(metaName(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Open returns the object for streaming: a regular file only (a directory, a missing key or anything else is
// ErrObjectNotFound) with its sidecar.
func (l *Local) Open(key string) (*os.File, Info, error) {
	if err := validLocalKey(key); err != nil {
		return nil, Info{}, ErrObjectNotFound
	}
	f, err := l.root.Open(dataName(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Info{}, ErrObjectNotFound
	}
	if err != nil {
		return nil, Info{}, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = ErrObjectNotFound
		}
		return nil, Info{}, err
	}
	info := Info{Size: fi.Size()}
	if m, err := l.readMeta(key); err == nil {
		info.ContentType, info.SHA256 = m.ContentType, m.SHA256
	}
	return f, info, nil
}

// SweepTemp removes partial uploads older than age (a crash between create and rename); storage.gc calls it.
func (l *Local) SweepTemp(age time.Duration) (int, error) {
	d, err := l.root.Open(tmpDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return 0, err
	}
	cut := l.now().Add(-age)
	n := 0
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || fi.IsDir() || fi.ModTime().After(cut) {
			continue
		}
		if l.root.Remove(tmpDir+"/"+e.Name()) == nil {
			n++
		}
	}
	return n, nil
}

// Check implements Backend: the directory takes a new file.
func (l *Local) Check(context.Context) error {
	f, name, err := l.tempFile()
	if err != nil {
		return err
	}
	_ = f.Close()
	return l.root.Remove(name)
}
