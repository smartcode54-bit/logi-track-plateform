package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gofiber/fiber/v3"
)

// CodeInvalidArgument is the 422 code for a well-formed request whose values break a rule
// (Appendix B §B.1.5); details.fields lists the offending fields.
const CodeInvalidArgument = "invalid_argument"

// FieldViolation is one entry of details.fields of a 422 invalid_argument: the camelCase field name, a
// stable reason code and optional parameters of the rule (for example {"min": 10}).
type FieldViolation struct {
	Field  string         `json:"field"`
	Reason string         `json:"reason"`
	Params map[string]any `json:"params,omitempty"`
}

// ErrInvalidArgument builds a 422 invalid_argument listing the offending fields.
func ErrInvalidArgument(fields ...FieldViolation) *Error {
	return NewError(http.StatusUnprocessableEntity, CodeInvalidArgument, "invalid argument").
		WithDetails(map[string]any{"fields": fields})
}

// DecodeJSON reads a JSON object body into v. An empty body leaves v unchanged (every field absent);
// malformed JSON, a non-object or trailing data is 400 bad_request. Unknown fields are ignored.
func DecodeJSON(c fiber.Ctx, v any) error {
	body := bytes.TrimSpace(c.Body())
	if len(body) == 0 {
		return nil
	}
	if body[0] != '{' {
		return ErrBadRequest("request body must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(v); err != nil {
		return ErrBadRequest("malformed JSON body")
	}
	if dec.More() {
		return ErrBadRequest("malformed JSON body")
	}
	return nil
}
