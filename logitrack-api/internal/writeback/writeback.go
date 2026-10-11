// Package writeback is the port of the class C write-back of the coexistence (developer-spec.md §11.10, §12.1, §13.7,
// §13.8, R25). While a domain is not PostgreSQL-owned (PG_OWNED_DOMAINS), Firestore stays the only writer of the
// collections the installed APKs write (vehicle_expenses, maintenance, tasks, ...): a staff or 4.x write Go serves
// goes to the Firestore document first, in the legacy shape (the domain encoders, e.g. expenses.LegacyWrite and
// maintenance.LegacyWrite, which name only the fields the write changed), the FS->PG mirror fills PostgreSQL, and the
// handler waits up to 3 s for the mirror acknowledgement (cache:mirror_ack), else answers 202. PostgreSQL is never a
// second writer of those rows.
//
// Writer is the port; Firestore adapts the Firestore REST client of internal/etl/gcp (service-account token, update
// masks); Memory is the in-memory stand-in of tests. No service calls it yet: T31 builds the write-back flow and the
// mirror acknowledgement on it, and switching it on in an environment is an operator decision recorded with the
// coexistence runbook. Until then staff writes go to PostgreSQL only, which is what a PostgreSQL-owned domain and a
// deployment without the mirror (the staging VM) need.
package writeback

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Writer writes staff changes of a Firestore-owned document.
type Writer interface {
	// Merge writes the given fields of collection/docID and keeps every other field (an update mask); the document is
	// created when it does not exist. A nil value removes the field (the legacy code's deleteField()). Values are
	// string, bool, int, int32, int64, float64, time.Time (a Firestore timestamp) and nil.
	Merge(ctx context.Context, collection, docID string, fields map[string]any) error
}

// ErrUnavailable is the API answer to a failed write-back: nothing of the request is kept and the client may retry
// (Appendix B §B.1.5 503 unavailable, details.reason write_back).
func ErrUnavailable(cause error) error {
	return httpx.NewError(http.StatusServiceUnavailable, httpx.CodeUnavailable, "the Firestore write-back failed").
		WithDetails(map[string]any{"reason": "write_back"}).Wrap(cause)
}

// Firestore is the Writer over the Firestore REST API (internal/etl/gcp).
type Firestore struct{ FS *gcp.Firestore }

// Merge implements Writer with a masked PATCH: the mask names every field, so a field sent as nil is deleted and the
// fields not named keep their values.
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

// dumpValue maps a field value onto the value types of the Firestore client (nil = delete the field).
func dumpValue(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string, bool, int64, float64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case time.Time:
		return dump.Timestamp{Time: x.UTC()}, nil
	}
	return nil, fmt.Errorf("unsupported value type %T", v)
}

// Write is one Merge Memory accepted.
type Write struct {
	Collection, DocID string
	Fields            map[string]any
}

// Memory is the in-memory Writer of tests: Docs holds the merged documents by "collection/docID", Writes every
// accepted Merge in order. Fail, when set, is returned by every Merge (nothing is written).
type Memory struct {
	mu     sync.Mutex
	docs   map[string]map[string]any
	writes []Write
	fail   error
}

// NewMemory returns an empty Memory.
func NewMemory() *Memory { return &Memory{docs: map[string]map[string]any{}} }

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
		if v == nil {
			delete(doc, k)
			continue
		}
		doc[k] = v
	}
	m.writes = append(m.writes, Write{Collection: collection, DocID: docID, Fields: maps.Clone(fields)})
	return nil
}

// Fail makes every following Merge return err (nil restores normal operation).
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
