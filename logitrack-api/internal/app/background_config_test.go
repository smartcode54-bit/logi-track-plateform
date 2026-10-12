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
		"S3_ENDPOINT=http://minio:9000", "S3_REGION=us-east-1", "S3_ACCESS_KEY_ID=placeholder", "S3_SECRET_ACCESS_KEY=placeholder",
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

// distances.compute (T21): the Distance Matrix switch is off by default, and on it needs the server key (the value is
// a placeholder: no request is made).
func TestWorkerConfigDistanceMatrix(t *testing.T) {
	cfg, err := config.LoadFrom[app.WorkerConfig](workerEnv())
	if err != nil || cfg.DistanceMatrixEnabled {
		t.Fatalf("default: %v %v", cfg.DistanceMatrixEnabled, err)
	}
	cfg, err = config.LoadFrom[app.WorkerConfig](workerEnv("DISTANCE_MATRIX_ENABLED=true", "GOOGLE_MAPS_SERVER_API_KEY=placeholder"))
	if err != nil || !cfg.DistanceMatrixEnabled {
		t.Fatalf("on: %v", err)
	}
	_, err = config.LoadFrom[app.WorkerConfig](workerEnv("DISTANCE_MATRIX_ENABLED=true", "GOOGLE_MAPS_SERVER_API_KEY="))
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_MAPS_SERVER_API_KEY") {
		t.Fatalf("on without a key: %v", err)
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

// The coexistence consumers (T24): off by default with no database URL or key; the mirror needs ETL_DATABASE_URL on a
// replica that runs group sync alone, and no other replica may hold that URL (R66); both need the Firestore project and
// the key file; the plan follows PG_OWNED_DOMAINS and the pins, whose errors name the variable and never the value.
func TestWorkerConfigCoexistence(t *testing.T) {
	cfg, err := config.LoadFrom[app.WorkerConfig](workerEnv())
	if err != nil || cfg.ETLSyncEnabled || cfg.CompatProjectionEnabled || cfg.Coexist.Mirror.Mode("tasks") != "full" ||
		len(cfg.Coexist.Projection) != 0 || cfg.FirestoreDatabaseID != "(default)" {
		t.Fatalf("defaults: %+v %v", cfg.Coexist, err)
	}
	on := []string{"ETL_SYNC_ENABLED=true", "COMPAT_PROJECTION_ENABLED=true", "PG_OWNED_DOMAINS=auth,masterdata",
		"ETL_DATABASE_URL=postgres://etl:placeholder@localhost:5432/x", "ETL_FIRESTORE_PROJECT_ID=logitrack-test",
		"GOOGLE_APPLICATION_CREDENTIALS=/run/secrets/sa.json", "COMPAT_PROJECTION_COLLECTIONS=hubs,drivers", "WORKER_CONSUMERS=sync"}
	cfg, err = config.LoadFrom[app.WorkerConfig](workerEnv(on...))
	if err != nil || cfg.Coexist.Mirror.Mode("drivers") != "fields" || strings.Join(cfg.Coexist.Projection, ",") != "hubs,drivers" {
		t.Fatalf("on: %+v %v", cfg.Coexist, err)
	}
	for env, want := range map[string]string{
		"ETL_DATABASE_URL=":                       "ETL_DATABASE_URL",
		"ETL_DATABASE_URL=mysql://x":              "ETL_DATABASE_URL",
		"ETL_FIRESTORE_PROJECT_ID=":               "ETL_FIRESTORE_PROJECT_ID, GOOGLE_APPLICATION_CREDENTIALS",
		"GOOGLE_APPLICATION_CREDENTIALS=":         "ETL_FIRESTORE_PROJECT_ID, GOOGLE_APPLICATION_CREDENTIALS",
		"PG_OWNED_DOMAINS=masterdata,secret_zz":   "PG_OWNED_DOMAINS",
		"ETL_MIRROR_COLLECTIONS=tasks,secret_zz":  "ETL_MIRROR_COLLECTIONS",
		"COMPAT_PROJECTION_COLLECTIONS=secret_zz": "COMPAT_PROJECTION_COLLECTIONS",
		// R66: the mirror runs on a replica of its own, which alone holds the logitrack_etl URL.
		"WORKER_CONSUMERS=all":         "WORKER_CONSUMERS",
		"WORKER_CONSUMERS=sync,notify": "WORKER_CONSUMERS",
		"ETL_SYNC_ENABLED=false":       "ETL_DATABASE_URL",
		// T29: the listeners poll every ETL_LISTEN_INTERVAL, 500ms to 1m (0 = off).
		"ETL_LISTEN_INTERVAL=100ms": "ETL_LISTEN_INTERVAL",
		"ETL_LISTEN_INTERVAL=2m":    "ETL_LISTEN_INTERVAL",
	} {
		_, err := config.LoadFrom[app.WorkerConfig](workerEnv(append(on, env)...))
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "secret_zz") || strings.Contains(err.Error(), "placeholder") {
			t.Errorf("%s: %v, want %q", env, err, want)
		}
	}
	if cfg.ETLListenInterval != 2*time.Second {
		t.Errorf("ETL_LISTEN_INTERVAL default %v", cfg.ETLListenInterval)
	}
	if off, err := config.LoadFrom[app.WorkerConfig](workerEnv(append(on, "ETL_LISTEN_INTERVAL=0")...)); err != nil {
		t.Errorf("listeners off: %v", err)
	} else if off.ETLListenInterval != 0 {
		t.Errorf("listeners off: %v", off.ETLListenInterval)
	}
	// The projection alone needs no ETL database URL.
	if _, err := config.LoadFrom[app.WorkerConfig](workerEnv("COMPAT_PROJECTION_ENABLED=true", "ETL_FIRESTORE_PROJECT_ID=logitrack-test",
		"GOOGLE_APPLICATION_CREDENTIALS=/run/secrets/sa.json")); err != nil {
		t.Errorf("projection only: %v", err)
	}
	// With the mirror on, the other replicas leave group sync out and hold no ETL URL; one that holds it refuses to start.
	rest := "WORKER_CONSUMERS=billing,notify,documents,integrations,hr,platform"
	if _, err := config.LoadFrom[app.WorkerConfig](workerEnv("ETL_SYNC_ENABLED=true", rest)); err != nil {
		t.Errorf("a replica without group sync: %v", err)
	}
	_, err = config.LoadFrom[app.WorkerConfig](workerEnv("ETL_SYNC_ENABLED=true", rest, "ETL_DATABASE_URL=postgres://etl:placeholder@localhost:5432/x"))
	if err == nil || !strings.Contains(err.Error(), "ETL_DATABASE_URL") || strings.Contains(err.Error(), "placeholder") {
		t.Errorf("a replica without group sync holding the URL: %v", err)
	}
	w := config.Describe[app.WorkerConfig]()
	for _, n := range []string{"ETL_SYNC_ENABLED", "ETL_DATABASE_URL", "ETL_MIRROR_COLLECTIONS", "ETL_LISTEN_INTERVAL", "COMPAT_PROJECTION_ENABLED",
		"COMPAT_PROJECTION_COLLECTIONS", "PG_OWNED_DOMAINS", "ETL_FIRESTORE_PROJECT_ID", "FIRESTORE_DATABASE_ID", "GOOGLE_APPLICATION_CREDENTIALS"} {
		if _, ok := w[n]; !ok {
			t.Errorf("worker does not read %s", n)
		}
	}
}

