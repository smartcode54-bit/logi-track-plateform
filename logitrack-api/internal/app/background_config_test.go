package app_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

// Placeholder URLs only: the tests never connect.
func workerEnv(extra ...string) []string {
	return append([]string{
		"APP_ENV=local", "METRICS_ADDR=127.0.0.1:9091",
		"DATABASE_URL=postgres://u:p@localhost:5432/x", "RABBITMQ_URL=amqp://u:p@localhost:5672/",
		"REDIS_URL=redis://localhost:6379/0",
		"EMAIL_ENABLED=true", "SMTP_HOST=mailpit", "SMTP_PORT=1025", "SMTP_FROM=no-reply@logitrack.test",
		"PUBLIC_WEB_BASE_URL=http://localhost:3000", "RABBITMQ_PREFETCH=", "SMTP_USER=", "SMTP_PASSWORD=",
		"FCM_ENABLED=false", "FCM_PROJECT_ID=", "FCM_SERVICE_ACCOUNT_JSON=",
	}, extra...)
}

func schedulerEnv(extra ...string) []string {
	return append([]string{
		"APP_ENV=local", "METRICS_ADDR=127.0.0.1:9092",
		"DATABASE_URL=postgres://u:p@localhost:5432/x", "RABBITMQ_URL=amqp://u:p@localhost:5672/",
		"REDIS_URL=redis://localhost:6379/0",
	}, extra...)
}

func TestWorkerConfigDefaults(t *testing.T) {
	cfg, err := config.LoadFrom[app.WorkerConfig](workerEnv())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Groups, app.WorkerGroups) || len(app.WorkerGroups) != 7 {
		t.Fatalf("WORKER_CONSUMERS=all resolves to %v (groups %v)", cfg.Groups, app.WorkerGroups)
	}
	if cfg.Prefetch != 0 || cfg.PasswordResetTTL != 30*time.Minute || !cfg.SMTPStartTLS || cfg.SMTPFromName != "LogiTrack" ||
		cfg.FCMEnabled || cfg.RedisKeyPrefix != "lt:local:" {
		t.Fatalf("defaults: %+v", cfg)
	}
}

// FCM on needs the project and the key file's path (main spec §16.1); off (the local default) needs
// neither. The key file itself is read when the worker builds notify.fcm.
func TestWorkerConfigFCM(t *testing.T) {
	cfg, err := config.LoadFrom[app.WorkerConfig](workerEnv("FCM_ENABLED=true", "FCM_PROJECT_ID=logitrack-prod",
		"FCM_SERVICE_ACCOUNT_JSON=/run/secrets/fcm.json"))
	if err != nil || !cfg.FCMEnabled || cfg.FCMProjectID != "logitrack-prod" || cfg.FCMServiceAccountJSON != "/run/secrets/fcm.json" {
		t.Fatalf("%+v %v", cfg, err)
	}
	for env, want := range map[string]string{
		"FCM_PROJECT_ID=":           "FCM_PROJECT_ID",
		"FCM_PROJECT_ID=Not_A_Proj": "FCM_PROJECT_ID",
		"FCM_SERVICE_ACCOUNT_JSON=": "FCM_SERVICE_ACCOUNT_JSON",
	} {
		_, err := config.LoadFrom[app.WorkerConfig](workerEnv("FCM_ENABLED=true", "FCM_PROJECT_ID=logitrack-prod",
			"FCM_SERVICE_ACCOUNT_JSON=/run/secrets/fcm.json", env))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", env, err, want)
		}
	}
	if _, err := config.LoadFrom[app.WorkerConfig](workerEnv("REDIS_URL=")); err == nil || !strings.Contains(err.Error(), "REDIS_URL") {
		t.Errorf("the worker needs REDIS_URL: %v", err)
	}
}

