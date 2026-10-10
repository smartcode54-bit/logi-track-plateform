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

// The buckets of Appendix B §B.6.3. webhook has no default: each provider route sets its own limit.
var (
	LoginIP          = Bucket{Name: "login_ip", Default: per(10, time.Minute), Env: "RATE_LIMIT_LOGIN"}
	LoginFail        = Bucket{Name: "login_fail", Default: per(5, 15*time.Minute)} // -> 423 locked; always applied
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

// Config is the rate-limit configuration (main spec §16.1). Enabled switches the middleware; the
// login_fail lockout of Appendix C §C.4.12 applies whatever it says.
type Config struct {
	Enabled     bool   `env:"RATE_LIMIT_ENABLED" envDefault:"true"`
	Login       string `env:"RATE_LIMIT_LOGIN" envDefault:"10/1m"`
	PublicForms string `env:"RATE_LIMIT_PUBLIC_FORMS" envDefault:"5/1h"`
	Evidence    string `env:"RATE_LIMIT_EVIDENCE" envDefault:"60/1m"`

	limits map[string]Limit // parsed by Validate, by bucket name
}

// Validate implements config.Validator: each RATE_LIMIT_* value is count/window.
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
