// Package httpx holds the HTTP conventions shared by every handler: the success
// and error envelopes, request ids, client IP resolution and access logging
// (main spec §2.5, Appendix B §B.1.5).
package httpx

import (
	"errors"
	"fmt"
	"maps"
	"net/http"

	"github.com/gofiber/fiber/v3"
)

// Error is an API error rendered as
// {"error":{"code","message","details","requestId"}} — exactly those four
// fields, no localised text (R48, R76). Clients branch on Code only.
type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s (%d): %s: %v", e.Code, e.Status, e.Message, e.cause)
	}
	return fmt.Sprintf("%s (%d): %s", e.Code, e.Status, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// WithDetails returns a copy of e with details merged in.
func (e *Error) WithDetails(kv map[string]any) *Error {
	c := *e
	c.Details = make(map[string]any, len(e.Details)+len(kv))
	maps.Copy(c.Details, e.Details)
	maps.Copy(c.Details, kv)
	return &c
}

// Wrap attaches an internal cause that is logged but never rendered.
func (e *Error) Wrap(cause error) *Error {
	c := *e
	c.cause = cause
	return &c
}

// NewError builds an Error. Codes must come from Appendix B §B.1.5.
func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Stable codes used by the platform layer (Appendix B §B.1.5).
const (
	CodeBadRequest       = "bad_request"
	CodeHeaderNotAllowed = "header_not_allowed"
	CodeUnauthenticated  = "unauthenticated"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodePayloadTooLarge  = "payload_too_large"
	CodeResourceExhaust  = "resource_exhausted"
	CodeInternal         = "internal"
	CodeUnavailable      = "unavailable"
)

// Common errors.
func ErrNotFound() *Error {
	return NewError(http.StatusNotFound, CodeNotFound, "resource not found")
}

func ErrBadRequest(msg string) *Error {
	return NewError(http.StatusBadRequest, CodeBadRequest, msg)
}

func ErrUnauthenticated() *Error {
	return NewError(http.StatusUnauthorized, CodeUnauthenticated, "authentication required")
}

func ErrUnavailable(msg string) *Error {
	return NewError(http.StatusServiceUnavailable, CodeUnavailable, msg)
}

func ErrInternal() *Error {
	return NewError(http.StatusInternalServerError, CodeInternal, "internal error")
}

// fromFiber maps framework errors (routing, body limit, timeouts) to stable codes.
func fromFiber(fe *fiber.Error) *Error {
	switch fe.Code {
	case http.StatusNotFound:
		return ErrNotFound()
	case http.StatusMethodNotAllowed:
		return NewError(fe.Code, CodeMethodNotAllowed, "method not allowed")
	case http.StatusRequestEntityTooLarge:
		return NewError(fe.Code, CodePayloadTooLarge, "request body too large")
	case http.StatusTooManyRequests:
		return NewError(fe.Code, CodeResourceExhaust, "too many requests")
	case http.StatusServiceUnavailable:
		return ErrUnavailable("service unavailable")
	}
	if fe.Code >= 400 && fe.Code < 500 {
		return NewError(fe.Code, CodeBadRequest, "bad request")
	}
	return ErrInternal().Wrap(fe)
}

// AsError converts any error returned by a handler into an *Error. Unknown
// errors become 500 internal and keep the original as the logged cause.
func AsError(err error) *Error {
	if ae, ok := errors.AsType[*Error](err); ok {
		return ae
	}
	if fe, ok := errors.AsType[*fiber.Error](err); ok {
		return fromFiber(fe)
	}
	return ErrInternal().Wrap(err)
}
