package config_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

type sample struct {
	Name    string        `env:"SAMPLE_NAME,required,notEmpty"`
	Other   string        `env:"SAMPLE_OTHER,required,notEmpty"`
	Timeout time.Duration `env:"SAMPLE_TIMEOUT" envDefault:"5s"`
	Count   int           `env:"SAMPLE_COUNT" envDefault:"1"`
	Derived []string      `env:"-"`
}

func (s *sample) Validate() error {
	if s.Count < 0 {
		return &config.Error{Invalid: []string{config.Invalidf("SAMPLE_COUNT", "must be >= 0")}}
	}
	return nil
}

func TestLoadReportsAllMissingVariablesTogether(t *testing.T) {
	_, err := config.LoadFrom[sample](nil)
	var cerr *config.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *config.Error, got %v", err)
	}
	if got, want := strings.Join(cerr.Missing, ","), "SAMPLE_NAME,SAMPLE_OTHER"; got != want {
		t.Fatalf("missing = %q, want %q", got, want)
	}
	if !strings.Contains(err.Error(), "missing required environment variables: SAMPLE_NAME, SAMPLE_OTHER") {
		t.Fatalf("message does not name the variables: %q", err)
	}
}

func TestLoadTreatsEmptyAsMissing(t *testing.T) {
	_, err := config.LoadFrom[sample]([]string{"SAMPLE_NAME=", "SAMPLE_OTHER=x"})
	var cerr *config.Error
	if !errors.As(err, &cerr) || len(cerr.Missing) != 1 || cerr.Missing[0] != "SAMPLE_NAME" {
		t.Fatalf("want SAMPLE_NAME missing, got %v", err)
	}
}

func TestLoadParseErrorNamesVariableNotValue(t *testing.T) {
	_, err := config.LoadFrom[sample]([]string{"SAMPLE_NAME=a", "SAMPLE_OTHER=b", "SAMPLE_TIMEOUT=s3cr3t-value"})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "SAMPLE_TIMEOUT") {
		t.Fatalf("error should name SAMPLE_TIMEOUT: %q", err)
	}
	if strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("error leaks the value: %q", err)
	}
}

func TestLoadRunsValidate(t *testing.T) {
	_, err := config.LoadFrom[sample]([]string{"SAMPLE_NAME=a", "SAMPLE_OTHER=b", "SAMPLE_COUNT=-1"})
	if err == nil || !strings.Contains(err.Error(), "SAMPLE_COUNT: must be >= 0") {
		t.Fatalf("want validation error, got %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.LoadFrom[sample]([]string{"SAMPLE_NAME=a", "SAMPLE_OTHER=b"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timeout != 5*time.Second || cfg.Count != 1 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestDescribeReportsPresenceOnly(t *testing.T) {
	t.Setenv("SAMPLE_NAME", "super-secret")
	t.Setenv("SAMPLE_OTHER", "")
	d := config.Describe[sample]()
	if d["SAMPLE_NAME"] != "set" || d["SAMPLE_OTHER"] != "unset" || d["SAMPLE_TIMEOUT"] == "" {
		t.Fatalf("unexpected describe: %v", d)
	}
	if _, ok := d["-"]; ok {
		t.Fatalf("env:\"-\" fields must not be described: %v", d)
	}
	for _, v := range d {
		if v != "set" && v != "unset" {
			t.Fatalf("describe leaked a value: %v", d)
		}
	}
}
