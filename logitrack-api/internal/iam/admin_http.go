package iam

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/supportprojection"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
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
// and /v1/tenants* (with the quarantine review of T24) behind requireAuth (auth.Service.RequireAuth, which resolves the principal through
// RBAC.Authorize), the carrier profiles /v1/subcontractors* of T20 (§B.2.10), and those
// of T51 (§B.2.5, §B.2.6): DELETE /v1/users/{id}, /v1/security-events, /v1/security/overview, /v1/security/status
// and /v1/stats/users-by-role (the role matrix is in RoleGroups). a may be nil while the route table is only listed.
func AdminGroups(a *Admin, requireAuth fiber.Handler) []ingress.Group {
	h := adminHTTP{a}
	need := authz.RequireCap
	groups := []ingress.Group{
		{Prefix: "/v1/users", Mount: func(r fiber.Router) {
			ingress.Bind(r, "", iamPolicy(http.MethodGet, "/v1/users", "iam.platform-user"), requireAuth, h.projection, need(authz.UsersView), h.listUsers)
			ingress.Bind(r, "", iamMutationPolicy("POST", "/v1/users", "iam.created-user"), requireAuth, h.projection, need(authz.UsersManage), h.createUser)
			ingress.Bind(r, "/:id", iamPolicy(http.MethodGet, "/v1/users/:id", "iam.platform-user"), requireAuth, h.projection, need(authz.UsersView), h.getUser)
			ingress.Bind(r, "/:id", iamMutationPolicy("PATCH", "/v1/users/:id", "iam.platform-user"), requireAuth, h.projection, need(authz.UsersManage), h.patchUser)
			r.Delete("/:id", requireAuth, authz.RequirePlatform(authz.PlatformAdmin), h.deleteUser)
			r.Post("/:id/invite", requireAuth, need(authz.UsersManage), h.invite)
			r.Post("/:id/disable", requireAuth, need(authz.UsersManage), h.disable)
			r.Post("/:id/enable", requireAuth, need(authz.UsersManage), h.enable)
			r.Post("/:id/password/temporary", requireAuth, need(authz.UsersManage), h.temporaryPassword)
			ingress.Bind(r, "/:id/sessions", iamPolicy(http.MethodGet, "/v1/users/:id/sessions", "iam.platform-session"), requireAuth, h.projection, need(authz.UsersRevokeSessions), h.sessions)
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
			searchPolicy := iamPolicy(http.MethodGet, "/v1/tenants/support-options", "iam.support-option")
			searchPolicy.CursorID = "iam.support-options"
			searchPolicy.ValidateCursor = func(s string, aud httpx.ProjectionAudience) bool {
				if a == nil {
					return false
				}
				actor, err := uuid.Parse(aud.Actor)
				if err != nil {
					return false
				}
				cur, ok := a.readSupportCursor(s)
				return ok && cur.Actor == actor
			}
			ingress.Bind(r, "/support-options", searchPolicy, requireAuth, h.projection, h.supportOptions)
			statePolicy := iamPolicy(http.MethodGet, "/v1/tenants/:id/support-access", "iam.support-access")
			ingress.Bind(r, "/:id/support-access", statePolicy, requireAuth, h.projection, h.getSupportAccess)
			r.Put("/:id/support-access", requireAuth, h.putSupportAccess)
			r.Post("/:id/members/:userId/ratify", requireAuth, h.ratify)
			// The quarantine review of P1 (T24): platform principals only, in the system context.
			r.Get("/quarantine/rows", requireAuth, need(authz.PlatformCrossTenantRead), h.listQuarantineRows)
			r.Post("/quarantine/rows/:table/:id/rehome", requireAuth, requireQuarantineMaintenance, h.rehomeQuarantineRow)
			ingress.Bind(r, "", iamPolicy(http.MethodGet, "/v1/tenants", "iam.tenant"), requireAuth, h.projection, need(authz.PlatformManageTenants, authz.FleetManageSubcontractors), h.listTenants)
			ingress.Bind(r, "", provisioningPolicy(), requireAuth, h.projection, platformOrMemberCap(authz.PlatformManageTenants), h.createTenant)
			ingress.Bind(r, "/:id", iamPolicy(http.MethodGet, "/v1/tenants/:id", "iam.tenant-detail"), requireAuth, h.projection, h.getTenant)
			ingress.Bind(r, "/:id", tenantPatchPolicy(), requireAuth, h.projection, platformOrMemberCap(authz.PlatformManageTenants), h.patchTenant)
			ingress.Bind(r, "/:id/members", iamPolicy(http.MethodGet, "/v1/tenants/:id/members", "iam.member"), requireAuth, h.projection, need(authz.UsersView), h.listMembers)
			// T62: a person by email, existing account or invite; the same answer either way.
			r.Post("/:id/members", requireAuth, need(authz.UsersAssignRole), need(authz.UsersManage), h.addMember)
			r.Put("/:id/members/:userId", requireAuth, need(authz.UsersAssignRole), h.setMember)
			r.Delete("/:id/members/:userId", requireAuth, need(authz.UsersAssignRole), h.removeMember)
		}},
		// fields=minimal: any authenticated principal (RLS rows); the full list, GET {id} and PATCH check their
		// rule in the service (fleet:manage_subcontractors, a member, the tenant's own tenant_admin).
		{Prefix: "/v1/subcontractors", Mount: func(r fiber.Router) {
			ingress.Bind(r, "", partnerPolicy(http.MethodGet, "/v1/subcontractors", "iam.subcontractor-summary"), requireAuth, h.projection, h.listSubcontractors)
			ingress.Bind(r, "", iamMutationPolicy("POST", "/v1/subcontractors", "iam.subcontractor"), requireAuth, h.projection, need(authz.FleetManageSubcontractors, authz.PlatformManageTenants), h.createSubcontractor)
			ingress.Bind(r, "/:id", partnerPolicy(http.MethodGet, "/v1/subcontractors/:id", "iam.subcontractor"), requireAuth, h.projection, h.getSubcontractor)
			ingress.Bind(r, "/:id", iamMutationPolicy("PATCH", "/v1/subcontractors/:id", "iam.subcontractor"), requireAuth, h.projection, h.patchSubcontractor)
		}},
		{Prefix: "/v1/security-events", Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.SecurityViewAudit), authz.RequireTenant(), h.listEvents)
		}},
		{Prefix: "/v1/security", Mount: func(r fiber.Router) {
			r.Get("/overview", requireAuth, need(authz.SecurityViewOverview), authz.RequireTenant(), h.overview)
			// The status page: platform-wide counters with no tenant data, so no tenant context (support holds
			// security:view_status but not security:view_overview).
			ingress.Bind(r, "/status", iamPolicy(http.MethodGet, "/v1/security/status", "iam.system-status"), requireAuth, h.projection, platformOrMemberCap(authz.SecurityViewStatus), h.status)
		}},
		{Prefix: "/v1/stats", Mount: func(r fiber.Router) {
			r.Get("/users-by-role", requireAuth, need(authz.SecurityViewOverview), authz.RequireTenant(), h.usersByRole)
		}},
	}
	return append(groups, h.supportGroups(requireAuth)...)
}

