package hr

import (
	"context"
	"errors"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The class C write-back of the leave decisions (main spec §11.10, §13.7, §13.8). The leave_requests documents the
// installed 3.x APKs write are read and written through T31's port, writeback.Writer: writeback.Firestore in the api (the
// one client the operations write-back uses too), writeback.Memory in tests.

// DefaultMirrorWait is how long a write-back decision waits for the mirror's acknowledgement (main spec §13.7, R53).
const DefaultMirrorWait = 3 * time.Second

// MirrorAcks waits for the mirror's read-your-writes acknowledgement (cache.Cache.WaitMirrorAck, R53): true once the
// mirror applied the document at or after want, false when the timeout passed first (or Redis is unreachable).
type MirrorAcks interface {
	WaitMirrorAck(ctx context.Context, collection, docID string, want time.Time, timeout time.Duration) (bool, error)
}

// LeaveWriteBack is the Firestore-first path of the leave decisions while the 3.x APKs write leave_requests.
type LeaveWriteBack struct {
	// Writer reads and writes the leave_requests documents (LeaveCollection); nil answers the decisions 503
	// write_back_unconfigured.
	Writer writeback.Writer
	// Acks is the mirror acknowledgement; nil answers every write-back 202. leave_requests has no listener (the
	// listeners poll the four operations collections, etl.ListenFields), so the sweep (ETL_SYNC_INTERVAL) acknowledges
	// it and a decision normally answers 202 after the wait.
	Acks MirrorAcks
	// Wait bounds the wait for the acknowledgement; 0 = DefaultMirrorWait.
	Wait time.Duration
}

// write applies one decision to leave_requests/{docID}: it reads the document, checks the transition against the
// document's own status (Firestore is the writer, the row may lag), and commits the legacy fields (LegacyLeaveDecision)
// under a precondition on the update time it was checked against. It returns the status checked (the event's from),
// the fields written and the commit time. A document that is missing (rowStatus names the row's status then), or that
// changed or vanished between the read and the commit (writeback.ErrConflict: nothing is written), is 409
// document_changed; a refused transition 409 transition_not_allowed; any other failure 503 write_back.
func (wb *LeaveWriteBack) write(ctx context.Context, docID, rowStatus string, move [2]string, approverUID, reason *string,
	now time.Time) (string, map[string]any, time.Time, error) {
	cur, updated, exists, err := wb.Writer.Get(ctx, LeaveCollection, docID)
	if err != nil {
		return "", nil, time.Time{}, writeback.ErrUnavailable(err)
	}
	if !exists {
		return "", nil, time.Time{}, errState(ReasonDocumentChanged, rowStatus, nil)
	}
	if updated.IsZero() {
		// Without the update time the write would carry no precondition and could overwrite a concurrent change.
		return "", nil, time.Time{}, writeback.ErrUnavailable(errors.New("hr: the leave document was read without its update time"))
	}
	from := LegacyLeaveStatus(cur["status"])
	if from != move[0] {
		return "", nil, time.Time{}, errState(ReasonTransitionNotAllowed, from, map[string]any{"to": move[1]})
	}
	fields := LegacyLeaveDecision(move[1], approverUID, reason, now)
	at, err := wb.Writer.Commit(ctx, writeback.DocWrite{Collection: LeaveCollection, DocID: docID, Fields: fields, UpdateTime: updated})
	if errors.Is(err, writeback.ErrConflict) {
		return "", nil, time.Time{}, errState(ReasonDocumentChanged, from, nil)
	}
	if err != nil {
		return "", nil, time.Time{}, writeback.ErrUnavailable(err)
	}
	return from, fields, at, nil
}

// acked waits for the mirror's acknowledgement of the write at at; false without Acks, after the wait, or when Redis
// fails (the answer is then the 202 echo: the write is in Firestore either way).
func (wb *LeaveWriteBack) acked(ctx context.Context, docID string, at time.Time) bool {
	if wb.Acks == nil {
		return false
	}
	wait := wb.Wait
	if wait <= 0 {
		wait = DefaultMirrorWait
	}
	ok, _ := wb.Acks.WaitMirrorAck(ctx, LeaveCollection, docID, at, wait)
	return ok
}
