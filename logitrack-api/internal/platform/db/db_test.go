package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// The URL holds a password, so no error may repeat it.
func TestOpenErrorsKeepThePasswordOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, url := range []string{
		"postgres://logitrack_app:s3cret-value@127.0.0.1:1/logitrack?connect_timeout=2",
		"postgres://logitrack_app:s3cret-value@127.0.0.1:1/logitrack?sslmode=nonsense",
		"not a url s3cret-value",
	} {
		p, err := db.Open(ctx, url, "test")
		if err == nil {
			p.Close()
			t.Fatalf("Open(%q) succeeded", url)
		}
		if strings.Contains(err.Error(), "s3cret-value") {
			t.Fatalf("error repeats the password: %v", err)
		}
	}
}
