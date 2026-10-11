package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
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

// ByIP is the client address resolved by httpx.ClientIP (TRUSTED_PROXY_CIDRS) as a subject
// (IPSubject): an IPv4 address as it is, an IPv6 address reduced to its /64.
func ByIP(c fiber.Ctx) string { return IPSubject(httpx.ClientIPFrom(c)) }

// ByProviderIP is the webhook subject {provider}:{ip} of Appendix B §B.6.3, the address reduced as in
// ByIP.
func ByProviderIP(provider string) func(fiber.Ctx) string {
	return func(c fiber.Ctx) string {
		ip := ByIP(c)
		if ip == "" {
			return ""
		}
		return provider + ":" + ip
	}
}

// IPSubject is the rate-limit subject of a client address: an IPv4 (or IPv4-mapped) address as it
// is, an IPv6 address as its /64 prefix, the unit a subscriber line or a VPS is routed (one client
// can rotate through 2^64 addresses of its /64). Anything that is not an address is returned
// unchanged ("" still skips a rule).
func IPSubject(s string) string {
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is6() || a.Is4In6() {
		return s
	}
	return netip.PrefixFrom(a.WithZone(""), 64).Masked().String()
}

// Middleware checks every rule in order and answers 429 resource_exhausted with Retry-After at the
// first denial (details.bucket names it). Rules before the denial have counted the request. With
// enabled false it only passes through. When Redis cannot answer the request is let through (fail
// open), logged and counted: a Redis outage must not take the API down. The rules are checked here,
// when the route is built: a rule without By, without a usable limit (webhook and login_fail have no
// default) or with a cost above the burst panics, so it can never fail open at request time.
func (l *Limiter) Middleware(enabled bool, rules ...Rule) fiber.Handler {
	rules = slices.Clone(rules)
	for i := range rules {
		r := &rules[i]
		if r.Limit == (Limit{}) {
			r.Limit = r.Bucket.Default
		}
		r.Cost = max(r.Cost, 1)
		if r.By == nil || !r.Limit.Valid() || r.Cost > r.Limit.Count {
			panic(fmt.Sprintf("ratelimit: rule for bucket %q needs By and a valid limit (limit %s, cost %d)", r.Bucket.Name, r.Limit, r.Cost))
		}
	}
	return func(c fiber.Ctx) error {
		if !enabled {
			return c.Next()
		}
		for _, r := range rules {
			subject := r.By(c)
			if subject == "" {
				continue
			}
			d, err := l.AllowN(c.Context(), r.Bucket.Name, subject, r.Limit, r.Cost)
			if err != nil {
				log := zerolog.Ctx(c.Context())
				if log.GetLevel() == zerolog.Disabled {
					log = &l.log
				}
				log.Warn().Err(err).Str("component", "ratelimit").Str("bucket", r.Bucket.Name).Msg("rate limit unavailable, request allowed")
				continue
			}
			if !d.Allowed {
				return RetryAfter(c, Denied(r.Bucket.Name, d))
			}
		}
		return c.Next()
	}
}

// Denied is the answer of a denied bucket (Appendix B §B.6.3): 429 resource_exhausted with details.bucket and
// details.retryAfterSeconds. Middleware answers it with Retry-After; a handler that spends a bucket itself (Check) sets
// the header through RetryAfter.
func Denied(bucket string, d Decision) *httpx.Error {
	return httpx.NewError(http.StatusTooManyRequests, httpx.CodeResourceExhaust, "too many requests").
		WithDetails(map[string]any{"bucket": bucket, "retryAfterSeconds": d.RetryAfterSeconds()})
}

// RetryAfter sets Retry-After on c when err is a Denied answer (Appendix B §B.1.5) and returns err unchanged.
func RetryAfter(c fiber.Ctx, err error) error {
	var he *httpx.Error
	if errors.As(err, &he) && he.Status == http.StatusTooManyRequests {
		if secs, ok := he.Details["retryAfterSeconds"].(int); ok {
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(secs))
		}
	}
	return err
}

// Check spends one request of bucket b for subject where Middleware cannot: the subject or the moment to count is known
// only inside the handler (public_form_email: the email of a body that passed validation, spent in the transaction
// that stores it). lim zero is the bucket's default. It answers nil when the request is allowed, when enabled is false,
// when subject is "" and when Redis cannot answer (fail open, logged, as Middleware); a denial is Denied (the caller
// sets Retry-After through RetryAfter); an unusable limit is ErrInvalidLimit, a bug never treated as an outage.
func (l *Limiter) Check(ctx context.Context, enabled bool, b Bucket, lim Limit, subject string) error {
	if !enabled || subject == "" {
		return nil
	}
	if lim == (Limit{}) {
		lim = b.Default
	}
	d, err := l.AllowN(ctx, b.Name, subject, lim, 1)
	if errors.Is(err, ErrInvalidLimit) {
		return err
	}
	if err != nil {
		log := zerolog.Ctx(ctx)
		if log.GetLevel() == zerolog.Disabled {
			log = &l.log
		}
		log.Warn().Err(err).Str("component", "ratelimit").Str("bucket", b.Name).Msg("rate limit unavailable, request allowed")
		return nil
	}
	if !d.Allowed {
		return Denied(b.Name, d)
	}
	return nil
}
