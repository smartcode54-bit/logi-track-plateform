package operations

import (
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// TasksPrefix is the route prefix of the staff task routes (Appendix B §B.2.11, internal listener).
const TasksPrefix = "/v1/tasks"

// Groups are the operations routes on the internal listener behind requireAuth (auth.Service.RequireAuth, which
// resolves the principal through iam.RBAC.Authorize). s may be nil while the route table is only listed. The read APIs
// (T30) and the rest of the write-back (T31) join this group.
//
//	PATCH /v1/tasks/{id}   operations:manage_tasks | platform:cross_tenant_write; T28 slice: {tenantId, driverId?},
//	                       the R13 reassignment by a dispatcher (a task it reads) or a platform admin
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := taskHandlers{s}
	need := authz.RequireCap
	return []ingress.Group{{Prefix: TasksPrefix, Mount: func(r fiber.Router) {
		r.Patch("/:id", requireAuth, need(authz.OperationsManageTasks, authz.PlatformCrossTenantWrite), h.patch)
	}}}
}

type taskHandlers struct{ s *Service }

// patch is PATCH /v1/tasks/{id}: 200 with the reassignment (Reassigned).
func (h taskHandlers) patch(c fiber.Ctx) error {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil || id == uuid.Nil {
		return httpx.ErrNotFound()
	}
	if len(c.Queries()) > 0 {
		return httpx.ErrBadRequest("this route takes no query parameter")
	}
	in, err := ParseReassign(c.Body())
	if err != nil {
		return err
	}
	out, err := h.s.ReassignTask(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}
