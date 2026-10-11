package locations

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Route prefixes (Appendix B §B.2.8, internal listener).
const (
	LocationsPrefix = "/v1/vehicle-locations"
	ProbePrefix     = "/v1/admin/cartrack"
)

// Groups are the routes of T53 on the internal listener behind requireAuth (auth.Service.RequireAuth). s may be nil
// while the route table is only listed.
//
//	GET /v1/vehicle-locations[?truckId=...]  fleet:view_live_map | dispatch:view_operations
//	GET /v1/admin/cartrack/probe             platform_admin
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	return []ingress.Group{
		{Prefix: LocationsPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, authz.RequireCap(authz.FleetViewLiveMap, authz.DispatchViewOperations), h.list)
		}},
		{Prefix: ProbePrefix, Mount: func(r fiber.Router) {
			r.Get("/probe", requireAuth, authz.RequirePlatform(authz.PlatformAdmin), h.probe)
		}},
	}
}

type handlers struct{ s *Service }

// list is GET /v1/vehicle-locations: ?truckId repeated, in the bracket form (truckId[]=) or comma-separated.
func (h handlers) list(c fiber.Ctx) error {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	for k := range c.Queries() {
		if k != "truckId" && k != "truckId[]" {
			return httpx.ErrBadRequest("unknown query parameter " + k)
		}
	}
	var ids []uuid.UUID
	args := c.RequestCtx().QueryArgs()
	for _, key := range []string{"truckId", "truckId[]"} {
		for _, raw := range args.PeekMulti(key) {
			for v := range strings.SplitSeq(string(raw), ",") {
				if v = strings.TrimSpace(v); v == "" {
					continue
				}
				id, err := uuid.Parse(v)
				if err != nil {
					return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "truckId", Reason: "invalid"})
				}
				ids = append(ids, id)
			}
		}
	}
	if len(ids) > MaxTruckIDs {
		return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "truckId", Reason: "too_many", Params: map[string]any{"max": MaxTruckIDs}})
	}
	out, err := h.s.List(c.Context(), p, ids)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, out)
}

// probe is GET /v1/admin/cartrack/probe: 200 with the outcome once the worker ran the probe, 202 with the job id while
// it still runs (the job is readable through GET /v1/jobs/{id}).
func (h handlers) probe(c fiber.Ctx) error {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	if len(c.Queries()) > 0 {
		return httpx.ErrBadRequest("the probe takes no query parameter")
	}
	res, err := h.s.Probe(c.Context(), p)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, probeStatusCode(res), res)
}
