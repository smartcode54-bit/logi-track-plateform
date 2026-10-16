package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// The R13 task reassignment (T28; Appendix C §C.1.7, §C.3.6, Appendix B §B.2.11 PATCH /v1/tasks/{id} with tenantId):
// a dispatcher moves a call-out of its organisation (a task in its own RLS write reach) to the carrier that will run
// it, and may assign that carrier's driver in the same step; a platform admin may do the same for any task. It is a
// move only: a driver change of a task that stays in its tenant is the ordinary update (T31). tenant_id never comes
// from a body except on this path (and the driver move and the quarantine re-home). The move itself runs in a system
// transaction after Go authorization (the Reassigner: iam.TaskMover), because after it the task is outside the
// dispatcher's RLS write reach.

// ReasonDispatcherOrPlatform is details.reason of the 403 of a reassignment by anyone but a dispatcher or a platform
// admin.
const ReasonDispatcherOrPlatform = "dispatcher_or_platform_only"

// Reassign is a validated reassignment: the target tenant and, when DriverSet, the driver to assign there (nil = leave
// the task without a driver).
type Reassign struct {
	TenantID  uuid.UUID
	DriverSet bool
	DriverID  *uuid.UUID
	// Platform tells the Reassigner which authorization the request passed: a platform admin (every task) or a
	// dispatcher (a task the dispatcher reads and its own RLS lets it update).
	Platform bool
}

// Reassigned is the answer of a reassignment: where the task is now and who runs it. Moved is false only for a request
// that changes nothing (the task already in the target tenant with that driver); nothing is then written.
type Reassigned struct {
	ID               uuid.UUID  `json:"id"`
	TaskNo           *string    `json:"taskNo"`
	TenantID         uuid.UUID  `json:"tenantId"`
	TenantSource     string     `json:"tenantSource"`
	PreviousTenantID uuid.UUID  `json:"previousTenantId"`
	Status           string     `json:"status"`
	DriverID         *uuid.UUID `json:"driverId"`
	PreviousDriverID *uuid.UUID `json:"previousDriverId"`
	RunOrder         *int32     `json:"runOrder"`
	Moved            bool       `json:"moved"`
}

// Reassigner performs the reassignment in its own system transaction (iam.TaskMover.ReassignTask).
type Reassigner interface {
	ReassignTask(ctx context.Context, p *authz.Principal, rid string, taskID uuid.UUID, in Reassign) (Reassigned, error)
}

// reassignFields are the body fields of the T28 slice of PATCH /v1/tasks/{id}. The other fields of the route (job
// category, plan date, places, truck, helper, ...) land with T31's write-back and are refused until then (422
// not_supported), never silently dropped.
var reassignFields = []string{"tenantId", "driverId"}

// ParseReassign reads the body of PATCH /v1/tasks/{id}: tenantId is required (a uuid), driverId optional (a uuid, or
// null to leave the task without a driver); any other field is 422 not_supported.
func ParseReassign(body []byte) (Reassign, error) {
	var raw map[string]json.RawMessage
	body = bytes.TrimSpace(body)
	if len(body) > 0 {
		if body[0] != '{' {
			return Reassign{}, errBadBody()
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return Reassign{}, errBadBody()
		}
	}
	var ck checker
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !slices.Contains(reassignFields, k) {
			ck.add(k, "not_supported")
		}
	}
	var in Reassign
	switch v, ok := raw["tenantId"]; {
	case !ok || isNull(v):
		ck.add("tenantId", "required")
	default:
		id, ok := parseID(v)
		if !ok {
			ck.add("tenantId", "invalid")
		}
		in.TenantID = id
	}
	if v, ok := raw["driverId"]; ok {
		in.DriverSet = true
		if !isNull(v) {
			id, ok := parseID(v)
			if !ok {
				ck.add("driverId", "invalid")
			}
			in.DriverID = &id
		}
	}
	if err := ck.err(); err != nil {
		return Reassign{}, err
	}
	return in, nil
}

func errBadBody() error { return httpx.ErrBadRequest("request body must be a JSON object") }

func isNull(v json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }

func parseID(v json.RawMessage) (uuid.UUID, bool) {
	var s string
	if json.Unmarshal(v, &s) != nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// ReassignTask authorizes a reassignment and hands it to the Reassigner: a dispatcher holding operations:manage_tasks
// (the task must be one it reads, which the Reassigner checks under its RLS, and one its own RLS lets it update: 403
// task_not_in_reach for another carrier's task it only reads through its parties' scope).
// Platform capabilities never substitute for this grant. An API key never reassigns.
func (s *Service) ReassignTask(ctx context.Context, p *authz.Principal, rid string, taskID uuid.UUID, in Reassign) (Reassigned, error) {
	if err := authz.RequireTenantWrite(p); err != nil {
		return Reassigned{}, err
	}
	if s.mover == nil {
		return Reassigned{}, errors.New("operations: no task reassigner is wired")
	}
	dispatcher := !p.IsMachine() && p.Dispatcher && p.Can(authz.OperationsManageTasks)
	if !dispatcher {
		return Reassigned{}, authz.ErrPermissionDenied("a task moves to another tenant only by an authorized dispatcher").
			WithDetails(map[string]any{"reason": ReasonDispatcherOrPlatform})
	}
	in.Platform = false // D1: platform capabilities never authorize tenant reassignment.
	return s.mover.ReassignTask(ctx, p, rid, taskID, in)
}
