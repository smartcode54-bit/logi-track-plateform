//go:build integration

package storagetest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

func TestMain(m *testing.M) {
	code := m.Run()
	Terminate()
	os.Exit(code)
}

// notInitialized puts a proxy in front of the shared MinIO that answers the first n requests the way a
// MinIO still initialising its subsystems does, and returns a MinIO whose endpoint is the proxy.
func notInitialized(t *testing.T, n int64) (*MinIO, *atomic.Int64) {
	t.Helper()
	upstream := Shared(t)
	target, err := url.Parse(upstream.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Host = r.In.Host // the client signed the proxy's host (SigV4)
	}}
	var refused atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refused.Add(1) <= n {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>XMinioServerNotInitialized</Code>` +
				`<Message>Server not initialized yet, please try again.</Message></Error>`))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return &MinIO{Endpoint: srv.URL, CORSOrigins: upstream.CORSOrigins, user: upstream.user, pass: upstream.pass}, &refused
}

// The failure of FLAKE4: one bootstrap against a MinIO that is not initialised yet fails.
func TestBootstrapFailsWhileMinIOInitialises(t *testing.T) {
	m, _ := notInitialized(t, 1<<30)
	s, err := storage.NewS3(m.Config(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bootstrap(context.Background(), zerolog.Nop()); err == nil || !strings.Contains(err.Error(), "Server not initialized") {
		t.Fatalf("bootstrap: %v", err)
	}
}

// awaitS3 retries until a whole bootstrap goes through, so tests start on an initialised server.
func TestAwaitS3WaitsForInitialisation(t *testing.T) {
	m, refused := notInitialized(t, 5)
	if err := m.awaitS3(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := refused.Load(); got <= 5 {
		t.Fatalf("%d requests: the refused ones were not retried", got)
	}
	// The server is usable afterwards: a test's own bootstrap succeeds at once.
	s, err := storage.NewS3(m.Config(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bootstrap(context.Background(), zerolog.Nop()); err != nil {
		t.Fatal(err)
	}
}
