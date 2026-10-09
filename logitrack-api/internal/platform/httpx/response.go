package httpx

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
	"github.com/valyala/fasthttp"
)

// errorBody is the wire shape of every error response.
type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
	RequestID string         `json:"requestId"`
}

// dataBody is the success envelope {"data": ..., "nextCursor"?, "meta"?}.
type dataBody struct {
	Data       any    `json:"data"`
	NextCursor string `json:"nextCursor,omitempty"`
	Meta       any    `json:"meta,omitempty"`
}

// WriteRawError renders e on a fasthttp request that never reached Fiber
// (unknown HTTP method), with the same envelope and X-Request-Id header.
func WriteRawError(rc *fasthttp.RequestCtx, e *Error, requestID string) {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	body, err := json.Marshal(errorBody{Error: errorPayload{
		Code: e.Code, Message: e.Message, Details: details, RequestID: requestID,
	}})
	if err != nil {
		body = []byte(`{"error":{"code":"internal","message":"internal error","details":{},"requestId":""}}`)
	}
	rc.Response.Header.Set(HeaderRequestID, requestID)
	rc.SetContentType(fiber.MIMEApplicationJSONCharsetUTF8)
	rc.SetStatusCode(e.Status)
	rc.SetBody(body)
}

// JSON writes {"data": v} with the given status.
func JSON(c fiber.Ctx, status int, v any) error {
	return c.Status(status).JSON(dataBody{Data: v})
}

// Page writes a keyset page {"data": items, "nextCursor": cursor}.
func Page(c fiber.Ctx, items any, nextCursor string, meta any) error {
	return c.Status(fiber.StatusOK).JSON(dataBody{Data: items, NextCursor: nextCursor, Meta: meta})
}

// WriteError renders e. Details is always an object so the envelope always has
// exactly four fields.
func WriteError(c fiber.Ctx, e *Error) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	return c.Status(e.Status).JSON(errorBody{Error: errorPayload{
		Code:      e.Code,
		Message:   e.Message,
		Details:   details,
		RequestID: RequestIDFrom(c),
	}})
}

// ErrorHandler is the Fiber error handler for both listeners. Server errors are
// logged with their cause; the response never carries internal detail.
func ErrorHandler(base zerolog.Logger) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		e := AsError(err)
		if e.Code == CodeInternal {
			// Only unexpected failures are logged here; deliberate 503s
			// (readiness while draining) are visible in the access line.
			if l := zerolog.Ctx(c.Context()); l.GetLevel() != zerolog.Disabled {
				l.Error().Err(err).Msg("request failed") // request_id already on the scoped logger
			} else {
				base.Error().Err(err).Str("request_id", RequestIDFrom(c)).Msg("request failed")
			}
		}
		if RequestIDFrom(c) == "" {
			// Errors raised before RequestID ran (e.g. body limit) still get an id.
			id := newID()
			c.Locals(keyRequestID, id)
			c.Set(HeaderRequestID, id)
		}
		return WriteError(c, e)
	}
}