type adminHTTP struct{ a *Admin }

func (adminHTTP) call(c fiber.Ctx) (call, error) {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return call{}, httpx.ErrUnauthenticated()
	}
	httpx.ResolveProjectionAudience(c)
	version := strconv.FormatInt(p.ContextVersion, 10)
	return call{ctx: c.Context(), p: p, rid: httpx.RequestIDFrom(c), contextVersion: version, labels: map[uuid.UUID]platformLabel{}, labelVersions: supportprojection.LabelVersions(c)}, nil
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
	if authz.IsSupport(authz.PrincipalFrom(c)) {
		return httpx.ErrSupportViewUnavailable()
	}
	if p := authz.PrincipalFrom(c); p != nil && p.IsPlatform() {
		return h.platformUsers(c)
	}
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
	if authz.IsSupport(authz.PrincipalFrom(c)) {
		return httpx.ErrSupportViewUnavailable()
	}
	if p := authz.PrincipalFrom(c); p != nil && p.IsPlatform() {
		return h.platformUser(c)
	}
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
	rows := []User{res.User}
	if err := h.a.projectUserLabels(cl, rows); err != nil {
		return err
	}
	res.User = rows[0]
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
	rows := []User{*u}
	if err := h.a.projectUserLabels(cl, rows); err != nil {
		return err
	}
	*u = rows[0]
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
	if p := authz.PrincipalFrom(c); p != nil && p.IsPlatform() {
		return h.platformSessions(c)
	}
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
	if authz.IsSupport(authz.PrincipalFrom(c)) {
		return httpx.ErrSupportViewUnavailable()
	}
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
	if authz.IsSupport(authz.PrincipalFrom(c)) {
		return httpx.ErrSupportViewUnavailable()
	}
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
	if lifecycle, ok := httpx.PolicyInput[httpx.LifecycleRequest](c); ok {
		in.Status = Field[string]{Set: true, V: lifecycle.Status}
		in.Reason = Field[string]{Set: true, V: lifecycle.Reason}
	} else if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	t, err := h.a.PatchTenant(cl, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, t)
}

