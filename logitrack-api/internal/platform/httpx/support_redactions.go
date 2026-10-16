package httpx

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// JSONWithRedactions retains field classifications from a repository projection. Only existing
// masked placeholders can receive metadata; the ordinary typed schema and final consent checks
// still apply. Serialization and receipt sealing remain inside the finalizer transaction.
func JSONWithRedactions(c fiber.Ctx, status int, value any, fields []RedactedField) error {
	if !projectionRequired(c) || len(fields) == 0 {
		return JSON(c, status, value)
	}
	audience := ProjectionAudienceFrom(c)
	finalize := audience.Finalize
	if finalize == nil {
		return ErrSupportProjectionUnavailable()
	}
	original := audience
	audience.Finalize = func(ctx context.Context, owners []string, write func(map[string]Consent) error) error {
		return finalize(ctx, owners, func(consents map[string]Consent) error {
			if err := write(consents); err != nil {
				return err
			}
			var body struct {
				Data json.RawMessage            `json:"data"`
				Meta map[string]json.RawMessage `json:"meta"`
			}
			if err := json.Unmarshal(c.Response().Body(), &body); err != nil || body.Meta == nil {
				return ErrSupportProjectionUnavailable()
			}
			var data any
			if err := json.Unmarshal(body.Data, &data); err != nil {
				return ErrSupportProjectionUnavailable()
			}
			var redacted []RedactedField
			if err := json.Unmarshal(body.Meta["redacted"], &redacted); err != nil {
				return ErrSupportProjectionUnavailable()
			}
			for _, field := range fields {
				if field.Kind != Person && field.Kind != Address && field.Kind != FreeText && field.Kind != Money && field.Kind != File && field.Kind != Secret {
					return ErrSupportProjectionUnavailable()
				}
				if !maskedPointer(data, field.Path) {
					return ErrSupportProjectionUnavailable()
				}
				redacted = append(redacted, field)
			}
			raw, err := json.Marshal(redacted)
			if err != nil {
				return err
			}
			body.Meta["redacted"] = raw
			if err := c.Status(status).JSON(body); err != nil {
				return err
			}
			sealResponse(c)
			return nil
		})
	}
	SetProjectionAudience(c, audience)
	defer SetProjectionAudience(c, original)
	return JSON(c, status, value)
}

func maskedPointer(data any, path string) bool {
	if !strings.HasPrefix(path, "/data/") {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/data/"), "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch node := data.(type) {
		case map[string]any:
			var exists bool
			data, exists = node[key]
			if !exists {
				return false
			}
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(node) {
				return false
			}
			data = node[index]
		default:
			return false
		}
	}
	return data == nil || data == "****"
}
