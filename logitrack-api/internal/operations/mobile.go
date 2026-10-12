package operations

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// The operations routes of the driver-app group (/v1/mobile, public listener and internal; Appendix B §B.2.21). T32
// ships the callable shim target of addDeliveryStop; the routes of APK 4.x (T55, P7a) join here.

// CodeNotTaskOwner is the refusal of a driver acting on another driver's task or trip (Appendix B §B.1.5; R28: the
// task's driver or helper only, no phone or plate fallback).
const CodeNotTaskOwner = billing.CodeNotTaskOwner

// TaskOwner is the task a driver-app route names, read past the caller's RLS: its id and the drivers that may act on
// it.
type TaskOwner struct {
	ID             uuid.UUID
	DriverID       *uuid.UUID
	HelperDriverID *uuid.UUID
}

// TaskOwners reads past a driver's RLS what its task routes need (iam.TaskOwners, read-only system transactions:
// operations opens only principal transactions, the WithSystem allow-list).
//
//   - TaskOwner resolves the {id} of a driver-app task route of any tenant: the PostgreSQL id (a canonical uuid) or the
//     Firestore task doc id (tasks.legacy_doc_id, R25). ok is false for no such task (a quarantined one included: it
//     is invisible to every principal but a platform reader).
//   - StopParty reads the Firestore side of the billing party a driver's stop takes from its hub (operationsdb
//     WriteParty): the carrier behind a partner-linked hub is outside a driver's tenant reach (tenants.p_read), yet
//     the write-back names it on the stop as addDeliveryStop did. ok is false for no such party.
type TaskOwners interface {
	TaskOwner(ctx context.Context, ref string) (TaskOwner, bool, error)
	StopParty(ctx context.Context, id uuid.UUID) (operationsdb.WritePartyRow, bool, error)
}

// maxTaskRef bounds the {id} of a driver-app task route (a uuid or a Firestore doc id, at most 1500 bytes).
const maxTaskRef = 1500

// ReasonTaskNotFound is the details.reason of the 404 of a driver-app task route whose task does not exist for the
// driver (no such id or doc id, a quarantined task, a task document gone from Firestore). It tells a missing task
// from every other 404 a shim can meet (a route an older API lacks, an API without FIREBASE_PROJECT_ID, a wrong
// LOGITRACK_API_BASE_URL): the addDeliveryStop shim answers "Task not found" for this reason only and logs any other
// 404 as an error (T32R).
const ReasonTaskNotFound = "task_not_found"

// errTaskNotFound is the 404 of a driver-app task route for a task that does not exist for the driver.
func errTaskNotFound() *httpx.Error {
	return httpx.ErrNotFound().WithDetails(map[string]any{"reason": ReasonTaskNotFound})
}

// driverStopFields are the body fields of the driver's stops route: the staff fields without
// destinationLinkedPartyId (a driver never chooses the billing party of a stop; the stop takes the party of its hub,
// which is the customer the APK sends, AddDeliveryStopDialog: h.linkedCustomerId).
var driverStopFields = []string{"destination", "destinationHubId", "sourceId", "isCustom"}

// MobileRoutes are the operations routes below /v1/mobile, mounted by app.MobileGroups:
//
//	POST /v1/mobile/tasks/{id}/stops   mobile:submit_trip + driver:self; requireShim admits the addDeliveryStop
//	                                   shim (a cf_shim API key with the driver's Firebase ID token, T32)
//
// s may be nil while the route table is only listed.
func MobileRoutes(s *Service, requireShim fiber.Handler) func(r fiber.Router) {
	h := handlers{s}
	return func(r fiber.Router) {
		r.Post("/tasks/:id/stops", requireShim, authz.RequireCap(authz.MobileSubmitTrip), h.addStopDriver)
	}
}

