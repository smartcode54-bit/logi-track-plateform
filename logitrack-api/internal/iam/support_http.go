package iam

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/supportprojection"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

func schemaFields(safe, person, secret string) map[string]httpx.Field {
	out := map[string]httpx.Field{}
	for _, group := range []struct {
		names string
		kind  httpx.FieldKind
	}{{safe, httpx.Safe}, {person, httpx.Person}, {secret, httpx.Secret}} {
		for _, name := range strings.Fields(group.names) {
			out[name] = httpx.Field{Kind: group.kind}
		}
	}
	return out
}

var memberUserSchema = httpx.NewSchema[MemberUser]("iam.member-user", schemaFields("id", "email displayName", ""), func(u MemberUser) string { return u.owner })
var _ = httpx.NewSchema[Member]("iam.member", func() map[string]httpx.Field {
	f := schemaFields("role status lastLoginAt grantedByPlatform ratifiedAt grantVersion eligibilityBasis eligibleToRatify", "", "")
	f["user"] = httpx.Field{Kind: httpx.Nested, Schema: memberUserSchema}
	return f
}(), func(m Member) string { return m.tenantID.String() })
var membershipSchema = httpx.NewSchema[Membership]("iam.membership", schemaFields("tenantId tenantKind role status tenantNameTh tenantNameEn", "", ""), func(m Membership) string { return m.TenantID.String() })
var scopeSchema = httpx.NewSchema[Scope]("iam.scope", schemaFields("kind billingPartyId", "name", ""), func(Scope) string { return "" })
var driverRefSchema = httpx.NewSchema[DriverRef]("iam.driver-ref", schemaFields("id", "displayName", ""), func(d DriverRef) string { return d.owner })
var userSchema = httpx.NewSchema[User]("iam.platform-user", func() map[string]httpx.Field {
	f := schemaFields("id status mustChangePassword lastLoginAt createdAt platformRoles manageAccount accountReason", "email displayName", "legacyAuthUid")
	f["photoUrl"] = httpx.Field{Kind: httpx.File}
	f["memberships"] = httpx.Field{Kind: httpx.Nested, Schema: membershipSchema}
	f["scopes"] = httpx.Field{Kind: httpx.Nested, Schema: scopeSchema}
	f["driver"] = httpx.Field{Kind: httpx.Nested, Schema: driverRefSchema}
	return f
}(), userOwner)
var _ = httpx.NewSchema[Session]("iam.platform-session", func() map[string]httpx.Field {
	f := schemaFields("id platform amr appVersion createdAt lastSeenAt", "", "")
	for _, name := range []string{"installId", "deviceLabel", "ip", "userAgent"} {
		f[name] = httpx.Field{Kind: httpx.FreeText}
	}
	return f
}(), func(s Session) string { return s.owner })
var eventDetailsSchema = httpx.NewSchema[PlatformEventDetails]("iam.platform-event-details", schemaFields("action basis outcome version", "", ""))
var _ = httpx.NewSchema[PlatformEvent]("iam.platform-event", func() map[string]httpx.Field {
	f := schemaFields("id createdAt type severity summary actorUserId targetUserId tenantId requestId", "actorEmail", "actorLegacyUid")
	f["details"] = httpx.Field{Kind: httpx.Nested, Schema: eventDetailsSchema}
	return f
}(), func(r PlatformEvent) string { return r.actorOwner })

var _ = httpx.NewSchema[SupportAccess]("iam.support-access", schemaFields("enabled version changedAt changedBy eligibleToToggle", "", ""))
var _ = httpx.NewSchema[Ratification]("iam.ratification", schemaFields("grantVersion ratifiedAt", "", ""))
var _ = httpx.NewSchema[SupportOption]("iam.support-option", schemaFields("id kind code reference nameTh nameEn status", "", ""), func(r SupportOption) string { return r.ID.String() })

func init() {
	httpx.RegisterSafeErrorFieldReasons("unknown", "in_past", "platform_key_required", "not_allowed_for_platform_key", "not_allowed_for_tenant_key", "not_allowed_for_scope")
	for _, code := range []string{CodeSupportAccessChanged, CodeMembershipGrantChanged, CodeRatificationNotAllowed, "support_access_required", CodeAlreadyExists, CodeFailedPrecondition} {
		httpx.RegisterSafeErrorCode(code)
	}
}

func iamPolicy(method, route, schema string) httpx.EndpointPolicy {
	return supportprojection.Policy(method, route, schema)
}

