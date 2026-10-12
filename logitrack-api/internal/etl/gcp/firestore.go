// Package gcp is the ETL's narrow client of Google Cloud (main spec §13.1, §13.6, §13.10): the Firestore
// v1 REST API (read every document of a collection or collection group, read one document, write one
// document with a precondition) and the Cloud Storage JSON API (download one object). No Admin SDK is
// linked, like the Firebase bridge (internal/auth/firebase) and the FCM sender (internal/platform/push):
// requests carry an OAuth2 token of the GOOGLE_APPLICATION_CREDENTIALS service account, signed locally.
// Tests use gcptest, an in-process stand-in; nothing here is ever pointed at production by a test.
package gcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// Scopes and endpoints.
const (
	FirestoreScope   = "https://www.googleapis.com/auth/datastore"
	StorageReadScope = "https://www.googleapis.com/auth/devstorage.read_only"
	FirestoreURL     = "https://firestore.googleapis.com/v1"
	StorageURL       = "https://storage.googleapis.com/storage/v1"
)

const (
	httpTimeout      = 60 * time.Second
	pageSize         = 300
	maxResponseBytes = 256 << 20
)

// ErrNotFound: the document or object does not exist.
var ErrNotFound = errors.New("gcp: not found")

// ErrPrecondition: a write's precondition failed (the document changed since it was read).
var ErrPrecondition = errors.New("gcp: precondition failed")

// errNoDocument marks a 404 whose message is Firestore's "No document to update" (or delete): a write with
// currentDocument.exists=true named a document that does not exist. Real Firestore answers that precondition with
// NOT_FOUND, not FAILED_PRECONDITION; Commit reports it as ErrPrecondition. Any other 404 (a missing database or
// project) stays ErrNotFound.
var errNoDocument = errors.New("gcp: no such document")

// APIError is an error answer of a Google API; Code is Google's status ("PERMISSION_DENIED"), never body text.
type APIError struct {
	Op     string
	Status int
	Code   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("gcp: %s: HTTP %d %s", e.Op, e.Status, e.Code)
}

var projectID = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// Firestore reads and writes documents of one database through the REST API.
type Firestore struct {
	root   string // projects/{p}/databases/{d}/documents
	base   string // FirestoreURL/{root}
	client *http.Client
	tokens oauth2.TokenSource
}

// NewFirestore builds the client without I/O. database "" is "(default)".
func NewFirestore(project, database string, client *http.Client, tokens oauth2.TokenSource) (*Firestore, error) {
	if !projectID.MatchString(project) {
		return nil, errors.New("gcp: ETL_FIRESTORE_PROJECT_ID is not a project id")
	}
	if database == "" {
		database = "(default)"
	}
	if database != "(default)" && !regexp.MustCompile(`^[a-z][a-z0-9-]{3,62}$`).MatchString(database) {
		return nil, errors.New("gcp: FIRESTORE_DATABASE_ID is not a database id")
	}
	if tokens == nil {
		return nil, errors.New("gcp: a token source is required")
	}
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	root := "projects/" + project + "/databases/" + database + "/documents"
	return &Firestore{root: root, base: FirestoreURL + "/" + root, client: client, tokens: tokens}, nil
}

// Root is projects/{p}/databases/{d}/documents.
func (f *Firestore) Root() string { return f.root }

// CollectionIDs lists the top-level collection ids.
func (f *Firestore) CollectionIDs(ctx context.Context) ([]string, error) {
	var ids []string
	token := ""
	for {
		var out struct {
			CollectionIDs []string `json:"collectionIds"`
			NextPageToken string   `json:"nextPageToken"`
		}
		body := map[string]any{"pageSize": pageSize}
		if token != "" {
			body["pageToken"] = token
		}
		if err := do(ctx, f.client, f.tokens, "listCollectionIds", http.MethodPost, f.base+":listCollectionIds", body, &out); err != nil {
			return nil, err
		}
		ids = append(ids, out.CollectionIDs...)
		if out.NextPageToken == "" {
			slices.Sort(ids)
			return ids, nil
		}
		token = out.NextPageToken
	}
}