// The pricing gate of billing.compute (T38, main spec §6.13): a billing replica of a deployment that runs the mirror
// leaves the records it writes to Firestore's pricer until billing is PostgreSQL-owned (P3); without the mirror (the
// staging VM) Go prices everything.
func TestFirestorePricing(t *testing.T) {
	for _, c := range []struct {
		env  []string
		want bool
	}{
		{nil, false}, // the staging VM: PostgreSQL only
		{[]string{"PG_OWNED_DOMAINS=auth,masterdata"}, false},
		{[]string{"ETL_SYNC_ENABLED=true", "PG_OWNED_DOMAINS=auth,masterdata"}, true},          // P1-P2
		{[]string{"ETL_SYNC_ENABLED=true", "PG_OWNED_DOMAINS=auth,masterdata,billing"}, false}, // P3
		{[]string{"ETL_SYNC_ENABLED=true", "PG_OWNED_DOMAINS=all"}, false},
	} {
		env := append([]string{"WORKER_CONSUMERS=billing,notify,documents,integrations,hr,platform"}, c.env...)
		cfg, err := config.LoadFrom[app.WorkerConfig](workerEnv(env...))
		if err != nil {
			t.Fatalf("%v: %v", c.env, err)
		}
		if got := app.FirestorePricing(cfg.ETLSyncEnabled, cfg.Coexist.Owned); got != c.want {
			t.Errorf("%v: FirestorePricing %v, want %v", c.env, got, c.want)
		}
	}
}

