// Package writeback is the port of the class C write-back of the coexistence (developer-spec.md §11.10, §12.1, §13.7,
// §13.8, R25). While a domain is not PostgreSQL-owned (PG_OWNED_DOMAINS), Firestore stays the only writer of the
// collections the installed APKs write (vehicle_expenses, maintenance, tasks, ...): a staff or 4.x write Go serves
// goes to the Firestore document first, in the legacy shape (the domain encoders, e.g. expenses.LegacyWrite and
// maintenance.LegacyWrite, which name only the fields the write changed), the FS->PG mirror fills PostgreSQL, and the
// handler waits up to 3 s for the mirror acknowledgement (cache:mirror_ack), else answers 202. PostgreSQL is never a
// second writer of those rows.
//
// Writer is the port; Firestore adapts the Firestore REST client of internal/etl/gcp (service-account token, update
// masks, atomic commits); Memory is the in-memory stand-in of tests. The operations staff writes of T31
// (internal/operations) and the chat writes of T47 (internal/chats) call it while their domain is Firestore-owned, and
// the leave decisions of T45 (internal/hr) until every domain is PostgreSQL-owned, all over the api's one Firestore
// client; the expenses and maintenance services (T40) still write PostgreSQL only (#60's open question). Switching it
// on in an environment is an operator decision
// recorded with the coexistence runbook: on a PostgreSQL-owned domain and on a deployment without the mirror (the
// staging VM) nothing calls it.
package writeback

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Writer writes staff changes of a Firestore-owned document.
type Writer interface {
	// Merge writes the given fields of collection/docID and keeps every other field (an update mask); the document is
	// created when it does not exist. A nil value removes the field (the legacy code's deleteField()); Null writes a
	// null. Values are string, bool, int, int32, int64, float64, time.Time (a Firestore timestamp), []any and
	// map[string]any of those (arrays and maps, e.g. deliveryStops), nil and, at the top level, Null.
	Merge(ctx context.Context, collection, docID string, fields map[string]any) error
	// Commit applies the writes of one request atomically (all or none, one documents:commit) and returns the commit
	// time: the update time of every document written, which the mirror acknowledges (cache:mirror_ack) once it
	// applied them. A failed precondition is ErrConflict and nothing is written.
	Commit(ctx context.Context, writes ...DocWrite) (time.Time, error)
	// Get reads one document's fields and update time; ok is false when it does not exist.
	Get(ctx context.Context, collection, docID string) (fields map[string]any, updateTime time.Time, ok bool, err error)
	// Find reads the documents of a top-level collection whose field holds the string value, in id order: one
	// equality query, which the single-field index Firestore keeps by default serves (the 3.x app runs the same one on
	// chats.driverId). The chat write-back finds a driver's chat documents the mirror has not applied yet with it (T47).
	Find(ctx context.Context, collection, field, value string) ([]Doc, error)
}

// Doc is a document Find read: its id, fields and update time.
type Doc struct {
	ID         string
	Fields     map[string]any
	UpdateTime time.Time
}

// Null is the value of a field written as a Firestore null: the field stays on the document and holds null, where a
// nil value removes it. Legacy writers that store field: null (the chats' assignedAdminId, assignedAt and closedAt,
// T47) are read with where(field, '==', null), which matches a document that holds the null and never one that lacks
// the field. It names a top-level field only.
var Null any = nullValue{}

type nullValue struct{}

func isNull(v any) bool {
	_, ok := v.(nullValue)
	return ok
}

// DocWrite is one write of a Commit. Delete removes the document; otherwise Fields are merged as Merge merges them
// (Replace writes Fields as the whole document instead, the copy of a renamed trip). MustNotExist creates only,
// MustExist updates only; UpdateTime, when set, writes only while the document still carries that update time.
type DocWrite struct {
	Collection, DocID string
	Fields            map[string]any
	Replace           bool
	Delete            bool
	MustNotExist      bool
	MustExist         bool
	UpdateTime        time.Time
}

// ErrConflict is a commit refused by a precondition: the document exists (MustNotExist) or changed since it was read.
var ErrConflict = errors.New("writeback: a precondition of the commit failed")

// ErrUnavailable is the API answer to a failed write-back: nothing of the request is kept and the client may retry
// (Appendix B §B.1.5 503 unavailable, details.reason write_back).
func ErrUnavailable(cause error) error {
	return httpx.NewError(http.StatusServiceUnavailable, httpx.CodeUnavailable, "the Firestore write-back failed").
		WithDetails(map[string]any{"reason": "write_back"}).Wrap(cause)
}

// Firestore is the Writer over the Firestore REST API (internal/etl/gcp).
type Firestore struct{ FS *gcp.Firestore }

