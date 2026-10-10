package iam

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Field is an optional JSON field of a PATCH body: Set when the key is present, Null when its value is null.
type Field[T any] struct {
	Set  bool
	Null bool
	V    T
}

// UnmarshalJSON implements json.Unmarshaler (called for null too).
func (f *Field[T]) UnmarshalJSON(b []byte) error {
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		f.Null = true
		return nil
	}
	return json.Unmarshal(b, &f.V)
}

// AdminGroups are the route groups of T19 on the internal listener (Appendix B §B.2.4, §B.2.5): /v1/users*
// and /v1/tenants* behind requireAuth (auth.Service.RequireAuth, which resolves the principal through
// RBAC.Authorize, X-Act-On-Tenant included). a may be nil while the route table is only listed.
func AdminGroups(a *Admin, requireAuth fiber.Handler) []ingress.Group {
	h := adminHTTP{a}
	need := authz.RequireCap
	return []ingress.Group{
		{Prefix: "/v1/users", Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.UsersView), h.listUsers)
			r.Post("", requireAuth, need(authz.UsersManage), h.createUser)
			r.Get("/:id", requireAuth, need(authz.UsersView), h.getUser)
			r.Patch("/:id", requireAuth, need(authz.UsersManage), h.patchUser)
			r.Post("/:id/invite", requireAuth, need(authz.UsersManage), h.invite)
			r.Post("/:id/disable", requireAuth, need(authz.UsersManage), h.disable)
			r.Post("/:id/enable", requireAuth, need(authz.UsersManage), h.enable)
			r.Post("/:id/password/temporary", requireAuth, need(authz.UsersManage), h.temporaryPassword)
			r.Get("/:id/sessions", requireAuth, need(authz.UsersRevokeSessions), h.sessions)
			r.Delete("/:id/sessions", requireAuth, need(authz.UsersRevokeSessions), h.revokeSessions)
			r.Delete("/:id/sessions/:sid", requireAuth, need(authz.UsersRevokeSessions), h.revokeSession)
			// customer: users:assign_role (+ steward); dispatcher: platform:manage_platform_roles (checked per kind).
			r.Put("/:id/scopes/:kind", requireAuth, need(authz.UsersAssignRole, authz.PlatformManageRoles), h.setScopes)
			r.Delete("/:id/scopes/:kind", requireAuth, need(authz.UsersAssignRole, authz.PlatformManageRoles), h.deleteScopes)
			r.Put("/:id/driver-link", requireAuth, need(authz.DriversEdit), need(authz.UsersManage), h.linkDriver)
			r.Delete("/:id/driver-link", requireAuth, need(authz.DriversEdit), need(authz.UsersManage), h.unlinkDriver)
			r.Post("/:id/platform-roles", requireAuth, need(authz.PlatformManageRoles), h.grantPlatformRole)
			r.Delete("/:id/platform-roles/:role", requireAuth, need(authz.PlatformManageRoles), h.revokePlatformRole)
		}},
		{Prefix: "/v1/tenants", Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.PlatformManageTenants, authz.FleetManageSubcontractors), h.listTenants)
			r.Post("", requireAuth, need(authz.PlatformManageTenants), h.createTenant)
			r.Get("/:id", requireAuth, h.getTenant)
			r.Patch("/:id", requireAuth, need(authz.PlatformManageTenants), h.patchTenant)
			r.Get("/:id/members", requireAuth, need(authz.UsersView), h.listMembers)
			r.Put("/:id/members/:userId", requireAuth, need(authz.UsersAssignRole), h.setMember)
			r.Delete("/:id/members/:userId", requireAuth, need(authz.UsersAssignRole), h.removeMember)
		}},
	}
}

type adminHTTP struct{ a *Admin }

func (adminHTTP) call(c fiber.Ctx) (call, error) {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return call{}, httpx.ErrUnauthenticated()
	}
	return call{ctx: c.Context(), p: p, rid: httpx.RequestIDFrom(c)}, nil
}

// pathID parses a uuid path parameter; anything else is 404 (no such resource).
func pathID(c fiber.Ctx, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params(name))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, httpx.ErrNotFound()
	}
	return id, nil
}

// onlyQuery refuses an undocumented query parameter (400 bad_request, Appendix B §B.1.5).
func onlyQuery(c fiber.Ctx, allowed ...string) error {
	for k := range c.Queries() {
		ok := false
		for _, a := range allowed {
			if k == a {
				ok = true
				break
			}
		}
		if !ok {
			return httpx.ErrBadRequest("unknown query parameter " + k)
		}
	}
	return nil
}

