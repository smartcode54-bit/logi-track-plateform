package chats

import (
	"net/http"
	"slices"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Prefix is the route prefix of the staff chat console (Appendix B §B.2.16, internal listener).
const Prefix = "/v1/chats"

// MobilePrefix is the path of the driver's own chat below the /v1/mobile group (Appendix B §B.2.21).
const MobilePrefix = "/chat"

// Groups are the staff routes of /v1/chats on the internal listener behind requireAuth (auth.Service.RequireAuth, people
// only). s may be nil while the route table is only listed.
//
//	GET   /v1/chats                   chat:view   ?filter=queue|mine|all&status&driverId&limit&cursor
//	POST  /v1/chats                   chat:send   {driverId} -> the driver's open chat (200) or a new one (201)
//	GET   /v1/chats/{id}              chat:view   the chat and its read state
//	PATCH /v1/chats/{id}              chat:send   {priority}
//	GET   /v1/chats/{id}/messages     chat:view   ?before&limit (keyset, newest first)
//	POST  /v1/chats/{id}/messages     chat:send   {text?, imageKey?, clientMessageId}
//	POST  /v1/chats/{id}/read         chat:view   {lastReadAt?} -> 204
//	POST  /v1/chats/{id}/assign       chat:send   {adminUserId?} (absent = self, null = back to the queue)
//	POST  /v1/chats/{id}/close        chat:send
//	POST  /v1/chats/{id}/reopen       chat:send
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	view, send := authz.RequireCap(authz.ChatView), authz.RequireCap(authz.ChatSend)
	return []ingress.Group{{Prefix: Prefix, Mount: func(r fiber.Router) {
		r = ingress.DenyRoutes(r, "P1.0")
		r.Get("", requireAuth, view, h.list)
		r.Post("", requireAuth, send, h.open)
		r.Get("/:id", requireAuth, view, h.get)
		r.Patch("/:id", requireAuth, send, h.patch)
		r.Get("/:id/messages", requireAuth, view, h.messages)
		r.Post("/:id/messages", requireAuth, send, h.send)
		r.Post("/:id/read", requireAuth, view, h.read)
		r.Post("/:id/assign", requireAuth, send, h.assign)
		r.Post("/:id/close", requireAuth, send, h.close)
		r.Post("/:id/reopen", requireAuth, send, h.reopen)
	}}}
}

// MountMobile registers the driver's own chat below the /v1/mobile group (public and internal listeners, a driver
// token; app.MobileGroups). s may be nil while the route table is only listed.
//
//	GET  /v1/mobile/chat                 mobile:chat  the driver's open chat and its read state (404 when none)
//	POST /v1/mobile/chat                 mobile:chat  get-or-create the driver's chat
//	GET  /v1/mobile/chat/messages        mobile:chat  ?before&limit (keyset, newest first)
//	POST /v1/mobile/chat/messages        mobile:chat  {text?, imageKey?, clientMessageId}
//	POST /v1/mobile/chat/read            mobile:chat  {lastReadAt?} -> 204
//	GET  /v1/mobile/chat/unread-count    mobile:chat  {count, messages}
func (s *Service) MountMobile(r fiber.Router, requireAuth fiber.Handler) {
	h := handlers{s}
	own := authz.RequireCap(authz.MobileChat)
	r.Get(MobilePrefix, requireAuth, own, h.myChat)
	r.Post(MobilePrefix, requireAuth, own, h.myOpen)
	r.Get(MobilePrefix+"/messages", requireAuth, own, h.myMessages)
	r.Post(MobilePrefix+"/messages", requireAuth, own, h.mySend)
	r.Post(MobilePrefix+"/read", requireAuth, own, h.myRead)
	r.Get(MobilePrefix+"/unread-count", requireAuth, own, h.myUnread)
}

type handlers struct{ s *Service }

func principal(c fiber.Ctx) (*authz.Principal, error) {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return nil, httpx.ErrUnauthenticated()
	}
	return p, nil
}

// pathID parses the chat id; anything else is 404.
func pathID(c fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, httpx.ErrNotFound()
	}
	return id, nil
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

func result(c fiber.Ctx, r WriteResult) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, r.Status, r.Data)
}

func (h handlers) list(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "filter", "status", "driverId", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := parseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	out, next, err := h.s.List(c.Context(), p, ListInput{Filter: c.Query("filter"), Status: c.Query("status"),
		DriverID: c.Query("driverId"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.Page(c, out, next, nil)
}

func (h handlers) open(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in OpenInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Open(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return result(c, out)
}

func (h handlers) get(c fiber.Ctx) error {
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
	out, err := h.s.Get(c.Context(), p, id)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) patch(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in PatchInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Patch(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return result(c, out)
}

func (h handlers) messages(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "before", "limit"); err != nil {
		return err
	}
	limit, err := parseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	out, next, err := h.s.Messages(c.Context(), p, id, c.Query("before"), limit)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.Page(c, out, next, nil)
}

func (h handlers) send(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in SendInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Send(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return result(c, out)
}

func (h handlers) read(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in ReadInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	if err := h.s.MarkRead(c.Context(), p, httpx.RequestIDFrom(c), id, in); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h handlers) assign(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in AssignInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Assign(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return result(c, out)
}

func (h handlers) close(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	out, err := h.s.Close(c.Context(), p, httpx.RequestIDFrom(c), id)
	if err != nil {
		return err
	}
	return result(c, out)
}

func (h handlers) reopen(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	out, err := h.s.Reopen(c.Context(), p, httpx.RequestIDFrom(c), id)
	if err != nil {
		return err
	}
	return result(c, out)
}

// --- the driver's own chat -------------------------------------------------------------------------------------------

func (h handlers) myChat(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	out, err := h.s.MyChat(c.Context(), p)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) myOpen(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	out, err := h.s.MyOpen(c.Context(), p, httpx.RequestIDFrom(c))
	if err != nil {
		return err
	}
	return result(c, out)
}

func (h handlers) myMessages(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "before", "limit"); err != nil {
		return err
	}
	limit, err := parseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	out, next, err := h.s.MyMessages(c.Context(), p, c.Query("before"), limit)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.Page(c, out, next, nil)
}

func (h handlers) mySend(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in SendInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.MySend(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return result(c, out)
}

func (h handlers) myRead(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in ReadInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	if err := h.s.MyRead(c.Context(), p, httpx.RequestIDFrom(c), in); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h handlers) myUnread(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	out, err := h.s.MyUnread(c.Context(), p)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, out)
}