// Documents reads every document of a top-level collection (group false) or of every collection with that id
// at any depth (group true, a collection-group query: chats/*/messages, drivers/*/mobile_installations),
// ordered by document name and paged with a cursor, so a long read never repeats or skips a document. Every page
// after the first is read at the readTime of the first response, so the collection is one consistent snapshot:
// a write that commits during the read is in no page, and its updateTime is after every updateTime of the dump,
// so no --since=watermark load built on this dump can filter it out. Firestore accepts a past readTime for one
// hour (seven days with point-in-time recovery); a read that runs longer fails instead of mixing instants.
func (f *Firestore) Documents(ctx context.Context, collection string, group bool) ([]dump.Doc, error) {
	docs, _, err := f.DocumentsAt(ctx, collection, group, time.Time{})
	return docs, err
}

// DocumentsAt is Documents at the read time at (zero: the first response's), and returns the read time it used, so
// a mirror sweep reads every collection at one instant (T29: a trip renamed in the same batch that re-points its
// incident reports is seen renamed in both collections, or in neither).
func (f *Firestore) DocumentsAt(ctx context.Context, collection string, group bool, at time.Time) ([]dump.Doc, time.Time, error) {
	q := map[string]any{
		"from":    []any{map[string]any{"collectionId": collection, "allDescendants": group}},
		"orderBy": []any{map[string]any{"field": map[string]any{"fieldPath": "__name__"}, "direction": "ASCENDING"}},
		"limit":   pageSize,
	}
	return f.query(ctx, collection, group, q, at, func(d dump.Doc) []any {
		return []any{map[string]any{"referenceValue": f.root + "/" + d.Path}}
	})
}

// Changed reads the documents of a top-level collection whose timestamp field is at or after since, ordered by that
// field and then by name, paged with a cursor, at one read time (at, or the first response's), which it returns. A
// document whose field is missing or holds no timestamp is not returned: the mirror's sweep, which reads whole
// collections, applies it (main spec §13.7, T29: the listeners poll this query between sweeps). The query needs only
// the single-field index Firestore keeps by default.
func (f *Firestore) Changed(ctx context.Context, collection, field string, since, at time.Time) ([]dump.Doc, time.Time, error) {
	return f.changed(ctx, collection, false, field, since, at)
}

// ChangedGroup is Changed over a collection group (every collection of that id at any depth: chats/*/messages, T47).
// Unlike Changed it needs a collection-group single-field index on the field, which Firestore does not keep by
// default (an index exemption of the project; without it the query is refused and the sweep alone applies the group).
func (f *Firestore) ChangedGroup(ctx context.Context, collection, field string, since, at time.Time) ([]dump.Doc, time.Time, error) {
	return f.changed(ctx, collection, true, field, since, at)
}

func (f *Firestore) changed(ctx context.Context, collection string, group bool, field string, since, at time.Time) ([]dump.Doc, time.Time, error) {
	if field == "" || since.IsZero() {
		return nil, time.Time{}, errors.New("gcp: Changed needs a field and an instant")
	}
	fp := quoteFieldPath(field)
	q := map[string]any{
		"from": []any{map[string]any{"collectionId": collection, "allDescendants": group}},
		"where": map[string]any{"fieldFilter": map[string]any{"field": map[string]any{"fieldPath": fp}, "op": "GREATER_THAN_OR_EQUAL",
			"value": map[string]any{"timestampValue": since.UTC().Format(time.RFC3339Nano)}}},
		"orderBy": []any{
			map[string]any{"field": map[string]any{"fieldPath": fp}, "direction": "ASCENDING"},
			map[string]any{"field": map[string]any{"fieldPath": "__name__"}, "direction": "ASCENDING"},
		},
		"limit": pageSize,
	}
	var bad error
	docs, read, err := f.query(ctx, collection, group, q, at, func(d dump.Doc) []any {
		ts, ok := d.Fields[field].(dump.Timestamp)
		if !ok {
			bad = fmt.Errorf("gcp: runQuery %s answered %s without a timestamp %s", collection, d.Path, field)
		}
		return []any{map[string]any{"timestampValue": ts.UTC().Format(time.RFC3339Nano)},
			map[string]any{"referenceValue": f.root + "/" + d.Path}}
	})
	if err == nil && bad != nil {
		err = bad
	}
	return docs, read, err
}

