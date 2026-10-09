package telemetry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/telemetry"
)

func TestTracesURLAppendsSignalPath(t *testing.T) {
	cases := map[string]string{
		"http://collector:4318":        "http://collector:4318/v1/traces",
		"http://collector:4318/":       "http://collector:4318/v1/traces",
		"https://otel.example/prefix/": "https://otel.example/prefix/v1/traces",
	}
	for in, want := range cases {
		got, err := telemetry.TracesURL(in)
		if err != nil || got != want {
			t.Fatalf("TracesURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := telemetry.TracesURL("collector:4318"); err == nil {
		t.Fatal("a URL without scheme must be rejected")
	}
}

func TestExporterPostsToV1Traces(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	stop, err := telemetry.SetupTracing(context.Background(), telemetry.TracingOptions{
		Endpoint: srv.URL, ServiceName: "test", SamplerArg: 1, Env: "local", Version: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("t").Start(context.Background(), "probe")
	span.End()
	if err := stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) == 0 || paths[0] != "/v1/traces" {
		t.Fatalf("exporter paths = %v, want /v1/traces", paths)
	}
}
