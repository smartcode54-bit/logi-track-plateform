package hr

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/hrdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// Reasons of the refusals of a decision (details.reason).
const (
	// ReasonTransitionNotAllowed: the request's status refuses the decision (409 failed_precondition, details.status,
	// details.to).
	ReasonTransitionNotAllowed = "transition_not_allowed"
	// ReasonWriteBackUnconfigured: Firestore writes leave_requests and the api has no Firestore writer (503
	// unavailable): PostgreSQL is never written in its place.
	ReasonWriteBackUnconfigured = "write_back_unconfigured"
	// ReasonDocumentChanged: the Firestore document changed (or was deleted) between the read the decision was checked
	// against and the write (409 failed_precondition); the client reloads and decides again.
	ReasonDocumentChanged = "document_changed"
	// ReasonNotInFirestore: the request has no Firestore document to write back to (a row born in PostgreSQL while
	// Firestore still writes leave requests; 409 failed_precondition).
	ReasonNotInFirestore = "not_in_firestore"
)

// originWriteBack is the producer header (outbox.HeaderOrigin) of the event a write-back handler appends after it wrote
// a Firestore-owned document (the value T31 names for its operations writes). Unlike the operations write-backs, a leave
// event keeps its realtime topics: the mirror appends no event for leave_requests, so this is the only one.
const originWriteBack = "writeback"

// LeaveDecision is the body of a decision: reason is the rejection's (required by reject, refused elsewhere).
type LeaveDecision struct {
	Reason *string `json:"reason"`
}

// LeaveResult is the answer of a decision: 200 with the request, or 202 with the write-back echo while the mirror has
// not acknowledged the Firestore write.
type LeaveResult struct {
	Status int
	Data   any
}

// LeaveWriteBackEcho is the 202 data of a write-back the mirror did not acknowledge within the wait (main spec §11.10,
// §13.7): the request's id, the Firestore document written, the update time of that write (the row carries the
// decision once the mirror applied it) and the fields written, in the legacy shape (timestamps as RFC 3339).
type LeaveWriteBackEcho struct {
	Pending    bool           `json:"pending"`
	ID         uuid.UUID      `json:"id"`
	Status     string         `json:"status"`
	Collection string         `json:"collection"`
	DocID      string         `json:"docId"`
	UpdateTime time.Time      `json:"updateTime"`
	Document   map[string]any `json:"document"`
}

// DecideLeave is POST /v1/leave-requests/{id}/{approve|reject|cancel} (hr:manage_leave): approve and reject a pending
// request, cancel an approved one; anything else is 409 failed_precondition (transition_not_allowed). reject needs a
// reason; approve and cancel take none.
func (s *Service) DecideLeave(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, action string, in LeaveDecision) (LeaveResult, error) {
	move, ok := leaveMoves[action]
	if !ok {
		return LeaveResult{}, httpx.ErrNotFound()
	}
	if _, err := writeTenant(p); err != nil {
		return LeaveResult{}, err
	}
	var ck checker
	var reason *string
	switch {
	case action == ActionReject:
		reason = ck.text("reason", in.Reason, maxRejectionReasonChars)
		if reason == nil && (in.Reason == nil || strings.TrimSpace(*in.Reason) == "") {
			ck.add("reason", "required")
		}
	case in.Reason != nil:
		ck.add("reason", "not_supported")
	}
	if err := ck.err(); err != nil {
		return LeaveResult{}, err
	}
	if s.leavePG {
		return s.decidePG(ctx, p, rid, id, action, move, reason)
	}
	if s.leaveWB == nil || s.leaveWB.Docs == nil {
		return LeaveResult{}, httpx.ErrUnavailable("leave requests are written in Firestore and the api has no Firestore writer").
			WithDetails(map[string]any{"reason": ReasonWriteBackUnconfigured})
	}
	return s.decideWriteBack(ctx, p, rid, id, action, move, reason)
}

