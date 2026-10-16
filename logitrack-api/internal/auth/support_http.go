package auth

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

type tenantContextBody struct {
	TenantID *string `json:"tenantId"`
	Mode     *string `json:"mode"`
}

var sessionContextSchema = httpx.NewSchema[SessionContextView]("auth.session_context", map[string]httpx.Field{"mode": {Kind: httpx.Safe}, "version": {Kind: httpx.Safe}})
var supportAccessSchema = httpx.NewSchema[SupportAccessView]("auth.support_access", map[string]httpx.Field{
	"tenantId": {Kind: httpx.Safe}, "enabled": {Kind: httpx.Safe}, "masked": {Kind: httpx.Safe}, "version": {Kind: httpx.Safe}, "readOnly": {Kind: httpx.Safe}, "visitId": {Kind: httpx.Safe},
})

// Labels are projected in the repository; writeMe/writeTenantLabels preserve their redactions.
// Finalize checks every label's captured consent version.
var tenantLabelSchema = httpx.NewSchema[Tenant]("auth.tenant_label", map[string]httpx.Field{
	"id": {Kind: httpx.Safe}, "nameTh": {Kind: httpx.Safe}, "nameEn": {Kind: httpx.Safe}, "kind": {Kind: httpx.Safe}, "role": {Kind: httpx.Safe}, "status": {Kind: httpx.Safe}, "code": {Kind: httpx.Safe}, "reference": {Kind: httpx.Safe},
}, func(t Tenant) string { return t.ID.String() })
var ownDriverSchema = httpx.NewSchema[DriverRef]("auth.own_driver", map[string]httpx.Field{"id": {Kind: httpx.Safe}})
var ownCustomerScopeSchema = httpx.NewSchema[CustomerScope]("auth.own_scope", map[string]httpx.Field{"billingPartyId": {Kind: httpx.Safe}, "name": {Kind: httpx.Safe}})
var _ = httpx.NewSchema[Me]("auth.me", map[string]httpx.Field{
	"id": {Kind: httpx.Safe}, "email": {Kind: httpx.Safe}, "displayName": {Kind: httpx.Safe}, "photoUrl": {Kind: httpx.Safe},
	"tenant": {Kind: httpx.Nested, Schema: tenantLabelSchema}, "tenants": {Kind: httpx.Nested, Schema: tenantLabelSchema},
	"platformRoles": {Kind: httpx.Safe}, "dispatcher": {Kind: httpx.Safe}, "steward": {Kind: httpx.Safe}, "driver": {Kind: httpx.Nested, Schema: ownDriverSchema},
	"customerScopes": {Kind: httpx.Nested, Schema: ownCustomerScopeSchema}, "capabilities": {Kind: httpx.Safe}, "mustChangePassword": {Kind: httpx.Safe}, "legacyAuthUid": {Kind: httpx.Safe},
	"sessionContext": {Kind: httpx.Nested, Schema: sessionContextSchema}, "supportAccess": {Kind: httpx.Nested, Schema: supportAccessSchema}, "platformCapabilities": {Kind: httpx.Safe},
})
var _ = httpx.NewSchema[SessionView]("auth.sessions", map[string]httpx.Field{
	"id": {Kind: httpx.Safe}, "platform": {Kind: httpx.Safe}, "amr": {Kind: httpx.Safe}, "installId": {Kind: httpx.Safe}, "deviceLabel": {Kind: httpx.Safe}, "appVersion": {Kind: httpx.Safe}, "ip": {Kind: httpx.Safe}, "userAgent": {Kind: httpx.Safe}, "createdAt": {Kind: httpx.Safe}, "lastSeenAt": {Kind: httpx.Safe}, "current": {Kind: httpx.Safe},
})
var _ = httpx.NewSchema[AccessToken]("auth.access", map[string]httpx.Field{"accessToken": {Kind: httpx.Safe}, "expiresIn": {Kind: httpx.Safe}})
var _ = httpx.NewSchema[SSETicket]("auth.sse_ticket", map[string]httpx.Field{"ticket": {Kind: httpx.Safe}, "expiresIn": {Kind: httpx.Safe}})

func bindOwn(r fiber.Router, method, path, full, schema string, class httpx.PolicyClass, handler any, handlers ...any) {
	predicate := "own-account-only,no-tenant-mutation,target-label-projection"
	if class == httpx.PolicySession {
		predicate = "live-own-session,existing-auth-guards,entry-disabled"
	}
	ingress.Bind(r, path, httpx.EndpointPolicy{ID: method + " " + full, Method: method, Route: full, Class: class, Phase: 1, Plane: "self", Target: "authenticated_caller", PredicateID: predicate, SchemaID: schema, Owner: "P1.1"}, handler, handlers...)
}

func (s *Service) publishContext(c fiber.Ctx, raw string) error {
	p, err := s.Authenticate(c.Context(), raw)
	if err != nil {
		return err
	}
	setPrincipal(c, p)
	a := httpx.ProjectionAudienceFrom(c)
	if authz.IsSupport(p) {
		a.Finalize = s.finalizeSupportAudience(p)
	} else {
		a = httpx.ProjectionAudience{Platform: p.IsPlatform(), Actor: p.UserID.String(), Session: p.SessionID.String(), ContextVersion: strconv.FormatInt(p.ContextVersion, 10)}
		a.Finalize = func(ctx context.Context, _ []string, fn func(map[string]httpx.Consent) error) error {
			if err := s.CheckSession(ctx, p); err != nil {
				return err
			}
			return fn(map[string]httpx.Consent{})
		}
	}
	httpx.SetProjectionAudience(c, a)
	return nil
}

// A committed logout/revocation/password operation has no data to disclose and may have ended its
// own credential. Seal only its bodyless response, never reuse this receipt for account data.
func completedSessionEmpty(c fiber.Ctx) error {
	a := httpx.ProjectionAudienceFrom(c)
	if a.Support {
		a.Finalize = func(_ context.Context, _ []string, fn func(map[string]httpx.Consent) error) error {
			// The committed operation has ended the credential. Keep its audience
			// identity stable for the outer receipt check; this response has no data.
			return fn(map[string]httpx.Consent{a.Tenant: {Version: a.ConsentVersion}})
		}
		httpx.SetProjectionAudience(c, a)
	}
	return httpx.Empty(c, http.StatusNoContent)
}

func labelRedactions(t *Tenant, prefix string) []httpx.RedactedField {
	if t == nil {
		return nil
	}
	fields := make([]httpx.RedactedField, 0, len(t.redacted))
	for _, field := range t.redacted {
		fields = append(fields, httpx.RedactedField{Path: prefix + "/" + field.Path, Kind: field.Kind})
	}
	return fields
}

func writeMe(c fiber.Ctx, me *Me) error {
	fields := labelRedactions(me.Tenant, "/data/tenant")
	for i := range me.Tenants {
		fields = append(fields, labelRedactions(&me.Tenants[i], "/data/tenants/"+strconv.Itoa(i))...)
	}
	return httpx.JSONWithRedactions(c, http.StatusOK, me, fields)
}

func writeTenantLabels(c fiber.Ctx, tenants []Tenant) error {
	var fields []httpx.RedactedField
	for i := range tenants {
		fields = append(fields, labelRedactions(&tenants[i], "/data/"+strconv.Itoa(i))...)
	}
	return httpx.JSONWithRedactions(c, http.StatusOK, tenants, fields)
}
