// Package gcptest is an in-process stand-in for the Google endpoints cmd/etl talks to: the OAuth2 token
// endpoint of the service-account JWT bearer grant (the assertion's signature is verified), the Firestore v1
// REST methods runQuery, listCollectionIds, get and patch (with preconditions and update masks) over an
// in-memory document table, and Cloud Storage media downloads. Everything is served through an
// http.RoundTripper, so tests never open a socket or reach Google. Test support only.
package gcptest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
)

const accessToken = "gcptest-access-token"

var (
	keyOnce sync.Once
	saKey   *rsa.PrivateKey
	keyErr  error
)

// Write is one patch the fake accepted.
type Write struct {
	Path   string
	Fields map[string]any
	Mask   []string
}

// Backend is the fake Google.
type Backend struct {
	t        testing.TB
	project  string
	database string
	mu       sync.Mutex
	docs     map[string]dump.Doc
	objects  map[string]object
	writes   []Write
	clock    time.Time
	tick     int64
	runQuery int
}

type object struct {
	data        []byte
	contentType string
}

// New returns an empty backend for project (database "(default)").
func New(t testing.TB, project string) *Backend {
	t.Helper()
	keyOnce.Do(func() { saKey, keyErr = rsa.GenerateKey(rand.Reader, 2048) })
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	return &Backend{t: t, project: project, database: "(default)", docs: map[string]dump.Doc{}, objects: map[string]object{},
		clock: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}

// Root is projects/{p}/databases/{d}/documents.
func (b *Backend) Root() string {
	return "projects/" + b.project + "/databases/" + b.database + "/documents"
}

// Put stores a document as is (its times included).
func (b *Backend) Put(d dump.Doc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.docs[d.Path] = d
}

// Doc returns a stored document.
func (b *Backend) Doc(path string) (dump.Doc, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.docs[path]
	return d, ok
}

// Writes returns the accepted patches in order.
func (b *Backend) Writes() []Write {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.writes)
}

// RunQueryCalls counts runQuery requests (paging tests).
func (b *Backend) RunQueryCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runQuery
}

// PutObject stores a Cloud Storage object.
func (b *Backend) PutObject(bucket, name, contentType string, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[bucket+"/"+name] = object{data: slices.Clone(data), contentType: contentType}
}

// ServiceAccount is a parsed key file whose key the token endpoint trusts.
func (b *Backend) ServiceAccount() *firebase.ServiceAccount {
	b.t.Helper()
	sa, err := firebase.ParseServiceAccount(b.ServiceAccountJSON())
	if err != nil {
		b.t.Fatal(err)
	}
	return sa
}

// ServiceAccountJSON is the key file (test-only key generated per test binary).
func (b *Backend) ServiceAccountJSON() []byte {
	b.t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(saKey)
	if err != nil {
		b.t.Fatal(err)
	}
	out, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": b.project, "private_key_id": "gcptest-key-1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "etl@" + b.project + ".iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		b.t.Fatal(err)
	}
	return out
}

// Client is an HTTP client served in-process; any other host fails.
func (b *Backend) Client() *http.Client { return &http.Client{Transport: transport{b}} }

// Tokens is a token source of the backend's service account.
func (b *Backend) Tokens(scopes ...string) oauth2.TokenSource {
	return b.ServiceAccount().TokenSource(b.Client(), scopes...)
}

// Firestore is a client of the backend.
func (b *Backend) Firestore() *gcp.Firestore {
	b.t.Helper()
	f, err := gcp.NewFirestore(b.project, "", b.Client(), b.Tokens(gcp.FirestoreScope))
	if err != nil {
		b.t.Fatal(err)
	}
	return f
}

// GCS is a Cloud Storage client of the backend.
func (b *Backend) GCS() *gcp.GCS {
	b.t.Helper()
	g, err := gcp.NewGCS(b.Client(), b.Tokens(gcp.StorageReadScope))
	if err != nil {
		b.t.Fatal(err)
	}
	return g
}

