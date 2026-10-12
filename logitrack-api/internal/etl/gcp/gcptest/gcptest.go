// Package gcptest is an in-process stand-in for the Google endpoints cmd/etl talks to: the OAuth2 token
// endpoint of the service-account JWT bearer grant (the assertion's signature is verified), the Firestore v1
// REST methods runQuery (by name, or the listeners' timestamp filter field >= since ordered by the field and the
// name, T29), listCollectionIds, get (at a read time too) and patch (with preconditions and update masks) over an
// in-memory document table with deletes (Delete, and Rename for renameTripRecord's copy-and-delete batch), and Cloud
// Storage media downloads. Everything is served through an
// http.RoundTripper, so tests never open a socket or reach Google. Test support only.
//
// Time is logical: every Put and accepted patch is one tick after the backend's clock, a runQuery answers with
// the readTime of the latest tick, and a runQuery that sends a readTime sees every document as it was at that
// tick (the versions are kept), the way Firestore serves a consistent read at a past readTime.
package gcptest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"maps"
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
	versions map[string][]version
	objects  map[string]object
	writes   []Write
	clock    time.Time
	tick     int64
	runQuery int
	onQuery  func(call int)
}

// version is a document as one tick left it; deleted marks the tick that removed it.
type version struct {
	tick    int64
	doc     dump.Doc
	deleted bool
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
	return &Backend{t: t, project: project, database: "(default)", docs: map[string]dump.Doc{}, versions: map[string][]version{},
		objects: map[string]object{}, clock: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}

// Root is projects/{p}/databases/{d}/documents.
func (b *Backend) Root() string {
	return "projects/" + b.project + "/databases/" + b.database + "/documents"
}

// Put stores a document as is (its times included), as a new version one tick later.
func (b *Backend) Put(d dump.Doc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now() // every write is one tick
	b.store(d)
}

// Set writes fields as the whole document at path the way a client's set() does (the 3.x app, a legacy web page): the
// server stamps the update time, and the create time of a new document, with the next tick.
func (b *Backend) Set(path string, fields map[string]any) dump.Doc {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	parts := strings.Split(path, "/")
	d := dump.Doc{ID: parts[len(parts)-1], Path: path, CreateTime: now, UpdateTime: now, Fields: maps.Clone(fields)}
	if cur, ok := b.docs[path]; ok {
		d.CreateTime = cur.CreateTime
	}
	b.store(d)
	return d
}

// Merge is a client's set(..., merge: true) / update(): the given fields replace the stored ones, a nil value deletes
// its field (FieldValue.delete()), the other fields stay; the document is created when missing.
func (b *Backend) Merge(path string, fields map[string]any) dump.Doc {
	b.mu.Lock()
	cur, ok := b.docs[path]
	b.mu.Unlock()
	next := map[string]any{}
	if ok {
		next = maps.Clone(cur.Fields)
	}
	for k, v := range fields {
		if v == nil {
			delete(next, k)
			continue
		}
		next[k] = v
	}
	return b.Set(path, next)
}

// store records d as the current document and as the version of the current tick.
func (b *Backend) store(d dump.Doc) {
	b.docs[d.Path] = d
	b.versions[d.Path] = append(b.versions[d.Path], version{tick: b.tick, doc: d})
}

// Delete removes a document (a client's delete()) as a new tick; a later read at an earlier readTime still sees it.
func (b *Backend) Delete(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now()
	b.remove(path)
}

func (b *Backend) remove(path string) {
	if _, ok := b.docs[path]; !ok {
		return
	}
	delete(b.docs, path)
	b.versions[path] = append(b.versions[path], version{tick: b.tick, deleted: true})
}

// Rename is renameTripRecord's batch (functions/src/renameTripRecord.ts): in one tick the document at oldPath is
// copied to newPath with the extra fields merged over it, and oldPath is deleted. It returns the new document.
func (b *Backend) Rename(oldPath, newPath string, extra map[string]any) dump.Doc {
	b.mu.Lock()
	defer b.mu.Unlock()
	cur, ok := b.docs[oldPath]
	if !ok {
		b.t.Fatalf("gcptest: rename of a missing document %s", oldPath)
	}
	now := b.now()
	fields := maps.Clone(cur.Fields)
	for k, v := range extra {
		fields[k] = v
	}
	parts := strings.Split(newPath, "/")
	d := dump.Doc{ID: parts[len(parts)-1], Path: newPath, CreateTime: now, UpdateTime: now, Fields: fields}
	b.store(d)
	b.remove(oldPath)
	return d
}

// visibleAt is every document as tick at left it (the caller holds the lock).
func (b *Backend) visibleAt(at int64) map[string]dump.Doc {
	visible := map[string]dump.Doc{}
	for path, vs := range b.versions {
		for i := len(vs) - 1; i >= 0; i-- {
			if vs[i].tick <= at {
				if !vs[i].deleted {
					visible[path] = vs[i].doc
				}
				break
			}
		}
	}
	return visible
}

// tickOf turns a readTime into the backend's tick (ok false for an unparsable or earlier time).
func (b *Backend) tickOf(readTime string) (int64, bool) {
	rt, err := time.Parse(time.RFC3339Nano, readTime)
	if err != nil || rt.Before(b.clock) {
		return 0, false
	}
	return int64(rt.Sub(b.clock) / time.Microsecond), true
}

// OnRunQuery calls fn after each runQuery is answered (call counts from 1), outside the backend's lock, so a test
// can write documents between the pages of one read.
func (b *Backend) OnRunQuery(fn func(call int)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onQuery = fn
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
	case strings.HasPrefix(rest, "/") && r.Method == http.MethodDelete:
		path, err := url.PathUnescape(rest[1:])
		if err != nil {
			apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
			return
		}
		b.handleDelete(w, r, path)
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
			b.handleGet(w, r, path)
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

// queryValue is a Firestore REST Value of a filter or a cursor (the kinds the ETL client sends).
type queryValue struct {
	ReferenceValue *string `json:"referenceValue"`
	TimestampValue *string `json:"timestampValue"`
}

func (b *Backend) handleRunQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReadTime        string `json:"readTime"`
		StructuredQuery struct {
			From []struct {
				CollectionID   string `json:"collectionId"`
				AllDescendants bool   `json:"allDescendants"`
			} `json:"from"`
			Where *struct {
				FieldFilter struct {
					Field struct {
						FieldPath string `json:"fieldPath"`
					} `json:"field"`
					Op    string     `json:"op"`
					Value queryValue `json:"value"`
				} `json:"fieldFilter"`
			} `json:"where"`
			OrderBy []struct {
				Field struct {
					FieldPath string `json:"fieldPath"`
				} `json:"field"`
			} `json:"orderBy"`
			Limit   int `json:"limit"`
			StartAt *struct {
				Values []queryValue `json:"values"`
				Before bool         `json:"before"`
			} `json:"startAt"`
		} `json:"structuredQuery"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.StructuredQuery.From) != 1 {
		apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
		return
	}
	q := req.StructuredQuery
	from := q.From[0]
	// A where clause is the listeners' timestamp filter (gcp.Firestore.Changed): field >= since, ordered by the field
	// and then by name, the cursor (timestamp, name). Without one the query is ordered by name, the cursor a name.
	field := ""
	var since time.Time
	if q.Where != nil {
		ff := q.Where.FieldFilter
		field = strings.Trim(ff.Field.FieldPath, "`")
		if ff.Op != "GREATER_THAN_OR_EQUAL" || ff.Value.TimestampValue == nil || len(q.OrderBy) != 2 ||
			strings.Trim(q.OrderBy[0].Field.FieldPath, "`") != field || q.OrderBy[1].Field.FieldPath != "__name__" {
			apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
			return
		}
		var err error
		if since, err = time.Parse(time.RFC3339Nano, *ff.Value.TimestampValue); err != nil {
			apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
			return
		}
	}
	afterName := ""
	var afterTS time.Time
	if q.StartAt != nil {
		vals := q.StartAt.Values
		switch {
		case field == "" && len(vals) == 1 && vals[0].ReferenceValue != nil:
			afterName = strings.TrimPrefix(*vals[0].ReferenceValue, b.Root()+"/")
		case field != "" && len(vals) == 2 && vals[0].TimestampValue != nil && vals[1].ReferenceValue != nil:
			t, err := time.Parse(time.RFC3339Nano, *vals[0].TimestampValue)
			if err != nil {
				apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
				return
			}
			afterTS, afterName = t, strings.TrimPrefix(*vals[1].ReferenceValue, b.Root()+"/")
		default:
			apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
			return
		}
	}
	b.mu.Lock()
	b.runQuery++
	call, hook := b.runQuery, b.onQuery
	at := b.tick
	if req.ReadTime != "" {
		var ok bool
		if at, ok = b.tickOf(req.ReadTime); !ok {
			b.mu.Unlock()
			apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
			return
		}
	}
	readTime := b.clock.Add(time.Duration(at) * time.Microsecond).Format(time.RFC3339Nano)
	visible := b.visibleAt(at)
	type hit struct {
		path string
		ts   time.Time
	}
	var hits []hit
	for path, d := range visible {
		if d.Collection() != from.CollectionID || (!from.AllDescendants && d.ParentPath() != "") {
			continue
		}
		h := hit{path: path}
		if field != "" {
			ts, ok := d.Fields[field].(dump.Timestamp)
			if !ok || ts.Before(since) {
				continue
			}
			h.ts = ts.UTC()
			if q.StartAt != nil && (h.ts.Before(afterTS) || (h.ts.Equal(afterTS) && path <= afterName)) {
				continue
			}
		} else if afterName != "" && path <= afterName {
			continue
		}
		hits = append(hits, h)
	}
	slices.SortFunc(hits, func(a, b hit) int {
		if c := a.ts.Compare(b.ts); c != 0 {
			return c
		}
		return strings.Compare(a.path, b.path)
	})
	if q.Limit > 0 && len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	out := []any{}
	for _, h := range hits {
		out = append(out, map[string]any{"document": b.restDoc(visible[h.path]), "readTime": readTime})
	}
	b.mu.Unlock()
	if len(out) == 0 {
		out = append(out, map[string]any{"readTime": readTime})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
	if hook != nil {
		hook(call)
	}
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

func (b *Backend) handleGet(w http.ResponseWriter, r *http.Request, path string) {
	b.mu.Lock()
	d, ok := b.docs[path]
	if rt := r.URL.Query().Get("readTime"); rt != "" {
		at, valid := b.tickOf(rt)
		if !valid {
			b.mu.Unlock()
			apiError(w, http.StatusBadRequest, "INVALID_ARGUMENT")
			return
		}
		d, ok = b.visibleAt(at)[path]
	}
	b.mu.Unlock()
	if !ok {
		apiError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(b.restDoc(d))
}

// handleDelete is documents.delete (T45, the holidays projection): the precondition first (exists, updateTime), then the
// document is removed as a new tick; deleting a missing document without a precondition succeeds, as Firestore does.
func (b *Backend) handleDelete(w http.ResponseWriter, r *http.Request, path string) {
	q := r.URL.Query()
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
	if exists {
		b.now()
		b.remove(path)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
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
	b.store(next)
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
