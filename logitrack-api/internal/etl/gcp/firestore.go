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
	var docs []dump.Doc
	last, readTime := "", ""
	for {
		q := map[string]any{
			"from":    []any{map[string]any{"collectionId": collection, "allDescendants": group}},
			"orderBy": []any{map[string]any{"field": map[string]any{"fieldPath": "__name__"}, "direction": "ASCENDING"}},
			"limit":   pageSize,
		}
		if last != "" {
			q["startAt"] = map[string]any{"values": []any{map[string]any{"referenceValue": last}}, "before": false}
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
			return nil, err
		}
		n := 0
		for _, r := range out {
			if readTime == "" && r.ReadTime != "" {
				if _, err := time.Parse(time.RFC3339Nano, r.ReadTime); err != nil {
					return nil, fmt.Errorf("gcp: runQuery %s: readTime %q: %w", collection, r.ReadTime, err)
				}
				readTime = r.ReadTime
			}
			if r.Document == nil {
				continue // the readTime-only element of an empty page
			}
			n++
			last = r.Document.Name
			d, err := f.decodeDoc(*r.Document)
			if err != nil {
				return nil, err
			}
			if d.Collection() != collection || (!group && d.ParentPath() != "") {
				return nil, fmt.Errorf("gcp: runQuery %s answered %s", collection, d.Path)
			}
			docs = append(docs, d)
		}
		if n < pageSize {
			return docs, nil
		}
		if readTime == "" {
			return nil, fmt.Errorf("gcp: runQuery %s: a full page without readTime cannot be continued as one snapshot", collection)
		}
	}
}

// Get reads one document; ErrNotFound when it does not exist.
func (f *Firestore) Get(ctx context.Context, path string) (dump.Doc, error) {
	var out restDoc
	if err := do(ctx, f.client, f.tokens, "get", http.MethodGet, f.base+"/"+escapePath(path), nil, &out); err != nil {
		return dump.Doc{}, err
	}
	return f.decodeDoc(out)
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
