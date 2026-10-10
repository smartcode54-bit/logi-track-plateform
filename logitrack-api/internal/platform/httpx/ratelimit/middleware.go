package ratelimit

import (
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Rule applies one bucket to a request.
type Rule struct {
	Bucket Bucket
	Limit  Limit                    // zero: the bucket's default
	Cost   int                      // zero: 1
	By     func(c fiber.Ctx) string // the subject; "" skips the rule for this request
}

// ByIP is the client address resolved by httpx.ClientIP (TRUSTED_PROXY_CIDRS).
func ByIP(c fiber.Ctx) string { return httpx.ClientIPFrom(c) }

// Middleware checks every rule in order and answers 429 resource_exhausted with Retry-After at the
// first denial (details.bucket names it). Rules before the denial have counted the request. With
// enabled false it only passes through. When Redis cannot answer the request is let through (fail
// open), logged and counted: a Redis outage must not take the API down.
func (l *Limiter) Middleware(enabled bool, rules ...Rule) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !enabled {
			return c.Next()
		}
		for _, r := range rules {
			subject := r.By(c)
			if subject == "" {
				continue
			}
			lim := r.Limit
			if lim == (Limit{}) {
				lim = r.Bucket.Default
			}
			cost := max(r.Cost, 1)
			d, err := l.AllowN(c.Context(), r.Bucket.Name, subject, lim, cost)
			if err != nil {
				log := zerolog.Ctx(c.Context())
				if log.GetLevel() == zerolog.Disabled {
					log = &l.log
				}
				log.Warn().Err(err).Str("component", "ratelimit").Str("bucket", r.Bucket.Name).Msg("rate limit unavailable, request allowed")
				continue
			}
			if !d.Allowed {
				secs := d.RetryAfterSeconds()
				c.Set(fiber.HeaderRetryAfter, strconv.Itoa(secs))
				return httpx.NewError(http.StatusTooManyRequests, httpx.CodeResourceExhaust, "too many requests").
					WithDetails(map[string]any{"bucket": r.Bucket.Name, "retryAfterSeconds": secs})
			}
		}
		return c.Next()
	}
}
