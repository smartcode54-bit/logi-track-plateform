package supportprojection

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

func Policy(method, route, schema string) httpx.EndpointPolicy {
	p := httpx.EndpointPolicy{ID: method + " " + route, Method: method, Route: route, Class: httpx.PolicyPlatform, Plane: "platform", Target: "explicit_platform", PredicateID: "iam.explicit-platform.v1", Owner: "P1.2"}
	if method != "GET" && method != "HEAD" {
		p.Class = httpx.PolicyTenantWrite
	}
	// Bind the reviewed seed verbatim. Legacy compatibility routes still project
	// ordinary platform reads, but cannot create new in-support admissions.
	for _, seed := range authz.SupportPolicySeed() {
		if seed.ID == p.ID {
			p = seed
			break
		}
	}
	p.SchemaID, p.CursorID = schema, "iam.keyset"
	p.ValidateCursor = func(s string, _ httpx.ProjectionAudience) bool {
		ok := decodeKeyset(s)
		if !ok {
			_, ok = security.DecodeCursor(s)
		}
		return ok
	}
	p.ResolveTarget = func(c fiber.Ctx) (httpx.PolicyTarget, error) {
		if p.Plane == "tenant" {
			return httpx.PolicyTarget{Plane: "tenant", TenantID: httpx.ProjectionAudienceFrom(c).Tenant}, nil
		}
		return httpx.PolicyTarget{Plane: "platform"}, nil
	}
	return p
}
func decodeKeyset(s string) bool {
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil {
		return false
	}
	var c struct {
		T  *time.Time `json:"t"`
		ID uuid.UUID  `json:"id"`
	}
	return json.Unmarshal(b, &c) == nil && c.ID != uuid.Nil
}
