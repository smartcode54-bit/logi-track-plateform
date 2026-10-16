package hubs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Route prefixes (Appendix B §B.2.9, internal listener). The job route is its own group next to /v1/jobs (T10).
const (
	HubsPrefix      = "/v1/hubs"
	DistancesPrefix = "/v1/distances"
	JobPrefix       = "/v1/jobs/" + jobs.TypeDistancesCompute
)

// Groups are the routes of T21 on the internal listener behind requireAuth (auth.Service.RequireAuth, which resolves
// the principal through iam.RBAC.Authorize). s may be nil while the route table is only listed.
//
//	GET   /v1/hubs                                   authenticated (?stationType&q; ETag; ?limit&cursor: one keyset page)
//	GET   /v1/hubs/maps                              authenticated (ETag)
//	GET   /v1/hubs/{id}                              authenticated (a uuid or a code; ETag)
//	POST  /v1/hubs                                   operations:manage_sources (a steward key, R60)
//	POST  /v1/hubs/import                            operations:manage_sources
//	PATCH /v1/hubs/{id}                              operations:manage_sources
//	GET   /v1/distances                              authenticated (?hubId&direction&limit&cursor)
//	GET   /v1/distances/meta                         authenticated
//	GET   /v1/distances/{originId}/{destinationId}   authenticated
//	POST  /v1/jobs/distances.compute                 operations:calculate_distances (a steward key, R60)
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap
	return []ingress.Group{
		{Prefix: HubsPrefix, Mount: func(r fiber.Router) {
			r = ingress.DenyRoutes(r, "P1.0")
			r.Get("", requireAuth, h.list)
			r.Get("/maps", requireAuth, h.maps) // before /:id, which would take "maps" for a code
			r.Get("/:id", requireAuth, h.lookup)
			r.Post("", requireAuth, need(authz.OperationsManageSources), h.create)
			r.Post("/import", requireAuth, need(authz.OperationsManageSources), h.importHubs)
			r.Patch("/:id", requireAuth, need(authz.OperationsManageSources), h.patch)
		}},
		{Prefix: DistancesPrefix, Mount: func(r fiber.Router) {
			r = ingress.DenyRoutes(r, "P1.0")
			r.Get("", requireAuth, h.listDistances)
			r.Get("/meta", requireAuth, h.meta)
			r.Get("/:originId/:destinationId", requireAuth, h.pair)
		}},
		{Prefix: JobPrefix, Mount: func(r fiber.Router) {
			r = ingress.DenyRoutes(r, "P1.0")
			r.Post("", requireAuth, need(jobs.TypeCapabilities[jobs.TypeDistancesCompute]), h.computeDistances)
		}},
	}
}

type handlers struct{ s *Service }

func principal(c fiber.Ctx) (*authz.Principal, error) {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return nil, httpx.ErrUnauthenticated()
	}
	return p, nil
}

// onlyQuery refuses an undocumented query parameter (400 bad_request, Appendix B §B.1.5).
func onlyQuery(c fiber.Ctx, allowed ...string) error {
	for k := range c.Queries() {
		if !slices.Contains(allowed, k) {
			return httpx.ErrBadRequest("unknown query parameter " + k)
		}
	}
	return nil
}

// cached writes {"data": v} (and the nextCursor of a keyset page, when there is a next page) with a strong ETag of the
// body and answers 304 to a matching If-None-Match (master-data reads, Appendix B §B.1.5 "Caching headers"). The
// client revalidates every time (no-cache).
func cached(c fiber.Ctx, v any, nextCursor string) error {
	body, err := json.Marshal(struct {
		Data       any    `json:"data"`
		NextCursor string `json:"nextCursor,omitempty"`
	}{v, nextCursor})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	c.Set(fiber.HeaderETag, etag)
	c.Set(fiber.HeaderCacheControl, "private, no-cache")
	if etagMatches(c.Get(fiber.HeaderIfNoneMatch), etag) {
		return c.SendStatus(http.StatusNotModified)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(http.StatusOK).Send(body)
}

// etagMatches is the weak comparison of If-None-Match (RFC 9110 §13.1.2): any listed tag, W/ ignored, or *.
func etagMatches(header, etag string) bool {
	for t := range strings.SplitSeq(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == etag {
			return true
		}
	}
	return false
}

func (h handlers) list(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "stationType", "q", "limit", "cursor"); err != nil {
		return err
	}
	// ?limit or ?cursor asks for one keyset page; neither keeps the whole master in one response (the maps, the
	// distance matrix and the master-data pages read it so).
	in := ListInput{StationType: c.Query("stationType"), Q: c.Query("q"), Cursor: c.Query("cursor")}
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n == 0 {
			return httpx.ErrInvalidArgument(violation("limit", "out_of_range"))
		}
		in.Limit = n
	}
	in.Page = c.Query("limit") != "" || in.Cursor != ""
	out, next, err := h.s.List(c.Context(), p, in)
	if err != nil {
		return err
	}
	return cached(c, out, next)
}

// lookup is GET /v1/hubs/{id}: one hub by uuid or code (a picker's label of the value it holds).
func (h handlers) lookup(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	// Path params stay percent-encoded (as in pair): decode them, so a code with a space or Thai text resolves.
	ref, err := url.PathUnescape(c.Params("id"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	out, err := h.s.Lookup(c.Context(), p, ref)
	if err != nil {
		return err
	}
	return cached(c, out, "")
}

func (h handlers) maps(c fiber.Ctx) error {
	if _, err := principal(c); err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	out, err := h.s.Maps(c.Context())
	if err != nil {
		return err
	}
	return cached(c, out, "")
}

func (h handlers) create(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in HubInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Create(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) patch(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil || id == uuid.Nil {
		return httpx.ErrNotFound()
	}
	var in HubInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Patch(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) importHubs(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in ImportInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Import(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) listDistances(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "hubId", "direction", "limit", "cursor"); err != nil {
		return err
	}
	limit := 0
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n == 0 {
			return httpx.ErrInvalidArgument(violation("limit", "out_of_range"))
		}
		limit = n
	}
	out, next, err := h.s.ListDistances(c.Context(), p, ListDistancesInput{HubID: c.Query("hubId"),
		Direction: c.Query("direction"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) pair(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	// Fiber leaves path params percent-encoded (UnescapePath is off app-wide): decode them, so a code with a space or
	// Thai text (SOCE (บัวโรย), SPK890103-ลาดกระบัง26) resolves as it does in ?hubId. A malformed escape names nothing.
	origin, err1 := url.PathUnescape(c.Params("originId"))
	destination, err2 := url.PathUnescape(c.Params("destinationId"))
	if err1 != nil || err2 != nil {
		return httpx.ErrNotFound()
	}
	out, err := h.s.GetPair(c.Context(), p, origin, destination)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) meta(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	out, err := h.s.DistancesMeta(c.Context(), p)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// computeDistances is POST /v1/jobs/distances.compute: 202 {"data":{"jobId"}} (Appendix B §B.1.5).
func (h handlers) computeDistances(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	j, err := h.s.SubmitDistances(c.Context(), p)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusAccepted, map[string]any{"jobId": j.ID})
}
