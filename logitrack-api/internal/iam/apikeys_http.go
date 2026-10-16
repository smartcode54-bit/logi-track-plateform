package iam

import (
	"github.com/google/uuid"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// APIKeyGroups is the route group of the API keys administration (T32 slice T32K, Appendix B §B.2.6) on the
// internal listener: GET /v1/api-keys, POST /v1/api-keys and DELETE /v1/api-keys/{id}, each behind
// security:manage_api_keys. requireAuth is auth.Service.RequireAuth() without AllowAPIKeys: keys are managed
// by people, so an X-Api-Key here is 403. k may be nil while the route table is only listed.
func APIKeyGroups(k *APIKeys, requireAuth fiber.Handler) []ingress.Group {
	h := apiKeysHTTP{k}
	need := authz.RequireCap(authz.SecurityManageAPIKeys)
	groups := []ingress.Group{{
		Prefix: "/v1/api-keys",
		Mount: func(r fiber.Router) {
			ingress.Bind(r, "", iamPolicy(http.MethodGet, "/v1/api-keys", "iam.platform-api-key"), requireAuth, h.projection, need, h.list)
			p := iamPolicy(http.MethodPost, "/v1/api-keys", "iam.created-api-key")
			p.Predicate = httpx.APIKeyPredicate(func(key string) string {
				info, ok := authz.Lookup(authz.Cap(key))
				if !ok {
					return ""
				}
				return string(info.Class)
			})
			original := p.Predicate
			p.Predicate = func(c fiber.Ctx) (httpx.PolicyClass, error) {
				// Ordinary callers retain Create's field-level 422 validation. The strict
				// classifier is only needed to admit an in-support platform exception.
				if !authz.IsSupport(authz.PrincipalFrom(c)) {
					return httpx.PolicyPlatform, nil
				}
				return original(c)
			}
			ingress.Bind(r, "", p, requireAuth, h.projection, need, h.create)
			r.Delete("/:id", requireAuth, need, h.revoke)
		},
	}}
	groups = append(groups, ingress.Group{Prefix: "/v1/platform/api-keys", Mount: func(r fiber.Router) {
		ingress.Bind(r, "", iamPolicy(http.MethodGet, "/v1/platform/api-keys", "iam.platform-api-key"), requireAuth, h.projection, func(c fiber.Ctx) error {
			if err := platformRead(authz.PrincipalFrom(c), authz.SecurityManageAPIKeys); err != nil {
				return err
			}
			return c.Next()
		}, h.list)
	}})
	return groups
}

type apiKeysHTTP struct{ k *APIKeys }

func (h apiKeysHTTP) projection(c fiber.Ctx) error { return PlatformProjection(h.k.pool)(c) }

func (h apiKeysHTTP) list(c fiber.Ctx) error {
	cl, err := adminHTTP{}.call(c)
	if err != nil {
		return err
	}
	platformEndpoint := strings.HasPrefix(httpx.EndpointPolicyFrom(c).Route, "/v1/platform/")
	allowed := []string{"limit", "cursor"}
	if platformEndpoint {
		allowed = append(allowed, "tenantId")
	}
	if _, err := httpx.StrictQuery(c, allowed...); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	var out []APIKey
	var next string
	if cl.p.IsPlatform() {
		if err := platformRead(cl.p, authz.SecurityManageAPIKeys); err != nil {
			return err
		}
		if limit > 100 {
			return errInvalid(violation("limit", "out_of_range"))
		}
		r := listReach(cl.p)
		if platformEndpoint {
			r = keyReach{all: true}
		}
		if raw := c.Query("tenantId"); raw != "" {
			id, e := uuid.Parse(raw)
			if e != nil || id == uuid.Nil {
				return errInvalid(violation("tenantId", "invalid"))
			}
			r = keyReach{tenants: []uuid.UUID{id}}
		}
		out, next, err = h.k.list(cl, limit, c.Query("cursor"), r)
	} else {
		out, next, err = h.k.List(cl, limit, c.Query("cursor"))
	}
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, out, next, nil)
}

func (h apiKeysHTTP) create(c fiber.Ctx) error {
	cl, err := adminHTTP{}.call(c)
	if err != nil {
		return err
	}
	var in CreateAPIKeyInput
	if validated, ok := httpx.PolicyInput[httpx.APIKeyRequest](c); ok {
		in = CreateAPIKeyInput{Name: validated.Name, Scope: validated.Scope, Capabilities: validated.Capabilities, TenantID: validated.TenantID, ExpiresAt: validated.ExpiresAt}
	} else if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.k.Create(cl, in)
	if err != nil {
		return err
	}
	noStore(c) // the secret is shown once (R49)
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h apiKeysHTTP) revoke(c fiber.Ctx) error {
	cl, err := adminHTTP{}.call(c)
	if err != nil {
		return err
	}
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if err := h.k.Revoke(cl, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}
