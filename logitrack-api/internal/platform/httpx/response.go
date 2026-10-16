package httpx

import (
	"encoding/json"
	"strconv"

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
	if projectionRequired(c) {
		return projectResponse(c, status, v, "", nil)
	}
	return c.Status(status).JSON(dataBody{Data: v})
}

// Page writes a keyset page {"data": items, "nextCursor": cursor}.
func Page(c fiber.Ctx, items any, nextCursor string, meta any) error {
	if projectionRequired(c) {
		return projectResponse(c, fiber.StatusOK, items, nextCursor, meta)
	}
	return c.Status(fiber.StatusOK).JSON(dataBody{Data: items, NextCursor: nextCursor, Meta: meta})
}

// WriteError renders e. Details is always an object so the envelope always has
// exactly four fields.
func WriteError(c fiber.Ctx, e *Error) error {
	if projectionRequired(c) {
		retryAfter := c.GetRespHeader(fiber.HeaderRetryAfter)
		cleanProjectionHeaders(c)
		// Error codes require review; messages and free text never cross projection.
		code, status := e.Code, e.Status
		message := "request refused"
		switch code {
		case CodeSupportReadOnly:
			message = "support access is read-only"
		case CodeSupportViewUnavailable:
			message = "not yet available in support view"
		case CodeSupportProjectionUnavailable:
			message = "support projection unavailable"
		case CodeSupportAccessMasked:
			message = "support access is masked"
		case CodeInternal:
			status, message = fiber.StatusInternalServerError, "internal error"
		case CodeNotFound, CodeUnauthenticated, "permission_denied", CodeBadRequest, CodeInvalidArgument, "token_expired", CodeResourceExhaust:
		default:
			if _, reviewed := safeErrorCodes.Load(code); !reviewed {
				code, status, message = CodeSupportProjectionUnavailable, 503, "support projection unavailable"
			}
		}
		if status == 429 {
			if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 && seconds <= 3600 {
				c.Set(fiber.HeaderRetryAfter, strconv.Itoa(seconds))
			}
		}
		details := map[string]any{}
		_, registered := safeErrorCodes.Load(code)
		if code == CodeInvalidArgument || registered {
			if fields := projectedErrorFields(e.Details["fields"]); len(fields) > 0 {
				details["fields"] = fields
			}
		}
		if code == "token_expired" && e.Details["reason"] == "claims_changed" {
			details["reason"] = "claims_changed"
		}
		if err := c.Status(status).JSON(errorBody{Error: errorPayload{Code: code, Message: message, Details: details, RequestID: RequestIDFrom(c)}}); err != nil {
			return err
		}
		sealResponse(c)
		return nil
	}
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