type transport struct{ b *Backend }

func (tr transport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	switch r.URL.Host {
	case "oauth2.googleapis.com":
		tr.b.token(w, r)
	case "firestore.googleapis.com":
		if !authorized(r) {
			apiError(w, http.StatusUnauthorized, "UNAUTHENTICATED")
		} else {
			tr.b.firestore(w, r)
		}
	case "storage.googleapis.com":
		if !authorized(r) {
			apiError(w, http.StatusUnauthorized, "UNAUTHENTICATED")
		} else {
			tr.b.storage(w, r)
		}
	default:
		return nil, fmt.Errorf("gcptest: no route to %s", r.URL.Host)
	}
	res := w.Result()
	res.Request = r
	return res, nil
}

func authorized(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+accessToken }

func apiError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": status, "status": code, "message": code}})
}

func (b *Backend) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		apiError(w, http.StatusBadRequest, "INVALID_GRANT")
		return
	}
	_, err := jwt.Parse(r.Form.Get("assertion"), func(*jwt.Token) (any, error) { return &saKey.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}))
	if err != nil {
		apiError(w, http.StatusBadRequest, "INVALID_GRANT")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": accessToken, "token_type": "Bearer", "expires_in": 3600})
}

func (b *Backend) now() time.Time {
	b.tick++
	return b.clock.Add(time.Duration(b.tick) * time.Microsecond)
}

