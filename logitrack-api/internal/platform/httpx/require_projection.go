package httpx

import "github.com/gofiber/fiber/v3"

// RequireResponseProjection keeps legacy platform responses behind the serializer
// without admitting their routes to the support phase allowlist.
func RequireResponseProjection(c fiber.Ctx) { c.Locals(requiredKey, true) }

// RegisterSafeErrorFieldReasons extends the closed vocabulary at startup. Values
// must be static machine reasons, never request values; error params stay hidden.
func RegisterSafeErrorFieldReasons(reasons ...string) {
	for _, reason := range reasons {
		if !errorCodeToken.MatchString(reason) {
			panic("httpx: invalid safe field reason")
		}
		safeFieldReasons = append(safeFieldReasons, reason)
	}
}
