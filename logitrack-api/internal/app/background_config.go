package app

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/coexist"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/bangchak"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push"
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

// Bangchak is the Bangchak retail price feed (P3, R70; main spec §16.1): GET /v1/fuel/retail in the api, the
// bangchak.snapshot consumer in the worker. Unset is off: the route answers 503 and a snapshot run ends failed naming
// the variable, so no request leaves the process.
type Bangchak struct {
	BangchakBaseURL string `env:"BANGCHAK_API_BASE_URL"`
}

func (b Bangchak) validate(errs *[]string) {
	if strings.TrimSpace(b.BangchakBaseURL) == "" {
		return
	}
	if err := bangchak.ValidateBaseURL(b.BangchakBaseURL); err != nil {
		*errs = append(*errs, config.Invalidf("BANGCHAK_API_BASE_URL", "%v", err))
	}
}

// BangchakClient is the price client, nil when the feed is off. It does no I/O.
func (b Bangchak) BangchakClient() (bangchak.Client, error) {
	if strings.TrimSpace(b.BangchakBaseURL) == "" {
		return nil, nil
	}
	c, err := bangchak.New(bangchak.Config{BaseURL: b.BangchakBaseURL})
	if err != nil {
		return nil, &config.Error{Invalid: []string{"BANGCHAK_API_BASE_URL: " + err.Error()}}
	}
	return c, nil
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
	Redis // idem:fcm and idem:push keys of notify.fcm (Appendix B §B.6.2)
	AMQPEnv
	// Storage: storage.gc deletes expired pending objects on their own backend (T11).
	Storage
	Prefetch  int      `env:"RABBITMQ_PREFETCH"`
	Consumers []string `env:"WORKER_CONSUMERS" envSeparator:"," envDefault:"all"`

	// notify.fcm (main spec §7.6): FCM HTTP v1 with the messaging-only service account. Off locally.
	FCMEnabled            bool   `env:"FCM_ENABLED"`
	FCMProjectID          string `env:"FCM_PROJECT_ID"`
	FCMServiceAccountJSON string `env:"FCM_SERVICE_ACCOUNT_JSON"`

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

	// distances.compute (T21): the Google Distance Matrix API with the server key. Off locally: the job then ends
	// failed naming the switch, and no request leaves the worker.
	DistanceMatrixEnabled  bool   `env:"DISTANCE_MATRIX_ENABLED"`
	GoogleMapsServerAPIKey string `env:"GOOGLE_MAPS_SERVER_API_KEY"`

	// bangchak.snapshot (T37): the daily and monthly fuel price snapshots (off without BANGCHAK_API_BASE_URL).
	Bangchak

	// The coexistence consumers of group sync (T24, developer-spec.md §13.7-§13.8): which collections are mirrored or
	// projected follows from PG_OWNED_DOMAINS; the pins narrow it. The mirror (ETL_SYNC_ENABLED) is the ETL load path
	// and logs in as logitrack_etl through ETL_DATABASE_URL (R66), on a replica that runs group sync alone
	// (WORKER_CONSUMERS=sync; compose service worker-sync), the only one that may hold that URL; the projection
	// (COMPAT_PROJECTION_ENABLED, the kill switch) uses DATABASE_URL. Both reach ETL_FIRESTORE_PROJECT_ID with the
	// GOOGLE_APPLICATION_CREDENTIALS key. Off (the default, and the staging VM, which runs PostgreSQL only) neither
	// needs a database URL or a key.
	PGOwnedDomains              string `env:"PG_OWNED_DOMAINS"`
	ETLSyncEnabled              bool   `env:"ETL_SYNC_ENABLED"`
	ETLDatabaseURL              string `env:"ETL_DATABASE_URL"`
	ETLMirrorCollections        string `env:"ETL_MIRROR_COLLECTIONS"`
	CompatProjectionEnabled     bool   `env:"COMPAT_PROJECTION_ENABLED"`
	CompatProjectionCollections string `env:"COMPAT_PROJECTION_COLLECTIONS"`
	FirestoreProjectID          string `env:"ETL_FIRESTORE_PROJECT_ID"`
	FirestoreDatabaseID         string `env:"FIRESTORE_DATABASE_ID" envDefault:"(default)"`
	GoogleCredentials           string `env:"GOOGLE_APPLICATION_CREDENTIALS"`

	// Groups is WORKER_CONSUMERS resolved by Validate ("all" = every group).
	Groups []string `env:"-"`
	// Coexist is the coexistence plan resolved by Validate.
	Coexist coexist.Plan `env:"-"`
}