// decidePG decides in the caller's request transaction (every domain PostgreSQL-owned).
func (s *Service) decidePG(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, action string, move [2]string, reason *string) (LeaveResult, error) {
	all, tenants := p.TenantReach()
	var out LeaveRequest
	var atts []hrdb.LeaveRequestAttachment
	err := s.rtx(ctx, p, func(tx pgx.Tx, q *hrdb.Queries) error {
		row, err := q.LockLeaveRequest(ctx, hrdb.LockLeaveRequestParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if row.Status != move[0] {
			return errState(ReasonTransitionNotAllowed, row.Status, map[string]any{"to": move[1]})
		}
		if action == ActionCancel {
			_, err = q.CancelLeaveRequest(ctx, id)
		} else {
			now := time.Now().UTC()
			_, err = q.DecideLeaveRequest(ctx, hrdb.DecideLeaveRequestParams{Status: move[1], ApproverUserID: actor(p), DecidedAt: &now,
				RejectionReason: reason, ID: id})
		}
		if err != nil {
			return err
		}
		if err := emitLeave(ctx, tx, row, row.Status, move[1], reason, rid, ""); err != nil {
			return err
		}
		r, err := q.GetLeaveRequest(ctx, hrdb.GetLeaveRequestParams{ID: id, AllTenants: all, TenantIds: tenants})
		if err != nil {
			return err
		}
		out = leaveOf(r.LeaveRequest, r.DriverName)
		atts, err = q.LeaveAttachments(ctx, []uuid.UUID{id})
		return err
	})
	if err != nil {
		return LeaveResult{}, err
	}
	list := []LeaveRequest{out}
	s.attach(ctx, list, atts)
	return LeaveResult{Status: http.StatusOK, Data: list[0]}, nil
}

// decideWriteBack writes the decision to the Firestore document first (main spec §11.10, §13.8): the transition is
// checked against the document's own status (Firestore is the writer), the write carries a precondition on the update
// time it was checked against, and PostgreSQL is filled by the mirror. The event is appended once the write is in
// Firestore; its notify.fcm push names the decision, so it does not wait for the mirror.
func (s *Service) decideWriteBack(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, action string, move [2]string, reason *string) (LeaveResult, error) {
	all, tenants := p.TenantReach()
	var row hrdb.LeaveRequest
	var approverUID *string
	err := s.rtx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		r, err := q.GetLeaveRequest(ctx, hrdb.GetLeaveRequestParams{ID: id, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		row = r.LeaveRequest
		if !p.IsMachine() {
			if approverUID, err = q.SelfLegacyUID(ctx, p.UserID); errors.Is(err, pgx.ErrNoRows) {
				err = nil
			}
		}
		return err
	})
	if err != nil {
		return LeaveResult{}, err
	}
	if row.LegacyDocID == nil || *row.LegacyDocID == "" {
		return LeaveResult{}, errState(ReasonNotInFirestore, row.Status, nil)
	}
	docID := *row.LegacyDocID
	wb := s.leaveWB
	cur, updated, exists, err := wb.Docs.Get(ctx, docID)
	if err != nil {
		return LeaveResult{}, writeback.ErrUnavailable(err)
	}
	if !exists {
		return LeaveResult{}, errState(ReasonDocumentChanged, row.Status, nil)
	}
	status := LegacyLeaveStatus(cur["status"])
	if status != move[0] {
		return LeaveResult{}, errState(ReasonTransitionNotAllowed, status, map[string]any{"to": move[1]})
	}
	now := time.Now().UTC()
	fields := LegacyLeaveDecision(move[1], approverUID, reason, now)
	at, err := wb.Docs.Update(ctx, docID, fields, updated)
	if errors.Is(err, ErrDocumentChanged) {
		return LeaveResult{}, errState(ReasonDocumentChanged, status, nil)
	}
	if err != nil {
		return LeaveResult{}, writeback.ErrUnavailable(err)
	}
	acked := false
	if wb.Acks != nil {
		wait := wb.Wait
		if wait <= 0 {
			wait = DefaultMirrorWait
		}
		// A Redis failure ends the wait: the answer is then the 202 echo, the write is in Firestore either way.
		acked, _ = wb.Acks.WaitMirrorAck(ctx, LeaveCollection, docID, at, wait)
	}
	// The event follows the write (and the acknowledgement, so a page that refetches on it reads the decision when the
	// mirror was quick). A failure to append it is logged, never an error: the decision is in Firestore and a retry of
	// the request would now meet transition_not_allowed. Its from is the document's status the decision was checked
	// against, not the row's: leave_requests has no listener, so the row may still hold a status the document left.
	if err := s.rtx(ctx, p, func(tx pgx.Tx, _ *hrdb.Queries) error {
		return emitLeave(ctx, tx, row, status, move[1], reason, rid, originWriteBack)
	}); err != nil {
		s.log.Error().Err(err).Str("leave_request_id", id.String()).Msg("leave write-back: the decision is in Firestore but its event was not appended")
	}
	if acked {
		var out LeaveRequest
		var atts []hrdb.LeaveRequestAttachment
		err := s.rtx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
			r, err := q.GetLeaveRequest(ctx, hrdb.GetLeaveRequestParams{ID: id, AllTenants: all, TenantIds: tenants})
			if err != nil {
				return err
			}
			out = leaveOf(r.LeaveRequest, r.DriverName)
			atts, err = q.LeaveAttachments(ctx, []uuid.UUID{id})
			return err
		})
		if err == nil && out.Status == move[1] {
			list := []LeaveRequest{out}
			s.attach(ctx, list, atts)
			return LeaveResult{Status: http.StatusOK, Data: list[0]}, nil
		}
	}
	return LeaveResult{Status: http.StatusAccepted, Data: LeaveWriteBackEcho{Pending: true, ID: id, Status: move[1],
		Collection: LeaveCollection, DocID: docID, UpdateTime: at.UTC(), Document: echoFields(fields)}}, nil
}

// echoFields renders the written fields for JSON: timestamps as RFC 3339, deleted fields as null.
func echoFields(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		if t, ok := v.(time.Time); ok {
			out[k] = t.UTC().Format(time.RFC3339Nano)
			continue
		}
		out[k] = v
	}
	return out
}

