package iam

import (
	"net/http"

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
	return []ingress.Group{{
		Prefix: "/v1/api-keys",
		Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need, h.list)
			r.Post("", requireAuth, need, h.create)
			r.Delete("/:id", requireAuth, need, h.revoke)
		},
	}}
}

type apiKeysHTTP struct{ k *APIKeys }

func (h apiKeysHTTP) list(c fiber.Ctx) error {
	cl, err := adminHTTP{}.call(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.k.List(cl, limit, c.Query("cursor"))
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
	if err := httpx.DecodeJSON(c, &in); err != nil {
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
