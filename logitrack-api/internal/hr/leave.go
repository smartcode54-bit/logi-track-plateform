package hr

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/hrdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// Leave requests (T45, issue #65; Appendix B §B.2.17, main spec §11.10, §12.1, §13.8). The installed 3.x APKs create
// and read leave requests in Firestore until P7b (class C, leave_request_repository.dart), so a staff decision has two
// paths:
//
//   - write-back (P4-P7a, while any domain is still Firestore-owned): the decision goes to the Firestore document
//     first, in the legacy shape (LegacyLeaveDecision: status APPROVED|REJECTED|CANCELLED, updatedAt, approverId,
//     approvedAt, rejectionReason, as leave-requests/page.tsx:117-129 wrote), under a precondition on the update time
//     the transition was checked against; the FS->PG mirror fills PostgreSQL, and the handler waits up to
//     LeaveWriteBack.Wait (3 s) for its acknowledgement (cache:mirror_ack) and answers like the PostgreSQL path, else
//     202 with the document it wrote. PostgreSQL is never written next to Firestore (§12.1: no dual master). hr is
//     PostgreSQL-owned from P4 (payroll, the holiday projection) while leave requests stay class C, so the switch is
//     not hr's domain flag but the end of the coexistence: every domain PostgreSQL-owned (P7b, and a deployment
//     without Firestore such as the staging VM with PG_OWNED_DOMAINS=all);
//   - PostgreSQL (P7b, the staging VM): the decision in the caller's request transaction under RLS, with its outbox
//     event in the same transaction.
//
// Either way the decision appends leave.decided (tenant:{tid}:hr, and leave.changed on driver:{driverId}; notify.fcm
// pushes leave_decided to the driver). Statuses are lower_snake (R65); the legacy upper-case literals are accepted on
// input and written only to Firestore.

// Leave request statuses (leave_requests.status, R65) and types.
const (
	LeavePending   = "pending"
	LeaveApproved  = "approved"
	LeaveRejected  = "rejected"
	LeaveCancelled = "cancelled"
)

// LeaveStatuses is the vocabulary of leave_requests.status.
var LeaveStatuses = []string{LeavePending, LeaveApproved, LeaveRejected, LeaveCancelled}

// RouteLeaveDecided is the outbox routing key of a staff decision (Appendix B §B.4.3, §B.5.3).
const RouteLeaveDecided = "leave.decided"

// LeaveCollection is the Firestore collection of leave requests (class C).
const LeaveCollection = "leave_requests"

// The decisions of POST /v1/leave-requests/{id}/{approve|reject|cancel}.
const (
	ActionApprove = "approve"
	ActionReject  = "reject"
	ActionCancel  = "cancel"
)

// leaveMoves are the transitions a decision makes (from -> to): approve and reject only a pending request, cancel only
// an approved one (Appendix B §B.2.17).
var leaveMoves = map[string][2]string{
	ActionApprove: {LeavePending, LeaveApproved},
	ActionReject:  {LeavePending, LeaveRejected},
	ActionCancel:  {LeaveApproved, LeaveCancelled},
}

// maxRejectionReasonChars bounds the reason of a rejection.
const maxRejectionReasonChars = 2000

// LeaveAttachment is one evidence file of a request: its object key and a presigned GET (main spec §9.5: leave_evidence
// needs hr:view_leave and lives at most 15 minutes). url is null when the file is missing at source or not committed
// (or signing failed), while key stays set (main spec §10 as built T25, item 9); both are null only without a storage
// service.
type LeaveAttachment struct {
	Key *string `json:"key"`
	URL *string `json:"url"`
}

