package hr

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
)

// The class C write-back of the leave decisions (main spec §11.10, §13.7, §13.8): the port, its Firestore REST adapter
// and an in-memory stand-in for tests.

// DefaultMirrorWait is how long a write-back decision waits for the mirror's acknowledgement (main spec §13.7, R53).
const DefaultMirrorWait = 3 * time.Second

// ErrDocumentChanged is a write refused by its precondition: the document changed, or was deleted, after it was read.
var ErrDocumentChanged = errors.New("hr: the Firestore document changed since it was read")

// LeaveDocs is the Firestore side of the leave write-back: the leave_requests documents (LeaveCollection) the installed
// 3.x APKs write.
type LeaveDocs interface {
	// Get reads leave_requests/{docID}: its fields (dump values: string, int64, float64, bool, dump.Timestamp, ...), its
	// update time, and whether it exists.
	Get(ctx context.Context, docID string) (fields map[string]any, updateTime time.Time, exists bool, err error)
	// Update merges fields into leave_requests/{docID} (an update mask: the other fields stay; a nil value deletes its
	// field) while the document still carries updateTime, and returns the write's update time. A changed or deleted
	// document is ErrDocumentChanged and nothing is written. Values are string, bool, int64, float64, time.Time and nil.
	Update(ctx context.Context, docID string, fields map[string]any, updateTime time.Time) (time.Time, error)
}

// MirrorAcks waits for the mirror's read-your-writes acknowledgement (cache.Cache.WaitMirrorAck, R53): true once the
// mirror applied the document at or after want, false when the timeout passed first (or Redis is unreachable).
type MirrorAcks interface {
	WaitMirrorAck(ctx context.Context, collection, docID string, want time.Time, timeout time.Duration) (bool, error)
}

// LeaveWriteBack is the Firestore-first path of the leave decisions while the 3.x APKs write leave_requests.
type LeaveWriteBack struct {
	Docs LeaveDocs
	// Acks is the mirror acknowledgement; nil answers every write-back 202. leave_requests has no listener (the
	// listeners poll the four operations collections, etl.ListenFields), so the sweep (ETL_SYNC_INTERVAL) acknowledges
	// it and a decision normally answers 202 after the wait.
	Acks MirrorAcks
	// Wait bounds the wait for the acknowledgement; 0 = DefaultMirrorWait.
	Wait time.Duration
}

// FirestoreLeaveDocs is LeaveDocs over the Firestore REST client of internal/etl/gcp (a service-account token; masked
// PATCH with an update-time precondition).
type FirestoreLeaveDocs struct{ FS *gcp.Firestore }

// Get implements LeaveDocs.
func (f FirestoreLeaveDocs) Get(ctx context.Context, docID string) (map[string]any, time.Time, bool, error) {
	if f.FS == nil {
		return nil, time.Time{}, false, errors.New("hr: no Firestore client")
	}
	d, ok, err := f.FS.Lookup(ctx, LeaveCollection+"/"+docID, time.Time{})
	if err != nil || !ok {
		return nil, time.Time{}, false, err
	}
	return d.Fields, d.UpdateTime, true, nil
}

// Update implements LeaveDocs with one masked PATCH under currentDocument.updateTime.
func (f FirestoreLeaveDocs) Update(ctx context.Context, docID string, fields map[string]any, updateTime time.Time) (time.Time, error) {
	if f.FS == nil {
		return time.Time{}, errors.New("hr: no Firestore client")
	}
	if updateTime.IsZero() {
		return time.Time{}, errors.New("hr: a write-back update needs the update time it was checked against")
	}
	enc, mask, err := encodeLeaveFields(fields)
	if err != nil {
		return time.Time{}, fmt.Errorf("hr: %s/%s: %w", LeaveCollection, docID, err)
	}
	d, err := f.FS.Patch(ctx, LeaveCollection+"/"+docID, enc, mask, gcp.Precondition{UpdateTime: updateTime})
	if errors.Is(err, gcp.ErrPrecondition) || errors.Is(err, gcp.ErrNotFound) {
		return time.Time{}, ErrDocumentChanged
	}
	if err != nil {
		return time.Time{}, err
	}
	return d.UpdateTime, nil
}

// encodeLeaveFields maps the values of a write onto the client's (time.Time -> dump.Timestamp) and its mask (every key;
// a nil value is in the mask only, so it is deleted).
func encodeLeaveFields(fields map[string]any) (map[string]any, []string, error) {
	enc := make(map[string]any, len(fields))
	mask := slices.Sorted(maps.Keys(fields))
	for _, k := range mask {
		switch v := fields[k].(type) {
		case nil:
		case time.Time:
			enc[k] = dump.Timestamp{Time: v.UTC()}
		case string, bool, int64, float64:
			enc[k] = v
		default:
			return nil, nil, fmt.Errorf("%s: unsupported value type %T", k, v)
		}
	}
	return enc, mask, nil
}

// MemoryLeaveDocs is the in-memory LeaveDocs of tests: documents by id with their update times (one microsecond per
// write, from a fixed epoch). Fail, when set, is returned by every call.
type MemoryLeaveDocs struct {
	mu    sync.Mutex
	docs  map[string]map[string]any
	times map[string]time.Time
	clock time.Time
	fail  error
	// OnUpdate, when set, is called after each accepted Update (outside the lock) with the document id, the fields and
	// the update time, so a test can play the mirror.
	OnUpdate func(docID string, fields map[string]any, at time.Time)
}

// NewMemoryLeaveDocs returns an empty MemoryLeaveDocs.
func NewMemoryLeaveDocs() *MemoryLeaveDocs {
	return &MemoryLeaveDocs{docs: map[string]map[string]any{}, times: map[string]time.Time{},
		clock: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}

// Put stores a document as the 3.x app wrote it.
func (m *MemoryLeaveDocs) Put(docID string, fields map[string]any) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = m.clock.Add(time.Microsecond)
	m.docs[docID] = maps.Clone(fields)
	m.times[docID] = m.clock
	return m.clock
}

// Doc returns a copy of a document.
func (m *MemoryLeaveDocs) Doc(docID string) (map[string]any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[docID]
	return maps.Clone(d), ok
}

// SetFail makes every following call return err (nil restores normal operation).
func (m *MemoryLeaveDocs) SetFail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = err
}

// Get implements LeaveDocs.
func (m *MemoryLeaveDocs) Get(_ context.Context, docID string) (map[string]any, time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, time.Time{}, false, m.fail
	}
	d, ok := m.docs[docID]
	return maps.Clone(d), m.times[docID], ok, nil
}

// Update implements LeaveDocs.
func (m *MemoryLeaveDocs) Update(_ context.Context, docID string, fields map[string]any, updateTime time.Time) (time.Time, error) {
	m.mu.Lock()
	if m.fail != nil {
		m.mu.Unlock()
		return time.Time{}, m.fail
	}
	if _, _, err := encodeLeaveFields(fields); err != nil {
		m.mu.Unlock()
		return time.Time{}, err
	}
	doc, ok := m.docs[docID]
	if !ok || !m.times[docID].Equal(updateTime) {
		m.mu.Unlock()
		return time.Time{}, ErrDocumentChanged
	}
	for k, v := range fields {
		if v == nil {
			delete(doc, k)
			continue
		}
		doc[k] = v
	}
	m.clock = m.clock.Add(time.Microsecond)
	at := m.clock
	m.times[docID] = at
	hook := m.OnUpdate
	m.mu.Unlock()
	if hook != nil {
		hook(docID, maps.Clone(fields), at)
	}
	return at, nil
}
