package httpx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strconv"
	"sync"

	"github.com/gofiber/fiber/v3"
)

// TenantReadPolicy keeps the reviewed seed identity and adds strict read predicates.
func TenantReadPolicy(p EndpointPolicy, schema, meta string, safeSearch bool, query ...string) EndpointPolicy {
	p.SchemaID, p.MetaSchemaID = schema, meta
	p.CursorID = "support-page-v1"
	p.ValidateCursor = func(s string, a ProjectionAudience) bool {
		v, err := openSupportCursor(s)
		return err == nil && v.Identity == cursorIdentity(a, p.Route)
	}
	p.Predicate = func(c fiber.Ctx) (PolicyClass, error) {
		if _, err := StrictQuery(c, query...); err != nil {
			return PolicyDeny, err
		}
		a := ProjectionAudienceFrom(c)
		if t := c.Query("tenantId"); t != "" && t != a.Tenant {
			return PolicyDeny, ErrSupportViewUnavailable()
		}
		if c.Query("q") != "" && !safeSearch && !a.Consents[a.Tenant].Enabled {
			return PolicyDeny, ErrSupportViewUnavailable()
		}
		if raw := c.Query("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 100 {
				return PolicyDeny, ErrInvalidArgument(FieldViolation{Field: "limit", Reason: "out_of_range"})
			}
		}
		if _, err := SupportCursor(c); err != nil {
			return PolicyDeny, err
		}
		return PolicyTenantRead, nil
	}
	return p
}

type supportPageCursor struct{ Identity, Query, Position string }

func cursorIdentity(a ProjectionAudience, route string) string {
	b, _ := json.Marshal([]string{a.Actor, a.Session, a.Tenant, a.ContextVersion, a.ConsentVersion, route, strconv.FormatBool(a.Support)})
	return string(b)
}
func cursorQuery(c fiber.Ctx) string {
	q, _ := url.ParseQuery(string(c.Request().URI().QueryString()))
	q.Del("cursor")
	sum := sha256.Sum256([]byte(q.Encode()))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

var pageCipher = sync.OnceValues(func() (cipher.AEAD, error) {
	var key []byte
	if raw := os.Getenv("SUPPORT_CURSOR_KEY"); raw != "" {
		var err error
		key, err = base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) < 32 {
			return nil, errors.New("invalid support cursor key")
		}
	} else if path := os.Getenv("JWT_SIGNING_KEY_FILE"); path != "" {
		var err error
		key, err = os.ReadFile(path)
		if err != nil || len(key) == 0 {
			return nil, errors.New("missing support cursor key source")
		}
	} else {
		if env := os.Getenv("APP_ENV"); env != "" && env != "local" {
			return nil, errors.New("shared support cursor key required")
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
	}
	sum := sha256.Sum256(append([]byte("logitrack:support-page:v1\x00"), key...))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
})

func openSupportCursor(s string) (supportPageCursor, error) {
	var out supportPageCursor
	if len(s) > 11000 {
		return out, errors.New("invalid cursor")
	}
	a, err := pageCipher()
	if err != nil {
		return out, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < a.NonceSize()+a.Overhead() || len(raw) > 8192 {
		return out, errors.New("invalid cursor")
	}
	b, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], nil)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

// SupportCursor unwraps only a cursor bound to this visit, consent and exact query. Ordinary cursors never cross modes.
func SupportCursor(c fiber.Ctx) (string, error) {
	raw := c.Query("cursor")
	a := ProjectionAudienceFrom(c)
	if !a.Support || raw == "" {
		return raw, nil
	}
	v, err := openSupportCursor(raw)
	if err != nil || v.Identity != cursorIdentity(a, EndpointPolicyFrom(c).Route) || v.Query != cursorQuery(c) || v.Position == "" {
		return "", ErrInvalidArgument(FieldViolation{Field: "cursor", Reason: "invalid"})
	}
	return v.Position, nil
}

// SupportPage seals a safe keyset position without exposing the raw service cursor on the wire.
func SupportPage(c fiber.Ctx, schema string, items any, next string, meta any) error {
	a := ProjectionAudienceFrom(c)
	if a.Support && next != "" {
		block, err := pageCipher()
		if err != nil {
			return ErrSupportProjectionUnavailable()
		}
		raw, _ := json.Marshal(supportPageCursor{cursorIdentity(a, EndpointPolicyFrom(c).Route), cursorQuery(c), next})
		nonce := make([]byte, block.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return ErrSupportProjectionUnavailable()
		}
		next = base64.RawURLEncoding.EncodeToString(block.Seal(nonce, nonce, raw, nil))
	}
	return PageAs(c, schema, items, next, meta)
}