// queryLimit reads ?limit (0 when absent); a non-number is 422.
func queryLimit(c fiber.Ctx) (int, error) {
	v := c.Query("limit")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, errInvalid(violation("limit", "out_of_range"))
	}
	if n == 0 {
		n = -1 // an explicit 0 is out of range, not the default
	}
	return n, nil
}

func noStore(c fiber.Ctx) { c.Set(fiber.HeaderCacheControl, "no-store") }

func (h adminHTTP) listUsers(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "q", "role", "status", "sort", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	users, next, err := h.a.ListUsers(cl.ctx, cl.p, ListUsersInput{Q: c.Query("q"), Role: c.Query("role"),
		Status: c.Query("status"), Sort: c.Query("sort"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, users, next, nil)
}

func (h adminHTTP) getUser(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	u, err := h.a.GetUser(cl.ctx, cl.p, id)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusOK, u)
}

func (h adminHTTP) createUser(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	var in CreateUserInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	res, err := h.a.CreateUser(cl, in)
	if err != nil {
		return err
	}
	noStore(c) // the temporary password is shown once (R29)
	return httpx.JSON(c, http.StatusCreated, res)
}

func (h adminHTTP) patchUser(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in PatchUserInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	u, err := h.a.PatchUser(cl, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, u)
}

func (h adminHTTP) invite(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in struct {
		Locale string `json:"locale"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	if err := h.a.Invite(cl, id, in.Locale); err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusAccepted, map[string]bool{"queued": true})
}

func (h adminHTTP) setDisabled(c fiber.Ctx, disabled bool) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	if err := h.a.SetDisabled(cl, id, disabled, in.Reason); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h adminHTTP) disable(c fiber.Ctx) error { return h.setDisabled(c, true) }
func (h adminHTTP) enable(c fiber.Ctx) error  { return h.setDisabled(c, false) }

func (h adminHTTP) temporaryPassword(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	pw, err := h.a.TemporaryPassword(cl, id)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusOK, map[string]string{"temporaryPassword": pw})
}

func (h adminHTTP) sessions(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	out, err := h.a.Sessions(cl, id)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusOK, out)
}

func (h adminHTTP) revokeSessions(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if err := h.a.RevokeSessions(cl, id, nil); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h adminHTTP) revokeSession(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	sid, err := pathID(c, "sid")
	if err != nil {
		return err
	}
	if err := h.a.RevokeSessions(cl, id, &sid); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h adminHTTP) setScopes(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in struct {
		BillingPartyIDs []string `json:"billingPartyIds"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	res, err := h.a.SetScopes(cl, id, c.Params("kind"), in.BillingPartyIDs)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}

func (h adminHTTP) deleteScopes(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if err := h.a.DeleteScopes(cl, id, c.Params("kind")); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h adminHTTP) linkDriver(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in struct {
		DriverID string `json:"driverId"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	driver, err := uuid.Parse(in.DriverID)
	if err != nil || driver == uuid.Nil {
		reason := "invalid"
		if in.DriverID == "" {
			reason = "required"
		}
		return errInvalid(violation("driverId", reason))
	}
	if err := h.a.LinkDriver(cl, id, driver); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h adminHTTP) unlinkDriver(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if err := h.a.UnlinkDriver(cl, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h adminHTTP) grantPlatformRole(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	if err := h.a.GrantPlatformRole(cl, id, in.Role); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h adminHTTP) revokePlatformRole(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if err := h.a.RevokePlatformRole(cl, id, c.Params("role")); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h adminHTTP) listTenants(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "kind", "status", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.a.ListTenants(cl, c.Query("kind"), c.Query("status"), limit, c.Query("cursor"))
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h adminHTTP) getTenant(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	t, err := h.a.GetTenant(cl, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, t)
}

func (h adminHTTP) createTenant(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	var in CreateTenantInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	t, err := h.a.CreateTenant(cl, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, t)
}

func (h adminHTTP) patchTenant(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in PatchTenantInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	t, err := h.a.PatchTenant(cl, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, t)
}

func (h adminHTTP) listMembers(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "role", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.a.Members(cl, id, c.Query("role"), limit, c.Query("cursor"))
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, out, next, nil)
}

func (h adminHTTP) setMember(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	tenant, err := pathID(c, "id")
	if err != nil {
		return err
	}
	user, err := pathID(c, "userId")
	if err != nil {
		return err
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	res, err := h.a.SetMember(cl, tenant, user, in.Role)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}

func (h adminHTTP) removeMember(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	tenant, err := pathID(c, "id")
	if err != nil {
		return err
	}
	user, err := pathID(c, "userId")
	if err != nil {
		return err
	}
	if err := h.a.RemoveMember(cl, tenant, user); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}