// emitLeave appends leave.decided in tx: tenant:{tid}:hr (hr:view_leave, hr:view_payroll) and the driver's own topic
// (leave.changed); notify.fcm pushes leave_decided. from is the status the decision was checked against and status the
// new one. The outbox row keeps a rejection's reason for notify.fcm (the write-back's row does not hold it until the
// mirror applied the document); realtime.Project keeps it off every stream (Appendix B §B.4.1). origin is the producer
// header ("writeback" for the write-back path, "" for the PostgreSQL one).
func emitLeave(ctx context.Context, tx pgx.Tx, row hrdb.LeaveRequest, from, status string, reason *string, rid, origin string) error {
	topics := []string{realtime.TenantTopic(row.TenantID.String(), realtime.FamilyHR)}
	payload := map[string]any{"entity": "leave_request", "id": row.ID.String(), "tenantId": row.TenantID.String(), "status": status,
		"from": from}
	if row.DriverID != nil {
		topics = append(topics, realtime.DriverTopic(row.DriverID.String()))
		payload["driverId"] = row.DriverID.String()
	}
	if reason != nil {
		payload["reason"] = *reason
	}
	headers := map[string]string{}
	if rid != "" {
		headers["requestId"] = rid
	}
	if origin != "" {
		headers[outbox.HeaderOrigin] = origin
	}
	tenant := row.TenantID
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteLeaveDecided, AggregateType: "leave_request", AggregateID: row.ID.String(),
		TenantID: &tenant, Payload: payload, Headers: headers, Topics: topics})
	return err
}

// attach fills the attachments of out (in the order of atts) with the evidence keys and presigned GETs, signed after the
// request transaction (storage.Service.SignedFiles runs on its own connection). A file it cannot sign (missing at
// source after the ETL, not committed, or every file when signing fails) keeps its key with a null url (main spec §10
// as built T25, item 9); a storage failure is logged.
func (s *Service) attach(ctx context.Context, out []LeaveRequest, atts []hrdb.LeaveRequestAttachment) {
	if len(atts) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(atts))
	for _, a := range atts {
		ids = append(ids, a.FileID)
	}
	var signed map[uuid.UUID]storage.Signed
	keys := map[uuid.UUID]string{}
	if s.files != nil {
		var err error
		if signed, err = s.files.SignedFiles(ctx, ids, storage.DocumentTTL); err != nil {
			s.log.Warn().Err(err).Msg("cannot sign the leave evidence URLs")
			signed = nil
		}
		var unsigned []uuid.UUID
		for _, id := range ids {
			if sg, ok := signed[id]; !ok || sg.URL == "" {
				unsigned = append(unsigned, id)
			}
		}
		if len(unsigned) > 0 {
			if keys, err = s.files.FileKeys(ctx, unsigned); err != nil {
				s.log.Warn().Err(err).Msg("cannot read the keys of the unsigned leave evidence")
				keys = map[uuid.UUID]string{}
			}
		}
	}
	index := make(map[uuid.UUID]int, len(out))
	for i, l := range out {
		index[l.ID] = i
	}
	for _, a := range atts {
		i, ok := index[a.LeaveRequestID]
		if !ok {
			continue
		}
		var at LeaveAttachment
		if sg, ok := signed[a.FileID]; ok && sg.URL != "" {
			key, url := sg.Key, sg.URL
			at = LeaveAttachment{Key: &key, URL: &url}
		} else if key, ok := keys[a.FileID]; ok && key != "" {
			at = LeaveAttachment{Key: &key}
		}
		out[i].Attachments = append(out[i].Attachments, at)
	}
}

// LegacyLeaveStatus maps a stored Firestore status literal onto the canonical vocabulary ("" when unknown): the 3.x app
// writes PENDING (leave_request_repository.dart), the legacy web APPROVED, REJECTED, CANCELLED.
func LegacyLeaveStatus(v any) string {
	s, _ := v.(string)
	s = strings.ToLower(strings.TrimSpace(s))
	for _, st := range LeaveStatuses {
		if s == st {
			return st
		}
	}
	return ""
}

// LegacyLeaveDecision is the Firestore write of one staff decision, as leave-requests/page.tsx:117-129 wrote it: status
// (upper case), updatedAt; for an approval or a rejection also approverId (the staff member's Firebase uid; left out
// when the user has none, since the mirror resolves approverId only through users.legacy_auth_uid) and approvedAt
// (the decision time, set on a rejection too), and a rejection's rejectionReason. A cancel writes the status alone.
// Values are string and time.Time (a Firestore timestamp).
func LegacyLeaveDecision(status string, approverUID, reason *string, now time.Time) map[string]any {
	out := map[string]any{"status": strings.ToUpper(status), "updatedAt": now.UTC()}
	if status == LeaveCancelled {
		return out
	}
	if approverUID != nil && strings.TrimSpace(*approverUID) != "" {
		out["approverId"] = strings.TrimSpace(*approverUID)
	}
	out["approvedAt"] = now.UTC()
	if reason != nil && utf8.ValidString(*reason) && strings.TrimSpace(*reason) != "" {
		out["rejectionReason"] = *reason
	}
	return out
}