func (b *Backend) firestore(w http.ResponseWriter, r *http.Request) {
	prefix := "/v1/" + b.Root()
	p := r.URL.EscapedPath()
	if !strings.HasPrefix(p, prefix) {
		apiError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	rest := strings.TrimPrefix(p, prefix)
	switch {
	case r.Method == http.MethodPost && rest == ":runQuery":
		b.handleRunQuery(w, r)
	case r.Method == http.MethodPost && rest == ":listCollectionIds":
		b.handleListCollections(w)
	case strings.HasPrefix(rest, "/") && (r.Method == http.MethodGet || r.Method == http.MethodPatch):
		path, err := url.PathUnescape(rest[1:])
		if err != nil {
			apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
			return
		}
		if r.Method == http.MethodGet {
			b.handleGet(w, path)
		} else {
			b.handlePatch(w, r, path)
		}
	default:
		apiError(w, http.StatusNotFound, "NOT_FOUND")
	}
}

func (b *Backend) restDoc(d dump.Doc) map[string]any {
	fields := map[string]any{}
	for k, v := range d.Fields {
		ev, err := gcp.EncodeValue(b.Root(), v)
		if err != nil {
			b.t.Errorf("gcptest: encode %s.%s: %v", d.Path, k, err)
		}
		fields[k] = ev
	}
	return map[string]any{"name": b.Root() + "/" + d.Path, "fields": fields,
		"createTime": d.CreateTime.UTC().Format(time.RFC3339Nano), "updateTime": d.UpdateTime.UTC().Format(time.RFC3339Nano)}
}

func (b *Backend) handleRunQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StructuredQuery struct {
			From []struct {
				CollectionID   string `json:"collectionId"`
				AllDescendants bool   `json:"allDescendants"`
			} `json:"from"`
			Limit   int `json:"limit"`
			StartAt *struct {
				Values []struct {
					ReferenceValue string `json:"referenceValue"`
				} `json:"values"`
				Before bool `json:"before"`
			} `json:"startAt"`
		} `json:"structuredQuery"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.StructuredQuery.From) != 1 {
		apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
		return
	}
	q := req.StructuredQuery
	from := q.From[0]
	after := ""
	if q.StartAt != nil && len(q.StartAt.Values) == 1 {
		after = strings.TrimPrefix(q.StartAt.Values[0].ReferenceValue, b.Root()+"/")
	}
	b.mu.Lock()
	b.runQuery++
	var paths []string
	for path, d := range b.docs {
		if d.Collection() != from.CollectionID || (!from.AllDescendants && d.ParentPath() != "") {
			continue
		}
		if after != "" && path <= after {
			continue
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	if q.Limit > 0 && len(paths) > q.Limit {
		paths = paths[:q.Limit]
	}
	out := []any{}
	for _, path := range paths {
		out = append(out, map[string]any{"document": b.restDoc(b.docs[path]), "readTime": b.clock.Format(time.RFC3339Nano)})
	}
	b.mu.Unlock()
	if len(out) == 0 {
		out = append(out, map[string]any{"readTime": b.clock.Format(time.RFC3339Nano)})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (b *Backend) handleListCollections(w http.ResponseWriter) {
	b.mu.Lock()
	seen := map[string]bool{}
	for _, d := range b.docs {
		if d.ParentPath() == "" {
			seen[d.Collection()] = true
		}
	}
	b.mu.Unlock()
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"collectionIds": ids})
}

func (b *Backend) handleGet(w http.ResponseWriter, path string) {
	b.mu.Lock()
	d, ok := b.docs[path]
	b.mu.Unlock()
	if !ok {
		apiError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(b.restDoc(d))
}

func (b *Backend) handlePatch(w http.ResponseWriter, r *http.Request, path string) {
	var req struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
		return
	}
	fields := map[string]any{}
	for k, raw := range req.Fields {
		v, err := gcp.DecodeValue(b.Root(), raw)
		if err != nil {
			apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
			return
		}
		fields[k] = v
	}
	q := r.URL.Query()
	mask := q["updateMask.fieldPaths"]
	for i, m := range mask {
		if strings.HasPrefix(m, "`") {
			mask[i] = strings.NewReplacer("\\`", "`", `\\`, `\`).Replace(strings.Trim(m, "`"))
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cur, exists := b.docs[path]
	if e := q.Get("currentDocument.exists"); e != "" {
		if want, _ := strconv.ParseBool(e); want != exists {
			apiError(w, http.StatusBadRequest, "FAILED_PRECONDITION")
			return
		}
	}
	if ut := q.Get("currentDocument.updateTime"); ut != "" {
		t, err := time.Parse(time.RFC3339Nano, ut)
		if err != nil || !exists || !cur.UpdateTime.Equal(t) {
			apiError(w, http.StatusBadRequest, "FAILED_PRECONDITION")
			return
		}
	}
	next := dump.Doc{Path: path, Fields: map[string]any{}}
	parts := strings.Split(path, "/")
	next.ID = parts[len(parts)-1]
	now := b.now()
	if exists {
		next.CreateTime = cur.CreateTime
		if len(mask) > 0 {
			for k, v := range cur.Fields {
				next.Fields[k] = v
			}
		}
	} else {
		next.CreateTime = now
	}
	if len(mask) == 0 {
		next.Fields = fields
	} else {
		for _, m := range mask {
			if v, ok := fields[m]; ok {
				next.Fields[m] = v
			} else {
				delete(next.Fields, m)
			}
		}
	}
	next.UpdateTime = now
	b.docs[path] = next
	b.writes = append(b.writes, Write{Path: path, Fields: fields, Mask: slices.Clone(mask)})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(b.restDoc(next))
}

func (b *Backend) storage(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/storage/v1/b/")
	bucket, obj, ok2 := strings.Cut(rest, "/o/")
	if !ok || !ok2 || r.Method != http.MethodGet || r.URL.Query().Get("alt") != "media" {
		apiError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	bucket, _ = url.PathUnescape(bucket)
	name, err := url.PathUnescape(obj)
	if err != nil {
		apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
		return
	}
	b.mu.Lock()
	o, found := b.objects[bucket+"/"+name]
	b.mu.Unlock()
	if !found {
		apiError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	w.Header().Set("Content-Type", o.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(o.data)))
	_, _ = io.Copy(w, strings.NewReader(string(o.data)))
}
