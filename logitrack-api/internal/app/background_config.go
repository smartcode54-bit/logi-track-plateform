package app

import (
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// AMQPEnv is the RabbitMQ connection (api never has one, main spec §16.1).
type AMQPEnv struct {
	RabbitMQURL string `env:"RABBITMQ_URL,required,notEmpty"`
}

func (a AMQPEnv) validate(errs *[]string) {
	if a.RabbitMQURL != "" && !strings.HasPrefix(a.RabbitMQURL, "amqp://") && !strings.HasPrefix(a.RabbitMQURL, "amqps://") {
		*errs = append(*errs, config.Invalidf("RABBITMQ_URL", "must be an amqp:// or amqps:// URL"))
	}
}

// WorkerGroups are the consumer groups of Appendix B §B.5.3 that WORKER_CONSUMERS selects from.
var WorkerGroups = func() []string {
	var out []string
	for _, q := range mq.Queues {
		if !slices.Contains(out, q.Group) {
			out = append(out, q.Group)
		}
	}
	return out
}()

// WorkerConfig is the worker process configuration.
type WorkerConfig struct {
	Common
	Runtime
	Database
	AMQPEnv
	// Storage: storage.gc deletes expired pending objects on their own backend (T11).
	Storage
	Prefetch  int      `env:"RABBITMQ_PREFETCH"`
	Consumers []string `env:"WORKER_CONSUMERS" envSeparator:"," envDefault:"all"`

	// notify.email (main spec §7.6)
	PublicWebBaseURL string        `env:"PUBLIC_WEB_BASE_URL"`
	PasswordResetTTL time.Duration `env:"PASSWORD_RESET_TTL" envDefault:"30m"`
	EmailEnabled     bool          `env:"EMAIL_ENABLED"`
	SMTPHost         string        `env:"SMTP_HOST"`
	SMTPPort         int           `env:"SMTP_PORT" envDefault:"587"`
	SMTPUser         string        `env:"SMTP_USER"`
	SMTPPassword     string        `env:"SMTP_PASSWORD"`
	SMTPFrom         string        `env:"SMTP_FROM"`
	SMTPFromName     string        `env:"SMTP_FROM_NAME" envDefault:"LogiTrack"`
	SMTPStartTLS     bool          `env:"SMTP_STARTTLS" envDefault:"true"`

	// Groups is WORKER_CONSUMERS resolved by Validate ("all" = every group).
	Groups []string `env:"-"`
}

// Validate implements config.Validator.
func (c *WorkerConfig) Validate() error {
	var errs []string
	c.Common.validate(&errs)
	c.Runtime.validate(&errs)
	c.Database.validate(&errs)
	c.AMQPEnv.validate(&errs)
	c.Storage.validate(&errs)
	if c.Prefetch < 0 || c.Prefetch > 1000 {
		errs = append(errs, config.Invalidf("RABBITMQ_PREFETCH", "must be between 0 and 1000"))
	}
	c.Groups = nil
	for _, g := range c.Consumers {
		g = strings.TrimSpace(g)
		switch {
		case g == "":
		case g == "all" || g == "*":
			c.Groups = slices.Clone(WorkerGroups)
		case slices.Contains(WorkerGroups, g):
			if !slices.Contains(c.Groups, g) {
				c.Groups = append(c.Groups, g)
			}
		default:
			errs = append(errs, config.Invalidf("WORKER_CONSUMERS", "entries must be all or among %s", strings.Join(WorkerGroups, ", ")))
		}
	}
	if len(c.Groups) == 0 && len(errs) == 0 {
		errs = append(errs, config.Invalidf("WORKER_CONSUMERS", "must name at least one group (or all)"))
	}
	if c.EmailEnabled {
		if c.SMTPHost == "" || c.SMTPFrom == "" {
			errs = append(errs, "SMTP_HOST, SMTP_FROM: required when EMAIL_ENABLED is true")
		}
		if c.SMTPPort < 1 || c.SMTPPort > 65535 {
			errs = append(errs, config.Invalidf("SMTP_PORT", "must be between 1 and 65535"))
		}
		if u, err := url.Parse(c.PublicWebBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, config.Invalidf("PUBLIC_WEB_BASE_URL", "must be an absolute http(s) URL when EMAIL_ENABLED is true"))
		}
	}
	if c.PasswordResetTTL < time.Minute || c.PasswordResetTTL > 24*time.Hour {
		errs = append(errs, config.Invalidf("PASSWORD_RESET_TTL", "must be between 1m and 24h"))
	}
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}

// SchedulerConfig is the scheduler process configuration.
type SchedulerConfig struct {
	Common
	Runtime
	Database
	Redis
	AMQPEnv
	RelayInterval time.Duration `env:"OUTBOX_RELAY_INTERVAL" envDefault:"200ms"`
	BatchSize     int           `env:"OUTBOX_BATCH_SIZE" envDefault:"500"`
	RTLogMaxLen   int64         `env:"RTLOG_MAXLEN" envDefault:"1000"`
	RTLogTTL      time.Duration `env:"RTLOG_TTL" envDefault:"24h"`
	// JWTAccessTTL is the life of the revoked-session markers the relay re-applies from
	// user.sessions_revoked (+ 30 s leeway, Appendix C §C.4.7): the same value the api signs with.
	JWTAccessTTL time.Duration `env:"JWT_ACCESS_TTL" envDefault:"15m"`
}

// Validate implements config.Validator.
func (c *SchedulerConfig) Validate() error {
	var errs []string
	c.Common.validate(&errs)
	c.Runtime.validate(&errs)
	c.Database.validate(&errs)
	c.Redis.validate(c.AppEnv, &errs)
	c.AMQPEnv.validate(&errs)
	if c.RelayInterval < 10*time.Millisecond || c.RelayInterval > time.Minute {
		errs = append(errs, config.Invalidf("OUTBOX_RELAY_INTERVAL", "must be between 10ms and 1m"))
	}
	if c.BatchSize < 1 || c.BatchSize > 10000 {
		errs = append(errs, config.Invalidf("OUTBOX_BATCH_SIZE", "must be between 1 and 10000"))
	}
	if c.RTLogMaxLen < 10 {
		errs = append(errs, config.Invalidf("RTLOG_MAXLEN", "must be at least 10"))
	}
	if c.RTLogTTL < time.Minute {
		errs = append(errs, config.Invalidf("RTLOG_TTL", "must be at least 1m"))
	}
	if c.JWTAccessTTL < time.Minute || c.JWTAccessTTL > time.Hour {
		errs = append(errs, config.Invalidf("JWT_ACCESS_TTL", "must be between 1m and 1h"))
	}
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}