func (h adminHTTP) listMembers(c fiber.Ctx) error {
	if p := authz.PrincipalFrom(c); p != nil && p.IsPlatform() {
		if authz.IsSupport(p) {
			return httpx.ErrSupportViewUnavailable()
		}
		return h.platformMembers(c)
	}
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
	var in SetMemberInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	res, err := h.a.SetMember(cl, tenant, user, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}

func (h adminHTTP) addMember(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	tenant, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in AddMemberInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	res, err := h.a.AddMember(cl, tenant, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, res)
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

func (h adminHTTP) listSubcontractors(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "fields", "q", "status", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.a.ListSubcontractors(cl, ListSubcontractorsInput{Fields: c.Query("fields"), Q: c.Query("q"),
		Status: c.Query("status"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	switch rows := out.(type) {
	case []SubcontractorMinimal:
		return httpx.PageAs(c, "iam.subcontractor-minimal", rows, next, nil)
	case []SubcontractorSummary:
		return httpx.Page(c, rows, next, nil)
	default:
		return httpx.ErrSupportProjectionUnavailable()
	}
}

func (h adminHTTP) getSubcontractor(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	out, err := h.a.GetSubcontractor(cl, id)
	if err != nil {
		return err
	}
	noStore(c) // may carry an ID card number and short-lived document URLs
	return httpx.JSON(c, http.StatusOK, out)
}

func (h adminHTTP) createSubcontractor(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	var in CreateSubcontractorInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.a.CreateSubcontractor(cl, in)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h adminHTTP) patchSubcontractor(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var in PatchSubcontractorInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.a.PatchSubcontractor(cl, id, in)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusOK, out)
}

func (h adminHTTP) deleteUser(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if err := h.a.DeleteUser(cl, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h adminHTTP) listEvents(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "type", "severity", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	rows, next, err := h.a.ListEvents(cl, EventsInput{Type: c.Query("type"), Severity: c.Query("severity"),
		From: c.Query("from"), To: c.Query("to"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, rows, next, EventsMeta{Types: security.EventTypes})
}

func (h adminHTTP) overview(c fiber.Ctx) error {
	if p := authz.PrincipalFrom(c); p != nil && p.IsPlatform() {
		return httpx.ErrSupportViewUnavailable()
	}
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	o, err := h.a.Overview(cl)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusOK, o)
}

func (h adminHTTP) status(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	s, err := h.a.Status(cl)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusOK, s)
}

func (h adminHTTP) usersByRole(c fiber.Ctx) error {
	if p := authz.PrincipalFrom(c); p != nil && p.IsPlatform() {
		return httpx.ErrSupportViewUnavailable()
	}
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	out, err := h.a.StatsUsersByRole(cl)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusOK, out)
}

func (h adminHTTP) getMatrix(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "tenantId", "layer"); err != nil {
		return err
	}
	m, err := h.a.GetMatrix(cl, MatrixTarget{TenantID: c.Query("tenantId"), Layer: c.Query("layer")})
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusOK, m)
}

// MatrixMeta is the meta of PUT /v1/roles/matrix: how many override rows the save changed (0: nothing was written,
// no event, no rbac:ver bump).
type MatrixMeta struct {
	Changes int `json:"changes"`
}

func (h adminHTTP) putMatrix(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "tenantId", "layer"); err != nil {
		return err
	}
	var in SaveMatrixInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	m, n, err := h.a.SaveMatrix(cl, MatrixTarget{TenantID: c.Query("tenantId"), Layer: c.Query("layer")}, in)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, m, "", MatrixMeta{Changes: n})
}