// Merge implements Writer with a masked PATCH: the mask names every field, so a field sent as nil is deleted, one sent
// as Null holds null, and the fields not named keep their values.
func (f Firestore) Merge(ctx context.Context, collection, docID string, fields map[string]any) error {
	if f.FS == nil {
		return errors.New("writeback: no Firestore client")
	}
	if collection == "" || docID == "" {
		return errors.New("writeback: a document path needs a collection and an id")
	}
	enc := make(map[string]any, len(fields))
	mask := make([]string, 0, len(fields))
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		mask = append(mask, k)
		if isNull(fields[k]) {
			enc[k] = nil // a present nil: gcp encodes it as nullValue
			continue
		}
		v, err := dumpValue(fields[k])
		if err != nil {
			return fmt.Errorf("writeback: %s/%s.%s: %w", collection, docID, k, err)
		}
		if v != nil {
			enc[k] = v
		}
	}
	_, err := f.FS.Patch(ctx, collection+"/"+docID, enc, mask, gcp.Precondition{})
	return err
}

// Commit implements Writer with one documents:commit.
func (f Firestore) Commit(ctx context.Context, writes ...DocWrite) (time.Time, error) {
	if f.FS == nil {
		return time.Time{}, errors.New("writeback: no Firestore client")
	}
	out, err := gcpWrites(writes)
	if err != nil {
		return time.Time{}, err
	}
	at, err := f.FS.Commit(ctx, out)
	if errors.Is(err, gcp.ErrPrecondition) {
		return time.Time{}, fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return at, err
}

// Get implements Writer.
func (f Firestore) Get(ctx context.Context, collection, docID string) (map[string]any, time.Time, bool, error) {
	if f.FS == nil {
		return nil, time.Time{}, false, errors.New("writeback: no Firestore client")
	}
	d, ok, err := f.FS.Lookup(ctx, collection+"/"+docID, time.Time{})
	if err != nil || !ok {
		return nil, time.Time{}, false, err
	}
	return d.Fields, d.UpdateTime, true, nil
}

// Find implements Writer with one runQuery (field == value, ordered by name, paged).
func (f Firestore) Find(ctx context.Context, collection, field, value string) ([]Doc, error) {
	if f.FS == nil {
		return nil, errors.New("writeback: no Firestore client")
	}
	docs, err := f.FS.Where(ctx, collection, field, value)
	if err != nil {
		return nil, err
	}
	out := make([]Doc, 0, len(docs))
	for _, d := range docs {
		out = append(out, Doc{ID: d.ID, Fields: d.Fields, UpdateTime: d.UpdateTime})
	}
	return out, nil
}

// gcpWrites maps the writes of a commit onto the client's (each Merge-like write masks exactly its fields).
func gcpWrites(writes []DocWrite) ([]gcp.Write, error) {
	if len(writes) == 0 {
		return nil, errors.New("writeback: a commit needs a write")
	}
	out := make([]gcp.Write, 0, len(writes))
	for _, w := range writes {
		if w.Collection == "" || w.DocID == "" {
			return nil, errors.New("writeback: a document path needs a collection and an id")
		}
		g := gcp.Write{Path: w.Collection + "/" + w.DocID, Delete: w.Delete}
		switch {
		case w.MustNotExist:
			no := false
			g.Pre.Exists = &no
		case !w.UpdateTime.IsZero():
			g.Pre.UpdateTime = w.UpdateTime
		case w.MustExist:
			yes := true
			g.Pre.Exists = &yes
		}
		if !w.Delete {
			g.Fields = map[string]any{}
			for _, k := range slices.Sorted(maps.Keys(w.Fields)) {
				if !w.Replace {
					g.Mask = append(g.Mask, k)
				}
				if isNull(w.Fields[k]) {
					g.Fields[k] = nil // a present nil: gcp encodes it as nullValue
					continue
				}
				v, err := dumpValue(w.Fields[k])
				if err != nil {
					return nil, fmt.Errorf("writeback: %s/%s.%s: %w", w.Collection, w.DocID, k, err)
				}
				if v != nil {
					g.Fields[k] = v
				}
			}
		}
		out = append(out, g)
	}
	return out, nil
}

// dumpValue maps a field value onto the value types of the Firestore client (nil = delete the field; Null is handled
// by the callers, at the top level only).
func dumpValue(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case nullValue:
		return nil, errors.New("writeback.Null names a top-level field only")
	case string, bool, int64, float64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case time.Time:
		return dump.Timestamp{Time: x.UTC()}, nil
	case dump.Timestamp, dump.Bytes, dump.Ref, dump.GeoPoint:
		return x, nil // a value read from Firestore (Get) written back as it was: the copy of a renamed trip
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			ev, err := dumpValue(e)
			if err != nil {
				return nil, err
			}
			out[i] = ev
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			ev, err := dumpValue(e)
			if err != nil {
				return nil, err
			}
			if ev != nil {
				out[k] = ev
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported value type %T", v)
}

// Write is one Merge (or one write of a Commit) Memory accepted; Delete marks a commit's delete.
type Write struct {
	Collection, DocID string
	Fields            map[string]any
	Delete            bool
}

// Memory is the in-memory Writer of tests: Docs holds the merged documents by "collection/docID", Writes every
// accepted Merge (and every write of an accepted Commit) in order. Fail, when set, is returned by every Merge and
// Commit (nothing is written). A Commit's time is one microsecond after the previous one, from a fixed epoch; OnCommit,
// when set, is called after each accepted commit (outside the lock) with its writes and time, so a test can play the
// mirror.
type Memory struct {
	mu       sync.Mutex
	docs     map[string]map[string]any
	times    map[string]time.Time
	writes   []Write
	fail     error
	clock    time.Time
	OnCommit func(writes []DocWrite, at time.Time)
}

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{docs: map[string]map[string]any{}, times: map[string]time.Time{},
		clock: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}

// Commit implements Writer: every precondition is checked first, then all writes take one time.
func (m *Memory) Commit(_ context.Context, writes ...DocWrite) (time.Time, error) {
	m.mu.Lock()
	if m.fail != nil {
		m.mu.Unlock()
		return time.Time{}, m.fail
	}
	if _, err := gcpWrites(writes); err != nil {
		m.mu.Unlock()
		return time.Time{}, err
	}
	for _, w := range writes {
		path := w.Collection + "/" + w.DocID
		_, exists := m.docs[path]
		if (w.MustNotExist && exists) || (w.MustExist && !exists) ||
			(!w.UpdateTime.IsZero() && (!exists || !m.times[path].Equal(w.UpdateTime))) {
			m.mu.Unlock()
			return time.Time{}, ErrConflict
		}
	}
	m.clock = m.clock.Add(time.Microsecond)
	at := m.clock
	for _, w := range writes {
		path := w.Collection + "/" + w.DocID
		if w.Delete {
			delete(m.docs, path)
			delete(m.times, path)
			m.writes = append(m.writes, Write{Collection: w.Collection, DocID: w.DocID, Delete: true})
			continue
		}
		doc := m.docs[path]
		if doc == nil || w.Replace {
			doc = map[string]any{}
			m.docs[path] = doc
		}
		for k, v := range w.Fields {
			setField(doc, k, v)
		}
		m.times[path] = at
		m.writes = append(m.writes, Write{Collection: w.Collection, DocID: w.DocID, Fields: maps.Clone(w.Fields)})
	}
	hook := m.OnCommit
	m.mu.Unlock()
	if hook != nil {
		hook(slices.Clone(writes), at)
	}
	return at, nil
}

// Get implements Writer.
func (m *Memory) Get(_ context.Context, collection, docID string) (map[string]any, time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, time.Time{}, false, m.fail
	}
	path := collection + "/" + docID
	d, ok := m.docs[path]
	return maps.Clone(d), m.times[path], ok, nil
}

// Put stores a document as it stands in Firestore (a fixture), with its update time.
func (m *Memory) Put(collection, docID string, fields map[string]any, updateTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[collection+"/"+docID] = maps.Clone(fields)
	m.times[collection+"/"+docID] = updateTime
}

// Merge implements Writer.
func (m *Memory) Merge(_ context.Context, collection, docID string, fields map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	if collection == "" || docID == "" {
		return errors.New("writeback: a document path needs a collection and an id")
	}
	for k, v := range fields {
		if isNull(v) {
			continue
		}
		if _, err := dumpValue(v); err != nil {
			return fmt.Errorf("writeback: %s/%s.%s: %w", collection, docID, k, err)
		}
	}
	path := collection + "/" + docID
	doc := m.docs[path]
	if doc == nil {
		doc = map[string]any{}
		m.docs[path] = doc
	}
	for k, v := range fields {
		setField(doc, k, v)
	}
	m.writes = append(m.writes, Write{Collection: collection, DocID: docID, Fields: maps.Clone(fields)})
	return nil
}

// setField applies one written field to a Memory document as Firestore would: nil removes it, Null keeps it with a
// nil value.
func setField(doc map[string]any, k string, v any) {
	switch {
	case v == nil:
		delete(doc, k)
	case isNull(v):
		doc[k] = nil
	default:
		doc[k] = v
	}
}

// Find implements Writer: the documents directly in collection whose field holds value, in id order.
func (m *Memory) Find(_ context.Context, collection, field, value string) ([]Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	var out []Doc
	for path, d := range m.docs {
		id, ok := strings.CutPrefix(path, collection+"/")
		if !ok || id == "" || strings.Contains(id, "/") {
			continue
		}
		if s, ok := d[field].(string); ok && s == value {
			out = append(out, Doc{ID: id, Fields: maps.Clone(d), UpdateTime: m.times[path]})
		}
	}
	slices.SortFunc(out, func(a, b Doc) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Fail makes every following Merge, Commit, Get and Find return err (nil restores normal operation).
func (m *Memory) Fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = err
}

// Doc returns a copy of the document at collection/docID.
func (m *Memory) Doc(collection, docID string) (map[string]any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[collection+"/"+docID]
	return maps.Clone(d), ok
}

// Writes returns the accepted Merges in order.
func (m *Memory) Writes() []Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.writes)
}