// Where reads the documents of a top-level collection whose field holds the string value, ordered by name and paged
// with a name cursor, at one read time. An equality filter needs only the single-field index Firestore keeps by default
// (the chat write-back finds a driver's chat documents by driverId with it, T47).
func (f *Firestore) Where(ctx context.Context, collection, field, value string) ([]dump.Doc, error) {
	if field == "" {
		return nil, errors.New("gcp: Where needs a field")
	}
	q := map[string]any{
		"from": []any{map[string]any{"collectionId": collection, "allDescendants": false}},
		"where": map[string]any{"fieldFilter": map[string]any{"field": map[string]any{"fieldPath": quoteFieldPath(field)}, "op": "EQUAL",
			"value": map[string]any{"stringValue": value}}},
		"orderBy": []any{map[string]any{"field": map[string]any{"fieldPath": "__name__"}, "direction": "ASCENDING"}},
		"limit":   pageSize,
	}
	docs, _, err := f.query(ctx, collection, false, q, time.Time{}, func(d dump.Doc) []any {
		return []any{map[string]any{"referenceValue": f.root + "/" + d.Path}}
	})
	return docs, err
}

// query pages one structured query at one read time; cursor gives the startAt values after a page's last document.
func (f *Firestore) query(ctx context.Context, collection string, group bool, q map[string]any, at time.Time,
	cursor func(dump.Doc) []any) ([]dump.Doc, time.Time, error) {
	var docs []dump.Doc
	var last *dump.Doc
	readTime := ""
	if !at.IsZero() {
		readTime = at.UTC().Format(time.RFC3339Nano)
	}
	for {
		if last != nil {
			q["startAt"] = map[string]any{"values": cursor(*last), "before": false}
		}
		body := map[string]any{"structuredQuery": q}
		if readTime != "" {
			body["readTime"] = readTime
		}
		var out []struct {
			Document *restDoc `json:"document"`
			ReadTime string   `json:"readTime"`
		}
		if err := do(ctx, f.client, f.tokens, "runQuery", http.MethodPost, f.base+":runQuery", body, &out); err != nil {
			return nil, time.Time{}, err
		}
		n := 0
		for _, r := range out {
			if readTime == "" && r.ReadTime != "" {
				if _, err := time.Parse(time.RFC3339Nano, r.ReadTime); err != nil {
					return nil, time.Time{}, fmt.Errorf("gcp: runQuery %s: readTime %q: %w", collection, r.ReadTime, err)
				}
				readTime = r.ReadTime
			}
			if r.Document == nil {
				continue // the readTime-only element of an empty page
			}
			n++
			d, err := f.decodeDoc(*r.Document)
			if err != nil {
				return nil, time.Time{}, err
			}
			if d.Collection() != collection || (!group && d.ParentPath() != "") {
				return nil, time.Time{}, fmt.Errorf("gcp: runQuery %s answered %s", collection, d.Path)
			}
			docs = append(docs, d)
			last = &docs[len(docs)-1]
		}
		if n < pageSize {
			if readTime == "" {
				return docs, time.Time{}, nil
			}
			rt, err := time.Parse(time.RFC3339Nano, readTime)
			if err != nil {
				return nil, time.Time{}, fmt.Errorf("gcp: runQuery %s: readTime %q: %w", collection, readTime, err)
			}
			return docs, rt.UTC(), nil
		}
		if readTime == "" {
			return nil, time.Time{}, fmt.Errorf("gcp: runQuery %s: a full page without readTime cannot be continued as one snapshot", collection)
		}
	}
}

// Lookup reads one document at the read time at (zero: now); ok false when it does not exist then.
func (f *Firestore) Lookup(ctx context.Context, path string, at time.Time) (dump.Doc, bool, error) {
	u := f.base + "/" + escapePath(path)
	if !at.IsZero() {
		u += "?" + url.Values{"readTime": {at.UTC().Format(time.RFC3339Nano)}}.Encode()
	}
	var out restDoc
	err := do(ctx, f.client, f.tokens, "get", http.MethodGet, u, nil, &out)
	if errors.Is(err, ErrNotFound) {
		return dump.Doc{}, false, nil
	}
	if err != nil {
		return dump.Doc{}, false, err
	}
	d, err := f.decodeDoc(out)
	return d, err == nil, err
}

// Get reads one document; ErrNotFound when it does not exist.
func (f *Firestore) Get(ctx context.Context, path string) (dump.Doc, error) {
	var out restDoc
	if err := do(ctx, f.client, f.tokens, "get", http.MethodGet, f.base+"/"+escapePath(path), nil, &out); err != nil {
		return dump.Doc{}, err
	}
	return f.decodeDoc(out)
}