func (h adminHTTP) projection(c fiber.Ctx) error { return PlatformProjection(h.a.pool)(c) }

func (h adminHTTP) supportGroups(requireAuth fiber.Handler) []ingress.Group {
	return []ingress.Group{{Prefix: "/v1/platform", Mount: func(r fiber.Router) {
		bind := func(path, schema string, cap authz.Cap, handler fiber.Handler) {
			ingress.Bind(r, path, iamPolicy(http.MethodGet, "/v1/platform"+path, schema), requireAuth, h.projection, func(c fiber.Ctx) error {
				if err := platformRead(authz.PrincipalFrom(c), cap); err != nil {
					return err
				}
				return c.Next()
			}, handler)
		}
		bind("/users", "iam.platform-user", authz.UsersView, h.platformUsers)
		bind("/users/:id", "iam.platform-user", authz.UsersView, h.platformUser)
		bind("/users/:id/sessions", "iam.platform-session", authz.UsersViewSessions, h.platformSessions)
		bind("/tenants/:id/members", "iam.member", authz.UsersView, h.platformMembers)
		bind("/security-events", "iam.platform-event", authz.SecurityViewAudit, h.platformEvents)
		bind("/security/overview", "iam.platform-overview", authz.SecurityViewOverview, h.platformOverview)
		bind("/stats/users-by-role", "iam.platform-role-counts", authz.SecurityViewOverview, h.platformRoleCounts)
	}}}
}