// The scheduler schedules etl.sync every ETL_SYNC_INTERVAL: whole minutes that divide an hour.
func TestSchedulerETLSyncInterval(t *testing.T) {
	cfg, err := config.LoadFrom[app.SchedulerConfig](schedulerEnv("ETL_SYNC_ENABLED=true"))
	if err != nil || !cfg.ETLSyncEnabled || app.ETLSyncSpec(cfg.ETLSyncInterval) != "*/5 * * * *" {
		t.Fatalf("default: %v %v", cfg.ETLSyncInterval, err)
	}
	for in, want := range map[time.Duration]string{time.Minute: "* * * * *", 15 * time.Minute: "*/15 * * * *",
		time.Hour: "0 * * * *", 7 * time.Minute: "", 90 * time.Second: "", 2 * time.Hour: ""} {
		if got := app.ETLSyncSpec(in); got != want {
			t.Errorf("%v: %q, want %q", in, got, want)
		}
	}
	if _, err := config.LoadFrom[app.SchedulerConfig](schedulerEnv("ETL_SYNC_INTERVAL=7m")); err == nil || !strings.Contains(err.Error(), "ETL_SYNC_INTERVAL") {
		t.Errorf("7m: %v", err)
	}
}

// The Cartrack sync (T53, R70): the credentials are the worker's alone; the client exists only with the base URL and
// both credentials (the local .env.example sets the WireMock URL and blank credentials: off, not an error); a bad URL
// stops the worker naming the variable, never a value; CARTRACK_SYNC_ENABLED is the scheduler's gate.
func TestCartrackConfig(t *testing.T) {
	cfg, err := config.LoadFrom[app.WorkerConfig](workerEnv())
	if err != nil {
		t.Fatal(err)
	}
	if c, err := cfg.CartrackClient(); c != nil || err != nil {
		t.Fatalf("unset: %v %v", c, err)
	}
	cfg, err = config.LoadFrom[app.WorkerConfig](workerEnv("CARTRACK_API_BASE_URL=http://mocks:8080/cartrack"))
	if err != nil {
		t.Fatal(err)
	}
	if c, err := cfg.CartrackClient(); c != nil || err != nil {
		t.Fatalf("no credentials: %v %v", c, err)
	}
	secret := "placeholder-" + strings.Repeat("x", 9)
	cfg, err = config.LoadFrom[app.WorkerConfig](workerEnv("CARTRACK_API_BASE_URL=http://mocks:8080/cartrack",
		"CARTRACK_API_USERNAME=placeholder-user", "CARTRACK_API_PASSWORD="+secret))
	if err != nil {
		t.Fatal(err)
	}
	if c, err := cfg.CartrackClient(); c == nil || err != nil {
		t.Fatalf("configured: %v %v", c, err)
	}
	_, err = config.LoadFrom[app.WorkerConfig](workerEnv("CARTRACK_API_BASE_URL=https://u:"+secret+"@fleet.example", "CARTRACK_API_PASSWORD="+secret))
	if err == nil || !strings.Contains(err.Error(), "CARTRACK_API_BASE_URL") || strings.Contains(err.Error(), secret) {
		t.Fatalf("bad URL: %v", err)
	}
	w, s := config.Describe[app.WorkerConfig](), config.Describe[app.SchedulerConfig]()
	for _, n := range []string{"CARTRACK_API_BASE_URL", "CARTRACK_API_USERNAME", "CARTRACK_API_PASSWORD"} {
		if _, ok := w[n]; !ok {
			t.Errorf("worker does not read %s", n)
		}
		if _, ok := s[n]; ok {
			t.Errorf("scheduler reads %s", n)
		}
	}
	if _, ok := s["CARTRACK_SYNC_ENABLED"]; !ok {
		t.Error("scheduler does not read CARTRACK_SYNC_ENABLED")
	}
	if _, ok := config.Describe[app.APIConfig]()["CARTRACK_API_PASSWORD"]; ok {
		t.Error("the api must never hold the Cartrack credentials (main spec §16.1)")
	}
	sc, err := config.LoadFrom[app.SchedulerConfig](schedulerEnv("CARTRACK_SYNC_ENABLED=true"))
	if err != nil || !sc.CartrackSyncEnabled {
		t.Fatalf("scheduler gate: %v", err)
	}
}