// Delete removes one document under the precondition (T45: the holidays projection deletes the document of a holiday
// deleted in PostgreSQL). A document that does not exist is no error without a precondition, as Firestore answers.
func (f *Firestore) Delete(ctx context.Context, path string, pre Precondition) error {
	q := url.Values{}
	switch {
	case pre.Exists != nil:
		q.Set("currentDocument.exists", strconv.FormatBool(*pre.Exists))
	case !pre.UpdateTime.IsZero():
		q.Set("currentDocument.updateTime", pre.UpdateTime.UTC().Format(time.RFC3339Nano))
	}
	u := f.base + "/" + escapePath(path)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return do(ctx, f.client, f.tokens, "delete", http.MethodDelete, u, nil, nil)
}

// Precondition guards a write: Exists false creates only; a non-zero UpdateTime writes only while the
// document still carries it.
type Precondition struct {
	Exists     *bool
	UpdateTime time.Time
}

// Patch writes the masked fields of one document (a field in mask but not in fields is deleted, fields
// outside mask are kept) under the precondition, and returns the stored document.
func (f *Firestore) Patch(ctx context.Context, path string, fields map[string]any, mask []string, pre Precondition) (dump.Doc, error) {
	enc := map[string]any{}
	for k, v := range fields {
		ev, err := EncodeValue(f.root, v)
		if err != nil {
			return dump.Doc{}, fmt.Errorf("gcp: %s.%s: %w", path, k, err)
		}
		enc[k] = ev
	}
	q := url.Values{}
	for _, m := range mask {
		q.Add("updateMask.fieldPaths", quoteFieldPath(m))
	}
	switch {
	case pre.Exists != nil:
		q.Set("currentDocument.exists", strconv.FormatBool(*pre.Exists))
	case !pre.UpdateTime.IsZero():
		q.Set("currentDocument.updateTime", pre.UpdateTime.UTC().Format(time.RFC3339Nano))
	}
	var out restDoc
	err := do(ctx, f.client, f.tokens, "patch", http.MethodPatch, f.base+"/"+escapePath(path)+"?"+q.Encode(), map[string]any{"fields": enc}, &out)
	if err != nil {
		return dump.Doc{}, err
	}
	return f.decodeDoc(out)
}

// Write is one write of a Commit: Delete removes the document at Path; otherwise Fields are written under Mask (a
// field in Mask but not in Fields is deleted, fields outside Mask are kept; an empty Mask replaces the document).
// Pre guards the write.
type Write struct {
	Path   string
	Fields map[string]any
	Mask   []string
	Delete bool
	Pre    Precondition
}

// Commit applies writes atomically (documents:commit: all of them or none) and returns the commit time, which is the
// update time of every document it wrote. A failed precondition is ErrPrecondition and nothing is written.
func (f *Firestore) Commit(ctx context.Context, writes []Write) (time.Time, error) {
	if len(writes) == 0 {
		return time.Time{}, errors.New("gcp: commit without writes")
	}
	body := make([]map[string]any, 0, len(writes))
	for _, w := range writes {
		name := f.root + "/" + w.Path
		var out map[string]any
		if w.Delete {
			out = map[string]any{"delete": name}
		} else {
			enc := map[string]any{}
			for k, v := range w.Fields {
				ev, err := EncodeValue(f.root, v)
				if err != nil {
					return time.Time{}, fmt.Errorf("gcp: %s.%s: %w", w.Path, k, err)
				}
				enc[k] = ev
			}
			out = map[string]any{"update": map[string]any{"name": name, "fields": enc}}
			if len(w.Mask) > 0 {
				paths := make([]string, len(w.Mask))
				for i, m := range w.Mask {
					paths[i] = quoteFieldPath(m)
				}
				out["updateMask"] = map[string]any{"fieldPaths": paths}
			}
		}
		switch {
		case w.Pre.Exists != nil:
			out["currentDocument"] = map[string]any{"exists": *w.Pre.Exists}
		case !w.Pre.UpdateTime.IsZero():
			out["currentDocument"] = map[string]any{"updateTime": w.Pre.UpdateTime.UTC().Format(time.RFC3339Nano)}
		}
		body = append(body, out)
	}
	var res struct {
		CommitTime time.Time `json:"commitTime"`
	}
	if err := do(ctx, f.client, f.tokens, "commit", http.MethodPost, f.base+":commit", map[string]any{"writes": body}, &res); err != nil {
		if errors.Is(err, errNoDocument) && slices.ContainsFunc(writes, func(w Write) bool {
			return (w.Pre.Exists != nil && *w.Pre.Exists) || !w.Pre.UpdateTime.IsZero()
		}) {
			return time.Time{}, fmt.Errorf("%w: commit: a document a write names does not exist", ErrPrecondition)
		}
		return time.Time{}, err
	}
	if res.CommitTime.IsZero() {
		return time.Time{}, errors.New("gcp: commit: the answer has no commit time")
	}
	return res.CommitTime, nil
}