func (h handlers) addStopDriver(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	ref, err := url.PathUnescape(c.Params("id"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	if len(c.Queries()) > 0 {
		return httpx.ErrBadRequest("this route takes no query parameter")
	}
	res, err := h.s.AddTaskStopForDriver(c.Context(), p, httpx.RequestIDFrom(c), ref, c.Body())
	if err != nil {
		return err
	}
	return httpx.JSON(c, res.Status, res.Data)
}

// MobileStopAnswer is the answer of POST /v1/mobile/tasks/{id}/stops: the task's stops after the new one (without
// billing parties), or, while the write-back waits for the mirror (202, pending), none; upgradedFromSingle is whether
// the task was not multi-drop before (the addDeliveryStop shim answers "Task upgraded to multi-delivery; stop added"
// then, else "Delivery stop added successfully", multiDeliveryTrips.ts:448-452).
type MobileStopAnswer struct {
	TaskID             uuid.UUID     `json:"taskId"`
	UpgradedFromSingle bool          `json:"upgradedFromSingle"`
	Pending            bool          `json:"pending"`
	Stops              []TaskStopRow `json:"stops"`
}

// AddTaskStopForDriver is POST /v1/mobile/tasks/{id}/stops {destination, destinationHubId?, sourceId?, isCustom?},
// the target of the addDeliveryStop shim (Appendix B §B.2.21, §B.2.22; R45): the service of POST /v1/tasks/{id}/stops
// for the driver acting as itself (driver:self) on a task it drives or helps on (R28; another driver's task is 403
// not_task_owner, no task 404). {id} is the PostgreSQL id or the Firestore task doc id (legacy_doc_id). The write goes
// to PostgreSQL under the driver's RLS (tasks.p_driver_update) or, while operations is Firestore-owned, to the task
// document first; the outbox gets task.updated either way. A missing task is 404 with details.reason task_not_found.
//
// Called through the addDeliveryStop shim (p.APIKeyScope cf_shim) the write-back answers right after the Firestore
// commit (202 pending, upgradedFromSingle read from the document before the write) instead of waiting up to 3 s for
// the mirror: the shim reads only upgradedFromSingle and the 3.x APK reads the Firestore document, so the wait would
// only add the mirror's lag to the < 300 ms the shim may add (AC of T32). The APK 4.x callers keep the wait
// (read-your-writes, main spec §11.10).
func (s *Service) AddTaskStopForDriver(ctx context.Context, p *authz.Principal, rid, ref string, body []byte) (WriteResult, error) {
	if !driverPrincipal(p) {
		return WriteResult{}, authz.ErrPermissionDenied("the route acts for a driver (driver:self)")
	}
	if !p.Can(authz.MobileSubmitTrip) {
		return WriteResult{}, authz.ErrPermissionDenied("missing capability", authz.MobileSubmitTrip)
	}
	if _, err := s.mode(); err != nil {
		return WriteResult{}, err
	}
	in, err := parseStop(body, driverStopFields)
	if err != nil {
		return WriteResult{}, err
	}
	if s.owners == nil {
		return WriteResult{}, errors.New("operations: the driver task routes need a task owner resolver (iam.TaskOwners)")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > maxTaskRef {
		return WriteResult{}, errTaskNotFound()
	}
	task, ok, err := s.owners.TaskOwner(ctx, ref)
	if err != nil {
		return WriteResult{}, err
	}
	if !ok {
		return WriteResult{}, errTaskNotFound()
	}
	me := *p.DriverID
	if !sameID(task.DriverID, &me) && !sameID(task.HelperDriverID, &me) {
		return WriteResult{}, httpx.NewError(http.StatusForbidden, CodeNotTaskOwner, "the task belongs to another driver")
	}
	wait := waitForMirror
	if p.APIKeyScope == authz.KeyCFShim {
		wait = answerOnCommit
	}
	out, err := s.addTaskStop(ctx, p, rid, task.ID, in, wait)
	if he, ok := errors.AsType[*httpx.Error](err); ok && he.Status == http.StatusNotFound {
		// The task left the driver's view between the lookup and the write (its row, or its Firestore document).
		return WriteResult{}, errTaskNotFound().Wrap(err)
	}
	if err != nil {
		return WriteResult{}, err
	}
	ans := MobileStopAnswer{TaskID: task.ID, UpgradedFromSingle: !out.wasMulti, Stops: out.stops}
	status := http.StatusOK
	if out.echo != nil {
		ans.Pending, ans.Stops, status = true, []TaskStopRow{}, http.StatusAccepted
	}
	if ans.Stops == nil {
		ans.Stops = []TaskStopRow{}
	}
	return WriteResult{Status: status, Data: ans}, nil
}
