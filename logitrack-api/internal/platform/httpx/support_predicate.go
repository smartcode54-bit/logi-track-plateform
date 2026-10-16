package httpx

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"reflect"
	"slices"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// StrictQuery rejects unknown and repeated keys, including percent-encoded aliases.
func StrictQuery(c fiber.Ctx, allowed ...string) (url.Values, error) {
	q, err := url.ParseQuery(string(c.Request().URI().QueryString()))
	if err != nil {
		return nil, ErrBadRequest("invalid query")
	}
	for k, v := range q {
		if !slices.Contains(allowed, k) || len(v) != 1 {
			return nil, ErrBadRequest("ambiguous query")
		}
	}
	return q, nil
}

// StrictBody rejects duplicate keys at every depth, extra fields and trailing JSON.
// DecodeJSON subsequently copies this same typed value instead of parsing again.
func StrictBody[T any](c fiber.Ctx) (T, error) {
	var v T
	body := bytes.TrimSpace(c.Body())
	if len(body) == 0 || body[0] != '{' {
		return v, ErrBadRequest("request body must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := uniqueJSON(dec); err != nil {
		return v, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return v, ErrBadRequest("malformed JSON body")
	}
	if err := exactBodyFields(body, reflect.TypeFor[T]()); err != nil {
		return v, err
	}
	dec = json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, ErrBadRequest("invalid request body")
	}
	c.Locals(inputKey, v)
	return v, nil
}

func uniqueJSON(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return ErrBadRequest("malformed JSON body")
	}
	d, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		if d == '{' {
			key, err := dec.Token()
			k, ok := key.(string)
			if err != nil || !ok || seen[k] {
				return ErrBadRequest("ambiguous request body")
			}
			seen[k] = true
		}
		if err := uniqueJSON(dec); err != nil {
			return err
		}
	}
	end, err := dec.Token()
	if err != nil || (d == '{' && end != json.Delim('}')) || (d == '[' && end != json.Delim(']')) {
		return ErrBadRequest("malformed JSON body")
	}
	return nil
}

func copyPolicyInput(c fiber.Ctx, dst any) (bool, error) {
	v := c.Locals(inputKey)
	if v == nil {
		return false, nil
	}
	d := reflect.ValueOf(dst)
	if d.Kind() != reflect.Pointer || d.IsNil() || d.Elem().Type() != reflect.TypeOf(v) {
		return true, ErrBadRequest("policy input type mismatch")
	}
	d.Elem().Set(reflect.ValueOf(v))
	return true, nil
}

type MatrixRequest struct{ Layer, TenantID string }

func MatrixPredicate(c fiber.Ctx) (PolicyClass, error) {
	q, err := StrictQuery(c, "layer", "tenantId")
	if err != nil {
		return PolicyDeny, err
	}
	in := MatrixRequest{Layer: q.Get("layer"), TenantID: q.Get("tenantId")}
	switch in.Layer {
	case "platform":
		if q.Has("tenantId") {
			return PolicyDeny, ErrBadRequest("platform matrix has no tenant target")
		}
		if c.Method() == "PUT" {
			body, err := StrictBody[MatrixWriteRequest](c)
			if err != nil || body.Overrides == nil {
				return PolicyDeny, ErrBadRequest("invalid platform matrix body")
			}
			c.Locals(inputKey, MatrixInput{Query: in, Body: &body})
		} else {
			c.Locals(inputKey, MatrixInput{Query: in})
		}
		return PolicyPlatform, nil
	case "tenant":
		id, err := uuid.Parse(in.TenantID)
		if err != nil || id == uuid.Nil {
			return PolicyDeny, ErrBadRequest("exact tenant target required")
		}
		c.Locals(inputKey, in)
		if c.Method() == "GET" || c.Method() == "HEAD" {
			return PolicyDeny, nil
		}
		return PolicyTenantWrite, nil
	}
	return PolicyDeny, ErrBadRequest("explicit matrix layer required")
}