type restDoc struct {
	Name       string                     `json:"name"`
	Fields     map[string]json.RawMessage `json:"fields"`
	CreateTime time.Time                  `json:"createTime"`
	UpdateTime time.Time                  `json:"updateTime"`
}

func (f *Firestore) decodeDoc(r restDoc) (dump.Doc, error) {
	path, ok := strings.CutPrefix(r.Name, f.root+"/")
	if !ok {
		return dump.Doc{}, fmt.Errorf("gcp: document name %q is outside %s", r.Name, f.root)
	}
	fields := make(map[string]any, len(r.Fields))
	for k, raw := range r.Fields {
		v, err := DecodeValue(f.root, raw)
		if err != nil {
			return dump.Doc{}, fmt.Errorf("gcp: %s.%s: %w", path, k, err)
		}
		fields[k] = v
	}
	parts := strings.Split(path, "/")
	return dump.Doc{ID: parts[len(parts)-1], Path: path, CreateTime: r.CreateTime.UTC(), UpdateTime: r.UpdateTime.UTC(), Fields: fields}, nil
}

// DecodeValue turns a Firestore REST Value into a dump value (references become paths below root).
func DecodeValue(root string, raw json.RawMessage) (any, error) {
	var v map[string]json.RawMessage
	if err := json.Unmarshal(raw, &v); err != nil || len(v) != 1 {
		return nil, errors.New("not a Firestore Value")
	}
	for kind, body := range v {
		switch kind {
		case "nullValue":
			return nil, nil
		case "booleanValue":
			var b bool
			err := json.Unmarshal(body, &b)
			return b, err
		case "integerValue":
			var s string
			if err := json.Unmarshal(body, &s); err != nil {
				var n int64
				err := json.Unmarshal(body, &n)
				return n, err
			}
			return strconv.ParseInt(s, 10, 64)
		case "doubleValue":
			var s string
			if json.Unmarshal(body, &s) == nil {
				switch s {
				case "NaN":
					return math.NaN(), nil
				case "Infinity":
					return math.Inf(1), nil
				case "-Infinity":
					return math.Inf(-1), nil
				}
				return strconv.ParseFloat(s, 64)
			}
			var d float64
			err := json.Unmarshal(body, &d)
			return d, err
		case "timestampValue":
			var s string
			if err := json.Unmarshal(body, &s); err != nil {
				return nil, err
			}
			t, err := time.Parse(time.RFC3339Nano, s)
			return dump.Timestamp{Time: t.UTC()}, err
		case "stringValue":
			var s string
			err := json.Unmarshal(body, &s)
			return s, err
		case "bytesValue":
			var s string
			if err := json.Unmarshal(body, &s); err != nil {
				return nil, err
			}
			b, err := base64.StdEncoding.DecodeString(s)
			return dump.Bytes(b), err
		case "referenceValue":
			var s string
			if err := json.Unmarshal(body, &s); err != nil {
				return nil, err
			}
			p, ok := strings.CutPrefix(s, root+"/")
			if !ok {
				return nil, fmt.Errorf("reference %q is outside %s", s, root)
			}
			return dump.Ref{Path: p}, nil
		case "geoPointValue":
			var g struct{ Latitude, Longitude float64 }
			err := json.Unmarshal(body, &g)
			return dump.GeoPoint{Lat: g.Latitude, Lng: g.Longitude}, err
		case "arrayValue":
			var a struct {
				Values []json.RawMessage `json:"values"`
			}
			if err := json.Unmarshal(body, &a); err != nil {
				return nil, err
			}
			out := make([]any, len(a.Values))
			for i, e := range a.Values {
				d, err := DecodeValue(root, e)
				if err != nil {
					return nil, err
				}
				out[i] = d
			}
			return out, nil
		case "mapValue":
			var m struct {
				Fields map[string]json.RawMessage `json:"fields"`
			}
			if err := json.Unmarshal(body, &m); err != nil {
				return nil, err
			}
			out := make(map[string]any, len(m.Fields))
			for k, e := range m.Fields {
				d, err := DecodeValue(root, e)
				if err != nil {
					return nil, err
				}
				out[k] = d
			}
			return out, nil
		default:
			return nil, fmt.Errorf("unknown Firestore value kind %q", kind)
		}
	}
	return nil, errors.New("not a Firestore Value")
}

