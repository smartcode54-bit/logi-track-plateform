package ratelimit

import (
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

// Bucket is one row of Appendix B §B.6.3: its name (rl:{name}:{subject}) and design limit. Env names
// the RATE_LIMIT_* variable that overrides the limit; the other buckets are code constants (changing
// one is a code change reviewed with the security tests, Appendix C §C.4.12).
type Bucket struct {
	Name    string
	Default Limit
	Env     string
}

func per(n int, d time.Duration) Limit { return Limit{Count: n, Window: d} }

// The buckets of Appendix B §B.6.3. Two have no Default, so Allow refuses them and Middleware panics
// on a rule that does not set Limit: webhook (each provider route sets its own limit) and login_fail.
// The auth routes (login_ip, refresh_session, sse_ticket, forgot_email, forgot_ip, reset_ip) are
// checked by internal/auth through Limiter.Allow, because their subject (a session id, an email from
// the body) is known only inside the handler; every other bucket goes through Middleware.
//
// login_fail is listed for its key name only (rl:login_fail:{sha256(email)}): it is not a GCRA bucket.
// The lockout of Appendix C §C.4.12 (5 failed sign-ins per email within 15 min -> 423 locked) counts
// each attempt atomically before the password check and clears the count on success; internal/auth
// (T05) owns that counter. GCRA would let one more guess through every window/count (3 min) instead
// of holding the lock, and a check made before counting lets concurrent guesses through.
var (
	LoginIP          = Bucket{Name: "login_ip", Default: per(10, time.Minute), Env: "RATE_LIMIT_LOGIN"}
	LoginFail        = Bucket{Name: "login_fail"}
	GoogleIP         = Bucket{Name: "google_ip", Default: per(30, time.Minute)}
	RefreshSession   = Bucket{Name: "refresh_session", Default: per(60, time.Minute)}
	ExchangeIP       = Bucket{Name: "exchange_ip", Default: per(30, time.Minute)}
	SSETicket        = Bucket{Name: "sse_ticket", Default: per(30, time.Minute)}
	ForgotEmail      = Bucket{Name: "forgot_email", Default: per(3, time.Hour)}
	ForgotIP         = Bucket{Name: "forgot_ip", Default: per(20, time.Hour)}
	ResetIP          = Bucket{Name: "reset_ip", Default: per(10, time.Hour)}
	PublicFormIP     = Bucket{Name: "public_form_ip", Default: per(5, time.Hour), Env: "RATE_LIMIT_PUBLIC_FORMS"}
	PublicFormEmail  = Bucket{Name: "public_form_email", Default: per(1, 24*time.Hour)}
	PublicIP         = Bucket{Name: "public_ip", Default: per(60, time.Minute)}
	EvidenceIP       = Bucket{Name: "evidence_ip", Default: per(60, time.Minute), Env: "RATE_LIMIT_EVIDENCE"}
	HeartbeatInstall = Bucket{Name: "heartbeat_install", Default: per(1, 30*time.Second)}
	PresignUser      = Bucket{Name: "presign_user", Default: per(120, time.Minute)}
	OCRDriver        = Bucket{Name: "ocr_driver", Default: per(60, time.Minute)}
	Webhook          = Bucket{Name: "webhook"}
	APIKey           = Bucket{Name: "apikey", Default: per(600, time.Minute)}
	User             = Bucket{Name: "user", Default: per(600, time.Minute)}
)

// Buckets lists every bucket of Appendix B §B.6.3.
var Buckets = []Bucket{
	LoginIP, LoginFail, GoogleIP, RefreshSession, ExchangeIP, SSETicket, ForgotEmail, ForgotIP, ResetIP,
	PublicFormIP, PublicFormEmail, PublicIP, EvidenceIP, HeartbeatInstall, PresignUser, OCRDriver, Webhook,
	APIKey, User,
}

// Config is the rate-limit configuration (main spec §16.1). Enabled switches the request buckets; the
// login_fail lockout of Appendix C §C.4.12 applies whatever it says. RATE_LIMIT_LOGIN sets login_ip,
// RATE_LIMIT_PUBLIC_FORMS public_form_ip and RATE_LIMIT_EVIDENCE evidence_ip; every other limit is a
// code constant.
type Config struct {
	Enabled     bool   `env:"RATE_LIMIT_ENABLED" envDefault:"true"`
	Login       string `env:"RATE_LIMIT_LOGIN" envDefault:"10/1m"`
	PublicForms string `env:"RATE_LIMIT_PUBLIC_FORMS" envDefault:"5/1h"`
	Evidence    string `env:"RATE_LIMIT_EVIDENCE" envDefault:"60/1m"`

	limits map[string]Limit // parsed by Validate, by bucket name
}

// Validate implements config.Validator: each RATE_LIMIT_* value is count/window with window/count of
// at least 1µs, so an unusable limit stops the process at start instead of failing open per request.
func (c *Config) Validate() error {
	var errs []string
	c.limits = map[string]Limit{}
	for _, v := range []struct {
		env, value string
		bucket     Bucket
	}{{"RATE_LIMIT_LOGIN", c.Login, LoginIP}, {"RATE_LIMIT_PUBLIC_FORMS", c.PublicForms, PublicFormIP}, {"RATE_LIMIT_EVIDENCE", c.Evidence, EvidenceIP}} {
		l, err := ParseLimit(v.value)
		if err != nil {
			errs = append(errs, config.Invalidf(v.env, "must be count/window, e.g. 10/1m"))
			continue
		}
		c.limits[v.bucket.Name] = l
	}
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}

// Limit is the effective limit of a bucket: the RATE_LIMIT_* value when the bucket has one, else its
// design default.
func (c *Config) Limit(b Bucket) Limit {
	if l, ok := c.limits[b.Name]; ok {
		return l
	}
	return b.Default
}