// LeaveRequest is a leave request as every route answers it. startDate and endDate are Bangkok days (yyyy-MM-dd), days
// the inclusive count; approverUserId and decidedAt name the last approval or rejection (legacy approverId,
// approvedAt).
type LeaveRequest struct {
	ID              uuid.UUID         `json:"id"`
	TenantID        uuid.UUID         `json:"tenantId"`
	DriverID        *uuid.UUID        `json:"driverId"`
	DriverName      *string           `json:"driverName"`
	Type            string            `json:"type"`
	Status          string            `json:"status"`
	StartDate       string            `json:"startDate"`
	EndDate         string            `json:"endDate"`
	Days            int               `json:"days"`
	Reason          string            `json:"reason"`
	ApproverUserID  *uuid.UUID        `json:"approverUserId"`
	DecidedAt       *time.Time        `json:"decidedAt"`
	RejectionReason *string           `json:"rejectionReason"`
	Attachments     []LeaveAttachment `json:"attachments"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}

func leaveOf(l hrdb.LeaveRequest, driverName string) LeaveRequest {
	days := int(l.EndDate.Sub(l.StartDate).Hours()/24) + 1
	return LeaveRequest{ID: l.ID, TenantID: l.TenantID, DriverID: l.DriverID, DriverName: nameOrNil(driverName), Type: l.LeaveType,
		Status: l.Status, StartDate: l.StartDate.Format(time.DateOnly), EndDate: l.EndDate.Format(time.DateOnly), Days: days,
		Reason: l.Reason, ApproverUserID: l.ApproverUserID, DecidedAt: utc(l.DecidedAt), RejectionReason: l.RejectionReason,
		Attachments: []LeaveAttachment{}, CreatedAt: l.CreatedAt.UTC(), UpdatedAt: l.UpdatedAt.UTC()}
}

// rtx runs fn in a request transaction of p: RLS decides every row. Leave and holidays are not money paths.
func (s *Service) rtx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *hrdb.Queries) error) error {
	return mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, hrdb.New(tx)) }))
}

// LeaveQuery are the query parameters of GET /v1/leave-requests.
type LeaveQuery struct {
	// Statuses filters by status (?status=a&status=b or ?status=a,b, legacy upper case accepted); empty = all.
	Statuses []string
	DriverID string
	Limit    int
	Cursor   string
}

// LeaveRequests is GET /v1/leave-requests (hr:view_leave): the requests of the caller's reach by status and driver,
// keyset (created_at, id), newest first, no cap (the legacy page listened to the whole collection,
// leave-requests/page.tsx:61-66). The first page (no cursor) carries meta.statusCounts over the requests matching the
// driver filter (the page's summary cards). Evidence files are signed after the request transaction.
func (s *Service) LeaveRequests(ctx context.Context, p *authz.Principal, in LeaveQuery) ([]LeaveRequest, string, StatusCounts, error) {
	var ck checker
	driver := ck.uuidParam("driverId", strings.TrimSpace(in.DriverID))
	statuses := []string{}
	for _, raw := range in.Statuses {
		for v := range strings.SplitSeq(raw, ",") {
			v = normalize(v)
			if v == "" {
				continue
			}
			if !slices.Contains(LeaveStatuses, v) {
				ck.add("status", "invalid")
				continue
			}
			if !slices.Contains(statuses, v) {
				statuses = append(statuses, v)
			}
		}
	}
	limit := ck.limit(in.Limit)
	after := ck.cursor(in.Cursor)
	if err := ck.err(); err != nil {
		return nil, "", nil, err
	}
	all, tenants := p.TenantReach()
	arg := hrdb.ListLeaveRequestsParams{AllTenants: all, TenantIds: tenants, Statuses: statuses, DriverID: driver, RowLimit: int32(limit) + 1}
	if after != nil {
		arg.AfterAt, arg.AfterID = &after.T, after.ID
	}
	var rows []hrdb.ListLeaveRequestsRow
	var atts []hrdb.LeaveRequestAttachment
	var counts StatusCounts
	more := false
	err := s.rtx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		var err error
		if rows, err = q.ListLeaveRequests(ctx, arg); err != nil {
			return err
		}
		if more = len(rows) > limit; more {
			rows = rows[:limit]
		}
		ids := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			ids[i] = r.LeaveRequest.ID
		}
		if atts, err = q.LeaveAttachments(ctx, ids); err != nil {
			return err
		}
		if after != nil {
			return nil
		}
		cs, err := q.LeaveStatusCounts(ctx, hrdb.LeaveStatusCountsParams{AllTenants: all, TenantIds: tenants, DriverID: driver})
		if err != nil {
			return err
		}
		counts = StatusCounts{}
		for _, st := range LeaveStatuses {
			counts[st] = 0
		}
		for _, c := range cs {
			counts[c.Status] = int64(c.N)
		}
		return nil
	})
	if err != nil {
		return nil, "", nil, err
	}
	next := ""
	if more {
		last := rows[limit-1].LeaveRequest
		next = cursor{T: last.CreatedAt, ID: last.ID}.encode()
	}
	out := make([]LeaveRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, leaveOf(r.LeaveRequest, r.DriverName))
	}
	s.attach(ctx, out, atts)
	return out, next, counts, nil
}