// LifecycleRequest is intentionally distinct from profile PATCH input.
type LifecycleRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func LifecyclePredicate(c fiber.Ctx) (PolicyClass, error) {
	if _, err := StrictQuery(c); err != nil {
		return PolicyDeny, err
	}
	in, err := StrictBody[LifecycleRequest](c)
	if err != nil {
		return PolicyTenantWrite, err
	}
	if !slices.Contains([]string{"active", "pending", "suspended"}, in.Status) || in.Reason == "" {
		return PolicyTenantWrite, ErrBadRequest("invalid lifecycle request")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil || id == uuid.Nil {
		return PolicyTenantWrite, ErrBadRequest("exact tenant target required")
	}
	return PolicyPlatform, nil
}

// APIKeyRequest is the shared validated input for the mixed creation endpoint.
type APIKeyRequest struct {
	Name         string   `json:"name"`
	Scope        string   `json:"scope"`
	Capabilities []string `json:"capabilities"`
	TenantID     *string  `json:"tenantId"`
	ExpiresAt    *string  `json:"expiresAt"`
}

// APIKeyPredicate's class lookup comes from authz; httpx has no authz dependency.
func APIKeyPredicate(classOf func(string) string) func(fiber.Ctx) (PolicyClass, error) {
	return func(c fiber.Ctx) (PolicyClass, error) {
		if _, err := StrictQuery(c); err != nil {
			return PolicyDeny, err
		}
		in, err := StrictBody[APIKeyRequest](c)
		if err != nil {
			return PolicyTenantWrite, err
		}
		if in.TenantID != nil {
			return PolicyTenantWrite, nil
		}
		if in.Name == "" || len(in.Capabilities) == 0 || !slices.Contains([]string{"integration", "script", "cf_shim", "release_publisher"}, in.Scope) {
			return PolicyDeny, ErrBadRequest("invalid platform key")
		}
		seen := map[string]bool{}
		for _, key := range in.Capabilities {
			class := classOf(key)
			if seen[key] || (class != "global" && class != "self") ||
				(in.Scope == "cf_shim" && class != "self") ||
				(in.Scope == "release_publisher" && key != "security:manage_mobile_release") {
				return PolicyDeny, ErrBadRequest("invalid platform key capabilities")
			}
			seen[key] = true
		}
		return PolicyPlatform, nil
	}
}

func PolicyInput[T any](c fiber.Ctx) (T, bool) {
	v, ok := c.Locals(inputKey).(T)
	return v, ok
}

// SetPolicyInput shares a validated dependency input with its business handler.
func SetPolicyInput(c fiber.Ctx, input any) { c.Locals(inputKey, input) }

type MatrixOverrideInput struct {
	Role       string `json:"role"`
	Capability string `json:"capability"`
	Allowed    *bool  `json:"allowed"`
}
type MatrixWriteRequest struct {
	Overrides *[]MatrixOverrideInput `json:"overrides"`
}
type MatrixInput struct {
	Query MatrixRequest
	Body  *MatrixWriteRequest
}

// encoding/json matches struct fields case-insensitively. Mixed policy bodies
// require exact wire names so differently cased aliases cannot override a target.
func exactBodyFields(body []byte, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
			return nil
		}
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(body, &object) != nil {
			return ErrBadRequest("invalid request body")
		}
		fields, err := visibleFields(t)
		if err != nil {
			return ErrBadRequest("invalid request schema")
		}
		known := map[string]reflect.Type{}
		for _, field := range fields {
			known[field.name] = field.typ
		}
		for key, value := range object {
			typ, ok := known[key]
			if !ok {
				return ErrBadRequest("unknown request field")
			}
			if err := exactBodyFields(value, typ); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var array []json.RawMessage
		if json.Unmarshal(body, &array) != nil {
			return ErrBadRequest("invalid request body")
		}
		for _, value := range array {
			if err := exactBodyFields(value, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Interface, reflect.Map:
		return ErrBadRequest("unclassified dynamic request body")
	}
	return nil
}