func TestWorkerConfigConsumers(t *testing.T) {
	cfg, err := config.LoadFrom[app.WorkerConfig](workerEnv("WORKER_CONSUMERS=notify, billing,notify"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Groups, []string{"notify", "billing"}) {
		t.Fatalf("groups %v", cfg.Groups)
	}
	for env, want := range map[string]string{
		"WORKER_CONSUMERS=notify,pdf":      "WORKER_CONSUMERS: entries must be all or among billing, notify, documents, integrations, hr, platform, sync",
		"WORKER_CONSUMERS= , ":             "WORKER_CONSUMERS: must name at least one group",
		"RABBITMQ_URL=http://localhost":    "RABBITMQ_URL",
		"PASSWORD_RESET_TTL=1s":            "PASSWORD_RESET_TTL",
		"PUBLIC_WEB_BASE_URL=/relative":    "PUBLIC_WEB_BASE_URL",
		"SMTP_HOST=":                       "SMTP_HOST, SMTP_FROM",
		"RABBITMQ_PREFETCH=-1":             "RABBITMQ_PREFETCH",
		"DATABASE_URL=mysql://localhost/x": "DATABASE_URL",
	} {
		_, err := config.LoadFrom[app.WorkerConfig](workerEnv(env))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", env, err, want)
		}
	}
	// Email off: SMTP settings are not needed.
	if _, err := config.LoadFrom[app.WorkerConfig](workerEnv("EMAIL_ENABLED=false", "SMTP_HOST=", "PUBLIC_WEB_BASE_URL=")); err != nil {
		t.Fatalf("email disabled: %v", err)
	}
}

func TestWorkerConfigNeverEchoesValues(t *testing.T) {
	_, err := config.LoadFrom[app.WorkerConfig](workerEnv("RABBITMQ_URL=http://user:s3cret@host"))
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error %v", err)
	}
}

func TestSchedulerConfig(t *testing.T) {
	cfg, err := config.LoadFrom[app.SchedulerConfig](schedulerEnv())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RedisKeyPrefix != "lt:local:" || cfg.RelayInterval != 200*time.Millisecond || cfg.BatchSize != 500 ||
		cfg.RTLogMaxLen != 1000 || cfg.RTLogTTL != 24*time.Hour || cfg.JWTAccessTTL != 15*time.Minute {
		t.Fatalf("defaults: %+v", cfg)
	}
	for env, want := range map[string]string{
		"REDIS_KEY_PREFIX=lt:prod:":    "REDIS_KEY_PREFIX",
		"OUTBOX_RELAY_INTERVAL=1ms":    "OUTBOX_RELAY_INTERVAL",
		"OUTBOX_BATCH_SIZE=0":          "OUTBOX_BATCH_SIZE",
		"RTLOG_MAXLEN=1":               "RTLOG_MAXLEN",
		"RTLOG_TTL=1s":                 "RTLOG_TTL",
		"JWT_ACCESS_TTL=2h":            "JWT_ACCESS_TTL",
		"REDIS_URL=localhost:6379":     "REDIS_URL",
		"DATABASE_MIN_CONNS=5":         "", // valid without a max
		"DATABASE_MAX_CONNS=x":         "DATABASE_MAX_CONNS",
		"RABBITMQ_URL=":                "RABBITMQ_URL",
		"METRICS_ADDR=":                "METRICS_ADDR",
		"OUTBOX_RELAY_INTERVAL=banana": "OUTBOX_RELAY_INTERVAL",
	} {
		_, err := config.LoadFrom[app.SchedulerConfig](schedulerEnv(env))
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", env, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", env, err, want)
		}
	}
	if _, err := config.LoadFrom[app.SchedulerConfig](schedulerEnv("DATABASE_MIN_CONNS=5", "DATABASE_MAX_CONNS=2")); err == nil {
		t.Error("MIN > MAX accepted")
	}
}

func TestBackgroundConfigsDescribeOnlyTheirNames(t *testing.T) {
	w := config.Describe[app.WorkerConfig]()
	s := config.Describe[app.SchedulerConfig]()
	for _, n := range []string{"RABBITMQ_URL", "WORKER_CONSUMERS", "SMTP_PASSWORD", "PASSWORD_RESET_TTL", "PUBLIC_WEB_BASE_URL",
		"REDIS_URL", "REDIS_KEY_PREFIX", "REDIS_TLS", "FCM_ENABLED", "FCM_PROJECT_ID", "FCM_SERVICE_ACCOUNT_JSON"} {
		if _, ok := w[n]; !ok {
			t.Errorf("worker does not read %s", n)
		}
	}
	for _, n := range []string{"OUTBOX_RELAY_INTERVAL", "OUTBOX_BATCH_SIZE", "RTLOG_MAXLEN", "RTLOG_TTL", "REDIS_URL", "RABBITMQ_URL", "JWT_ACCESS_TTL"} {
		if _, ok := s[n]; !ok {
			t.Errorf("scheduler does not read %s", n)
		}
	}
	for _, n := range []string{"SMTP_PASSWORD", "WORKER_CONSUMERS", "FCM_SERVICE_ACCOUNT_JSON"} {
		if _, ok := s[n]; ok {
			t.Errorf("scheduler reads %s", n)
		}
	}
}
