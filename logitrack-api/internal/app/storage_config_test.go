package app_test

import (
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

// localEnv is baseEnv on the local backend, as on the first deployment (no S3 yet).
func localEnv(extra ...string) []string {
	env := override(baseEnv(), []string{"S3_ENDPOINT=", "S3_PRESIGN_ENDPOINT=", "S3_REGION=", "S3_ACCESS_KEY_ID=", "S3_SECRET_ACCESS_KEY=",
		"STORAGE_BACKEND=local", "LOCAL_MEDIA_DIR=/var/lib/logitrack/media",
		"LOCAL_MEDIA_PUBLIC_BASE_URL=https://logi.example.test/media", "LOCAL_MEDIA_SIGNING_KEY=" + strings.Repeat("k", 40)})
	return override(env, extra)
}

func TestStorageConfigDefaultsAndBackends(t *testing.T) {
	cfg, err := config.LoadFrom[app.APIConfig](baseEnv())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StorageBackend != "s3" || cfg.S3Bucket != "logitrack" || cfg.S3PublicBucket != "logitrack-public" || !cfg.S3UsePathStyle ||
		cfg.UploadMaxBytes != 10<<20 || cfg.S3PresignPutTTL.Minutes() != 15 || cfg.S3PresignGetTTL.Hours() != 1 ||
		cfg.EvidenceTokenTTLDays != 0 || cfg.EvidencePresignTTL.Minutes() != 15 {
		t.Fatalf("storage defaults: %+v %+v", cfg.Storage, cfg.StorageAPI)
	}
	if _, err := config.LoadFrom[app.APIConfig](localEnv()); err != nil {
		t.Fatalf("local backend without any S3 setting: %v", err)
	}
	// After the switch to s3 the local settings stay, so local objects keep downloading.
	if _, err := config.LoadFrom[app.APIConfig](override(localEnv(), override(baseEnv(), []string{"STORAGE_BACKEND=s3"}))); err != nil {
		t.Fatalf("s3 with the local backend kept: %v", err)
	}
}

func TestStorageConfigRefusesBadValues(t *testing.T) {
	for name, tc := range map[string]struct {
		env  []string
		want string
	}{
		"unknown backend":          {override(baseEnv(), []string{"STORAGE_BACKEND=gcs"}), "STORAGE_BACKEND"},
		"s3 without endpoint":      {override(baseEnv(), []string{"S3_ENDPOINT="}), "S3_ENDPOINT"},
		"s3 without credentials":   {override(baseEnv(), []string{"S3_SECRET_ACCESS_KEY="}), "S3_SECRET_ACCESS_KEY"},
		"s3 without presign":       {override(baseEnv(), []string{"S3_PRESIGN_ENDPOINT="}), "S3_PRESIGN_ENDPOINT"},
		"presign with a path":      {override(baseEnv(), []string{"S3_PRESIGN_ENDPOINT=https://media.test/s3"}), "S3_PRESIGN_ENDPOINT"},
		"scheme vs S3_USE_SSL":     {override(baseEnv(), []string{"S3_USE_SSL=true"}), "S3_USE_SSL"},
		"same bucket twice":        {override(baseEnv(), []string{"S3_PUBLIC_BUCKET=logitrack"}), "S3_PUBLIC_BUCKET"},
		"bad bucket name":          {override(baseEnv(), []string{"S3_BUCKET=Bad_Bucket"}), "S3_BUCKET"},
		"cors wildcard":            {override(baseEnv(), []string{"CORS_ALLOWED_ORIGINS=*"}), "CORS_ALLOWED_ORIGINS"},
		"cors with a path":         {override(baseEnv(), []string{"CORS_ALLOWED_ORIGINS=http://localhost:3000/app"}), "CORS_ALLOWED_ORIGINS"},
		"get ttl over 7 days":      {override(baseEnv(), []string{"S3_PRESIGN_GET_TTL=200h"}), "S3_PRESIGN_GET_TTL"},
		"upload limit too big":     {override(baseEnv(), []string{"UPLOAD_MAX_BYTES=1073741824"}), "UPLOAD_MAX_BYTES"},
		"negative evidence ttl":    {override(baseEnv(), []string{"EVIDENCE_TOKEN_TTL_DAYS=-1"}), "EVIDENCE_TOKEN_TTL_DAYS"},
		"local without dir":        {localEnv("LOCAL_MEDIA_DIR="), "LOCAL_MEDIA_DIR"},
		"relative dir":             {localEnv("LOCAL_MEDIA_DIR=media"), "LOCAL_MEDIA_DIR"},
		"local without base url":   {localEnv("LOCAL_MEDIA_PUBLIC_BASE_URL="), "LOCAL_MEDIA_PUBLIC_BASE_URL"},
		"base url with a query":    {localEnv("LOCAL_MEDIA_PUBLIC_BASE_URL=https://x.test/media?a=1"), "LOCAL_MEDIA_PUBLIC_BASE_URL"},
		"short signing key":        {localEnv("LOCAL_MEDIA_SIGNING_KEY=short-secret-value"), "LOCAL_MEDIA_SIGNING_KEY"},
		"public groups beyond six": {override(baseEnv(), []string{"PUBLIC_ROUTE_GROUPS=/media,/v1/me"}), "PUBLIC_ROUTE_GROUPS"},
	} {
		_, err := config.LoadFrom[app.APIConfig](tc.env)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %s", name, err, tc.want)
			continue
		}
		for _, secret := range []string{"short-secret-value", strings.Repeat("k", 40)} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: the error echoes a value", name)
			}
		}
	}
}

func TestWorkerStorageConfig(t *testing.T) {
	if _, err := config.LoadFrom[app.WorkerConfig](workerEnv("S3_ENDPOINT=", "STORAGE_BACKEND=local", "LOCAL_MEDIA_DIR=/srv/media")); err != nil {
		t.Fatalf("worker on the local backend: %v", err)
	}
	if _, err := config.LoadFrom[app.WorkerConfig](workerEnv("STORAGE_BACKEND=local")); err == nil || !strings.Contains(err.Error(), "LOCAL_MEDIA_DIR") {
		t.Fatalf("worker local without a directory: %v", err)
	}
	w := config.Describe[app.WorkerConfig]()
	for _, n := range []string{"STORAGE_BACKEND", "LOCAL_MEDIA_DIR", "S3_ENDPOINT", "S3_BUCKET"} {
		if _, ok := w[n]; !ok {
			t.Errorf("worker does not read %s", n)
		}
	}
	// Signing is the api's alone (main spec §16.1 consumers).
	for _, n := range []string{"LOCAL_MEDIA_SIGNING_KEY", "LOCAL_MEDIA_PUBLIC_BASE_URL", "S3_PRESIGN_ENDPOINT", "UPLOAD_MAX_BYTES"} {
		if _, ok := w[n]; ok {
			t.Errorf("worker reads %s", n)
		}
	}
}
