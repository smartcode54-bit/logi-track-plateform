package httpx

import (
	"github.com/gofiber/fiber/v3"
	"net/http"
)

// Empty seals an explicitly registered bodyless success. HEAD policies still
// require their GET/schema or an explicit HEAD binding; 304 is never supported.
func Empty(c fiber.Ctx, status int) error {
	if !projectionRequired(c) {
		return c.SendStatus(status)
	}
	p := EndpointPolicyFrom(c)
	a := ProjectionAudienceFrom(c)
	if p.SchemaID != "empty" || status != http.StatusNoContent || a.Finalize == nil {
		return ErrSupportProjectionUnavailable()
	}
	owners := []string{}
	if a.Support {
		owners = append(owners, a.Tenant)
	}
	return a.Finalize(c.Context(), owners, func(consents map[string]Consent) error {
		if a.Support {
			current, ok := consents[a.Tenant]
			if !ok || current.Version != a.ConsentVersion {
				return NewError(401, "token_expired", "token expired").WithDetails(map[string]any{"reason": "claims_changed"})
			}
		}
		cleanProjectionHeaders(c)
		if err := c.Status(status).Send(nil); err != nil {
			return err
		}
		sealResponse(c)
		return nil
	})
}