// Validate implements config.Validator.
func (c *WorkerConfig) Validate() error {
	var errs []string
	c.Common.validate(&errs)
	c.Runtime.validate(&errs)
	c.Database.validate(&errs)
	c.Redis.validate(c.AppEnv, &errs)
	c.AMQPEnv.validate(&errs)
	c.Storage.validate(&errs)
	c.Bangchak.validate(&errs)
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
	if c.DistanceMatrixEnabled && strings.TrimSpace(c.GoogleMapsServerAPIKey) == "" {
		errs = append(errs, config.Invalidf("GOOGLE_MAPS_SERVER_API_KEY", "required when DISTANCE_MATRIX_ENABLED is true"))
	}
	plan, perrs := coexist.ParsePlan(c.PGOwnedDomains, c.ETLMirrorCollections, c.CompatProjectionCollections)
	errs = append(errs, perrs...)
	c.Coexist = plan
	// R66 (T24S): the BYPASSRLS logitrack_etl login is held only by the replica that runs the mirror, and that replica
	// runs group sync alone, so no other consumer of the worker ever holds it.
	mirror := c.ETLSyncEnabled && slices.Contains(c.Groups, "sync")
	switch {
	case mirror && !slices.Equal(c.Groups, []string{"sync"}):
		errs = append(errs, config.Invalidf("WORKER_CONSUMERS", "must be sync alone on the replica that runs the mirror (ETL_SYNC_ENABLED): "+
			"it holds ETL_DATABASE_URL, which no other consumer may hold (R66); run the other groups on another replica"))
	case mirror && c.ETLDatabaseURL == "":
		errs = append(errs, config.Invalidf("ETL_DATABASE_URL", "is required on the replica that runs the mirror (ETL_SYNC_ENABLED, "+
			"WORKER_CONSUMERS=sync): the mirror logs in as logitrack_etl (R66)"))
	case mirror && !strings.HasPrefix(c.ETLDatabaseURL, "postgres://") && !strings.HasPrefix(c.ETLDatabaseURL, "postgresql://"):
		errs = append(errs, config.Invalidf("ETL_DATABASE_URL", "must be a postgres:// URL"))
	case !mirror && c.ETLDatabaseURL != "":
		errs = append(errs, config.Invalidf("ETL_DATABASE_URL", "must be empty unless this replica runs the mirror (ETL_SYNC_ENABLED, "+
			"WORKER_CONSUMERS=sync): the logitrack_etl login bypasses RLS (R66)"))
	}
	if (c.ETLSyncEnabled || c.CompatProjectionEnabled) && slices.Contains(c.Groups, "sync") {
		if c.FirestoreProjectID == "" || c.GoogleCredentials == "" {
			errs = append(errs, "ETL_FIRESTORE_PROJECT_ID, GOOGLE_APPLICATION_CREDENTIALS: required when ETL_SYNC_ENABLED or COMPAT_PROJECTION_ENABLED is true on a replica that runs group sync")
		}
	}
	if c.FCMEnabled {
		if !push.ValidProjectID(c.FCMProjectID) {
			errs = append(errs, config.Invalidf("FCM_PROJECT_ID", "must be a Firebase project id when FCM_ENABLED is true"))
		}
		if c.FCMServiceAccountJSON == "" {
			errs = append(errs, config.Invalidf("FCM_SERVICE_ACCOUNT_JSON", "must name the service-account key file when FCM_ENABLED is true"))
		}
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
	// FuelMonthlySnapshotEnabled schedules bangchak.snapshot at 05:00 Asia/Bangkok (T37, R70; main spec §7.4); off,
	// the snapshots run only from POST /v1/jobs/bangchak.snapshot.
	FuelMonthlySnapshotEnabled bool `env:"FUEL_MONTHLY_SNAPSHOT_ENABLED"`
	// JWTAccessTTL is the life of the revoked-session markers the relay re-applies from
	// user.sessions_revoked (+ 30 s leeway, Appendix C §C.4.7): the same value the api signs with.
	JWTAccessTTL time.Duration `env:"JWT_ACCESS_TTL" envDefault:"15m"`
	// ETLSyncEnabled schedules the etl.sync sweep every ETLSyncInterval (main spec §7.4; T24); the interval is whole
	// minutes that divide an hour, so the slots are Bangkok clock minutes (*/5 * * * * by default).
	ETLSyncEnabled  bool          `env:"ETL_SYNC_ENABLED"`
	ETLSyncInterval time.Duration `env:"ETL_SYNC_INTERVAL" envDefault:"5m"`
}

// ETLSyncSpec is the cron spec of ETL_SYNC_INTERVAL ("" when it is not whole minutes that divide an hour).
func ETLSyncSpec(d time.Duration) string {
	m := int(d / time.Minute)
	switch {
	case d%time.Minute != 0 || m < 1 || m > 60 || 60%m != 0:
		return ""
	case m == 60:
		return "0 * * * *"
	case m == 1:
		return "* * * * *"
	}
	return "*/" + strconv.Itoa(m) + " * * * *"
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
	if ETLSyncSpec(c.ETLSyncInterval) == "" {
		errs = append(errs, config.Invalidf("ETL_SYNC_INTERVAL", "must be whole minutes that divide an hour (1m ... 60m)"))
	}
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}