// EncodeValue turns a dump value into a Firestore REST Value.
func EncodeValue(root string, v any) (map[string]any, error) {
	switch x := v.(type) {
	case nil:
		return map[string]any{"nullValue": nil}, nil
	case bool:
		return map[string]any{"booleanValue": x}, nil
	case int:
		return map[string]any{"integerValue": strconv.Itoa(x)}, nil
	case int64:
		return map[string]any{"integerValue": strconv.FormatInt(x, 10)}, nil
	case float64:
		switch {
		case math.IsNaN(x):
			return map[string]any{"doubleValue": "NaN"}, nil
		case math.IsInf(x, 1):
			return map[string]any{"doubleValue": "Infinity"}, nil
		case math.IsInf(x, -1):
			return map[string]any{"doubleValue": "-Infinity"}, nil
		}
		return map[string]any{"doubleValue": x}, nil
	case string:
		return map[string]any{"stringValue": x}, nil
	case dump.Timestamp:
		return map[string]any{"timestampValue": x.UTC().Format(time.RFC3339Nano)}, nil
	case dump.Bytes:
		return map[string]any{"bytesValue": base64.StdEncoding.EncodeToString(x)}, nil
	case dump.Ref:
		return map[string]any{"referenceValue": root + "/" + x.Path}, nil
	case dump.GeoPoint:
		return map[string]any{"geoPointValue": map[string]any{"latitude": x.Lat, "longitude": x.Lng}}, nil
	case []any:
		vals := make([]any, len(x))
		for i, e := range x {
			ev, err := EncodeValue(root, e)
			if err != nil {
				return nil, err
			}
			vals[i] = ev
		}
		return map[string]any{"arrayValue": map[string]any{"values": vals}}, nil
	case map[string]any:
		fields := make(map[string]any, len(x))
		for k, e := range x {
			ev, err := EncodeValue(root, e)
			if err != nil {
				return nil, err
			}
			fields[k] = ev
		}
		return map[string]any{"mapValue": map[string]any{"fields": fields}}, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}

var simpleField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// quoteFieldPath backquotes a top-level field name that is not a simple identifier.
func quoteFieldPath(f string) string {
	if simpleField.MatchString(f) {
		return f
	}
	return "`" + strings.NewReplacer(`\`, `\\`, "`", "\\`").Replace(f) + "`"
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// do sends one request with a bearer token; a non-2xx answer is an *APIError (404 ErrNotFound, 409/412 on a
// precondition ErrPrecondition). Errors never carry a body or a token.
func do(ctx context.Context, client *http.Client, tokens oauth2.TokenSource, op, method, u string, body, out any) error {
	tok, err := tokens.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil {
			return fmt.Errorf("gcp: %s: access token: token endpoint answered HTTP %d", op, re.Response.StatusCode)
		}
		return fmt.Errorf("gcp: %s: access token unavailable", op)
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gcp: %s: encode: %w", op, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return fmt.Errorf("gcp: %s: %w", op, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	tok.SetAuthHeader(req)
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("gcp: %s: %w", op, ctx.Err())
		}
		return fmt.Errorf("gcp: %s: transport failure", op)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("gcp: %s: read response", op)
	}
	if res.StatusCode/100 != 2 {
		code := statusCode(raw)
		switch {
		case res.StatusCode == http.StatusNotFound && strings.HasPrefix(errorMessage(raw), "No document to "):
			return fmt.Errorf("%w: %w: %s", ErrNotFound, errNoDocument, op)
		case res.StatusCode == http.StatusNotFound:
			return fmt.Errorf("%w: %s", ErrNotFound, op)
		case res.StatusCode == http.StatusPreconditionFailed || code == "FAILED_PRECONDITION" || code == "ALREADY_EXISTS":
			return fmt.Errorf("%w: %s", ErrPrecondition, op)
		}
		return &APIError{Op: op, Status: res.StatusCode, Code: code}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("gcp: %s: undecodable response", op)
		}
	}
	return nil
}

// errorMessage is the message of Google's {"error":{"message":"..."}}, only tested for a prefix: an error never carries
// it (it names the document path).
func errorMessage(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	return e.Error.Message
}

// statusCode keeps only Google's upper-case status of {"error":{"status":"..."}}.
func statusCode(raw []byte) string {
	var e struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) != nil || len(e.Error.Status) > 64 ||
		strings.ContainsFunc(e.Error.Status, func(r rune) bool { return (r < 'A' || r > 'Z') && r != '_' }) {
		return ""
	}
	return e.Error.Status
}
