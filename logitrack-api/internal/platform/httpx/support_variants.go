package httpx

import "github.com/gofiber/fiber/v3"

// PageAs selects an explicitly classified typed response variant of the same
// endpoint. Ownership, phase, predicates and audience remain those of the route.
func PageAs(c fiber.Ctx, schema string, items any, next string, meta any) error {
	if projectionRequired(c) {
		if _, ok := responseSchemas.Load(schema); !ok {
			return ErrSupportProjectionUnavailable()
		}
		policy := EndpointPolicyFrom(c)
		policy.SchemaID = schema
		if err := EndpointGate(c, policy); err != nil {
			return err
		}
	}
	return Page(c, items, next, meta)
}

// OrdinaryPage is a raw ordinary-member contract. It cannot serialize any bytes
// when a support or projected platform response is required.
func OrdinaryPage(c fiber.Ctx, items any, next string, meta any) error {
	if projectionRequired(c) {
		return ErrSupportViewUnavailable()
	}
	return Page(c, items, next, meta)
}

// OrdinaryRedirect preserves member downloads while refusing any projected caller.
func OrdinaryRedirect(c fiber.Ctx, target string) error {
	if projectionRequired(c) {
		return ErrSupportViewUnavailable()
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	c.Set(fiber.HeaderLocation, target)
	return c.SendStatus(fiber.StatusFound)
}