func (h adminHTTP) supportOptions(c fiber.Ctx) error {
	if err := h.a.supportRate(c, "support_options", 60, false); err != nil {
		return err
	}
	if _, err := httpx.StrictQuery(c, "q", "limit", "cursor"); err != nil {
		return err
	}
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	rows, next, err := h.a.SupportOptions(cl, c.Query("q"), limit, c.Query("cursor"))
	if err != nil {
		return err
	}
	// Search and labels were projected together in SQL; reject changes before final serialization.
	labelVersions := map[uuid.UUID]int64{}
	for _, row := range rows {
		labelVersions[row.ID] = row.labelVersion
	}
	supportprojection.ExpectLabels(c, labelVersions)
	audience := httpx.ProjectionAudienceFrom(c)
	finalize := audience.Finalize
	audience.Finalize = func(ctx context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
		return finalize(ctx, owners, func(current map[string]httpx.Consent) error {
			for _, row := range rows {
				if v, ok := current[row.ID.String()]; !ok || v.Version != row.consentVersion {
					return httpx.NewError(401, "token_expired", "token expired").WithDetails(map[string]any{"reason": "claims_changed"})
				}
			}
			return fn(current)
		})
	}
	httpx.SetProjectionAudience(c, audience)
	noStore(c)
	return httpx.Page(c, rows, next, nil)
}
func (h adminHTTP) getSupportAccess(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	out, err := h.a.SupportAccess(cl, id, nil)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, 200, out)
}
func (h adminHTTP) putSupportAccess(c fiber.Ctx) error {
	if err := h.a.supportRate(c, "support_consent", 10, true); err != nil {
		return err
	}
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	in, err := httpx.StrictBody[SupportAccessInput](c)
	if err != nil {
		return err
	}
	out, err := h.a.SupportAccess(cl, id, &in)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, 200, out)
}
func (h adminHTTP) ratify(c fiber.Ctx) error {
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
	in, err := httpx.StrictBody[RatifyInput](c)
	if err != nil {
		return err
	}
	out, err := h.a.Ratify(cl, tenant, user, in)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, 200, out)
}
func (h adminHTTP) platformUsers(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if _, err := httpx.StrictQuery(c, "q", "status", "role", "sort", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.a.PlatformUsers(cl, ListUsersInput{Q: c.Query("q"), Status: c.Query("status"), Role: c.Query("role"), Sort: c.Query("sort"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	if err := h.a.projectUserLabels(cl, out); err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, out, next, nil)
}
func (h adminHTTP) platformUser(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	out, err := h.a.PlatformUser(cl, id)
	if err != nil {
		return err
	}
	rows := []User{*out}
	if err := h.a.projectUserLabels(cl, rows); err != nil {
		return err
	}
	*out = rows[0]
	noStore(c)
	return httpx.JSON(c, 200, out)
}
func (h adminHTTP) platformMembers(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if _, err := httpx.StrictQuery(c, "role", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	if limit > 100 {
		return errInvalid(violation("limit", "out_of_range"))
	}
	out, next, err := h.a.members(cl, id, c.Query("role"), limit, c.Query("cursor"), true)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, out, next, nil)
}
func (h adminHTTP) platformSessions(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	out, err := h.a.platformSessions(cl, id)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, 200, out)
}
func (h adminHTTP) platformEvents(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	if _, err := httpx.StrictQuery(c, "type", "severity", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.a.PlatformEvents(cl, EventsInput{Type: c.Query("type"), Severity: c.Query("severity"), From: c.Query("from"), To: c.Query("to"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, out, next, nil)
}
func (h adminHTTP) platformOverview(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	out, err := h.a.overview(cl, reach{all: true})
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, 200, out)
}
func (h adminHTTP) platformRoleCounts(c fiber.Ctx) error {
	cl, err := h.call(c)
	if err != nil {
		return err
	}
	out, err := h.a.statsUsersByRole(cl, reach{all: true})
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, 200, out)
}

// PlatformReadPolicy is the common registration contract for P1.2's platform read endpoints.
func PlatformReadPolicy(method, route, schema string) httpx.EndpointPolicy {
	return iamPolicy(method, route, schema)
}

func partnerPolicy(method, route, schema string) httpx.EndpointPolicy {
	p := iamPolicy(method, route, schema)
	p.Predicate = func(c fiber.Ctx) (httpx.PolicyClass, error) {
		if authz.IsSupport(authz.PrincipalFrom(c)) {
			return httpx.PolicyTenantRead, nil
		}
		return httpx.PolicyPlatform, nil
	}
	p.ResolveTarget = func(c fiber.Ctx) (httpx.PolicyTarget, error) {
		if principal := authz.PrincipalFrom(c); authz.IsSupport(principal) {
			if principal.TenantID == nil {
				return httpx.PolicyTarget{}, httpx.ErrSupportViewUnavailable()
			}
			return httpx.PolicyTarget{Plane: "tenant", TenantID: principal.TenantID.String()}, nil
		}
		return httpx.PolicyTarget{Plane: "platform"}, nil
	}
	return p
}

func iamMutationPolicy(method, route, schema string) httpx.EndpointPolicy {
	p := iamPolicy(method, route, schema)
	p.Predicate = func(c fiber.Ctx) (httpx.PolicyClass, error) {
		if authz.IsSupport(authz.PrincipalFrom(c)) {
			return httpx.PolicyTenantWrite, nil
		}
		return httpx.PolicyPlatform, nil
	}
	return p
}

func platformOrMemberCap(cap authz.Cap) fiber.Handler {
	return func(c fiber.Ctx) error {
		p := authz.PrincipalFrom(c)
		if authz.IsSupport(p) {
			if err := platformRead(p, cap); err != nil {
				return err
			}
			return c.Next()
		}
		return authz.RequireCap(cap)(c)
	}
}

// Provisioning is a pure platform control. It has no user grant/invite body fields.
func provisioningPolicy() httpx.EndpointPolicy {
	p := iamPolicy("POST", "/v1/tenants", "iam.tenant")
	p.Predicate = func(c fiber.Ctx) (httpx.PolicyClass, error) {
		if authz.IsSupport(authz.PrincipalFrom(c)) {
			if _, err := httpx.StrictQuery(c); err != nil {
				return httpx.PolicyDeny, err
			}
			if _, err := httpx.StrictBody[CreateTenantInput](c); err != nil {
				return httpx.PolicyTenantWrite, err
			}
		}
		return httpx.PolicyPlatform, nil
	}
	return p
}
func lifecycleOnly(in PatchTenantInput) bool {
	return in.Status.Set && !in.Status.Null && slices.Contains(tenantStatuses, in.Status.V) && in.Reason.Set && !in.Reason.Null && strings.TrimSpace(in.Reason.V) != "" && utf8.RuneCountInString(strings.TrimSpace(in.Reason.V)) <= 500 && !in.Code.Set && !in.Kind.Set && !in.NameTh.Set && !in.NameEn.Set && !in.ContractorTenantID.Set
}
func tenantPatchPolicy() httpx.EndpointPolicy {
	p := iamPolicy("PATCH", "/v1/tenants/:id", "iam.tenant")
	p.Predicate = func(c fiber.Ctx) (httpx.PolicyClass, error) {
		if !authz.IsSupport(authz.PrincipalFrom(c)) {
			return httpx.PolicyPlatform, nil
		}
		return httpx.LifecyclePredicate(c)
	}
	return p
}
