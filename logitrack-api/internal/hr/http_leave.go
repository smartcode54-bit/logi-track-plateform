package hr

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// The handlers of /v1/leave-requests* and /v1/holidays* (T45).

func (h handlers) leaveRequests(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "status", "driverId", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	var statuses []string
	for _, v := range c.Request().URI().QueryArgs().PeekMulti("status") {
		statuses = append(statuses, string(v))
	}
	out, next, counts, err := h.s.LeaveRequests(c.Context(), p, LeaveQuery{Statuses: statuses, DriverID: c.Query("driverId"),
		Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	var meta any
	if counts != nil {
		meta = listMeta{StatusCounts: counts}
	}
	return httpx.Page(c, out, next, meta)
}

// decide is POST /v1/leave-requests/{id}/{action}: 200 with the request, or 202 with the write-back echo.
func (h handlers) decide(action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		id, err := pathID(c)
		if err != nil {
			return err
		}
		if err := onlyQuery(c); err != nil {
			return err
		}
		var in LeaveDecision
		if err := httpx.DecodeJSON(c, &in); err != nil {
			return err
		}
		out, err := h.s.DecideLeave(c.Context(), p, httpx.RequestIDFrom(c), id, action, in)
		if err != nil {
			return err
		}
		return httpx.JSON(c, out.Status, out.Data)
	}
}

// holidays is GET /v1/holidays: {"data": [...]} with a strong ETag of the body; a matching If-None-Match is 304
// (master-data reads, Appendix B §B.1.5 "Caching headers"). The client revalidates every time (no-cache).
func (h handlers) holidays(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "year"); err != nil {
		return err
	}
	out, err := h.s.Holidays(c.Context(), p, c.Query("year"))
	if err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		Data any `json:"data"`
	}{out})
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

// objectBody is the request body as a JSON object (an empty body is an empty object); anything else is 400.
func objectBody(c fiber.Ctx) ([]byte, error) {
	body := bytes.TrimSpace(c.Body())
	if len(body) == 0 {
		return []byte("{}"), nil
	}
	if body[0] != '{' || !json.Valid(body) {
		return nil, httpx.ErrBadRequest("request body must be a JSON object")
	}
	return body, nil
}

func (h handlers) saveHoliday(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	body, err := objectBody(c)
	if err != nil {
		return err
	}
	out, err := h.s.SaveHoliday(c.Context(), p, httpx.RequestIDFrom(c), body)
	if err != nil {
		return err
	}
	status := http.StatusOK
	if out.Created {
		status = http.StatusCreated
	}
	return httpx.JSON(c, status, out.Holiday)
}

func (h handlers) patchHoliday(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	body, err := objectBody(c)
	if err != nil {
		return err
	}
	out, err := h.s.PatchHoliday(c.Context(), p, httpx.RequestIDFrom(c), id, body)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) deleteHoliday(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	if err := h.s.DeleteHoliday(c.Context(), p, httpx.RequestIDFrom(c), id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h handlers) generateHolidays(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	var in GenerateInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.GenerateHolidays(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}
